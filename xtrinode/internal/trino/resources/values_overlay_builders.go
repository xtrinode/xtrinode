package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// buildContainerFromMap converts a Helm values container map to corev1.Container
// Uses YAML marshaling/unmarshaling for full passthrough support
func buildContainerFromMap(containerMap map[string]interface{}) (corev1.Container, error) {
	yamlBytes, err := yaml.Marshal(containerMap)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("failed to marshal container map: %w", err)
	}

	var container corev1.Container
	if err := yaml.Unmarshal(yamlBytes, &container); err != nil {
		return corev1.Container{}, fmt.Errorf("failed to unmarshal container: %w", err)
	}
	return container, nil
}

func buildEnvVarFromMap(envMap map[string]interface{}) (corev1.EnvVar, error) {
	yamlBytes, err := yaml.Marshal(envMap)
	if err != nil {
		return corev1.EnvVar{}, fmt.Errorf("failed to marshal env var map: %w", err)
	}

	var envVar corev1.EnvVar
	if err := yaml.Unmarshal(yamlBytes, &envVar); err != nil {
		return corev1.EnvVar{}, fmt.Errorf("failed to unmarshal env var: %w", err)
	}
	return envVar, nil
}

// buildVolumeFromMap converts a Helm values volume map to corev1.Volume
// Uses YAML marshaling/unmarshaling for full passthrough support
func buildVolumeFromMap(volumeMap map[string]interface{}) (corev1.Volume, error) {
	yamlBytes, err := yaml.Marshal(volumeMap)
	if err != nil {
		return corev1.Volume{}, fmt.Errorf("failed to marshal volume map: %w", err)
	}

	var volume corev1.Volume
	if err := yaml.Unmarshal(yamlBytes, &volume); err != nil {
		return corev1.Volume{}, fmt.Errorf("failed to unmarshal volume: %w", err)
	}
	return volume, nil
}

// buildVolumeMountFromMap converts a Helm values volume mount map to corev1.VolumeMount
func buildVolumeMountFromMap(mountMap map[string]interface{}) (corev1.VolumeMount, error) {
	yamlBytes, err := yaml.Marshal(mountMap)
	if err != nil {
		return corev1.VolumeMount{}, fmt.Errorf("failed to marshal volume mount map: %w", err)
	}

	var mount corev1.VolumeMount
	if err := yaml.Unmarshal(yamlBytes, &mount); err != nil {
		return corev1.VolumeMount{}, fmt.Errorf("failed to unmarshal volume mount: %w", err)
	}
	return mount, nil
}

// buildLifecycle converts a Helm values lifecycle map to corev1.Lifecycle
func buildLifecycle(lifecycleMap map[string]interface{}) (*corev1.Lifecycle, error) {
	if len(lifecycleMap) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(lifecycleMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal lifecycle map: %w", err)
	}

	var lifecycle corev1.Lifecycle
	if err := yaml.Unmarshal(yamlBytes, &lifecycle); err != nil {
		return nil, fmt.Errorf("failed to unmarshal lifecycle: %w", err)
	}
	return &lifecycle, nil
}

// buildProbeFromMap converts a Helm values probe map to corev1.Probe
func buildProbeFromMap(probeMap map[string]interface{}) (*corev1.Probe, error) {
	if len(probeMap) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(probeMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal probe map: %w", err)
	}

	var probe corev1.Probe
	if err := yaml.Unmarshal(yamlBytes, &probe); err != nil {
		return nil, fmt.Errorf("failed to unmarshal probe: %w", err)
	}
	return &probe, nil
}

// buildResourceRequirements converts sidecar resource settings to corev1.ResourceRequirements.
func buildResourceRequirements(resourcesMap map[string]interface{}) (*corev1.ResourceRequirements, error) {
	if len(resourcesMap) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(resourcesMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resources map: %w", err)
	}

	var requirements corev1.ResourceRequirements
	if err := yaml.Unmarshal(yamlBytes, &requirements); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resources: %w", err)
	}
	return &requirements, nil
}

// buildContainerPortFromMap converts a Helm values port map to corev1.ContainerPort
func buildContainerPortFromMap(portMap map[string]interface{}) (corev1.ContainerPort, error) {
	yamlBytes, err := yaml.Marshal(portMap)
	if err != nil {
		return corev1.ContainerPort{}, fmt.Errorf("failed to marshal port map: %w", err)
	}

	var port corev1.ContainerPort
	if err := yaml.Unmarshal(yamlBytes, &port); err != nil {
		return corev1.ContainerPort{}, fmt.Errorf("failed to unmarshal port: %w", err)
	}
	if port.ContainerPort == 0 {
		if containerPort, ok := ParseInt32(portMap["port"]); ok {
			port.ContainerPort = containerPort
		}
	}
	return port, nil
}

// buildEnvFromSourceFromMap converts a Helm values envFrom map to corev1.EnvFromSource
func buildEnvFromSourceFromMap(envFromMap map[string]interface{}) (corev1.EnvFromSource, error) {
	yamlBytes, err := yaml.Marshal(envFromMap)
	if err != nil {
		return corev1.EnvFromSource{}, fmt.Errorf("failed to marshal envFrom map: %w", err)
	}

	var envFrom corev1.EnvFromSource
	if err := yaml.Unmarshal(yamlBytes, &envFrom); err != nil {
		return corev1.EnvFromSource{}, fmt.Errorf("failed to unmarshal envFrom: %w", err)
	}
	return envFrom, nil
}

// buildSecurityContextFromMap converts a Helm values security context map to *corev1.SecurityContext
func buildSecurityContextFromMap(securityContextMap map[string]interface{}) (*corev1.SecurityContext, error) {
	if len(securityContextMap) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(securityContextMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal security context map: %w", err)
	}

	var securityContext corev1.SecurityContext
	if err := yaml.Unmarshal(yamlBytes, &securityContext); err != nil {
		return nil, fmt.Errorf("failed to unmarshal security context: %w", err)
	}
	return &securityContext, nil
}

// buildDeploymentStrategyFromMap converts a Helm values deployment strategy map to appsv1.DeploymentStrategy
func buildDeploymentStrategyFromMap(strategyMap map[string]interface{}) (*appsv1.DeploymentStrategy, error) {
	if len(strategyMap) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(strategyMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal deployment strategy map: %w", err)
	}

	var strategy appsv1.DeploymentStrategy
	if err := yaml.Unmarshal(yamlBytes, &strategy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal deployment strategy: %w", err)
	}
	return &strategy, nil
}
