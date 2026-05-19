package resources

import (
	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// addConfigAndSecretMounts adds configMounts and secretMounts from valuesOverlay
func addConfigAndSecretMounts(container *corev1.Container, xtrinode *analyticsv1.XTrinode, role string) {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	// Add global configMounts
	if configMounts, ok := xtrinode.Spec.GetValuesOverlayMap()["configMounts"].([]interface{}); ok {
		addConfigMountsFromList(container, configMounts)
	}

	// Add role-specific configMounts
	addRoleSpecificConfigMounts(container, xtrinode, role)

	// Add global secretMounts
	if secretMounts, ok := xtrinode.Spec.GetValuesOverlayMap()["secretMounts"].([]interface{}); ok {
		addSecretMountsFromList(container, secretMounts)
	}

	// Add role-specific secretMounts
	addRoleSpecificSecretMounts(container, xtrinode, role)
}

// addConfigMountsFromList processes a list of configMounts
func addConfigMountsFromList(container *corev1.Container, configMounts []interface{}) {
	for _, cm := range configMounts {
		cmMap, ok := cm.(map[string]interface{})
		if !ok {
			continue
		}
		//nolint:errcheck // best-effort type assertion; validated by empty string check below
		name, _ := cmMap["name"].(string)
		//nolint:errcheck // best-effort type assertion; defaults to empty string on failure
		path, _ := cmMap["path"].(string)
		//nolint:errcheck // best-effort type assertion; defaults to empty string on failure
		subPath, _ := cmMap["subPath"].(string)
		//nolint:errcheck // best-effort type assertion; validated by empty string check below
		configMap, _ := cmMap["configMap"].(string)
		if name != "" && path != "" && configMap != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      name,
				MountPath: path,
				SubPath:   subPath,
				ReadOnly:  true,
			})
		}
	}
}

// addSecretMountsFromList processes a list of secretMounts
func addSecretMountsFromList(container *corev1.Container, secretMounts []interface{}) {
	for _, sm := range secretMounts {
		smMap, ok := sm.(map[string]interface{})
		if !ok {
			continue
		}
		//nolint:errcheck // best-effort type assertion; validated by empty string check below
		name, _ := smMap["name"].(string)
		//nolint:errcheck // best-effort type assertion; defaults to empty string on failure
		path, _ := smMap["path"].(string)
		//nolint:errcheck // best-effort type assertion; defaults to empty string on failure
		subPath, _ := smMap["subPath"].(string)
		//nolint:errcheck // best-effort type assertion; validated by empty string check below
		secretName, _ := smMap["secretName"].(string)
		if name != "" && path != "" && secretName != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      name,
				MountPath: path,
				SubPath:   subPath,
				ReadOnly:  true,
			})
		}
	}
}

// addRoleSpecificConfigMounts adds role-specific configMounts based on role
func addRoleSpecificConfigMounts(container *corev1.Container, xtrinode *analyticsv1.XTrinode, role string) {
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
		addConfigMountsFromList(container, configMounts)
	}
}

// addRoleSpecificSecretMounts adds role-specific secretMounts based on role
func addRoleSpecificSecretMounts(container *corev1.Container, xtrinode *analyticsv1.XTrinode, role string) {
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
		addSecretMountsFromList(container, secretMounts)
	}
}
