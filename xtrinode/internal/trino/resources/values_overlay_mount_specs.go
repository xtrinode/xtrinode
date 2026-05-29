package resources

import (
	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

type valuesOverlayConfigMount struct {
	name      string
	path      string
	subPath   string
	configMap string
}

type valuesOverlaySecretMount struct {
	name       string
	path       string
	subPath    string
	secretName string
}

func configMountsFromOverlay(values map[string]interface{}) []valuesOverlayConfigMount {
	items, ok := values["configMounts"].([]interface{})
	if !ok {
		return nil
	}
	mounts := make([]valuesOverlayConfigMount, 0, len(items))
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		mounts = append(mounts, valuesOverlayConfigMount{
			name:      overlayString(itemMap, "name"),
			path:      overlayString(itemMap, "path"),
			subPath:   overlayString(itemMap, "subPath"),
			configMap: overlayString(itemMap, "configMap"),
		})
	}
	return mounts
}

func secretMountsFromOverlay(values map[string]interface{}) []valuesOverlaySecretMount {
	items, ok := values["secretMounts"].([]interface{})
	if !ok {
		return nil
	}
	mounts := make([]valuesOverlaySecretMount, 0, len(items))
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		mounts = append(mounts, valuesOverlaySecretMount{
			name:       overlayString(itemMap, "name"),
			path:       overlayString(itemMap, "path"),
			subPath:    overlayString(itemMap, "subPath"),
			secretName: overlayString(itemMap, "secretName"),
		})
	}
	return mounts
}

func roleConfigMountsFromOverlay(xtrinode *analyticsv1.XTrinode, role string) []valuesOverlayConfigMount {
	return configMountsFromOverlay(roleValuesOverlay(xtrinode, role))
}

func roleSecretMountsFromOverlay(xtrinode *analyticsv1.XTrinode, role string) []valuesOverlaySecretMount {
	return secretMountsFromOverlay(roleValuesOverlay(xtrinode, role))
}

func overlayString(values map[string]interface{}, key string) string {
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return value
}

func appendValuesOverlayConfigVolumeMounts(container *corev1.Container, mounts []valuesOverlayConfigMount) {
	for _, mount := range mounts {
		if mount.name == "" || mount.path == "" || mount.configMap == "" {
			continue
		}
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      mount.name,
			MountPath: mount.path,
			SubPath:   mount.subPath,
			ReadOnly:  true,
		})
	}
}

func appendValuesOverlaySecretVolumeMounts(container *corev1.Container, mounts []valuesOverlaySecretMount) {
	for _, mount := range mounts {
		if mount.name == "" || mount.path == "" || mount.secretName == "" {
			continue
		}
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      mount.name,
			MountPath: mount.path,
			SubPath:   mount.subPath,
			ReadOnly:  true,
		})
	}
}

func appendValuesOverlayConfigVolumes(volumes *[]corev1.Volume, mounts []valuesOverlayConfigMount) {
	for _, mount := range mounts {
		if mount.name == "" || mount.configMap == "" {
			continue
		}
		*volumes = append(*volumes, corev1.Volume{
			Name: mount.name,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: mount.configMap,
					},
				},
			},
		})
	}
}

func appendValuesOverlaySecretVolumes(volumes *[]corev1.Volume, mounts []valuesOverlaySecretMount) {
	for _, mount := range mounts {
		if mount.name == "" || mount.secretName == "" {
			continue
		}
		*volumes = append(*volumes, corev1.Volume{
			Name: mount.name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: mount.secretName,
				},
			},
		})
	}
}
