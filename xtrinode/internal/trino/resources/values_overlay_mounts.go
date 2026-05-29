package resources

import (
	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// addConfigAndSecretMounts adds configMounts and secretMounts from valuesOverlay
func addConfigAndSecretMounts(container *corev1.Container, xtrinode *analyticsv1.XTrinode, role string) {
	values := xtrinode.Spec.GetValuesOverlayMap()
	if values == nil {
		return
	}

	appendValuesOverlayConfigVolumeMounts(container, configMountsFromOverlay(values))
	appendValuesOverlayConfigVolumeMounts(container, roleConfigMountsFromOverlay(xtrinode, role))
	appendValuesOverlaySecretVolumeMounts(container, secretMountsFromOverlay(values))
	appendValuesOverlaySecretVolumeMounts(container, roleSecretMountsFromOverlay(xtrinode, role))
}
