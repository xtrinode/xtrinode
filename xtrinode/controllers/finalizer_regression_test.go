package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type gatewayServiceTestDouble struct {
	drainErr        error
	deregisterErr   error
	drainCalls      int
	deregisterCalls int
}

func (g *gatewayServiceTestDouble) RegisterRoute(_ context.Context, _ *analyticsv1.XTrinode) error {
	return nil
}

func (g *gatewayServiceTestDouble) DrainRoute(_ context.Context, _ *analyticsv1.XTrinode) error {
	g.drainCalls++
	return g.drainErr
}

func (g *gatewayServiceTestDouble) DeregisterRoute(_ context.Context, _ *analyticsv1.XTrinode) error {
	g.deregisterCalls++
	return g.deregisterErr
}

type gracefulShutdownServiceTestDouble struct {
	safeToScaleDown bool
	checkErr        error
	waitErr         error
	checkCalls      int
	waitCalls       int
}

func (g *gracefulShutdownServiceTestDouble) CheckQueriesBeforeScaleDown(_ context.Context, _ *analyticsv1.XTrinode, _ logr.Logger) (bool, error) {
	g.checkCalls++
	return g.safeToScaleDown, g.checkErr
}

func (g *gracefulShutdownServiceTestDouble) WaitForPodTermination(_ context.Context, _ *analyticsv1.XTrinode, _ logr.Logger) error {
	g.waitCalls++
	return g.waitErr
}

func TestFinalize_DrainFailureDoesNotMarkDrainStarted(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       key.Name,
			Namespace:  key.Namespace,
			Finalizers: []string{FinalizerName},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{drainErr: errors.New("route config parse failed")}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway

	result, err := reconciler.finalize(ctx, xtrinode)
	require.Error(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)
	assert.Equal(t, 1, gateway.drainCalls)
	assert.Equal(t, 0, gateway.deregisterCalls)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assert.Empty(t, updated.Annotations[config.DrainStartedAtAnnotation])
	assert.Contains(t, updated.Finalizers, FinalizerName)
}

func TestFinalize_DeregisterFailureKeepsFinalizer(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       key.Name,
			Namespace:  key.Namespace,
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-6 * time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{deregisterErr: errors.New("route config update failed")}
	graceful := &gracefulShutdownServiceTestDouble{safeToScaleDown: true}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.Error(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)
	assert.Equal(t, 0, gateway.drainCalls)
	assert.Equal(t, 1, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 1, graceful.waitCalls)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assert.Contains(t, updated.Finalizers, FinalizerName)
}

func TestFinalize_DrainCompletionAnnotationDoesNotSkipQueryRecheck(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       key.Name,
			Namespace:  key.Namespace,
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation:   time.Now().Add(-time.Minute).Format(time.RFC3339),
				config.DrainCompletedAtAnnotation: time.Now().Format(time.RFC3339),
				config.DrainResultAnnotation:      "query_complete",
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{checkErr: errors.New("coordinator query endpoint unavailable")}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Equal(t, config.GatewayDrainRequeueInterval, result.RequeueAfter)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 0, graceful.waitCalls)
	assert.Equal(t, 0, gateway.deregisterCalls)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assertDrainCompletionAnnotations(t, &updated, "query_complete")
	assert.Contains(t, updated.Finalizers, FinalizerName)
}

func TestFinalize_QueryAwareDrainCompletesBeforeFallbackWindow(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       key.Name,
			Namespace:  key.Namespace,
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{safeToScaleDown: true}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
	assert.Equal(t, 0, gateway.drainCalls)
	assert.Equal(t, 1, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 1, graceful.waitCalls)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assertDrainCompletionAnnotations(t, &updated, "query_complete")
	assert.NotContains(t, updated.Finalizers, FinalizerName)
}

func TestCleanupResourcesRetainsNodePoolWhenPolicyRetain(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			NodePool: &analyticsv1.NodePoolSpec{
				Name:           "runtime-pool",
				Provider:       "gcp",
				DeletionPolicy: analyticsv1.NodePoolDeletionPolicyRetain,
				GCP:            &analyticsv1.GCPNodePoolSpec{MachineType: "n1-standard-8"},
			},
		},
	}
	reconciler := newTestReconciler(newTestClient(scheme, xtrinode), scheme)
	reconciler.GatewayService = &gatewayServiceTestDouble{}
	nodePoolAdapter := &recordingNodePoolAdapter{}
	reconciler.NodePoolAdapter = nodePoolAdapter

	err := reconciler.cleanupResources(ctx, xtrinode, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, 0, nodePoolAdapter.deleteCalls)
	assert.Equal(t, 1, nodePoolAdapter.retainCalls)
}

func TestCleanupResourcesDeletesNodePoolWhenPolicyDelete(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			NodePool: &analyticsv1.NodePoolSpec{
				Name:           "runtime-pool",
				Provider:       "gcp",
				DeletionPolicy: analyticsv1.NodePoolDeletionPolicyDelete,
				GCP:            &analyticsv1.GCPNodePoolSpec{MachineType: "n1-standard-8"},
			},
		},
	}
	reconciler := newTestReconciler(newTestClient(scheme, xtrinode), scheme)
	reconciler.GatewayService = &gatewayServiceTestDouble{}
	nodePoolAdapter := &recordingNodePoolAdapter{}
	reconciler.NodePoolAdapter = nodePoolAdapter

	err := reconciler.cleanupResources(ctx, xtrinode, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, 1, nodePoolAdapter.deleteCalls)
}

func TestCleanupResourcesScalesNodePoolToZeroWhenPolicyScaleToZero(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			NodePool: &analyticsv1.NodePoolSpec{
				Name:           "runtime-pool",
				Provider:       "gcp",
				DeletionPolicy: analyticsv1.NodePoolDeletionPolicyScaleToZero,
				GCP:            &analyticsv1.GCPNodePoolSpec{MachineType: "n1-standard-8"},
			},
		},
	}
	reconciler := newTestReconciler(newTestClient(scheme, xtrinode), scheme)
	reconciler.GatewayService = &gatewayServiceTestDouble{}
	nodePoolAdapter := &recordingNodePoolAdapter{}
	reconciler.NodePoolAdapter = nodePoolAdapter

	err := reconciler.cleanupResources(ctx, xtrinode, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, 0, nodePoolAdapter.deleteCalls)
	assert.Equal(t, 1, nodePoolAdapter.scaleCalls)
	assert.Equal(t, []int32{0}, nodePoolAdapter.scaleValues)
	assert.Equal(t, 1, nodePoolAdapter.retainCalls)
}

func TestFinalize_QueryAwareDrainWaitsForActiveQueries(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "runtime",
			Namespace:  "team-a",
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{safeToScaleDown: false}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Equal(t, config.GatewayDrainRequeueInterval, result.RequeueAfter)
	assert.Equal(t, 0, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 0, graceful.waitCalls)
}

func TestFinalize_DrainCompletionNotFoundIsTreatedAsAlreadyFinalized(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "runtime",
			Namespace:  "team-a",
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{safeToScaleDown: true}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
	assert.Equal(t, 0, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 0, graceful.waitCalls)
}

func TestFinalize_QueryCheckErrorUsesTimeFallbackAfterDrainWindow(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       key.Name,
			Namespace:  key.Namespace,
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-config.GatewayDrainDuration - time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{checkErr: errors.New("coordinator query endpoint unavailable")}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
	assert.Equal(t, 1, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 1, graceful.waitCalls)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assertDrainCompletionAnnotations(t, &updated, "time_fallback")
}

func TestFinalize_QueryCheckErrorWaitsBeforeFallbackWindow(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "runtime",
			Namespace:  "team-a",
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{checkErr: errors.New("coordinator query endpoint unavailable")}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Equal(t, config.GatewayDrainRequeueInterval, result.RequeueAfter)
	assert.Equal(t, 0, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 0, graceful.waitCalls)
}

func assertDrainCompletionAnnotations(t *testing.T, xtrinode *analyticsv1.XTrinode, expectedResult string) {
	t.Helper()
	require.NotNil(t, xtrinode.Annotations)
	completedAt := xtrinode.Annotations[config.DrainCompletedAtAnnotation]
	require.NotEmpty(t, completedAt)
	_, err := time.Parse(time.RFC3339, completedAt)
	require.NoError(t, err)
	assert.Equal(t, expectedResult, xtrinode.Annotations[config.DrainResultAnnotation])
}

func TestFinalize_QueryCheckErrorUsesCustomDrainWindow(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "runtime",
			Namespace:  "team-a",
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				config.DrainStartedAtAnnotation: time.Now().Add(-6 * time.Minute).Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	cli := newTestClient(scheme, xtrinode)
	gateway := &gatewayServiceTestDouble{}
	graceful := &gracefulShutdownServiceTestDouble{checkErr: errors.New("coordinator query endpoint unavailable")}
	reconciler := newTestReconciler(cli, scheme)
	reconciler.GatewayService = gateway
	reconciler.GracefulShutdownService = graceful
	reconciler.DrainDuration = 10 * time.Minute
	reconciler.DrainRequeueInterval = 7 * time.Second

	result, err := reconciler.finalize(ctx, xtrinode)
	require.NoError(t, err)
	assert.Equal(t, 7*time.Second, result.RequeueAfter)
	assert.Equal(t, 0, gateway.deregisterCalls)
	assert.Equal(t, 1, graceful.checkCalls)
	assert.Equal(t, 0, graceful.waitCalls)
}
