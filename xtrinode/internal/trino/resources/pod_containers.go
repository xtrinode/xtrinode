package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/sizing"
)

func buildTrinoContainer(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	role string,
	configMapName string,
	catalogs []string,
) (corev1.Container, error) {
	// Validate resource quantities instead of using MustParse.
	cpuReq, err := resource.ParseQuantity(preset.CoordinatorCPUReq)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("invalid coordinator CPU request %q: %w", preset.CoordinatorCPUReq, err)
	}
	memReq, err := resource.ParseQuantity(preset.CoordinatorMemReq)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("invalid coordinator memory request %q: %w", preset.CoordinatorMemReq, err)
	}
	cpuLim, err := resource.ParseQuantity(preset.CoordinatorCPULim)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("invalid coordinator CPU limit %q: %w", preset.CoordinatorCPULim, err)
	}
	memLim, err := resource.ParseQuantity(preset.CoordinatorMemLim)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("invalid coordinator memory limit %q: %w", preset.CoordinatorMemLim, err)
	}

	if role == "worker" {
		cpuReq, err = resource.ParseQuantity(preset.WorkerCPUReq)
		if err != nil {
			return corev1.Container{}, fmt.Errorf("invalid worker CPU request %q: %w", preset.WorkerCPUReq, err)
		}
		memReq, err = resource.ParseQuantity(preset.WorkerMemReq)
		if err != nil {
			return corev1.Container{}, fmt.Errorf("invalid worker memory request %q: %w", preset.WorkerMemReq, err)
		}
		cpuLim, err = resource.ParseQuantity(preset.WorkerCPULim)
		if err != nil {
			return corev1.Container{}, fmt.Errorf("invalid worker CPU limit %q: %w", preset.WorkerCPULim, err)
		}
		memLim, err = resource.ParseQuantity(preset.WorkerMemLim)
		if err != nil {
			return corev1.Container{}, fmt.Errorf("invalid worker memory limit %q: %w", preset.WorkerMemLim, err)
		}
	}

	// Get image pull policy from valuesOverlay
	imagePullPolicy := corev1.PullIfNotPresent
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if image, ok := xtrinode.Spec.GetValuesOverlayMap()["image"].(map[string]interface{}); ok {
			if pullPolicy, ok := image["pullPolicy"].(string); ok {
				imagePullPolicy = corev1.PullPolicy(pullPolicy)
			}
		}
	}

	container := corev1.Container{
		Name:            fmt.Sprintf("trino-%s", role),
		Image:           getTrinoImage(xtrinode),
		ImagePullPolicy: imagePullPolicy,
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: trinoHTTPPort(xtrinode),
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    cpuReq,
				corev1.ResourceMemory: memReq,
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    cpuLim,
				corev1.ResourceMemory: memLim,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "config-volume",
				MountPath: "/etc/trino",
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/info",
					Port: intstr.FromString("http"),
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
			SuccessThreshold:    1,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/usr/lib/trino/bin/health-check"},
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
			SuccessThreshold:    1,
		},
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/usr/lib/trino/bin/health-check"},
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       2,
			TimeoutSeconds:      2,
			FailureThreshold:    60,
			SuccessThreshold:    1,
		},
	}

	if jmxEnabled(xtrinode, role) {
		container.Ports = append(container.Ports,
			corev1.ContainerPort{
				Name:          "jmx-registry",
				ContainerPort: jmxRegistryPort(xtrinode, role),
				Protocol:      corev1.ProtocolTCP,
			},
			corev1.ContainerPort{
				Name:          "jmx-server",
				ContainerPort: jmxServerPort(xtrinode, role),
				Protocol:      corev1.ProtocolTCP,
			},
		)
	}

	// Add catalog volume mount - single projected volume merges all catalog ConfigMaps
	// into /etc/trino/catalog/ so Trino finds {catalogName}.properties
	if len(catalogs) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "catalog-volume",
			MountPath: "/etc/trino/catalog",
			ReadOnly:  true,
		})
	}

	// Add TLS volume mounts if enabled
	if xtrinode.Spec.TLS != nil {
		if xtrinode.Spec.TLS.ServerSecretClass != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "server-tls",
				MountPath: "/etc/trino/tls/server",
				ReadOnly:  true,
			})
		}
		if xtrinode.Spec.TLS.InternalSecretClass != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "internal-tls",
				MountPath: "/etc/trino/tls/internal",
				ReadOnly:  true,
			})
		}
	}

	// Add custom ConfigMap volume mounts
	for _, cmName := range xtrinode.Spec.CustomConfigMaps {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      cmName,
			MountPath: fmt.Sprintf("/etc/trino/custom/%s", cmName),
			ReadOnly:  true,
		})
	}

	// Add resource groups volume mount if configured (coordinator only, matching upstream chart)
	resourceGroupsTypeConfigMap := false
	if role == "coordinator" && xtrinode.Spec.GetValuesOverlayMap() != nil {
		if resourceGroups, ok := xtrinode.Spec.GetValuesOverlayMap()["resourceGroups"].(map[string]interface{}); ok {
			if rgType, ok := resourceGroups["type"].(string); ok && rgType == "configmap" {
				resourceGroupsTypeConfigMap = true
			}
		}
	}
	if role == "coordinator" && (resourceGroupsTypeConfigMap || xtrinode.Spec.ResourceGroupsProfile != "") {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "resource-groups-volume",
			MountPath: "/etc/trino/resource-groups",
			ReadOnly:  true,
		})
	}

	// Add access control volume mount if configured for this role.
	if shouldMountAccessControlVolume(xtrinode, role) {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "access-control-volume",
			MountPath: "/etc/trino/access-control",
			ReadOnly:  true,
		})
	}

	// Add secret mounts (global)
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.SecretMounts) > 0 {
		for _, secretMount := range xtrinode.Spec.HelmChartConfig.SecretMounts {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      secretMount.Name,
				MountPath: secretMount.Path,
				SubPath:   secretMount.SubPath,
				ReadOnly:  true,
			})
		}
	}

	// Add secret mounts (role-specific)
	switch role {
	case "coordinator":
		if xtrinode.Spec.HelmChartConfig != nil &&
			xtrinode.Spec.HelmChartConfig.Coordinator != nil &&
			len(xtrinode.Spec.HelmChartConfig.Coordinator.SecretMounts) > 0 {
			for _, secretMount := range xtrinode.Spec.HelmChartConfig.Coordinator.SecretMounts {
				container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
					Name:      secretMount.Name,
					MountPath: secretMount.Path,
					SubPath:   secretMount.SubPath,
					ReadOnly:  true,
				})
			}
		}
	case "worker":
		if xtrinode.Spec.HelmChartConfig != nil &&
			xtrinode.Spec.HelmChartConfig.Worker != nil &&
			len(xtrinode.Spec.HelmChartConfig.Worker.SecretMounts) > 0 {
			for _, secretMount := range xtrinode.Spec.HelmChartConfig.Worker.SecretMounts {
				container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
					Name:      secretMount.Name,
					MountPath: secretMount.Path,
					SubPath:   secretMount.SubPath,
					ReadOnly:  true,
				})
			}
		}
	}

	// Add configMounts and secretMounts from valuesOverlay
	addConfigAndSecretMounts(&container, xtrinode, role)

	// Add HTTPS port if TLS enabled
	if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.ServerSecretClass != "" {
		container.Ports = append(container.Ports, corev1.ContainerPort{
			Name:          "https",
			ContainerPort: config.TrinoPortHTTPS,
			Protocol:      corev1.ProtocolTCP,
		})
	}

	// Add environment variables
	container.Env = buildEnvVars(xtrinode)

	// Add environment variables from valuesOverlay
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.Env) > 0 {
		for _, envVar := range xtrinode.Spec.HelmChartConfig.Env {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  envVar.Name,
				Value: envVar.Value,
			})
		}
	}
	container.Env = appendTrinoControlAuthEnv(container.Env, xtrinode, role)

	roleConfig := roleValuesOverlay(xtrinode, role)
	if roleConfig != nil {
		if lifecycleMap, ok := roleConfig["lifecycle"].(map[string]interface{}); ok {
			lifecycle, err := buildLifecycle(lifecycleMap)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("failed to build %s lifecycle: %w", role, err)
			}
			container.Lifecycle = lifecycle
		}

		if livenessProbeMap, ok := roleConfig["livenessProbe"].(map[string]interface{}); ok {
			probe, err := buildProbeFromMap(livenessProbeMap)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("failed to build %s liveness probe: %w", role, err)
			}
			if probe != nil {
				container.LivenessProbe = probe
			}
		}

		if readinessProbeMap, ok := roleConfig["readinessProbe"].(map[string]interface{}); ok {
			probe, err := buildProbeFromMap(readinessProbeMap)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("failed to build %s readiness probe: %w", role, err)
			}
			if probe != nil {
				container.ReadinessProbe = probe
			}
		}

		if startupProbeMap, ok := roleConfig["startupProbe"].(map[string]interface{}); ok {
			probe, err := buildProbeFromMap(startupProbeMap)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("failed to build %s startup probe: %w", role, err)
			}
			if probe != nil {
				container.StartupProbe = probe
			}
		}

		if resourcesMap, ok := roleConfig["resources"].(map[string]interface{}); ok {
			resources, err := buildResourceRequirements(resourcesMap)
			if err != nil {
				return corev1.Container{}, fmt.Errorf("failed to build %s resources: %w", role, err)
			}
			if resources != nil {
				container.Resources = *resources
			}
		}

		if additionalPorts, ok := roleConfig["additionalExposedPorts"].(map[string]interface{}); ok {
			for _, portValue := range additionalPorts {
				if portMap, ok := portValue.(map[string]interface{}); ok {
					port, err := buildContainerPortFromMap(portMap)
					if err != nil {
						return corev1.Container{}, fmt.Errorf("failed to build %s container port: %w", role, err)
					}
					container.Ports = append(container.Ports, port)
				}
			}
		}
	}

	// Add envFrom from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if envFromList, ok := xtrinode.Spec.GetValuesOverlayMap()["envFrom"].([]interface{}); ok {
			for _, envFromItem := range envFromList {
				if envFromMap, ok := envFromItem.(map[string]interface{}); ok {
					envFrom, err := buildEnvFromSourceFromMap(envFromMap)
					if err != nil {
						return corev1.Container{}, fmt.Errorf("failed to build envFrom: %w", err)
					}
					container.EnvFrom = append(container.EnvFrom, envFrom)
				}
			}
		}
	}

	if roleConfig != nil {
		if additionalMounts, ok := roleConfig["additionalVolumeMounts"].([]interface{}); ok {
			for _, mountItem := range additionalMounts {
				if mountMap, ok := mountItem.(map[string]interface{}); ok {
					mount, err := buildVolumeMountFromMap(mountMap)
					if err != nil {
						return corev1.Container{}, fmt.Errorf("failed to build %s volume mount: %w", role, err)
					}
					container.VolumeMounts = append(container.VolumeMounts, mount)
				}
			}
		}
	}

	// Add authentication volume mounts from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		// Password authentication mount
		// Check if passwordAuthSecret is provided OR passwordAuth is provided as string
		passwordAuthSecretName := GetPasswordAuthSecretName(xtrinode)
		if passwordAuthSecretName != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "file-password-authentication-volume",
				MountPath: "/etc/trino/auth/password",
				ReadOnly:  true,
			})
		}
		// Groups authentication mount
		// Check if groupsAuthSecret is provided OR groups is provided as string
		groupsAuthSecretName := GetGroupsAuthSecretName(xtrinode)
		if groupsAuthSecretName != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "file-groups-authentication-volume",
				MountPath: "/etc/trino/auth/group",
				ReadOnly:  true,
			})
		}
	}

	// Add session properties mount from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if sessionProperties, ok := xtrinode.Spec.GetValuesOverlayMap()["sessionProperties"].(map[string]interface{}); ok {
			if sessionType, ok := sessionProperties["type"].(string); ok && (sessionType == "configmap" || sessionType == "properties") {
				container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
					Name:      "session-property-config-volume",
					MountPath: "/etc/trino/session-property-config.json",
					SubPath:   "session-property-config.json",
					ReadOnly:  true,
				})
			}
		}
	}

	// Add Kafka schemas mount (always mounted to match official Helm chart pattern;
	// the ConfigMap is always created, even if empty, so the mount never blocks pod startup)
	{
		kafkaMountPath := "/etc/trino/kafka"
		if xtrinode.Spec.GetValuesOverlayMap() != nil {
			if kafka, ok := xtrinode.Spec.GetValuesOverlayMap()["kafka"].(map[string]interface{}); ok {
				if mountPath, ok := kafka["mountPath"].(string); ok && mountPath != "" {
					kafkaMountPath = mountPath
				}
			}
		}
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "schemas-volume",
			MountPath: kafkaMountPath,
			ReadOnly:  true,
		})
	}

	return container, nil
}
