package gracefulshutdown

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func trinoLabels(name, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "trino",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "xtrinode-operator",
		"app.kubernetes.io/component":  component,
	}
}

func workerLabels(name string) map[string]string {
	return trinoLabels(name, "worker")
}

func defaultControlCredential() controlauth.Credential {
	return controlauth.Credential{Username: config.TrinoOperatorUser}
}

func testXTrinode() *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
}

func testGracefulShutdownScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	return scheme
}

func TestCheckQueriesBeforeScaleDownURL_ActiveQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]string{
			{"queryId": "1", "state": "RUNNING"},
			{"queryId": "2", "state": "FINISHED"},
		}))
	}))
	t.Cleanup(server.Close)

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(context.Background(), server.URL, defaultControlCredential(), logr.Discard())
	require.NoError(t, err)
	assert.False(t, safe)
}

func TestCheckQueriesBeforeScaleDownURL_NoActiveQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]string{
			{"queryId": "1", "state": "FINISHED"},
			{"queryId": "2", "state": "FAILED"},
		}))
	}))
	t.Cleanup(server.Close)

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(context.Background(), server.URL, defaultControlCredential(), logr.Discard())
	require.NoError(t, err)
	assert.True(t, safe)
}

func TestCheckQueriesBeforeScaleDownURL_SendsTrinoUserHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, config.TrinoOperatorUser, r.Header.Get(config.TrinoUserHeader))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]string{}))
	}))
	t.Cleanup(server.Close)

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(context.Background(), server.URL, defaultControlCredential(), logr.Discard())
	require.NoError(t, err)
	assert.True(t, safe)
}

func TestCheckQueriesBeforeScaleDownURLWithCredentialSendsBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "lifecycle-control", r.Header.Get(config.TrinoUserHeader))
		username, password, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "lifecycle-control", username)
		require.Equal(t, "secret", password)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]string{}))
	}))
	t.Cleanup(server.Close)

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(
		context.Background(),
		server.URL,
		controlauth.Credential{Username: "lifecycle-control", Password: "secret", HasPassword: true},
		logr.Discard(),
	)
	require.NoError(t, err)
	assert.True(t, safe)
}

func TestCheckQueriesBeforeScaleDownURL_FailsClosedOnCoordinatorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary coordinator error", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(context.Background(), server.URL, defaultControlCredential(), logr.Discard())
	require.Error(t, err)
	assert.False(t, safe)
	assert.Contains(t, err.Error(), "status 500")
}

func TestCheckQueriesBeforeScaleDownURL_FailsClosedOnCoordinatorRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should be closed before the request")
	}))
	queryURL := server.URL
	server.Close()

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(context.Background(), queryURL, defaultControlCredential(), logr.Discard())
	require.Error(t, err)
	assert.False(t, safe)
	assert.Contains(t, err.Error(), "failed to query coordinator")
}

func TestCheckQueriesBeforeScaleDown_AllowsUnavailableCoordinatorWhenRuntimeAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should be closed before the request")
	}))
	queryURL := server.URL
	server.Close()

	cli := fake.NewClientBuilder().WithScheme(testGracefulShutdownScheme(t)).Build()

	safe, err := checkQueriesBeforeScaleDown(context.Background(), cli, testXTrinode(), queryURL, logr.Discard())
	require.NoError(t, err)
	assert.True(t, safe)
}

func TestCheckQueriesBeforeScaleDown_FailsClosedWhenCoordinatorDeploymentReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should be closed before the request")
	}))
	queryURL := server.URL
	server.Close()

	coordinator := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.BuildCoordinatorDeploymentName("test"),
			Namespace: "default",
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas:     1,
			AvailableReplicas: 1,
		},
	}
	cli := fake.NewClientBuilder().WithScheme(testGracefulShutdownScheme(t)).WithObjects(coordinator).Build()

	safe, err := checkQueriesBeforeScaleDown(context.Background(), cli, testXTrinode(), queryURL, logr.Discard())
	require.Error(t, err)
	assert.False(t, safe)
	assert.Contains(t, err.Error(), "failed to query coordinator")
}

func TestCheckQueriesBeforeScaleDown_FailsClosedWhenCoordinatorPodReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should be closed before the request")
	}))
	queryURL := server.URL
	server.Close()

	coordinatorPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-test-coordinator-0",
			Namespace: "default",
			Labels:    trinoLabels("test", "coordinator"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(testGracefulShutdownScheme(t)).WithObjects(coordinatorPod).Build()

	safe, err := checkQueriesBeforeScaleDown(context.Background(), cli, testXTrinode(), queryURL, logr.Discard())
	require.Error(t, err)
	assert.False(t, safe)
	assert.Contains(t, err.Error(), "failed to query coordinator")
}

func TestWaitForPodTermination_NoPods(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := WaitForPodTermination(ctx, cli, xtrinode, log)
	assert.NoError(t, err, "Should succeed when no pods exist")
}

func TestWaitForPodTermination_NoTerminatingPods(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	// Create pods that are not terminating
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-test-worker-0",
			Namespace: "default",
			Labels:    workerLabels("test"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod1).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := WaitForPodTermination(ctx, cli, xtrinode, log)
	assert.NoError(t, err, "Should succeed when no pods are terminating")
}

func TestWaitForPodTermination_PodStillTerminating(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	gracePeriodSeconds := int64(3600)                                // 60 minutes
	deletionTime := metav1.NewTime(time.Now().Add(-5 * time.Minute)) // Deleted 5 minutes ago

	// Create pod that is still terminating (within grace period)
	// Fake client requires finalizers when DeletionTimestamp is set
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "trino-test-worker-0",
			Namespace:         "default",
			Labels:            workerLabels("test"),
			Finalizers:        []string{"test-finalizer"},
			DeletionTimestamp: &deletionTime,
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &gracePeriodSeconds,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := WaitForPodTermination(ctx, cli, xtrinode, log)
	assert.Error(t, err, "Should return error when pod is still terminating")
	assert.Contains(t, err.Error(), "still terminating")
}

func TestWaitForPodTermination_PodTerminated(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	gracePeriodSeconds := int64(300)                                  // 5 minutes
	deletionTime := metav1.NewTime(time.Now().Add(-10 * time.Minute)) // Deleted 10 minutes ago (past grace period)

	// Create pod that has finished terminating (past grace period)
	// Fake client requires finalizers when DeletionTimestamp is set
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "trino-test-worker-0",
			Namespace:         "default",
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{"test-finalizer"},
			Labels:            workerLabels("test"),
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &gracePeriodSeconds,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	// Should succeed because elapsed time (10 min) > grace period (5 min)
	err := WaitForPodTermination(ctx, cli, xtrinode, log)
	assert.NoError(t, err, "Should succeed when pod has finished terminating")
}

func TestWaitForPodTermination_DefaultGracePeriod(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	deletionTime := metav1.NewTime(time.Now().Add(-5 * time.Minute)) // Deleted 5 minutes ago

	// Create pod with no TerminationGracePeriodSeconds (should use default)
	// Fake client requires finalizers when DeletionTimestamp is set
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "trino-test-worker-0",
			Namespace:         "default",
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{"test-finalizer"},
			Labels:            workerLabels("test"),
		},
		// No TerminationGracePeriodSeconds - should use config.DefaultWorkerGracePeriodSeconds
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := WaitForPodTermination(ctx, cli, xtrinode, log)
	// Should return error because 5 minutes < default grace period (60 minutes)
	assert.Error(t, err, "Should return error when pod is still within default grace period")
	assert.Contains(t, err.Error(), "still terminating")
}

func TestWaitForPodTermination_ListError(t *testing.T) {
	ctx := context.Background()
	// Use a scheme without corev1 to cause List to fail
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := WaitForPodTermination(ctx, cli, xtrinode, log)
	assert.Error(t, err, "Should return error when List fails")
	assert.Contains(t, err.Error(), "failed to list pods")
}
