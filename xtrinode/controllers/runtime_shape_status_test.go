package controllers

import (
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetObservedRuntimeShapeStatusWorksForSuspendedRuntime(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			Suspended:  true,
			MaxWorkers: int32Ptr(3),
			Resources: &analyticsv1.RuntimeResourcesSpec{
				Worker: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1536Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
		},
	}

	err := setObservedRuntimeShapeStatus(xtrinode)

	require.NoError(t, err)
	require.NotNil(t, xtrinode.Status.ObservedRuntimeShape)
	require.NotEmpty(t, xtrinode.Status.ObservedRuntimeShape.Hash)
	require.Equal(t, "s", xtrinode.Status.ObservedRuntimeShape.Preset)
	require.Equal(t, "fixed", xtrinode.Status.ObservedRuntimeShape.AutoscalingMode)
	require.NotNil(t, xtrinode.Status.ObservedRuntimeShape.Workers.Fixed)
	require.Equal(t, int32(3), *xtrinode.Status.ObservedRuntimeShape.Workers.Fixed)
	require.Equal(t, int32(3), xtrinode.Status.ObservedRuntimeShape.Workers.Quota)
	require.Equal(t, int32(1), xtrinode.Status.ObservedRuntimeShape.CapacityUnits)
	requireQuantityEqual(t, resource.MustParse("500m"), xtrinode.Status.ObservedRuntimeShape.Worker.Requests[corev1.ResourceCPU])
	requireQuantityEqual(t, resource.MustParse("2Gi"), xtrinode.Status.ObservedRuntimeShape.Worker.Limits[corev1.ResourceMemory])
}
