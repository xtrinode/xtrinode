package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// BuildResourceGroupsConfigMapCoordinator builds the resource groups ConfigMap for coordinator
// Helm chart: configmap-coordinator.yaml:181-194 (separate ConfigMap when type == "configmap")
func BuildResourceGroupsConfigMapCoordinator(xtrinode *analyticsv1.XTrinode) *corev1.ConfigMap {
	// A named resourceGroupsProfile is user-provided and mounted directly; the operator
	// only creates the inline valuesOverlay resourceGroups.type=configmap variant.
	if xtrinode.Spec.ResourceGroupsProfile != "" {
		return nil
	}

	// Check if resource groups type is configmap
	resourceGroupsConfig := ""
	typeIsConfigMap := false

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if resourceGroups, ok := xtrinode.Spec.GetValuesOverlayMap()["resourceGroups"].(map[string]interface{}); ok {
			if rgType, ok := resourceGroups["type"].(string); ok && rgType == "configmap" {
				typeIsConfigMap = true
				if config, ok := resourceGroups["resourceGroupsConfig"].(string); ok {
					resourceGroupsConfig = config
				}
			}
		}
	}

	if !typeIsConfigMap || resourceGroupsConfig == "" {
		return nil
	}

	labels := TrinoLabels(xtrinode)
	labels[AppComponentLabel] = "coordinator"

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("trino-%s-resource-groups-volume-coordinator", xtrinode.Name),
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Data: map[string]string{
			"resource-groups.json": resourceGroupsConfig,
		},
	}
}

// BuildResourceGroupsConfigMapWorker returns nil because upstream Trino wires
// resource groups only on the coordinator.
func BuildResourceGroupsConfigMapWorker(_ *analyticsv1.XTrinode) *corev1.ConfigMap {
	return nil
}
