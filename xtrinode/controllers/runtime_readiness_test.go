package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/pkg/gateway"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCheckTrinoRuntimeReadyCoordinatorDeploymentMissing(t *testing.T) {
	reconciler := newRuntimeReadinessReconciler(t)
	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), testRuntimeReadinessXTrinode(nil))

	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Equal(t, "CoordinatorDeploymentMissing", readiness.Reason)
}

func TestCheckTrinoRuntimeReadyRequiresCoordinatorEndpoint(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(nil)
	reconciler := newRuntimeReadinessReconciler(t, testRuntimeReadinessDeployment(
		config.BuildCoordinatorDeploymentName(xtrinode.Name),
		xtrinode.Namespace,
		1,
		1,
		1,
		int32Ptr(1),
	))

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Equal(t, "CoordinatorEndpointsNotReady", readiness.Reason)
}

func TestCheckTrinoRuntimeReadyRequiresWorkerFloor(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(int32Ptr(2))
	worker := testRuntimeReadinessDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(2))
	worker.Status.UpdatedReplicas = 2
	reconciler := newRuntimeReadinessReconciler(
		t,
		testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
		testRuntimeReadinessEndpoint(xtrinode),
		worker,
	)

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Equal(t, "WorkerDeploymentNotReady", readiness.Reason)
	require.Equal(t, int32(2), readiness.RequiredWorkers)
	require.Equal(t, int32(1), readiness.WorkerReadyReplicas)
}

func TestCheckTrinoRuntimeReadyRejectsFailedCoordinatorRollout(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(nil)
	coordinator := testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1))
	coordinator.Status.Conditions = []appsv1.DeploymentCondition{
		{
			Type:    appsv1.DeploymentProgressing,
			Status:  corev1.ConditionFalse,
			Reason:  "ProgressDeadlineExceeded",
			Message: "ReplicaSet has timed out progressing.",
		},
	}
	reconciler := newRuntimeReadinessReconciler(t, coordinator, testRuntimeReadinessEndpoint(xtrinode))

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Equal(t, "CoordinatorRolloutFailed", readiness.Reason)
}

func TestCheckTrinoRuntimeReadyRequiresCurrentWorkerRevision(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(int32Ptr(2))
	worker := testRuntimeReadinessDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 2, 2, int32Ptr(2))
	worker.Status.UpdatedReplicas = 1
	reconciler := newRuntimeReadinessReconciler(
		t,
		testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
		testRuntimeReadinessEndpoint(xtrinode),
		worker,
	)

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Equal(t, "WorkerRolloutPending", readiness.Reason)
}

func TestCheckTrinoRuntimeReadyReadyWithRequiredWorkers(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(int32Ptr(1))
	reconciler := newRuntimeReadinessReconciler(
		t,
		testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
		testRuntimeReadinessEndpoint(xtrinode),
		testRuntimeReadinessDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
	)

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.True(t, readiness.Ready)
	require.Equal(t, int32(1), readiness.RequiredWorkers)
	require.Equal(t, int32(1), readiness.WorkerReadyReplicas)
}

func TestCheckTrinoRuntimeReadyIgnoresKEDATransientScaleUpReplicaCount(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(nil)
	enabled := true
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled:       &enabled,
		ScalerType:    "prometheus",
		ScalingMetric: "query",
	}
	reconciler := newRuntimeReadinessReconciler(
		t,
		testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
		testRuntimeReadinessEndpoint(xtrinode),
		testRuntimeReadinessDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(2)),
	)

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.True(t, readiness.Ready)
	require.Equal(t, int32(0), readiness.RequiredWorkers)
	require.Equal(t, int32(1), readiness.WorkerReadyReplicas)
}

func TestCheckTrinoRuntimeReadyRequiresNativeHPAFloor(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(nil)
	xtrinode.Spec.ValuesOverlay = controllerValuesOverlay(t, map[string]interface{}{
		"server": map[string]interface{}{
			"autoscaling": map[string]interface{}{
				"enabled":                           true,
				"minReplicas":                       int64(2),
				"maxReplicas":                       int64(4),
				"targetCPUUtilizationPercentage":    int64(70),
				"targetMemoryUtilizationPercentage": "",
			},
		},
	})
	worker := testRuntimeReadinessDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, nil)
	worker.Status.UpdatedReplicas = 2
	reconciler := newRuntimeReadinessReconciler(
		t,
		testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
		testRuntimeReadinessEndpoint(xtrinode),
		worker,
	)

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Equal(t, "WorkerDeploymentNotReady", readiness.Reason)
	require.Equal(t, int32(2), readiness.RequiredWorkers)
	require.Equal(t, int32(1), readiness.WorkerReadyReplicas)
}

func TestCheckTrinoRuntimeReadyReadyWhenNoWorkersRequired(t *testing.T) {
	xtrinode := testRuntimeReadinessXTrinode(nil)
	reconciler := newRuntimeReadinessReconciler(
		t,
		testRuntimeReadinessDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1, 1, 1, int32Ptr(1)),
		testRuntimeReadinessEndpoint(xtrinode),
	)

	readiness, err := reconciler.checkTrinoRuntimeReady(context.Background(), xtrinode)

	require.NoError(t, err)
	require.True(t, readiness.Ready)
	require.Equal(t, int32(0), readiness.RequiredWorkers)
	require.Equal(t, int32(0), readiness.WorkerReadyReplicas)
}

func TestSyncPendingGatewayRoutePublishesResumingBackend(t *testing.T) {
	ctx := context.Background()
	xtrinode := testRuntimeReadinessXTrinode(nil)
	xtrinode.Status.Phase = string(status.PhaseReconciling)
	reconciler := newRuntimeReadinessReconciler(t)

	err := reconciler.syncPendingGatewayRoute(ctx, xtrinode, trinoRuntimeReadiness{
		Message: "Coordinator service has no ready HTTP endpoints",
	})

	require.NoError(t, err)
	require.Equal(t, gateway.StateResuming, readRuntimeReadinessGatewayBackend(t, ctx, reconciler).State)
	gatewayCondition := status.GetCondition(xtrinode, status.ConditionTypeGatewayReady)
	require.NotNil(t, gatewayCondition)
	require.Equal(t, metav1.ConditionFalse, gatewayCondition.Status)
	require.Equal(t, status.ConditionReasonRuntimeNotReady, gatewayCondition.Reason)
}

func TestReconcileReadyGatewayRoutePublishesRunningBackend(t *testing.T) {
	ctx := context.Background()
	xtrinode := testRuntimeReadinessXTrinode(nil)
	xtrinode.Status.Phase = string(status.PhaseReconciling)
	reconciler := newRuntimeReadinessReconciler(t)

	err := reconciler.reconcileReadyGatewayRoute(ctx, xtrinode)

	require.NoError(t, err)
	require.Equal(t, gateway.StateRunning, readRuntimeReadinessGatewayBackend(t, ctx, reconciler).State)
	gatewayCondition := status.GetCondition(xtrinode, status.ConditionTypeGatewayReady)
	require.NotNil(t, gatewayCondition)
	require.Equal(t, metav1.ConditionTrue, gatewayCondition.Status)
}

func TestUpdateStatusPreservesFreshLastActivityWhenCallerStatusIsStale(t *testing.T) {
	ctx := context.Background()
	lastActivity := metav1.NewTime(time.Now().Add(-30 * time.Second).UTC())
	stored := testRuntimeReadinessXTrinode(nil)
	stored.Status.LastActivity = &lastActivity

	stale := testRuntimeReadinessXTrinode(nil)
	stale.Status.Phase = string(status.PhaseReady)

	scheme := newTestScheme()
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(stored).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		Build()
	reconciler := newTestReconciler(cli, scheme)
	require.NoError(t, reconciler.updateStatus(ctx, stale, newTestLogger()))

	var updated analyticsv1.XTrinode
	require.NoError(t, reconciler.Get(ctx, client.ObjectKeyFromObject(stored), &updated))
	require.NotNil(t, updated.Status.LastActivity)
	require.WithinDuration(t, lastActivity.Time, updated.Status.LastActivity.Time, time.Second)
}

func newRuntimeReadinessReconciler(t *testing.T, objects ...client.Object) *XTrinodeReconciler {
	t.Helper()
	scheme := newTestScheme()
	return newTestReconciler(newTestClient(scheme, objects...), scheme)
}

func testRuntimeReadinessXTrinode(minWorkers *int32) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			MinWorkers: minWorkers,
		},
	}
}

func readRuntimeReadinessGatewayBackend(t *testing.T, ctx context.Context, reconciler *XTrinodeReconciler) gateway.Backend {
	t.Helper()
	configMap := &corev1.ConfigMap{}
	err := reconciler.Get(ctx, client.ObjectKey{Name: gateway.GatewayConfigMapName, Namespace: gateway.GatewayConfigMapNamespace}, configMap)
	require.NoError(t, err)

	var routeConfig struct {
		Routes []gateway.RouteEntry `yaml:"routes"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(configMap.Data[gateway.GatewayConfigMapKey]), &routeConfig))
	require.Len(t, routeConfig.Routes, 1)
	require.Len(t, routeConfig.Routes[0].Backends, 1)
	return routeConfig.Routes[0].Backends[0]
}

func testRuntimeReadinessDeployment(name, namespace string, generation int64, readyReplicas, availableReplicas int32, replicas *int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: generation,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: generation,
			UpdatedReplicas:    readyReplicas,
			ReadyReplicas:      readyReplicas,
			AvailableReplicas:  availableReplicas,
		},
	}
}

func testRuntimeReadinessEndpoint(xtrinode *analyticsv1.XTrinode) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.BuildCoordinatorServiceName(xtrinode.Name) + "-slice",
			Namespace: xtrinode.Namespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: config.BuildCoordinatorServiceName(xtrinode.Name),
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{"10.0.0.1"},
				Conditions: discoveryv1.EndpointConditions{
					Ready: boolPtr(true),
				},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Name: stringPtr("http"), Port: int32Ptr(config.TrinoPortHTTP)},
		},
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func stringPtr(v string) *string {
	return &v
}
