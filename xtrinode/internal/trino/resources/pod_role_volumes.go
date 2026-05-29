package resources

import (
	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// addRoleSpecificSecretVolumes adds role-specific secret volumes from HelmChartConfig
func addRoleSpecificSecretVolumes(volumes *[]corev1.Volume, xtrinode *analyticsv1.XTrinode, role string) {
	if xtrinode.Spec.HelmChartConfig == nil {
		return
	}

	var secretMounts []analyticsv1.SecretMountSpec

	switch role {
	case "coordinator":
		if xtrinode.Spec.HelmChartConfig.Coordinator != nil {
			secretMounts = xtrinode.Spec.HelmChartConfig.Coordinator.SecretMounts
		}
	case "worker":
		if xtrinode.Spec.HelmChartConfig.Worker != nil {
			secretMounts = xtrinode.Spec.HelmChartConfig.Worker.SecretMounts
		}
	}

	for _, secretMount := range secretMounts {
		*volumes = append(*volumes, corev1.Volume{
			Name: secretMount.Name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretMount.SecretName,
				},
			},
		})
	}
}

// addRoleSpecificConfigVolumes adds role-specific config volumes from valuesOverlay
func addRoleSpecificConfigVolumes(volumes *[]corev1.Volume, xtrinode *analyticsv1.XTrinode, role string) {
	appendValuesOverlayConfigVolumes(volumes, roleConfigMountsFromOverlay(xtrinode, role))
}

// addRoleSpecificSecretVolumesFromOverlay adds role-specific secret volumes from valuesOverlay
func addRoleSpecificSecretVolumesFromOverlay(volumes *[]corev1.Volume, xtrinode *analyticsv1.XTrinode, role string) {
	appendValuesOverlaySecretVolumes(volumes, roleSecretMountsFromOverlay(xtrinode, role))
}
