package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCalculateNamespaceGuardrailLimitsAggregatesSharedNamespace(t *testing.T) {
	current := testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil)
	secondMaxWorkers := int32(2)
	second := testNamespaceGuardrailXTrinode("runtime-b", "team-a", "m", &secondMaxWorkers)
	otherNamespace := testNamespaceGuardrailXTrinode("runtime-c", "team-b", "xl", nil)
	reconciler := newNamespaceGuardrailReconciler(t, current, second, otherNamespace)

	limits, err := reconciler.calculateNamespaceGuardrailLimits(context.Background(), current)

	require.NoError(t, err)
	require.Equal(t, 2, limits.RuntimeCount)

	expectedCPU, expectedMemory := expectedNamespaceQuota(t, reconciler, current, second)
	requireQuantityEqual(t, expectedCPU, limits.MaxCPU)
	requireQuantityEqual(t, expectedMemory, limits.MaxMemory)
	requireQuantityEqual(t, resource.MustParse("4"), limits.WorkerCPURequest)
	requireQuantityEqual(t, resource.MustParse("16Gi"), limits.WorkerMemoryRequest)
	requireQuantityEqual(t, resource.MustParse("16"), limits.WorkerCPULimit)
	requireQuantityEqual(t, resource.MustParse("64Gi"), limits.WorkerMemoryLimit)
}

func TestCalculateNamespaceGuardrailLimitsIncludesCurrentWhenListMissesIt(t *testing.T) {
	current := testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil)
	second := testNamespaceGuardrailXTrinode("runtime-b", "team-a", "xs", nil)
	reconciler := newNamespaceGuardrailReconciler(t, second)

	limits, err := reconciler.calculateNamespaceGuardrailLimits(context.Background(), current)

	require.NoError(t, err)
	require.Equal(t, 2, limits.RuntimeCount)

	expectedCPU, expectedMemory := expectedNamespaceQuota(t, reconciler, current, second)
	requireQuantityEqual(t, expectedCPU, limits.MaxCPU)
	requireQuantityEqual(t, expectedMemory, limits.MaxMemory)
}

func TestCalculateNamespaceGuardrailLimitsUsesTypedResourcesAndFixedWorkers(t *testing.T) {
	current := testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil)
	maxWorkers := int32(3)
	current.Spec.MaxWorkers = &maxWorkers
	current.Spec.Resources = &analyticsv1.RuntimeResourcesSpec{
		Coordinator: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Worker: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
		},
	}
	reconciler := newNamespaceGuardrailReconciler(t, current)

	limits, err := reconciler.calculateNamespaceGuardrailLimits(context.Background(), current)

	require.NoError(t, err)
	require.Equal(t, 1, limits.RuntimeCount)
	requireQuantityEqual(t, resource.MustParse("34"), limits.MaxCPU)
	requireQuantityEqual(t, resource.MustParse("132Gi"), limits.MaxMemory)
	requireQuantityEqual(t, resource.MustParse("4"), limits.WorkerCPURequest)
	requireQuantityEqual(t, resource.MustParse("16Gi"), limits.WorkerMemoryRequest)
	requireQuantityEqual(t, resource.MustParse("8"), limits.WorkerCPULimit)
	requireQuantityEqual(t, resource.MustParse("32Gi"), limits.WorkerMemoryLimit)
}

func TestCalculateNamespaceGuardrailLimitsHonorsRecreateStrategyWithoutSurge(t *testing.T) {
	current := testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil)
	maxWorkers := int32(3)
	current.Spec.MaxWorkers = &maxWorkers
	current.Spec.Resources = &analyticsv1.RuntimeResourcesSpec{
		Coordinator: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		Worker: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
		},
	}
	current.Spec.ValuesOverlay = controllerValuesOverlay(t, map[string]interface{}{
		"coordinator": map[string]interface{}{
			"deployment": map[string]interface{}{
				"strategy": map[string]interface{}{
					"type": "Recreate",
				},
			},
		},
		"worker": map[string]interface{}{
			"deployment": map[string]interface{}{
				"strategy": map[string]interface{}{
					"type": "Recreate",
				},
			},
		},
	})
	reconciler := newNamespaceGuardrailReconciler(t, current)

	limits, err := reconciler.calculateNamespaceGuardrailLimits(context.Background(), current)

	require.NoError(t, err)
	requireQuantityEqual(t, resource.MustParse("25"), limits.MaxCPU)
	requireQuantityEqual(t, resource.MustParse("98Gi"), limits.MaxMemory)
}

func TestCalculateNamespaceGuardrailLimitsSkipsDeletingXTrinodes(t *testing.T) {
	current := markNamespaceGuardrailXTrinodeDeleting(testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil))
	deletingPeer := markNamespaceGuardrailXTrinodeDeleting(testNamespaceGuardrailXTrinode("runtime-b", "team-a", "m", nil))
	survivor := testNamespaceGuardrailXTrinode("runtime-c", "team-a", "xs", nil)
	otherNamespace := testNamespaceGuardrailXTrinode("runtime-d", "team-b", "xl", nil)
	reconciler := newNamespaceGuardrailReconciler(t, current, deletingPeer, survivor, otherNamespace)

	limits, err := reconciler.calculateNamespaceGuardrailLimits(context.Background(), current)

	require.NoError(t, err)
	require.Equal(t, 1, limits.RuntimeCount)
	expectedCPU, expectedMemory := expectedNamespaceQuota(t, reconciler, survivor)
	requireQuantityEqual(t, expectedCPU, limits.MaxCPU)
	requireQuantityEqual(t, expectedMemory, limits.MaxMemory)
}

func TestEnsureNamespaceWithLabelsUsesNamespaceScope(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "team-a",
			Labels: map[string]string{
				config.RuntimeLabel: "runtime-old",
			},
		},
	}
	xtrinode := testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil)
	reconciler := newNamespaceGuardrailReconciler(t, namespace, xtrinode)

	err := reconciler.ensureNamespaceWithLabels(context.Background(), xtrinode, newTestLogger())

	require.NoError(t, err)

	var updated corev1.Namespace
	err = reconciler.Get(context.Background(), client.ObjectKey{Name: "team-a"}, &updated)
	require.NoError(t, err)
	require.Equal(t, "true", updated.Labels[config.ManagedLabel])
	require.Equal(t, guardrailScopeNamespace, updated.Labels[guardrailScopeLabel])
	require.NotContains(t, updated.Labels, config.RuntimeLabel)
}

func TestBuildNamespaceGuardrailObjectsAreNamespaceScoped(t *testing.T) {
	maxCPU := resource.MustParse("228")
	maxMemory := resource.MustParse("912Gi")
	quota := buildNamespaceResourceQuota("team-a", maxCPU, maxMemory)

	require.Equal(t, namespaceResourceQuotaName, quota.Name)
	require.Equal(t, "team-a", quota.Namespace)
	require.Empty(t, quota.OwnerReferences)
	require.Equal(t, managedByXTrinodeOperator, quota.Labels[managedByLabel])
	require.Equal(t, "true", quota.Labels[config.ManagedLabel])
	require.Equal(t, guardrailScopeNamespace, quota.Labels[guardrailScopeLabel])
	require.NotContains(t, quota.Labels, config.RuntimeLabel)
	requireQuantityEqual(t, maxCPU, quota.Spec.Hard[corev1.ResourceCPU])
	requireQuantityEqual(t, maxMemory, quota.Spec.Hard[corev1.ResourceMemory])

	limitRange := buildNamespaceLimitRange(
		"team-a",
		resource.MustParse("4"),
		resource.MustParse("16Gi"),
		resource.MustParse("16"),
		resource.MustParse("64Gi"),
	)

	require.Equal(t, namespaceLimitRangeName, limitRange.Name)
	require.Equal(t, "team-a", limitRange.Namespace)
	require.Empty(t, limitRange.OwnerReferences)
	require.NotContains(t, limitRange.Labels, config.RuntimeLabel)
	require.Len(t, limitRange.Spec.Limits, 1)
	item := limitRange.Spec.Limits[0]
	require.Equal(t, corev1.LimitTypeContainer, item.Type)
	requireQuantityEqual(t, resource.MustParse("4"), item.DefaultRequest[corev1.ResourceCPU])
	requireQuantityEqual(t, resource.MustParse("16Gi"), item.DefaultRequest[corev1.ResourceMemory])
	requireQuantityEqual(t, resource.MustParse("16"), item.Default[corev1.ResourceCPU])
	requireQuantityEqual(t, resource.MustParse("64Gi"), item.Default[corev1.ResourceMemory])
	requireQuantityEqual(t, resource.MustParse("16"), item.Max[corev1.ResourceCPU])
	requireQuantityEqual(t, resource.MustParse("64Gi"), item.Max[corev1.ResourceMemory])
}

func TestReconcileNamespaceGuardrailsAfterDeleteRemovesGuardrailsWhenLastRuntime(t *testing.T) {
	xtrinode := markNamespaceGuardrailXTrinodeDeleting(testNamespaceGuardrailXTrinode("runtime-a", "team-a", "s", nil))
	sharedQuota := buildNamespaceResourceQuota("team-a", resource.MustParse("20"), resource.MustParse("80Gi"))
	sharedLimitRange := buildNamespaceLimitRange(
		"team-a",
		resource.MustParse("1"),
		resource.MustParse("4Gi"),
		resource.MustParse("2"),
		resource.MustParse("8Gi"),
	)
	reconciler := newNamespaceGuardrailReconciler(t, xtrinode, sharedQuota, sharedLimitRange)

	err := reconciler.reconcileNamespaceGuardrailsAfterDelete(context.Background(), xtrinode, newTestLogger())

	require.NoError(t, err)
	requireDeleted(t, reconciler, sharedQuota)
	requireDeleted(t, reconciler, sharedLimitRange)
}

func TestDeleteNamespaceGuardrailResourcesIsIdempotent(t *testing.T) {
	reconciler := newNamespaceGuardrailReconciler(t)

	err := reconciler.deleteNamespaceGuardrailResources(context.Background(), "team-a", newTestLogger())

	require.NoError(t, err)
}

func newNamespaceGuardrailReconciler(t *testing.T, objects ...client.Object) *XTrinodeReconciler {
	t.Helper()
	scheme := newTestScheme()
	return newTestReconciler(newTestClient(scheme, objects...), scheme)
}

func testNamespaceGuardrailXTrinode(name, namespace, size string, maxWorkers *int32) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       size,
			MaxWorkers: maxWorkers,
		},
	}
}

func markNamespaceGuardrailXTrinodeDeleting(xtrinode *analyticsv1.XTrinode) *analyticsv1.XTrinode {
	now := metav1.Now()
	xtrinode.DeletionTimestamp = &now
	xtrinode.Finalizers = []string{FinalizerName}
	return xtrinode
}

func expectedNamespaceQuota(t *testing.T, reconciler *XTrinodeReconciler, xtrinodes ...*analyticsv1.XTrinode) (expectedCPU, expectedMemory resource.Quantity) {
	t.Helper()
	expectedCPU = resource.MustParse("0")
	expectedMemory = resource.MustParse("0")
	for _, xtrinode := range xtrinodes {
		shape, err := runtimeshape.Resolve(xtrinode)
		require.NoError(t, err)
		maxCPU, maxMemory := shapeQuotaLimits(xtrinode, shape)
		expectedCPU.Add(maxCPU)
		expectedMemory.Add(maxMemory)
	}
	return expectedCPU, expectedMemory
}

func requireQuantityEqual(t *testing.T, expected, actual resource.Quantity) {
	t.Helper()
	require.Equal(t, 0, actual.Cmp(expected), "expected %s, got %s", expected.String(), actual.String())
}

func requireDeleted(t *testing.T, reconciler *XTrinodeReconciler, object client.Object) {
	t.Helper()
	err := reconciler.Get(context.Background(), client.ObjectKeyFromObject(object), object)
	require.Error(t, err)
}
