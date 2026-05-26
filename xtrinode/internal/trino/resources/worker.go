package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/rollout"
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
)

// BuildWorkerDeployment builds the worker Deployment with revision stamping
// Note: Replicas are controlled by KEDA ScaledObject, so this sets replicas to 0 initially
func BuildWorkerDeployment(
	xtrinode *analyticsv1.XTrinode,
	configMapName string,
	catalogs []string,
	revision string,
	rolloutHash string,
	catalogSecretEnvVars []corev1.EnvVar,
) (*appsv1.Deployment, error) {
	shape, err := runtimeshape.Resolve(xtrinode)
	if err != nil {
		return nil, err
	}
	podTemplateRevision := rolloutHash
	if podTemplateRevision == "" {
		podTemplateRevision = revision
	}
	return buildWorkerDeployment(xtrinode, shape, configMapName, catalogs, revision, podTemplateRevision, rolloutHash, catalogSecretEnvVars)
}

func buildWorkerDeployment(
	xtrinode *analyticsv1.XTrinode,
	shape *runtimeshape.ResolvedRuntimeShape,
	configMapName string,
	catalogs []string,
	resourceRevision string,
	podTemplateRevision string,
	rolloutHash string,
	catalogSecretEnvVars []corev1.EnvVar,
) (*appsv1.Deployment, error) {
	var replicas *int32
	if shape.AutoscalingMode == runtimeshape.AutoscalingModeFixed && shape.FixedWorkers != nil {
		fixedWorkers := *shape.FixedWorkers
		replicas = &fixedWorkers
	}

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
	trinoContainer, err := buildTrinoContainer(xtrinode, shape, "worker", configMapName, catalogs)
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

	applySchedulingShape(&deployment.Spec.Template.Spec, shape.Placement.Worker)
	if len(deployment.Spec.Template.Spec.TopologySpreadConstraints) == 0 {
		if xtrinode.Spec.HelmChartConfig != nil &&
			xtrinode.Spec.HelmChartConfig.Worker != nil &&
			len(xtrinode.Spec.HelmChartConfig.Worker.TopologySpreadConstraints) > 0 {
			deployment.Spec.Template.Spec.TopologySpreadConstraints = xtrinode.Spec.HelmChartConfig.Worker.TopologySpreadConstraints
		}
	}

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			// Add priority class name if specified
			if priorityClassName, ok := worker["priorityClassName"].(string); ok && priorityClassName != "" {
				deployment.Spec.Template.Spec.PriorityClassName = priorityClassName
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

	// Apply deployment metadata from valuesOverlay.
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if deploymentMap, ok := worker["deployment"].(map[string]interface{}); ok {
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
		// Stamp the worker-specific rollout hash.
		rollout.StampRolloutHash(&deployment.Spec.Template, rollout.WorkerRolloutHashKey, rolloutHash)
	}

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
