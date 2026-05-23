package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	podTemplateRevision := rolloutHash
	if podTemplateRevision == "" {
		podTemplateRevision = revision
	}
	return buildCoordinatorDeployment(xtrinode, preset, configMapName, catalogs, revision, podTemplateRevision, rolloutHash, catalogSecretEnvVars)
}

func buildCoordinatorDeployment(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	configMapName string,
	catalogs []string,
	resourceRevision string,
	podTemplateRevision string,
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
	StampRevision(deployment, resourceRevision)

	if podTemplateRevision != "" {
		// Stamp the rendered pod revision on the PodTemplate. Kubernetes
		// rolls pods only when this rendered revision changes.
		StampRevisionOnPodTemplate(&deployment.Spec.Template, podTemplateRevision)
	}

	if rolloutHash != "" {
		// Stamp the coordinator rollout hash; it includes mounted external content.
		rollout.StampRolloutHash(&deployment.Spec.Template, rollout.CoordinatorRolloutHashKey, rolloutHash)
	}

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
