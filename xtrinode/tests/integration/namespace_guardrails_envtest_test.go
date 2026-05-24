//go:build integration

package integration

import (
	"context"
	"path/filepath"
	stdlibRuntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/controllers"
	"github.com/xtrinode/xtrinode/internal/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNamespaceGuardrailsEnvtest_ConcurrentPeersAndDeletionRecalculateSharedResources(t *testing.T) {
	ctx := context.Background()
	scheme := newIntegrationScheme()
	cli, stop := startIntegrationEnvtest(t, scheme, xtrinodeCRDDirectory(t))
	defer stop()

	const namespace = "team-guardrail-race"
	require.NoError(t, createNamespace(ctx, cli, namespace))
	require.NoError(t, createNamespace(ctx, cli, "xtrinode-gateway"))

	reconciler := newIntegrationXTrinodeReconciler(cli, scheme)
	first := namespaceGuardrailIntegrationXTrinode("guardrail-a", namespace)
	second := namespaceGuardrailIntegrationXTrinode("guardrail-b", namespace)
	require.NoError(t, cli.Create(ctx, first))
	require.NoError(t, cli.Create(ctx, second))

	reconcileConcurrently(t, ctx, reconciler, namespace, first.Name, second.Name)
	requireNamespaceQuotaEventually(t, ctx, cli, namespace, "16", "64Gi")
	requireNamespaceLimitRangeEventually(t, ctx, cli, namespace, "2", "8Gi")

	deleteThroughFinalizer(t, ctx, cli, reconciler, namespace, first.Name)
	requireNamespaceQuotaEventually(t, ctx, cli, namespace, "8", "32Gi")
	requireNamespaceLimitRangeEventually(t, ctx, cli, namespace, "2", "8Gi")

	deleteThroughFinalizer(t, ctx, cli, reconciler, namespace, second.Name)
	requireNamespaceGuardrailDeletedEventually(t, ctx, cli, &corev1.ResourceQuota{}, namespace, "xtrinode-namespace-quota")
	requireNamespaceGuardrailDeletedEventually(t, ctx, cli, &corev1.LimitRange{}, namespace, "xtrinode-namespace-limits")
}

func namespaceGuardrailIntegrationXTrinode(name, namespace string) *analyticsv1.XTrinode {
	minWorkers := int32(0)
	maxWorkers := int32(1)
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "xs",
			MinWorkers: &minWorkers,
			MaxWorkers: &maxWorkers,
			Routing: &analyticsv1.RoutingSpec{
				Header:       "X-Trino-XTrinode=" + namespace + "/" + name,
				RoutingGroup: name,
			},
		},
	}
}

func newIntegrationXTrinodeReconciler(cli client.Client, scheme *k8sruntime.Scheme) *controllers.XTrinodeReconciler {
	logger := logr.Discard()
	recorder := events.NewRecorder(record.NewFakeRecorder(100), events.DefaultConfig())
	return &controllers.XTrinodeReconciler{
		Client:                  cli,
		Scheme:                  scheme,
		EventRecorder:           recorder,
		NodePoolAdapter:         controllers.NewNodePoolAdapter(cli, logger),
		GatewayService:          controllers.NewGatewayService(cli),
		KEDAService:             controllers.NewKEDAService(cli, scheme),
		CatalogService:          controllers.NewCatalogService(cli),
		TrinoResourcesService:   controllers.NewTrinoResourcesService(cli, scheme, "integration-test"),
		AutosuspendService:      controllers.NewAutosuspendService(cli),
		GracefulShutdownService: controllers.NewGracefulShutdownService(cli),
		OperatorVersion:         "integration-test",
	}
}

func reconcileConcurrently(t *testing.T, ctx context.Context, reconciler *controllers.XTrinodeReconciler, namespace string, names ...string) {
	t.Helper()
	for round := 0; round < 2; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, len(names))
		for _, name := range names {
			name := name
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}})
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
	}
}

func deleteThroughFinalizer(t *testing.T, ctx context.Context, cli client.Client, reconciler *controllers.XTrinodeReconciler, namespace, name string) {
	t.Helper()
	var xtrinode analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &xtrinode))
	require.NoError(t, cli.Delete(ctx, &xtrinode))

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}})
	require.NoError(t, err)

	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &xtrinode))
	if xtrinode.Annotations == nil {
		xtrinode.Annotations = map[string]string{}
	}
	xtrinode.Annotations["xtrinode.analytics.xtrinode.io/drain-started-at"] = "2000-01-01T00:00:00Z"
	require.NoError(t, cli.Update(ctx, &xtrinode))

	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var deleted analyticsv1.XTrinode
		err := cli.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &deleted)
		return errors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond)
}

func requireNamespaceQuotaEventually(t *testing.T, ctx context.Context, cli client.Client, namespace, cpu, memory string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var quota corev1.ResourceQuota
		if err := cli.Get(ctx, types.NamespacedName{Name: "xtrinode-namespace-quota", Namespace: namespace}, &quota); err != nil {
			return false
		}
		return quantityEqual(quota.Spec.Hard[corev1.ResourceCPU], cpu) &&
			quantityEqual(quota.Spec.Hard[corev1.ResourceMemory], memory)
	}, 10*time.Second, 100*time.Millisecond)
}

func requireNamespaceLimitRangeEventually(t *testing.T, ctx context.Context, cli client.Client, namespace, cpu, memory string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var limitRange corev1.LimitRange
		if err := cli.Get(ctx, types.NamespacedName{Name: "xtrinode-namespace-limits", Namespace: namespace}, &limitRange); err != nil {
			return false
		}
		if len(limitRange.Spec.Limits) != 1 {
			return false
		}
		item := limitRange.Spec.Limits[0]
		return quantityEqual(item.Default[corev1.ResourceCPU], cpu) &&
			quantityEqual(item.Default[corev1.ResourceMemory], memory) &&
			quantityEqual(item.Max[corev1.ResourceCPU], cpu) &&
			quantityEqual(item.Max[corev1.ResourceMemory], memory)
	}, 10*time.Second, 100*time.Millisecond)
}

func requireNamespaceGuardrailDeletedEventually(t *testing.T, ctx context.Context, cli client.Client, object client.Object, namespace, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		err := cli.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, object)
		return errors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond)
}

func quantityEqual(actual resource.Quantity, expected string) bool {
	expectedQuantity := resource.MustParse(expected)
	return actual.Cmp(expectedQuantity) == 0
}

func xtrinodeCRDDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := stdlibRuntime.Caller(0)
	require.True(t, ok)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "helm", "xtrinode-operator", "crds")
	require.FileExists(t, filepath.Join(dir, "analytics.xtrinode.io_xtrinodes.yaml"))
	return dir
}
