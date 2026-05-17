package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/rollout"
	"github.com/xtrinode/xtrinode/internal/sizing"
)

// BuildWorkerDeployment builds the worker Deployment with revision stamping
// Note: Replicas are controlled by KEDA ScaledObject, so this sets replicas to 0 initially
func BuildWorkerDeployment(
	xtrinode *analyticsv1.XTrinode,
	preset *sizing.SizePreset,
	configMapName string,
	catalogs []string,
	revision string,
	rolloutHash string,
	catalogSecretEnvVars []corev1.EnvVar,
) (*appsv1.Deployment, error) {
	// Worker replica ownership:
	// - If KEDA enabled: set to nil (KEDA controls scaling, we don't manage this field)
	// - Else if server.workers specified: use that value
	// - Else: use preset default (typically 2)
	var replicas *int32
	kedaEnabled := isKEDAEnabled(xtrinode)

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			// Check for explicit workers count (only if KEDA not enabled)
			if !kedaEnabled {
				if workers, ok := ParseInt32(server["workers"]); ok {
					replicas = &workers
				}
			}
		}
	}

	// Set default replicas if KEDA not enabled and no explicit count
	if !kedaEnabled && replicas == nil {
		defaultReplicas := int32(config.DefaultWorkerReplicas)
		replicas = &defaultReplicas
	}

	if !kedaEnabled && xtrinode.Spec.MinWorkers != nil && *xtrinode.Spec.MinWorkers > 0 {
		if replicas == nil || *replicas < *xtrinode.Spec.MinWorkers {
			minWorkers := *xtrinode.Spec.MinWorkers
			replicas = &minWorkers
		}
	}

	// If KEDA enabled, replicas stays nil (KEDA owns this field)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            workerDeploymentName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoPodLabels(xtrinode, ComponentWorker),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: TrinoSelectorLabels(xtrinode, ComponentWorker),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      TrinoPodLabels(xtrinode, ComponentWorker),
					Annotations: buildWorkerPodAnnotations(xtrinode, configMapName, catalogs),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            serviceAccountName(xtrinode),
					AutomountServiceAccountToken:  automountServiceAccountToken(xtrinode),
					SecurityContext:               buildSecurityContext(xtrinode),
					TerminationGracePeriodSeconds: buildTerminationGracePeriod(xtrinode, "worker"),
					Containers:                    []corev1.Container{},
					Volumes:                       nil, // Will be set below
				},
			},
		},
	}

	// Build main Trino container
	trinoContainer, err := buildTrinoContainer(xtrinode, preset, "worker", configMapName, catalogs)
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

	// Build volumes
	volumes, err := buildVolumes(xtrinode, configMapName, catalogs, "worker")
	if err != nil {
		return nil, fmt.Errorf("failed to build volumes: %w", err)
	}
	deployment.Spec.Template.Spec.Volumes = volumes

	// Add graceful shutdown preStop hook if enabled.
	gracefulShutdownEnabled, gracePeriodSeconds := workerGracefulShutdownSettings(xtrinode)

	if gracefulShutdownEnabled {
		container := &deployment.Spec.Template.Spec.Containers[0]
		// Check if lifecycle is already set (conflicts with graceful shutdown)
		if container.Lifecycle != nil && container.Lifecycle.PreStop != nil {
			// Lifecycle already set, skip graceful shutdown (user must configure manually)
		} else {
			container.Lifecycle = &corev1.Lifecycle{
				PreStop: &corev1.LifecycleHandler{
					Exec: &corev1.ExecAction{
						Command: []string{
							"/bin/sh",
							"-c",
							// Send SHUTTING_DOWN state to worker (matches official Helm chart API call).
							// Use finite sleep instead of tail --pid=1 to avoid potential deadlock.
							workerGracefulShutdownCommand(xtrinode, gracePeriodSeconds),
						},
					},
				},
			}
			// Ensure terminationGracePeriodSeconds is at least 2x gracePeriodSeconds
			if deployment.Spec.Template.Spec.TerminationGracePeriodSeconds != nil {
				minGracePeriod := gracePeriodSeconds * 2
				if *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds < minGracePeriod {
					deployment.Spec.Template.Spec.TerminationGracePeriodSeconds = &minGracePeriod
				}
			}
		}
	}

	// Add init containers if specified
	if xtrinode.Spec.HelmChartConfig != nil && len(xtrinode.Spec.HelmChartConfig.InitContainers) > 0 {
		if workerInitContainers, ok := xtrinode.Spec.HelmChartConfig.InitContainers["worker"]; ok {
			deployment.Spec.Template.Spec.InitContainers = workerInitContainers
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

	// Add JMX exporter sidecar if enabled
	if jmxExporterEnabled(xtrinode, "worker") {
		jmxContainer := buildJMXExporterContainer(xtrinode, "worker")
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, jmxContainer)
	}

	// Add sidecar containers from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if sidecarContainers, ok := xtrinode.Spec.GetValuesOverlayMap()["sidecarContainers"].(map[string]interface{}); ok {
			if workerSidecars, ok := sidecarContainers["worker"].([]interface{}); ok {
				for _, sidecar := range workerSidecars {
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

	// Add node selector, affinity, tolerations, topology spread constraints
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if nodeSelector, ok := worker["nodeSelector"].(map[string]interface{}); ok {
				deployment.Spec.Template.Spec.NodeSelector = convertToStringMap(nodeSelector)
			}
			// Add affinity from valuesOverlay
			if affinityMap, ok := worker["affinity"].(map[string]interface{}); ok {
				affinity, err := buildAffinityFromMap(affinityMap)
				if err != nil {
					return nil, fmt.Errorf("failed to build worker affinity: %w", err)
				}
				if affinity != nil {
					deployment.Spec.Template.Spec.Affinity = affinity
				}
			}
			// Add tolerations from valuesOverlay
			if tolerationsList, ok := worker["tolerations"].([]interface{}); ok {
				tolerations, err := buildTolerationsFromList(tolerationsList)
				if err != nil {
					return nil, fmt.Errorf("failed to build worker tolerations: %w", err)
				}
				deployment.Spec.Template.Spec.Tolerations = tolerations
			}
			// Add priority class name if specified
			if priorityClassName, ok := worker["priorityClassName"].(string); ok && priorityClassName != "" {
				deployment.Spec.Template.Spec.PriorityClassName = priorityClassName
			}
			// Add topology spread constraints from valuesOverlay
			if topologySpreadConstraints, ok := worker["topologySpreadConstraints"].([]interface{}); ok {
				constraints := []corev1.TopologySpreadConstraint{}
				for _, constraint := range topologySpreadConstraints {
					if constraintMap, ok := constraint.(map[string]interface{}); ok {
						// Convert map to TopologySpreadConstraint using YAML
						yamlBytes, err := yaml.Marshal(constraintMap)
						if err == nil {
							var tsc corev1.TopologySpreadConstraint
							if err := yaml.Unmarshal(yamlBytes, &tsc); err == nil {
								constraints = append(constraints, tsc)
							}
						}
					}
				}
				if len(constraints) > 0 {
					deployment.Spec.Template.Spec.TopologySpreadConstraints = constraints
				}
			}
			// Add topology spread constraints from HelmChartConfig (if not already set)
			if len(deployment.Spec.Template.Spec.TopologySpreadConstraints) == 0 &&
				xtrinode.Spec.HelmChartConfig != nil &&
				xtrinode.Spec.HelmChartConfig.Worker != nil &&
				len(xtrinode.Spec.HelmChartConfig.Worker.TopologySpreadConstraints) > 0 {
				deployment.Spec.Template.Spec.TopologySpreadConstraints = xtrinode.Spec.HelmChartConfig.Worker.TopologySpreadConstraints
			}
		}
	} else if xtrinode.Spec.HelmChartConfig != nil &&
		xtrinode.Spec.HelmChartConfig.Worker != nil &&
		len(xtrinode.Spec.HelmChartConfig.Worker.TopologySpreadConstraints) > 0 {
		// Use HelmChartConfig if valuesOverlay not set
		deployment.Spec.Template.Spec.TopologySpreadConstraints = xtrinode.Spec.HelmChartConfig.Worker.TopologySpreadConstraints
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

	// Add deployment settings from valuesOverlay (can override rolloutPolicy)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if deploymentMap, ok := worker["deployment"].(map[string]interface{}); ok {
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

	// Stamp the worker-specific rollout hash.
	rollout.StampRolloutHash(&deployment.Spec.Template, rollout.WorkerRolloutHashKey, rolloutHash)

	return deployment, nil
}

func workerDeploymentName(xtrinode *analyticsv1.XTrinode) string {
	return fmt.Sprintf("trino-%s-worker", xtrinode.Name)
}

func buildWorkerPodAnnotations(xtrinode *analyticsv1.XTrinode, configMapName string, catalogs []string) map[string]string {
	annotations := make(map[string]string)

	// ConfigMap names are revisioned, so this value changes when rendered worker config changes.
	annotations["checksum/worker-config"] = configMapName

	// Note: Catalog content changes are handled by the worker rollout hash.
	// No need for catalog annotations here - rollout hash is the source of truth.

	// Add access control checksum if graceful shutdown enabled
	if gracefulShutdownEnabled, _ := workerGracefulShutdownSettings(xtrinode); gracefulShutdownEnabled {
		annotations["checksum/access-control-config"] = fmt.Sprintf("trino-%s-access-control-volume-worker", xtrinode.Name)
	}

	// Add custom annotations from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if customAnnotations, ok := worker["annotations"].(map[string]interface{}); ok {
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
