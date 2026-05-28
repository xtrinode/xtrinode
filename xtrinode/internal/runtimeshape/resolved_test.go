package runtimeshape

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolvePresetOnlyFixedRuntime(t *testing.T) {
	xtrinode := runtimeShapeXTrinode("runtime", "s")

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, "s", shape.PresetName)
	require.Equal(t, AutoscalingModeFixed, shape.AutoscalingMode)
	require.NotNil(t, shape.FixedWorkers)
	require.Equal(t, int32(2), *shape.FixedWorkers)
	require.Equal(t, int32(2), shape.QuotaWorkers)
	require.Equal(t, int32(2), shape.CapacityWorkers)
	require.Equal(t, int32(2), shape.CapacityUnits)
	require.Equal(t, SourcePreset, shape.Source.WorkerResources)
	require.NotEmpty(t, shape.Hash)
	workerCPU := shape.Worker.Requests[corev1.ResourceCPU]
	require.Equal(t, "2", workerCPU.String())
}

func TestResolveTypedResourcesAndFixedWorkers(t *testing.T) {
	maxWorkers := int32(3)
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.MaxWorkers = &maxWorkers
	xtrinode.Spec.Resources = &analyticsv1.RuntimeResourcesSpec{
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

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, SourceTyped, shape.Source.WorkerResources)
	require.Equal(t, SourceTyped, shape.Source.WorkerCount)
	require.Equal(t, int32(3), *shape.FixedWorkers)
	require.Equal(t, int32(6), shape.CapacityUnits)
	require.Equal(t, resource.MustParse("4"), shape.Worker.Requests[corev1.ResourceCPU])
	require.Equal(t, resource.MustParse("32Gi"), shape.Worker.Limits[corev1.ResourceMemory])
}

func TestResolveKEDAUsesMaxWorkersForQuotaAndCapacity(t *testing.T) {
	maxWorkers := int32(6)
	minWorkers := int32(1)
	prometheusQuery := "sum(xtrinode_gateway_inflight_queries)"
	enabled := true
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.MinWorkers = &minWorkers
	xtrinode.Spec.MaxWorkers = &maxWorkers
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled:         &enabled,
		PrometheusQuery: &prometheusQuery,
	}

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, AutoscalingModeKEDA, shape.AutoscalingMode)
	require.Nil(t, shape.FixedWorkers)
	require.Equal(t, int32(1), shape.MinWorkers)
	require.Equal(t, int32(6), shape.MaxWorkers)
	require.Equal(t, int32(6), shape.QuotaWorkers)
	require.Equal(t, int32(6), shape.CapacityWorkers)
	require.Equal(t, int32(6), shape.CapacityUnits)
}

func TestResolveKEDAWithoutMetricConfigUsesFixedWorkers(t *testing.T) {
	enabled := true
	maxWorkers := int32(4)
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{Enabled: &enabled}
	xtrinode.Spec.MaxWorkers = &maxWorkers

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, AutoscalingModeFixed, shape.AutoscalingMode)
	require.NotNil(t, shape.FixedWorkers)
	require.Equal(t, int32(4), *shape.FixedWorkers)
	require.Equal(t, int32(4), shape.QuotaWorkers)
	require.Equal(t, int32(4), shape.CapacityWorkers)
	require.Equal(t, SourceTyped, shape.Source.WorkerCount)
}

func TestResolveTypedRoutingCapacityOverride(t *testing.T) {
	capacityUnits := int32(9)
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.Routing = &analyticsv1.RoutingSpec{CapacityUnits: &capacityUnits}

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, int32(9), shape.CapacityUnits)
	require.Equal(t, SourceTyped, shape.Source.Capacity)
}

func TestResolveNodePoolSchedulePodsAddsPlacementSelectorAndStatus(t *testing.T) {
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.NodePool = &analyticsv1.NodePoolSpec{
		Name:         "runtime-pool",
		Provider:     "gcp",
		SchedulePods: true,
		GCP:          &analyticsv1.GCPNodePoolSpec{MachineType: "n1-standard-8"},
	}

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, "runtime-pool", shape.Placement.Coordinator.NodeSelector[config.NodePoolSchedulingLabel])
	require.Equal(t, "runtime-pool", shape.Placement.Worker.NodeSelector[config.NodePoolSchedulingLabel])
	status := shape.ObservedStatus()
	require.Equal(t, analyticsv1.ObservedRuntimeShapeStatusVersion, status.Version)
	require.Equal(t, shape.Hash, status.Hash)
	require.True(t, status.NodePool.ProvisioningRequested)
	require.True(t, status.NodePool.SchedulePods)
	require.Equal(t, analyticsv1.NodePoolDeletionPolicyDelete, status.NodePool.DeletionPolicy)
}

func TestResolveExistingNodePoolPlacementExpandsProviderSelector(t *testing.T) {
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.Placement = &analyticsv1.PlacementSpec{
		ExistingNodePool: &analyticsv1.ExistingNodePoolPlacementSpec{
			Provider: "gcp",
			Name:     "analytics-pool",
		},
	}

	shape, err := Resolve(xtrinode)

	require.NoError(t, err)
	require.Equal(t, "analytics-pool", shape.Placement.Coordinator.NodeSelector["cloud.google.com/gke-nodepool"])
	require.Equal(t, "analytics-pool", shape.Placement.Worker.NodeSelector["cloud.google.com/gke-nodepool"])
}

func TestResolveExistingNodePoolPlacementRejectsSelectorConflict(t *testing.T) {
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.Placement = &analyticsv1.PlacementSpec{
		NodeSelector: map[string]string{
			"cloud.google.com/gke-nodepool": "other-pool",
		},
		ExistingNodePool: &analyticsv1.ExistingNodePoolPlacementSpec{
			Provider: "gcp",
			Name:     "analytics-pool",
		},
	}

	_, err := Resolve(xtrinode)

	require.Error(t, err)
	require.Contains(t, err.Error(), "existingNodePool")
}

func TestObservedStatusStaysCompactWithLargePlacementInput(t *testing.T) {
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	terms := make([]corev1.NodeSelectorTerm, 0, 50)
	for i := 0; i < 50; i++ {
		terms = append(terms, corev1.NodeSelectorTerm{
			MatchExpressions: []corev1.NodeSelectorRequirement{
				{Key: "topology.example.com/zone", Operator: corev1.NodeSelectorOpIn, Values: []string{"a", "b", "c"}},
			},
		})
	}
	xtrinode.Spec.Placement = &analyticsv1.PlacementSpec{
		Affinity: &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: terms},
			},
		},
	}

	shape, err := Resolve(xtrinode)
	require.NoError(t, err)
	payload, err := json.Marshal(shape.ObservedStatus())
	require.NoError(t, err)

	require.Less(t, len(payload), 2048)
	require.NotContains(t, string(payload), "topology.example.com/zone")
}

func TestResolveNodePoolSchedulePodsRejectsConflictingSelector(t *testing.T) {
	xtrinode := runtimeShapeXTrinode("runtime", "s")
	xtrinode.Spec.NodePool = &analyticsv1.NodePoolSpec{
		Name:         "runtime-pool",
		Provider:     "gcp",
		SchedulePods: true,
		GCP:          &analyticsv1.GCPNodePoolSpec{MachineType: "n1-standard-8"},
	}
	xtrinode.Spec.Placement = &analyticsv1.PlacementSpec{
		NodeSelector: map[string]string{
			config.NodePoolSchedulingLabel: "other-pool",
		},
	}

	_, err := Resolve(xtrinode)

	require.Error(t, err)
	require.Contains(t, err.Error(), "nodePool.schedulePods")
}

func runtimeShapeXTrinode(name, size string) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: size,
		},
	}
}
