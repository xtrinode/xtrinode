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
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	var roleConfig map[string]interface{}
	var ok bool

	switch role {
	case "coordinator":
		roleConfig, ok = xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{})
	case "worker":
		roleConfig, ok = xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{})
	default:
		return
	}

	if !ok {
		return
	}

	if configMounts, ok := roleConfig["configMounts"].([]interface{}); ok {
		for _, cm := range configMounts {
			cmMap, ok := cm.(map[string]interface{})
			if !ok {
				continue
			}
			//nolint:errcheck // best-effort type assertion; validated by empty string check below
			name, _ := cmMap["name"].(string)
			//nolint:errcheck // best-effort type assertion; validated by empty string check below
			configMap, _ := cmMap["configMap"].(string)
			if name != "" && configMap != "" {
				*volumes = append(*volumes, corev1.Volume{
					Name: name,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: configMap,
							},
						},
					},
				})
			}
		}
	}
}

// addRoleSpecificSecretVolumesFromOverlay adds role-specific secret volumes from valuesOverlay
func addRoleSpecificSecretVolumesFromOverlay(volumes *[]corev1.Volume, xtrinode *analyticsv1.XTrinode, role string) {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	var roleConfig map[string]interface{}
	var ok bool

	switch role {
	case "coordinator":
		roleConfig, ok = xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{})
	case "worker":
		roleConfig, ok = xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{})
	default:
		return
	}

	if !ok {
		return
	}

	if secretMounts, ok := roleConfig["secretMounts"].([]interface{}); ok {
		for _, sm := range secretMounts {
			smMap, ok := sm.(map[string]interface{})
			if !ok {
				continue
			}
			//nolint:errcheck // best-effort type assertion; validated by empty string check below
			name, _ := smMap["name"].(string)
			//nolint:errcheck // best-effort type assertion; validated by empty string check below
			secretName, _ := smMap["secretName"].(string)
			if name != "" && secretName != "" {
				*volumes = append(*volumes, corev1.Volume{
					Name: name,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName,
						},
					},
				})
			}
		}
	}
}
