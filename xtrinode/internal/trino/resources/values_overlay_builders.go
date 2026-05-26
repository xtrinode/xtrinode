package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func decodeValuesOverlayMap[T any](value map[string]interface{}, name string) (T, error) {
	var out T
	yamlBytes, err := yaml.Marshal(value)
	if err != nil {
		return out, fmt.Errorf("failed to marshal %s map: %w", name, err)
	}
	if err := yaml.Unmarshal(yamlBytes, &out); err != nil {
		return out, fmt.Errorf("failed to unmarshal %s: %w", name, err)
	}
	return out, nil
}

func boolValue(value interface{}) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func buildEnvVarFromMap(envMap map[string]interface{}) (corev1.EnvVar, error) {
	return decodeValuesOverlayMap[corev1.EnvVar](envMap, "env var")
}

// buildVolumeFromMap converts a Helm values volume map to corev1.Volume
// Uses YAML marshaling/unmarshaling for full passthrough support
func buildVolumeFromMap(volumeMap map[string]interface{}) (corev1.Volume, error) {
	return decodeValuesOverlayMap[corev1.Volume](volumeMap, "volume")
}

// buildVolumeMountFromMap converts a Helm values volume mount map to corev1.VolumeMount
func buildVolumeMountFromMap(mountMap map[string]interface{}) (corev1.VolumeMount, error) {
	return decodeValuesOverlayMap[corev1.VolumeMount](mountMap, "volume mount")
}

// buildLifecycle converts a Helm values lifecycle map to corev1.Lifecycle
func buildLifecycle(lifecycleMap map[string]interface{}) (*corev1.Lifecycle, error) {
	if len(lifecycleMap) == 0 {
		return nil, nil
	}

	lifecycle, err := decodeValuesOverlayMap[corev1.Lifecycle](lifecycleMap, "lifecycle")
	if err != nil {
		return nil, err
	}
	return &lifecycle, nil
}

// buildProbeFromMap converts a Helm values probe map to corev1.Probe
func buildProbeFromMap(probeMap map[string]interface{}) (*corev1.Probe, error) {
	if len(probeMap) == 0 {
		return nil, nil
	}

	probe, err := decodeValuesOverlayMap[corev1.Probe](probeMap, "probe")
	if err != nil {
		return nil, err
	}
	return &probe, nil
}

// buildResourceRequirements converts sidecar resource settings to corev1.ResourceRequirements.
func buildResourceRequirements(resourcesMap map[string]interface{}) (*corev1.ResourceRequirements, error) {
	if len(resourcesMap) == 0 {
		return nil, nil
	}

	requirements, err := decodeValuesOverlayMap[corev1.ResourceRequirements](resourcesMap, "resources")
	if err != nil {
		return nil, err
	}
	return &requirements, nil
}

// buildContainerPortFromMap converts a Helm values port map to corev1.ContainerPort
func buildContainerPortFromMap(portMap map[string]interface{}) (corev1.ContainerPort, error) {
	port, err := decodeValuesOverlayMap[corev1.ContainerPort](portMap, "port")
	if err != nil {
		return corev1.ContainerPort{}, err
	}
	if port.ContainerPort == 0 {
		if containerPort, ok := ParseInt32(portMap["port"]); ok {
			port.ContainerPort = containerPort
		}
	}
	return port, nil
}

// buildSecurityContextFromMap converts a Helm values security context map to *corev1.SecurityContext
func buildSecurityContextFromMap(securityContextMap map[string]interface{}) (*corev1.SecurityContext, error) {
	if len(securityContextMap) == 0 {
		return nil, nil
	}
	if boolValue(securityContextMap["privileged"]) {
		return nil, fmt.Errorf("privileged containers are not allowed through valuesOverlay")
	}
	if boolValue(securityContextMap["allowPrivilegeEscalation"]) {
		return nil, fmt.Errorf("privilege escalation is not allowed through valuesOverlay")
	}
	if capabilities, ok := securityContextMap["capabilities"].(map[string]interface{}); ok {
		if adds, ok := capabilities["add"].([]interface{}); ok && len(adds) > 0 {
			return nil, fmt.Errorf("added Linux capabilities are not allowed through valuesOverlay")
		}
	}

	securityContext, err := decodeValuesOverlayMap[corev1.SecurityContext](securityContextMap, "security context")
	if err != nil {
		return nil, err
	}
	return &securityContext, nil
}
