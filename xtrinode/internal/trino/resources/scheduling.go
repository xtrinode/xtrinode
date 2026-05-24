package resources

import (
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
	corev1 "k8s.io/api/core/v1"
)

func applySchedulingShape(podSpec *corev1.PodSpec, shape runtimeshape.SchedulingShape) {
	if len(shape.NodeSelector) > 0 {
		podSpec.NodeSelector = copySchedulingStringMap(shape.NodeSelector)
	}
	if len(shape.Tolerations) > 0 {
		podSpec.Tolerations = append([]corev1.Toleration(nil), shape.Tolerations...)
	}
	if shape.Affinity != nil {
		podSpec.Affinity = shape.Affinity.DeepCopy()
	}
	if len(shape.TopologySpreadConstraints) > 0 {
		podSpec.TopologySpreadConstraints = append([]corev1.TopologySpreadConstraint(nil), shape.TopologySpreadConstraints...)
	}
}

func copySchedulingStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
