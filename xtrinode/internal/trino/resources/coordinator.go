package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/rollout"
	"github.com/xtrinode/xtrinode/internal/sizing"
)

// BuildCoordinatorDeployment builds the coordinator Deployment with revision stamping
func BuildCoordinatorDeployment(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	configMapName string,
	catalogs []string,
	revision string,
	rolloutHash string,
	catalogSecretEnvVars []corev1.EnvVar,
) (*appsv1.Deployment, error) {
	replicas := getCoordinatorReplicas(xtrinode)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            coordinatorDeploymentName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoPodLabels(xtrinode, ComponentCoordinator),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: TrinoSelectorLabels(xtrinode, ComponentCoordinator),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      TrinoPodLabels(xtrinode, ComponentCoordinator),
					Annotations: buildPodAnnotations(xtrinode, configMapName, catalogs),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            serviceAccountName(xtrinode),
					AutomountServiceAccountToken:  automountServiceAccountToken(xtrinode),
					SecurityContext:               buildSecurityContext(xtrinode),
					TerminationGracePeriodSeconds: buildTerminationGracePeriod(xtrinode, "coordinator"),
					Containers:                    []corev1.Container{},
					Volumes:                       nil, // Will be set below
				},
			},
		},
	}

	// Add init containers if specified
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.InitContainers) > 0 {
		if coordinatorInitContainers, ok := xtrinode.Spec.HelmChartConfig.InitContainers["coordinator"]; ok {
			deployment.Spec.Template.Spec.InitContainers = coordinatorInitContainers
		}
	}

	// Add image pull secrets
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.ImagePullSecrets) > 0 {
		imagePullSecrets := make([]corev1.LocalObjectReference, 0, len(xtrinode.Spec.HelmChartConfig.ImagePullSecrets))
		for _, ips := range xtrinode.Spec.HelmChartConfig.ImagePullSecrets {
			imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: ips.Name})
		}
		deployment.Spec.Template.Spec.ImagePullSecrets = imagePullSecrets
	}

	// Build the main Trino container before any sidecars.
	trinoContainer, err := buildTrinoContainer(xtrinode, preset, "coordinator", configMapName, catalogs)
	if err != nil {
		return nil, fmt.Errorf("failed to build Trino container: %w", err)
	}
	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, trinoContainer)

	// Inject catalog secret environment variables into the main Trino container
	// This enables password injection via env vars instead of storing in ConfigMaps
	if len(catalogSecretEnvVars) > 0 {
		deployment.Spec.Template.Spec.Containers[0].Env = append(
			deployment.Spec.Template.Spec.Containers[0].Env,
			catalogSecretEnvVars...,
		)
	}

	// Build volumes after the main container.
	volumes, err := buildVolumes(xtrinode, configMapName, catalogs, "coordinator")
	if err != nil {
		return nil, fmt.Errorf("failed to build volumes: %w", err)
	}
	deployment.Spec.Template.Spec.Volumes = volumes

	// Add JMX exporter sidecar if enabled (after main container)
	if jmxExporterEnabled(xtrinode, "coordinator") {
		jmxContainer := buildJMXExporterContainer(xtrinode, "coordinator")
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, jmxContainer)
	}

	// Add sidecar containers from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if sidecarContainers, ok := xtrinode.Spec.GetValuesOverlayMap()["sidecarContainers"].(map[string]interface{}); ok {
			if coordinatorSidecars, ok := sidecarContainers["coordinator"].([]interface{}); ok {
				for _, sidecar := range coordinatorSidecars {
					if sidecarMap, ok := sidecar.(map[string]interface{}); ok {
						container, err := buildContainerFromMap(sidecarMap)
						if err != nil {
							return nil, fmt.Errorf("failed to build sidecar container: %w", err)
						}
						deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, container)
					}
				}
			}
		}
	}

	// Add node selector, affinity, tolerations from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if nodeSelector, ok := coordinator["nodeSelector"].(map[string]interface{}); ok {
				deployment.Spec.Template.Spec.NodeSelector = convertToStringMap(nodeSelector)
			}
			// Add affinity from valuesOverlay
			if affinityMap, ok := coordinator["affinity"].(map[string]interface{}); ok {
				affinity, err := buildAffinityFromMap(affinityMap)
				if err != nil {
					return nil, fmt.Errorf("failed to build affinity: %w", err)
				}
				if affinity != nil {
					deployment.Spec.Template.Spec.Affinity = affinity
				}
			}
			// Add tolerations from valuesOverlay
			if tolerationsList, ok := coordinator["tolerations"].([]interface{}); ok {
				tolerations, err := buildTolerationsFromList(tolerationsList)
				if err != nil {
					return nil, fmt.Errorf("failed to build tolerations: %w", err)
				}
				deployment.Spec.Template.Spec.Tolerations = tolerations
			}
		}
	}

	// Add termination grace period from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if gracePeriod, ok := ParseInt64(coordinator["terminationGracePeriodSeconds"]); ok {
				deployment.Spec.Template.Spec.TerminationGracePeriodSeconds = &gracePeriod
			}
		}
	}

	// Add shareProcessNamespace from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if shareProcessNamespace, ok := xtrinode.Spec.GetValuesOverlayMap()["shareProcessNamespace"].(map[string]interface{}); ok {
			if coordinatorShare, ok := shareProcessNamespace["coordinator"].(bool); ok {
				deployment.Spec.Template.Spec.ShareProcessNamespace = &coordinatorShare
			}
		}
	}

	// Add priority class name if specified (matches official Helm chart: coordinator.priorityClassName)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if priorityClassName, ok := coordinator["priorityClassName"].(string); ok && priorityClassName != "" {
				deployment.Spec.Template.Spec.PriorityClassName = priorityClassName
			}
		}
	}

	// Add container security context from valuesOverlay to the Trino container.
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if containerSecurityContextMap, ok := xtrinode.Spec.GetValuesOverlayMap()["containerSecurityContext"].(map[string]interface{}); ok {
			securityContext, err := buildSecurityContextFromMap(containerSecurityContextMap)
			if err != nil {
				return nil, fmt.Errorf("failed to build container security context: %w", err)
			}
			if securityContext != nil {
				// Find and apply to the Trino coordinator container by name (not by index)
				for i := range deployment.Spec.Template.Spec.Containers {
					if deployment.Spec.Template.Spec.Containers[i].Name == "trino-coordinator" {
						deployment.Spec.Template.Spec.Containers[i].SecurityContext = securityContext
						break
					}
				}
			}
		}
	}

	// Apply rollout policy from CRD spec (rollout mechanics, not versioning)
	if xtrinode.Spec.RolloutPolicy != nil {
		if xtrinode.Spec.RolloutPolicy.RevisionHistoryLimit != nil {
			deployment.Spec.RevisionHistoryLimit = xtrinode.Spec.RolloutPolicy.RevisionHistoryLimit
		}
		if xtrinode.Spec.RolloutPolicy.RollingUpdateStrategy != nil {
			strategy := appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxSurge:       xtrinode.Spec.RolloutPolicy.RollingUpdateStrategy.MaxSurge,
					MaxUnavailable: xtrinode.Spec.RolloutPolicy.RollingUpdateStrategy.MaxUnavailable,
				},
			}
			deployment.Spec.Strategy = strategy
		}
	}

	// Add deployment strategy from valuesOverlay (can override rolloutPolicy)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if deploymentMap, ok := coordinator["deployment"].(map[string]interface{}); ok {
				if strategyMap, ok := deploymentMap["strategy"].(map[string]interface{}); ok {
					strategy, err := buildDeploymentStrategyFromMap(strategyMap)
					if err != nil {
						return nil, fmt.Errorf("failed to build deployment strategy: %w", err)
					}
					if strategy != nil {
						deployment.Spec.Strategy = *strategy
					}
				}
				if annotations, ok := deploymentMap["annotations"].(map[string]interface{}); ok {
					if deployment.Annotations == nil {
						deployment.Annotations = make(map[string]string)
					}
					for k, v := range annotations {
						if vStr, ok := v.(string); ok {
							deployment.Annotations[k] = vStr
						}
					}
				}
				if progressDeadline, ok := ParseInt32(deploymentMap["progressDeadlineSeconds"]); ok {
					deployment.Spec.ProgressDeadlineSeconds = &progressDeadline
				}
				if revisionHistoryLimit, ok := ParseInt32(deploymentMap["revisionHistoryLimit"]); ok {
					deployment.Spec.RevisionHistoryLimit = &revisionHistoryLimit
				}
			}
		}
	}

	// Set default revision history limit if not specified
	if deployment.Spec.RevisionHistoryLimit == nil {
		defaultLimit := int32(10)
		deployment.Spec.RevisionHistoryLimit = &defaultLimit
	}

	// Stamp revision on Deployment (enables self-healing reconciliation)
	StampRevision(deployment, revision)

	// Stamp revision on PodTemplate (forces rollout when revision changes)
	StampRevisionOnPodTemplate(&deployment.Spec.Template, revision)

	// Stamp the coordinator rollout hash; it includes the catalog digest.
	rollout.StampRolloutHash(&deployment.Spec.Template, rollout.CoordinatorRolloutHashKey, rolloutHash)

	return deployment, nil
}

func coordinatorDeploymentName(xtrinode *analyticsv1.XTrinode) string {
	return config.BuildCoordinatorServiceName(xtrinode.Name) + "-coordinator"
}

// getCoordinatorReplicas extracts coordinator replicas from valuesOverlay (0 or 1 only)
func getCoordinatorReplicas(xtrinode *analyticsv1.XTrinode) int32 {
	replicas := int32(config.DefaultCoordinatorReplicas)
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return replicas
	}

	coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{})
	if !ok {
		return replicas
	}

	if replicasVal, ok := ParseInt32(coordinator["replicas"]); ok {
		if replicasVal >= 0 && replicasVal <= 1 {
			return replicasVal
		}
	}
	return replicas
}

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

func buildVolumes(
	xtrinode *analyticsv1.XTrinode,
	configMapName string,
	catalogs []string,
	role string,
) ([]corev1.Volume, error) {
	volumes := []corev1.Volume{
		{
			Name: "config-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		},
	}

	// Add catalog volume - projected volume merges all catalog ConfigMaps into /etc/trino/catalog/
	// so Trino finds {catalogName}.properties (avoids subPath mount when parent dir doesn't exist)
	if len(catalogs) > 0 {
		sources := make([]corev1.VolumeProjection, 0, len(catalogs))
		for _, catalogName := range catalogs {
			propsFile := fmt.Sprintf("%s.properties", catalogName)
			sources = append(sources, corev1.VolumeProjection{
				ConfigMap: &corev1.ConfigMapProjection{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("trino-catalog-%s", catalogName),
					},
					Items: []corev1.KeyToPath{{
						Key:  propsFile,
						Path: propsFile,
					}},
				},
			})
		}
		volumes = append(volumes, corev1.Volume{
			Name: "catalog-volume",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: sources,
				},
			},
		})
	}

	// Add TLS volumes if enabled
	if xtrinode.Spec.TLS != nil {
		if xtrinode.Spec.TLS.ServerSecretClass != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "server-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: xtrinode.Spec.TLS.ServerSecretClass,
					},
				},
			})
		}
		if xtrinode.Spec.TLS.InternalSecretClass != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "internal-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: xtrinode.Spec.TLS.InternalSecretClass,
					},
				},
			})
		}
	}

	// Add secret mount volumes (global)
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.SecretMounts) > 0 {
		for _, secretMount := range xtrinode.Spec.HelmChartConfig.SecretMounts {
			volumes = append(volumes, corev1.Volume{
				Name: secretMount.Name,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secretMount.SecretName,
					},
				},
			})
		}
	}

	// Add role-specific secret mount volumes
	addRoleSpecificSecretVolumes(&volumes, xtrinode, role)

	// Add configMount volumes from valuesOverlay (global)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if configMounts, ok := xtrinode.Spec.GetValuesOverlayMap()["configMounts"].([]interface{}); ok {
			for _, cm := range configMounts {
				if cmMap, ok := cm.(map[string]interface{}); ok {
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					name, _ := cmMap["name"].(string)
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					configMap, _ := cmMap["configMap"].(string)
					if name != "" && configMap != "" {
						volumes = append(volumes, corev1.Volume{
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
		// Add role-specific configMount volumes
		addRoleSpecificConfigVolumes(&volumes, xtrinode, role)
		// Add secretMount volumes from valuesOverlay (global)
		if secretMounts, ok := xtrinode.Spec.GetValuesOverlayMap()["secretMounts"].([]interface{}); ok {
			for _, sm := range secretMounts {
				if smMap, ok := sm.(map[string]interface{}); ok {
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					name, _ := smMap["name"].(string)
					//nolint:errcheck // best-effort type assertion; validated by empty string check below
					secretName, _ := smMap["secretName"].(string)
					if name != "" && secretName != "" {
						volumes = append(volumes, corev1.Volume{
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
		// Add role-specific secretMount volumes
		addRoleSpecificSecretVolumesFromOverlay(&volumes, xtrinode, role)
	}

	// Add custom ConfigMaps
	if len(xtrinode.Spec.CustomConfigMaps) > 0 {
		for _, cmName := range xtrinode.Spec.CustomConfigMaps {
			volumes = append(volumes, corev1.Volume{
				Name: cmName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cmName,
						},
					},
				},
			})
		}
	}

	// Add resource groups volume if configured (coordinator only, matching upstream chart)
	resourceGroupsTypeConfigMap := false
	if role == "coordinator" && xtrinode.Spec.GetValuesOverlayMap() != nil {
		if resourceGroups, ok := xtrinode.Spec.GetValuesOverlayMap()["resourceGroups"].(map[string]interface{}); ok {
			if rgType, ok := resourceGroups["type"].(string); ok && rgType == "configmap" {
				resourceGroupsTypeConfigMap = true
			}
		}
	}
	if role == "coordinator" && (resourceGroupsTypeConfigMap || xtrinode.Spec.ResourceGroupsProfile != "") {
		configMapName := xtrinode.Spec.ResourceGroupsProfile
		if configMapName == "" {
			configMapName = fmt.Sprintf("trino-%s-resource-groups-volume-coordinator", xtrinode.Name)
		}
		volumes = append(volumes, corev1.Volume{
			Name: "resource-groups-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		})
	}

	// Add access control volume if configured for this role.
	if shouldMountAccessControlVolume(xtrinode, role) {
		accessControlCMName := fmt.Sprintf("trino-%s-access-control-volume-%s", xtrinode.Name, role)
		volumes = append(volumes, corev1.Volume{
			Name: "access-control-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: accessControlCMName,
					},
				},
			},
		})
	}

	// Add JMX exporter config volume if enabled
	if jmxExporterEnabled(xtrinode, role) {
		volumes = append(volumes, corev1.Volume{
			Name: "jmx-exporter-config-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: jmxExporterConfigMapName(xtrinode, role),
					},
				},
			},
		})
	}

	// Add authentication volumes from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		// Password authentication volume
		// Check if passwordAuthSecret is provided OR passwordAuth is provided as string
		passwordAuthSecretName := GetPasswordAuthSecretName(xtrinode)
		if passwordAuthSecretName != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "file-password-authentication-volume",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: passwordAuthSecretName,
						Items: []corev1.KeyToPath{
							{Key: "password.db", Path: "password.db"},
						},
					},
				},
			})
		}
		// Groups authentication volume
		// Check if groupsAuthSecret is provided OR groups is provided as string
		groupsAuthSecretName := GetGroupsAuthSecretName(xtrinode)
		if groupsAuthSecretName != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "file-groups-authentication-volume",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: groupsAuthSecretName,
						Items: []corev1.KeyToPath{
							{Key: "group.db", Path: "group.db"},
						},
					},
				},
			})
		}
	}

	// Add session properties volume from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if sessionProperties, ok := xtrinode.Spec.GetValuesOverlayMap()["sessionProperties"].(map[string]interface{}); ok {
			if sessionType, ok := sessionProperties["type"].(string); ok && (sessionType == "configmap" || sessionType == "properties") {
				volumes = append(volumes, corev1.Volume{
					Name: "session-property-config-volume",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: fmt.Sprintf("trino-%s-session-property-config", xtrinode.Name),
							},
						},
					},
				})
			}
		}
	}

	// Add Kafka schemas volume (always added to match official Helm chart pattern;
	// ConfigMap trino-{name}-schemas-volume-{role} is always created even if empty)
	volumes = append(volumes, corev1.Volume{
		Name: "schemas-volume",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("trino-%s-schemas-volume-%s", xtrinode.Name, role),
				},
			},
		},
	})

	// Add additional volumes from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		switch role {
		case "coordinator":
			if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
				if additionalVolumes, ok := coordinator["additionalVolumes"].([]interface{}); ok {
					for _, vol := range additionalVolumes {
						if volMap, ok := vol.(map[string]interface{}); ok {
							volume, err := buildVolumeFromMap(volMap)
							if err != nil {
								return nil, fmt.Errorf("failed to build volume: %w", err)
							}
							volumes = append(volumes, volume)
						}
					}
				}
			}
		case "worker":
			if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
				if additionalVolumes, ok := worker["additionalVolumes"].([]interface{}); ok {
					for _, vol := range additionalVolumes {
						if volMap, ok := vol.(map[string]interface{}); ok {
							volume, err := buildVolumeFromMap(volMap)
							if err != nil {
								return nil, fmt.Errorf("failed to build volume: %w", err)
							}
							volumes = append(volumes, volume)
						}
					}
				}
			}
		}
	}

	return volumes, nil
}

func buildPodAnnotations(xtrinode *analyticsv1.XTrinode, configMapName string, catalogs []string) map[string]string {
	annotations := make(map[string]string)

	// ConfigMap names are revisioned, so this value changes when rendered coordinator config changes.
	annotations["checksum/coordinator-config"] = configMapName

	// Note: Catalog content changes are handled by rollout hash system
	// Coordinator rolls on catalog changes via trino.io/rollout-hash-coordinator annotation
	// No need for catalog annotations here - rollout hash is the source of truth

	// Add custom annotations from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if customAnnotations, ok := coordinator["annotations"].(map[string]interface{}); ok {
				for k, v := range customAnnotations {
					if vStr, ok := v.(string); ok {
						annotations[k] = vStr
					}
				}
			}
		}
	}

	return annotations
}

func buildSecurityContext(xtrinode *analyticsv1.XTrinode) *corev1.PodSecurityContext {
	// Check if pod security context is configured in valuesOverlay (matches official chart: .Values.securityContext)
	// An explicitly set empty map {} means no security context (user opted out)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if rawSecCtx, exists := xtrinode.Spec.GetValuesOverlayMap()["securityContext"]; exists {
			if securityContextMap, ok := rawSecCtx.(map[string]interface{}); ok {
				if len(securityContextMap) == 0 {
					return nil
				}
				yamlBytes, err := yaml.Marshal(securityContextMap)
				if err == nil {
					var podSecurityContext corev1.PodSecurityContext
					if err := yaml.Unmarshal(yamlBytes, &podSecurityContext); err == nil {
						return &podSecurityContext
					}
				}
			}
		}
	}
	// Default security context
	return &corev1.PodSecurityContext{
		RunAsNonRoot: func() *bool { b := true; return &b }(),
		RunAsUser:    func() *int64 { uid := int64(1000); return &uid }(),
		FSGroup:      func() *int64 { gid := int64(1000); return &gid }(),
	}
}

func buildTerminationGracePeriod(xtrinode *analyticsv1.XTrinode, role string) *int64 {
	// Default: coordinator 15 minutes, worker 60 minutes
	var gracePeriod int64 = 15 * 60 // 15 minutes
	if role == "worker" {
		gracePeriod = 60 * 60 // 60 minutes
	}

	// Override from valuesOverlay if specified
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if roleMap, ok := xtrinode.Spec.GetValuesOverlayMap()[role].(map[string]interface{}); ok {
			if tgp, ok := ParseInt64(roleMap["terminationGracePeriodSeconds"]); ok {
				gracePeriod = tgp
			}
		}
	}

	return &gracePeriod
}

func buildEnvVars(xtrinode *analyticsv1.XTrinode) []corev1.EnvVar {
	envVars := []corev1.EnvVar{}

	// Add environment variables from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if env, ok := xtrinode.Spec.GetValuesOverlayMap()["env"].([]interface{}); ok {
			for _, envItem := range env {
				if envMap, ok := envItem.(map[string]interface{}); ok {
					envVar := corev1.EnvVar{}
					if name, ok := envMap["name"].(string); ok {
						envVar.Name = name
					}
					if value, ok := envMap["value"].(string); ok {
						envVar.Value = value
					}
					envVars = append(envVars, envVar)
				}
			}
		}
	}

	return envVars
}

func buildJMXExporterContainer(xtrinode *analyticsv1.XTrinode, role string) corev1.Container {
	// Default JMX exporter configuration
	jmxImage := config.DefaultJMXExporterImage
	jmxPort := jmxExporterPort(xtrinode, role)
	jmxPullPolicy := corev1.PullPolicy("Always")
	var jmxSecurityContext *corev1.SecurityContext
	var jmxResources corev1.ResourceRequirements

	// Get role-specific JMX config (coordinator or worker)
	roleJmxConfig := roleJMXValues(xtrinode, role)

	// Parse JMX exporter configuration
	if xtrinode.Spec.KEDA != nil && xtrinode.Spec.KEDA.JMXExporter != nil {
		if xtrinode.Spec.KEDA.JMXExporter.Image != "" {
			jmxImage = xtrinode.Spec.KEDA.JMXExporter.Image
		}
	}

	// Override from valuesOverlay
	if roleJmxConfig != nil {
		if exporter, ok := roleJmxConfig["exporter"].(map[string]interface{}); ok {
			if image, ok := exporter["image"].(string); ok && image != "" {
				jmxImage = image
			}
			if pullPolicy, ok := exporter["pullPolicy"].(string); ok && pullPolicy != "" {
				jmxPullPolicy = corev1.PullPolicy(pullPolicy)
			}
			if securityContextMap, ok := exporter["securityContext"].(map[string]interface{}); ok {
				securityContext, err := buildSecurityContextFromMap(securityContextMap)
				if err == nil {
					jmxSecurityContext = securityContext
				}
			}
			if resourcesMap, ok := exporter["resources"].(map[string]interface{}); ok {
				resources, err := buildResourceRequirements(resourcesMap)
				if err == nil {
					jmxResources = *resources
				}
			}
		}
	}

	container := corev1.Container{
		Name:            "jmx-exporter",
		Image:           jmxImage,
		ImagePullPolicy: jmxPullPolicy,
		Args: []string{
			fmt.Sprintf("%d", jmxPort),
			"/etc/jmx-exporter/jmx-exporter-config.yaml",
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "jmx-exporter",
				ContainerPort: jmxPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "jmx-exporter-config-volume",
				MountPath: "/etc/jmx-exporter",
			},
		},
	}

	// Add security context if specified
	if jmxSecurityContext != nil {
		container.SecurityContext = jmxSecurityContext
	}

	// Add resources if specified
	if jmxResources.Requests != nil || jmxResources.Limits != nil {
		container.Resources = jmxResources
	}

	return container
}

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

func convertToStringMap(m map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		if vStr, ok := v.(string); ok {
			result[k] = vStr
		}
	}
	return result
}

// buildResourceRequirements converts a Helm values resources map to corev1.ResourceRequirements
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

// buildAffinityFromMap converts a Helm values affinity map to corev1.Affinity
func buildAffinityFromMap(affinityMap map[string]interface{}) (*corev1.Affinity, error) {
	if len(affinityMap) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(affinityMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal affinity map: %w", err)
	}

	var affinity corev1.Affinity
	if err := yaml.Unmarshal(yamlBytes, &affinity); err != nil {
		return nil, fmt.Errorf("failed to unmarshal affinity: %w", err)
	}
	return &affinity, nil
}

// buildTolerationsFromList converts a Helm values tolerations list to []corev1.Toleration
func buildTolerationsFromList(tolerationsList []interface{}) ([]corev1.Toleration, error) {
	if len(tolerationsList) == 0 {
		return nil, nil
	}

	yamlBytes, err := yaml.Marshal(tolerationsList)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tolerations list: %w", err)
	}

	var tolerations []corev1.Toleration
	if err := yaml.Unmarshal(yamlBytes, &tolerations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tolerations: %w", err)
	}
	return tolerations, nil
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

func getTrinoImage(xtrinode *analyticsv1.XTrinode) string {
	// Check valuesOverlay for image configuration
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if image, ok := xtrinode.Spec.GetValuesOverlayMap()["image"].(map[string]interface{}); ok {
			// Check if useRepositoryAsSoleImageReference is true
			if useSoleRef, ok := image["useRepositoryAsSoleImageReference"].(bool); ok && useSoleRef {
				if repo, ok := image["repository"].(string); ok && repo != "" {
					return repo
				}
			}

			repository := config.DefaultTrinoImageRepository
			if repo, ok := image["repository"].(string); ok && repo != "" {
				repository = repo
			}

			// Check for registry
			registry := ""
			if reg, ok := image["registry"].(string); ok && reg != "" {
				registry = reg
			}

			// Check for digest (takes precedence over tag)
			if digest, ok := image["digest"].(string); ok && digest != "" {
				if registry != "" {
					return fmt.Sprintf("%s/%s@%s", registry, repository, digest)
				}
				return fmt.Sprintf("%s@%s", repository, digest)
			}

			// Use tag
			tag := config.DefaultTrinoImageTag
			if t, ok := image["tag"].(string); ok && t != "" {
				tag = t
			}

			if registry != "" {
				return fmt.Sprintf("%s/%s:%s", registry, repository, tag)
			}
			return fmt.Sprintf("%s:%s", repository, tag)
		}
	}
	// Default image
	return fmt.Sprintf("%s:%s", config.DefaultTrinoImageRepository, config.DefaultTrinoImageTag)
}
