//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	xkeda "github.com/xtrinode/xtrinode/internal/keda"
	apiserver "github.com/xtrinode/xtrinode/pkg/api-server"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestKEDAEnvtest_PrometheusScaledObjectRoundTripsAgainstCRD(t *testing.T) {
	ctx := context.Background()
	scheme := newIntegrationScheme()
	cli, stop := startIntegrationEnvtest(t, scheme, kedaCRDDirectory(t))
	defer stop()

	require.NoError(t, createNamespace(ctx, cli, "team-a"))
	xtrinode := integrationXTrinode("runtime", "team-a")
	require.NoError(t, cli.Create(ctx, workerDeployment(xtrinode)))

	prometheusServer := "http://prometheus.monitoring.svc:9090"
	prometheusQuery := `max(jvm_memory_bytes_used{namespace="{namespace}",pod=~"{releaseName}-worker.*",xtrinode="{xtrinodeName}"})`
	threshold := "1048576"
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled:          boolPtr(true),
		ScalerType:       "prometheus",
		ScalingMetric:    "query",
		PrometheusServer: &prometheusServer,
		PrometheusQuery:  &prometheusQuery,
		Threshold:        &threshold,
	}

	require.NoError(t, xkeda.EnsureScaledObject(ctx, cli, scheme, xtrinode, logr.Discard()))

	var scaledObject kedav1alpha1.ScaledObject
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-workers", Namespace: "team-a"}, &scaledObject))
	require.NotNil(t, scaledObject.Spec.ScaleTargetRef)
	assert.Equal(t, "trino-runtime-worker", scaledObject.Spec.ScaleTargetRef.Name)
	require.NotNil(t, scaledObject.Spec.MinReplicaCount)
	require.NotNil(t, scaledObject.Spec.MaxReplicaCount)
	assert.Equal(t, int32(0), *scaledObject.Spec.MinReplicaCount)
	assert.Equal(t, int32(4), *scaledObject.Spec.MaxReplicaCount)
	require.Len(t, scaledObject.Spec.Triggers, 1)
	assert.Equal(t, "prometheus", scaledObject.Spec.Triggers[0].Type)
	assert.Equal(t, prometheusServer, scaledObject.Spec.Triggers[0].Metadata["serverAddress"])
	assert.Equal(t, threshold, scaledObject.Spec.Triggers[0].Metadata["threshold"])
	assert.Contains(t, scaledObject.Spec.Triggers[0].Metadata["query"], `namespace="team-a"`)
	assert.Contains(t, scaledObject.Spec.Triggers[0].Metadata["query"], `pod=~"trino-runtime-worker.*"`)
	assert.Contains(t, scaledObject.Spec.Triggers[0].Metadata["query"], `xtrinode="runtime"`)

	updatedQuery := `sum(custom_metric{namespace="{namespace}",xtrinode="{xtrinodeName}"})`
	xtrinode.Spec.KEDA.PrometheusQuery = &updatedQuery
	require.NoError(t, xkeda.EnsureScaledObject(ctx, cli, scheme, xtrinode, logr.Discard()))

	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-workers", Namespace: "team-a"}, &scaledObject))
	assert.Equal(t, `sum(custom_metric{namespace="team-a",xtrinode="runtime"})`, scaledObject.Spec.Triggers[0].Metadata["query"])
}

func TestKEDAEnvtest_QueryHTTPAuthResourcesCreatedAndCleanedUp(t *testing.T) {
	ctx := context.Background()
	scheme := newIntegrationScheme()
	cli, stop := startIntegrationEnvtest(t, scheme, kedaCRDDirectory(t))
	defer stop()

	require.NoError(t, createNamespace(ctx, cli, "team-a"))
	xtrinode := integrationXTrinode("runtime", "team-a")
	require.NoError(t, cli.Create(ctx, workerDeployment(xtrinode)))

	queryThreshold := "1"
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled:       boolPtr(true),
		ScalerType:    "http",
		ScalingMetric: "query",
		Threshold:     &queryThreshold,
	}
	require.NoError(t, xkeda.EnsureScaledObject(ctx, cli, scheme, xtrinode, logr.Discard()))

	var scaledObject kedav1alpha1.ScaledObject
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-workers", Namespace: "team-a"}, &scaledObject))
	require.Len(t, scaledObject.Spec.Triggers, 1)
	trigger := scaledObject.Spec.Triggers[0]
	assert.Equal(t, "metrics-api", trigger.Type)
	assert.Equal(t, queryThreshold, trigger.Metadata["targetValue"])
	assert.Contains(t, trigger.Metadata["url"], "trino-runtime.team-a.svc.cluster.local:8080/v1/query")
	require.NotNil(t, trigger.AuthenticationRef)
	assert.Equal(t, "trino-runtime-keda-metrics-auth", trigger.AuthenticationRef.Name)

	var authSecret corev1.Secret
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-keda-metrics-auth", Namespace: "team-a"}, &authSecret))
	assert.Equal(t, "apiKey", string(authSecret.Data["authModes"]))
	assert.Equal(t, "X-Trino-User", string(authSecret.Data["keyParamName"]))

	var triggerAuth kedav1alpha1.TriggerAuthentication
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-keda-metrics-auth", Namespace: "team-a"}, &triggerAuth))
	require.Len(t, triggerAuth.Spec.SecretTargetRef, 4)

	prometheusQuery := `sum(xtrinode_gateway_inflight_queries{namespace="{namespace}",xtrinode="{xtrinodeName}"})`
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled:         boolPtr(true),
		ScalerType:      "prometheus",
		ScalingMetric:   "query",
		PrometheusQuery: &prometheusQuery,
	}
	require.NoError(t, xkeda.EnsureScaledObject(ctx, cli, scheme, xtrinode, logr.Discard()))

	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-workers", Namespace: "team-a"}, &scaledObject))
	require.Len(t, scaledObject.Spec.Triggers, 1)
	assert.Equal(t, "prometheus", scaledObject.Spec.Triggers[0].Type)
	assert.Nil(t, scaledObject.Spec.Triggers[0].AuthenticationRef)

	err := cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-keda-metrics-auth", Namespace: "team-a"}, &authSecret)
	assert.True(t, errors.IsNotFound(err), "metrics-api auth Secret should be removed when no longer needed")
	err = cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-keda-metrics-auth", Namespace: "team-a"}, &triggerAuth)
	assert.True(t, errors.IsNotFound(err), "metrics-api TriggerAuthentication should be removed when no longer needed")
}

func TestKEDAEnvtest_QueryHTTPAuthUsesControlAuthForPasswordTrino(t *testing.T) {
	ctx := context.Background()
	scheme := newIntegrationScheme()
	cli, stop := startIntegrationEnvtest(t, scheme, kedaCRDDirectory(t))
	defer stop()

	require.NoError(t, createNamespace(ctx, cli, "team-a"))
	xtrinode := integrationXTrinode("runtime", "team-a")
	xtrinode.Spec.TrinoControlAuth = &analyticsv1.TrinoControlAuthSpec{
		Username: "lifecycle-control",
		PasswordSecret: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
			Key:                  "password",
		},
	}
	xtrinode.Spec.ValuesOverlay = &apiextensionsv1.JSON{Raw: []byte(`{"server":{"config":{"authenticationType":"PASSWORD"}}}`)}
	queryThreshold := "1"
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled:       boolPtr(true),
		ScalerType:    "http",
		ScalingMetric: "query",
		Threshold:     &queryThreshold,
	}

	require.NoError(t, cli.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-control",
			Namespace: "team-a",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"password": []byte("control-password")},
	}))
	require.NoError(t, cli.Create(ctx, workerDeployment(xtrinode)))
	require.NoError(t, xkeda.EnsureScaledObject(ctx, cli, scheme, xtrinode, logr.Discard()))

	var authSecret corev1.Secret
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-keda-metrics-auth", Namespace: "team-a"}, &authSecret))
	assert.Equal(t, "basic,apiKey", string(authSecret.Data["authModes"]))
	assert.Equal(t, "lifecycle-control", string(authSecret.Data["username"]))
	assert.Equal(t, "https", string(authSecret.Data["apiKey"]))
	assert.Equal(t, "header", string(authSecret.Data["method"]))
	assert.Equal(t, "X-Forwarded-Proto", string(authSecret.Data["keyParamName"]))
	assert.NotContains(t, authSecret.Data, "password")

	var triggerAuth kedav1alpha1.TriggerAuthentication
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-runtime-keda-metrics-auth", Namespace: "team-a"}, &triggerAuth))
	require.Len(t, triggerAuth.Spec.SecretTargetRef, 6)
	assertIntegrationAuthTargetRef(t, triggerAuth.Spec.SecretTargetRef, "authModes", "trino-runtime-keda-metrics-auth", "authModes")
	assertIntegrationAuthTargetRef(t, triggerAuth.Spec.SecretTargetRef, "username", "trino-runtime-keda-metrics-auth", "username")
	assertIntegrationAuthTargetRef(t, triggerAuth.Spec.SecretTargetRef, "password", "trino-control", "password")
	assertIntegrationAuthTargetRef(t, triggerAuth.Spec.SecretTargetRef, "apiKey", "trino-runtime-keda-metrics-auth", "apiKey")
	assertIntegrationAuthTargetRef(t, triggerAuth.Spec.SecretTargetRef, "method", "trino-runtime-keda-metrics-auth", "method")
	assertIntegrationAuthTargetRef(t, triggerAuth.Spec.SecretTargetRef, "keyParamName", "trino-runtime-keda-metrics-auth", "keyParamName")
}

func TestLeaseEnvtest_CrossReplicaAcquireGateAndRelease(t *testing.T) {
	ctx := context.Background()
	scheme := newIntegrationScheme()
	cli, stop := startIntegrationEnvtest(t, scheme)
	defer stop()

	require.NoError(t, createNamespace(ctx, cli, "leases"))

	key := apiserver.MakeRuntimeKey("team-a", "runtime")
	leaseA := apiserver.NewLeaseManager(cli, logr.Discard(), "leases", 2*time.Minute, "api-server-a")
	leaseB := apiserver.NewLeaseManager(cli, logr.Discard(), "leases", 2*time.Minute, "api-server-b")

	first, err := leaseA.AcquireLease(ctx, key, apiserver.LeaseKeyTypeRuntime)
	require.NoError(t, err)
	require.True(t, first.Acquired)
	assert.Equal(t, "api-server-a", first.Holder)

	second, err := leaseB.AcquireLease(ctx, key, apiserver.LeaseKeyTypeRuntime)
	require.NoError(t, err)
	require.False(t, second.Acquired)
	assert.Equal(t, "api-server-a", second.Holder)

	require.NoError(t, leaseB.ReleaseLease(ctx, key, apiserver.LeaseKeyTypeRuntime))
	stillGated, err := leaseB.AcquireLease(ctx, key, apiserver.LeaseKeyTypeRuntime)
	require.NoError(t, err)
	require.False(t, stillGated.Acquired, "non-holder release must not remove another replica's lease")

	require.NoError(t, leaseA.ReleaseLease(ctx, key, apiserver.LeaseKeyTypeRuntime))
	afterRelease, err := leaseB.AcquireLease(ctx, key, apiserver.LeaseKeyTypeRuntime)
	require.NoError(t, err)
	require.True(t, afterRelease.Acquired)
	assert.Equal(t, "api-server-b", afterRelease.Holder)
}

func TestLeaseEnvtest_ConcurrentReplicaRaceHasSingleWinner(t *testing.T) {
	ctx := context.Background()
	scheme := newIntegrationScheme()
	cli, stop := startIntegrationEnvtest(t, scheme)
	defer stop()

	require.NoError(t, createNamespace(ctx, cli, "leases"))

	key := apiserver.MakeRuntimeKey("team-a", "race")
	const replicas = 8

	start := make(chan struct{})
	results := make(chan apiserver.K8sLeaseResult, replicas)
	errs := make(chan error, replicas)
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			lm := apiserver.NewLeaseManager(cli, logr.Discard(), "leases", 2*time.Minute, "api-server-"+string(rune('a'+i)))
			result, err := lm.AcquireLease(ctx, key, apiserver.LeaseKeyTypeRuntime)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	acquired := 0
	gated := 0
	for result := range results {
		if result.Acquired {
			acquired++
		} else {
			gated++
			assert.NotEmpty(t, result.Holder)
		}
	}
	assert.Equal(t, 1, acquired)
	assert.Equal(t, replicas-1, gated)
}

func newIntegrationScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	utilruntime.Must(analyticsv1.AddToScheme(scheme))
	return scheme
}

func startIntegrationEnvtest(t *testing.T, scheme *runtime.Scheme, crdPaths ...string) (client.Client, func()) {
	t.Helper()

	binaryAssetsDir, err := envtest.SetupEnvtestDefaultBinaryAssetsDirectory()
	require.NoError(t, err)

	testEnv := &envtest.Environment{
		Scheme:                   scheme,
		CRDDirectoryPaths:        crdPaths,
		ErrorIfCRDPathMissing:    true,
		BinaryAssetsDirectory:    binaryAssetsDir,
		ControlPlaneStartTimeout: 60 * time.Second,
		ControlPlaneStopTimeout:  60 * time.Second,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		if isMissingEnvtestAssets(err) {
			t.Skipf("envtest control-plane binaries are not installed: %v", err)
		}
		require.NoError(t, err)
	}

	cli, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	return cli, func() {
		require.NoError(t, testEnv.Stop())
	}
}

func isMissingEnvtestAssets(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "unable to find")
}

func createNamespace(ctx context.Context, cli client.Client, namespace string) error {
	return cli.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})
}

func integrationXTrinode(name, namespace string) *analyticsv1.XTrinode {
	minWorkers := int32(0)
	maxWorkers := int32(4)
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("envtest-" + name),
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			MinWorkers: &minWorkers,
			MaxWorkers: &maxWorkers,
		},
	}
}

func workerDeployment(xtrinode *analyticsv1.XTrinode) *appsv1.Deployment {
	labels := map[string]string{"app": "trino", "xtrinode": xtrinode.Name, "role": "worker"}
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-" + xtrinode.Name + "-worker",
			Namespace: xtrinode.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "worker",
						Image: "trinodb/trino:480",
					}},
				},
			},
		},
	}
}

func kedaCRDDirectory(t *testing.T) string {
	t.Helper()

	moduleDir := os.Getenv("KEDA_MODULE_DIR")
	if moduleDir == "" {
		out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/kedacore/keda/v2").Output()
		require.NoError(t, err)
		moduleDir = strings.TrimSpace(string(out))
	}

	dir := filepath.Join(moduleDir, "config", "crd", "bases")
	_, err := os.Stat(filepath.Join(dir, "keda.sh_scaledobjects.yaml"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "keda.sh_triggerauthentications.yaml"))
	require.NoError(t, err)
	return dir
}

func boolPtr(value bool) *bool {
	return &value
}

func assertIntegrationAuthTargetRef(t *testing.T, refs []kedav1alpha1.AuthSecretTargetRef, parameter, name, key string) {
	t.Helper()
	for _, ref := range refs {
		if ref.Parameter == parameter {
			assert.Equal(t, name, ref.Name)
			assert.Equal(t, key, ref.Key)
			return
		}
	}
	t.Fatalf("missing auth target ref for parameter %q", parameter)
}
