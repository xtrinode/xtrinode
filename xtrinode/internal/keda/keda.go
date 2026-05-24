package keda

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
	"github.com/xtrinode/xtrinode/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// EnsureScaledObject creates or updates a KEDA ScaledObject for worker autoscaling.
// The controller only calls this when KEDA is explicitly enabled and has scaler
// configuration. Without that, workers use the fixed replica count from the
// XTrinode spec.
//
//nolint:gocyclo // Scaler trigger assembly stays centralized so defaults and fallback behavior remain visible together.
func EnsureScaledObject(ctx context.Context, cli client.Client, scheme *runtime.Scheme, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	releaseName := config.BuildReleaseName(xtrinode.Name)
	scaledObjectName := config.BuildScaledObjectName(xtrinode.Name)
	deploymentName := config.BuildWorkerDeploymentName(xtrinode.Name)

	log.Info("Ensuring KEDA ScaledObject", "name", scaledObjectName, "namespace", xtrinode.Namespace, "deployment", deploymentName)

	// Validate that Deployment exists before creating ScaledObject
	deployment := &appsv1.Deployment{}
	deploymentErr := cli.Get(ctx, client.ObjectKey{
		Name:      deploymentName,
		Namespace: xtrinode.Namespace,
	}, deployment)
	if deploymentErr != nil {
		if k8serrors.IsNotFound(deploymentErr) {
			// Deployment doesn't exist yet - this is OK during initial creation
			// ScaledObject will be created but won't scale until Deployment exists
			log.Info("Deployment not found yet, ScaledObject will be created but won't scale until Deployment exists",
				"deployment", deploymentName,
				"namespace", xtrinode.Namespace)
		} else {
			log.Error(deploymentErr, "failed to check if Deployment exists", "deployment", deploymentName)
			// Continue anyway - ScaledObject creation will fail if Deployment doesn't exist
		}
	} else {
		log.Info("Deployment found, ScaledObject can scale", "deployment", deploymentName)
	}

	shape, err := runtimeshape.Resolve(xtrinode)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime shape for KEDA ScaledObject: %w", err)
	}
	minReplicas := int32Ptr(shape.MinWorkers)
	maxReplicas := int32Ptr(shape.MaxWorkers)

	// Determine scaler type. KEDA itself is opt-in at the controller layer; once
	// active, defaults depend on whether the user supplied Prometheus-specific fields.
	scalerType := ""
	scalingMetric := ""
	threshold := config.KEDADefaultMemoryThreshold
	prometheusServer := config.PrometheusDefaultURL
	var prometheusQuery string
	var httpEndpoint string
	var httpValueLocation string
	httpFormat := "prometheus"

	if xtrinode.Spec.KEDA != nil {
		if xtrinode.Spec.KEDA.ScalerType != "" {
			scalerType = normalizeKEDAKeyword(xtrinode.Spec.KEDA.ScalerType)
		}
		if xtrinode.Spec.KEDA.ScalingMetric != "" {
			scalingMetric = normalizeKEDAKeyword(xtrinode.Spec.KEDA.ScalingMetric)
		}
		if xtrinode.Spec.KEDA.Threshold != nil {
			threshold = *xtrinode.Spec.KEDA.Threshold
		}
		if xtrinode.Spec.KEDA.PrometheusServer != nil {
			prometheusServer = *xtrinode.Spec.KEDA.PrometheusServer
		}
		if xtrinode.Spec.KEDA.PrometheusQuery != nil {
			prometheusQuery = renderPrometheusQueryTemplate(*xtrinode.Spec.KEDA.PrometheusQuery, releaseName, xtrinode.Namespace, xtrinode.Name)
		}
		if xtrinode.Spec.KEDA.HTTPEndpoint != nil {
			httpEndpoint = strings.TrimSpace(*xtrinode.Spec.KEDA.HTTPEndpoint)
		}
		if normalizeKEDAKeyword(httpEndpoint) == "aggregator" {
			return fmt.Errorf("failed to configure KEDA metrics-api scaler: httpEndpoint aggregator is no longer supported")
		}
		if xtrinode.Spec.KEDA.HTTPValueLocation != nil {
			httpValueLocation = *xtrinode.Spec.KEDA.HTTPValueLocation
		}
	}
	if scalerType == "" {
		if prometheusQuery != "" || (xtrinode.Spec.KEDA != nil && xtrinode.Spec.KEDA.PrometheusServer != nil && strings.TrimSpace(*xtrinode.Spec.KEDA.PrometheusServer) != "") {
			scalerType = "prometheus"
		} else {
			scalerType = "http"
		}
	}
	if scalingMetric == "" {
		if scalerType == "prometheus" {
			scalingMetric = "query"
		} else {
			scalingMetric = "memory"
		}
	}

	// Build HTTP endpoint and value location only for metrics-api based HTTP
	// scaling. Memory and CPU use KEDA resource scalers and do not read an HTTP
	// metrics endpoint.
	if scalerType == "http" && !isKEDAResourceMetric(scalingMetric) {
		httpEndpoint, httpValueLocation = buildHTTPScalerConfig(xtrinode, releaseName, scalingMetric, httpEndpoint, httpValueLocation)
		httpFormat = buildHTTPFormat(scalingMetric, httpEndpoint)
	} else if scalerType == "prometheus" && prometheusQuery == "" {
		// Build default Prometheus query if custom query not provided
		prometheusQuery = buildPrometheusQueryForMetric(releaseName, xtrinode.Namespace, scalingMetric)
	}
	if scalerType == "prometheus" && strings.TrimSpace(prometheusQuery) == "" {
		return fmt.Errorf("failed to configure KEDA Prometheus scaler: no query for scalingMetric %q", scalingMetric)
	}
	if scalerType == "http" && !isKEDAResourceMetric(scalingMetric) && strings.TrimSpace(httpEndpoint) == "" {
		return fmt.Errorf("failed to configure KEDA metrics-api scaler: no endpoint for scalingMetric %q", scalingMetric)
	}

	// Set default threshold based on metric type if not specified
	if xtrinode.Spec.KEDA == nil || xtrinode.Spec.KEDA.Threshold == nil {
		switch scalingMetric {
		case "query":
			threshold = config.KEDADefaultQueryThreshold
		case "memory", "cpu":
			threshold = config.KEDADefaultMemoryThreshold
		default:
			threshold = config.KEDADefaultMemoryThreshold
		}
	}

	// Build ScaledObject
	authName := ""
	if shouldUseTrinoQueryAuth(scalerType, scalingMetric, httpEndpoint, xtrinode) {
		authName = buildMetricsAPIAuthName(releaseName)
		if authErr := ensureMetricsAPIAuth(ctx, cli, scheme, xtrinode, authName); authErr != nil {
			return authErr
		}
	} else if cleanupErr := cleanupMetricsAPIAuth(ctx, cli, releaseName, xtrinode.Namespace, log); cleanupErr != nil {
		return cleanupErr
	}

	scaledObject := buildScaledObjectSpec(scaledObjectName, deploymentName, xtrinode, minReplicas, maxReplicas, scalerType, scalingMetric, threshold, prometheusServer, prometheusQuery, httpEndpoint, httpValueLocation, httpFormat, authName)

	// Apply advanced configuration (cooldown periods, fallback)
	applyKEDAAdvancedConfig(scaledObject, xtrinode)

	// Check if ScaledObject exists to track creation vs update
	existingScaledObject := &kedav1alpha1.ScaledObject{}
	isCreate := false
	if getErr := cli.Get(ctx, client.ObjectKey{Name: scaledObjectName, Namespace: xtrinode.Namespace}, existingScaledObject); getErr != nil {
		if k8serrors.IsNotFound(getErr) {
			isCreate = true
		}
	}

	// Server-side Apply requires GVK to be set
	gvk, err := apiutil.GVKForObject(scaledObject, scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK for ScaledObject: %w", err)
	}
	scaledObject.GetObjectKind().SetGroupVersionKind(gvk)
	if err := cli.Patch(ctx, scaledObject, client.Apply, client.FieldOwner("xtrinode-operator"), client.ForceOwnership); err != nil {
		log.Error(err, "failed to create/update KEDA ScaledObject", "name", scaledObjectName)
		return fmt.Errorf("failed to create/update KEDA ScaledObject: %w", err)
	}

	// Track KEDA ScaledObject creation metric
	if isCreate {
		metrics.KEDAScaledObjectCreated.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
	}

	log.Info("KEDA ScaledObject ensured successfully",
		"name", scaledObjectName,
		"minReplicas", xtrinode.Spec.MinWorkers,
		"maxReplicas", xtrinode.Spec.MaxWorkers)

	return nil
}

// DisableScaledObject disables KEDA scaling by deleting the ScaledObject.
// Resource scalers such as CPU and memory cannot be configured with
// minReplicaCount=0, so suspend removes KEDA ownership before scaling workers
// to zero. Resume reconciliation recreates the ScaledObject from the XTrinode
// spec.
func DisableScaledObject(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	releaseName := config.BuildReleaseName(xtrinode.Name)
	scaledObjectName := fmt.Sprintf("%s-workers", releaseName)

	log.Info("Disabling KEDA ScaledObject", "name", scaledObjectName, "namespace", xtrinode.Namespace)

	// Delete only if it currently exists; suspend owns worker replica counts
	// directly after KEDA relinquishes the target.
	scaledObject := &kedav1alpha1.ScaledObject{}
	err := cli.Get(ctx, client.ObjectKey{
		Name:      scaledObjectName,
		Namespace: xtrinode.Namespace,
	}, scaledObject)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ScaledObject doesn't exist, nothing to disable
			log.Info("KEDA ScaledObject doesn't exist, nothing to disable", "name", scaledObjectName)
			return nil
		}
		return fmt.Errorf("failed to get KEDA ScaledObject: %w", err)
	}

	if err := cli.Delete(ctx, scaledObject); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("KEDA ScaledObject already gone", "name", scaledObjectName)
			return nil
		}
		log.Error(err, "failed to disable KEDA ScaledObject", "name", scaledObjectName)
		return fmt.Errorf("failed to disable KEDA ScaledObject: %w", err)
	}

	log.Info("KEDA ScaledObject disabled successfully", "name", scaledObjectName)
	return nil
}

// EnableScaledObjectWithWakeMinWorkers enables KEDA scaling with wakeMinWorkers as min
// Used during resume to ensure wakeMinWorkers are maintained
func EnableScaledObjectWithWakeMinWorkers(ctx context.Context, cli client.Client, scheme *runtime.Scheme, xtrinode *analyticsv1.XTrinode, wakeMinWorkers int32, log logr.Logger) error {
	releaseName := config.BuildReleaseName(xtrinode.Name)
	scaledObjectName := fmt.Sprintf("%s-workers", releaseName)

	log.Info("Enabling KEDA ScaledObject with wakeMinWorkers", "name", scaledObjectName, "wakeMinWorkers", wakeMinWorkers, "namespace", xtrinode.Namespace)

	// Get existing ScaledObject to preserve other settings
	scaledObject := &kedav1alpha1.ScaledObject{}
	err := cli.Get(ctx, client.ObjectKey{
		Name:      scaledObjectName,
		Namespace: xtrinode.Namespace,
	}, scaledObject)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ScaledObject was deleted during suspend. Recreate it with the
			// temporary wake floor instead of the steady-state spec minimum.
			wakeXTrinode := xtrinode.DeepCopy()
			wakeXTrinode.Spec.MinWorkers = &wakeMinWorkers
			return EnsureScaledObject(ctx, cli, scheme, wakeXTrinode, log)
		}
		return fmt.Errorf("failed to get KEDA ScaledObject: %w", err)
	}

	// Set min to wakeMinWorkers, restore max from spec
	shape, err := runtimeshape.Resolve(xtrinode)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime shape for KEDA wake scaling: %w", err)
	}
	maxReplicas := int32Ptr(shape.MaxWorkers)

	scaledObject.Spec.MinReplicaCount = &wakeMinWorkers
	scaledObject.Spec.MaxReplicaCount = maxReplicas

	// Add annotation to track wake time for wakeTTL enforcement
	if scaledObject.Annotations == nil {
		scaledObject.Annotations = make(map[string]string)
	}
	scaledObject.Annotations[config.WakeTimeAnnotation] = metav1.Now().Format(time.RFC3339)
	if xtrinode.Spec.WakeTTL != nil {
		scaledObject.Annotations[config.WakeTTLAnnotation] = xtrinode.Spec.WakeTTL.Duration.String()
	}

	// Update ScaledObject
	if err := cli.Update(ctx, scaledObject); err != nil {
		log.Error(err, "failed to enable KEDA ScaledObject", "name", scaledObjectName)
		return fmt.Errorf("failed to enable KEDA ScaledObject: %w", err)
	}

	log.Info("KEDA ScaledObject enabled with wakeMinWorkers", "name", scaledObjectName, "wakeMinWorkers", wakeMinWorkers)
	return nil
}

// DeleteScaledObject deletes the KEDA ScaledObject
func DeleteScaledObject(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	releaseName := config.BuildReleaseName(xtrinode.Name)
	scaledObjectName := fmt.Sprintf("%s-workers", releaseName)

	log.Info("Deleting KEDA ScaledObject", "name", scaledObjectName, "namespace", xtrinode.Namespace)

	scaledObject := &kedav1alpha1.ScaledObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaledObjectName,
			Namespace: xtrinode.Namespace,
		},
	}

	err := cli.Delete(ctx, scaledObject)
	if err != nil {
		// If not found, that's OK - already deleted
		if client.IgnoreNotFound(err) == nil {
			log.Info("KEDA ScaledObject already deleted", "name", scaledObjectName)
			return nil
		}
		log.Error(err, "failed to delete KEDA ScaledObject", "name", scaledObjectName)
		return fmt.Errorf("failed to delete KEDA ScaledObject: %w", err)
	}

	metrics.KEDAScaledObjectDeleted.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
	log.Info("KEDA ScaledObject deleted successfully", "name", scaledObjectName)

	if err := cleanupMetricsAPIAuth(ctx, cli, releaseName, xtrinode.Namespace, log); err != nil {
		return err
	}

	return nil
}

// buildPrometheusQuery builds the Prometheus query for worker scaling.
func buildPrometheusQuery(releaseName, namespace string) string {
	return buildPrometheusQueryForMetric(releaseName, namespace, "query")
}

func renderPrometheusQueryTemplate(queryTemplate, releaseName, namespace, xtrinodeName string) string {
	query := strings.ReplaceAll(queryTemplate, "{releaseName}", releaseName)
	query = strings.ReplaceAll(query, "{namespace}", namespace)
	query = strings.ReplaceAll(query, "{xtrinodeName}", xtrinodeName)
	return query
}

// buildPrometheusQueryForMetric builds the Prometheus query based on scaling metric type
func buildPrometheusQueryForMetric(releaseName, namespace, metric string) string {
	switch normalizeKEDAKeyword(metric) {
	case "memory":
		// Memory-based scaling: average memory usage per worker
		// Query: (sum of memory allocated across all workers) / (number of workers)
		return fmt.Sprintf(
			`(sum(trino_memory_allocated_bytes{pod=~%q,namespace=%q}) / 1024 / 1024 / 1024) / max(count(kube_pod_labels{pod=~%q,namespace=%q}), 1)`,
			releaseName+"-worker-.*", namespace, releaseName+"-worker-.*", namespace,
		)
	case "cpu":
		// CPU-based scaling: average CPU usage per worker
		// Query: (sum of CPU usage across all workers) / (number of workers)
		return fmt.Sprintf(
			`(sum(rate(container_cpu_usage_seconds_total{pod=~%q,namespace=%q}[5m])) * 100) / max(count(kube_pod_labels{pod=~%q,namespace=%q}), 1)`,
			releaseName+"-worker-.*", namespace, releaseName+"-worker-.*", namespace,
		)
	case "query":
		// Query-based scaling through Prometheus uses gateway-observed query
		// pressure. This catches queued queries while workers are scaled to zero,
		// before Trino worker metrics can exist.
		xtrinodeName := strings.TrimPrefix(releaseName, "trino-")
		return fmt.Sprintf(
			`sum(xtrinode_gateway_inflight_queries{exported_namespace=%q,xtrinode=%q}) or sum(xtrinode_gateway_inflight_queries{namespace=%q,xtrinode=%q})`,
			namespace,
			xtrinodeName,
			namespace,
			xtrinodeName,
		)
	default:
		return ""
	}
}

// buildHTTPScalerEndpoint builds the HTTP endpoint URL for HTTP scaler
func buildHTTPScalerEndpoint(xtrinode *analyticsv1.XTrinode, releaseName, scalingMetric, httpEndpoint string) string {
	switch normalizeKEDAKeyword(httpEndpoint) {
	case "":
		return buildDefaultEndpoint(xtrinode, releaseName, scalingMetric)
	case "coordinator":
		if normalizeKEDAKeyword(scalingMetric) == "query" {
			return config.BuildCoordinatorQueryAPIURL(xtrinode.Name, xtrinode.Namespace)
		}
		return config.BuildCoordinatorMetricsURL(xtrinode.Name, xtrinode.Namespace)
	case "jmx":
		return buildJMXEndpoint(xtrinode)
	default:
		return strings.TrimSpace(httpEndpoint)
	}
}

// buildJMXEndpoint builds the JMX exporter metrics endpoint URL
func buildJMXEndpoint(xtrinode *analyticsv1.XTrinode) string {
	jmxPort := int32(config.JMXExporterPort)
	if xtrinode.Spec.KEDA != nil && xtrinode.Spec.KEDA.JMXExporter != nil && xtrinode.Spec.KEDA.JMXExporter.Port != nil {
		jmxPort = *xtrinode.Spec.KEDA.JMXExporter.Port
	}
	return config.BuildJMXMetricsURL(xtrinode.Name, xtrinode.Namespace, jmxPort)
}

// buildDefaultEndpoint builds the default endpoint based on scaling metric type
func buildDefaultEndpoint(xtrinode *analyticsv1.XTrinode, releaseName, scalingMetric string) string {
	switch normalizeKEDAKeyword(scalingMetric) {
	case "query":
		// Query-based scale-from-zero uses Trino's live query API because
		// /metrics does not expose WAITING_FOR_RESOURCES queries as queued.
		return config.BuildCoordinatorQueryAPIURL(xtrinode.Name, xtrinode.Namespace)
	case "memory", "cpu":
		// Memory/CPU-based HTTP mode maps to KEDA resource scalers, not an HTTP endpoint.
		return ""
	default:
		// Default to coordinator
		return config.BuildCoordinatorMetricsURL(xtrinode.Name, xtrinode.Namespace)
	}
}

// buildHTTPValueLocation builds the value selector for metrics-api scalers.
func buildHTTPValueLocation(scalingMetric, httpValueLocation string) string {
	if httpValueLocation != "" {
		return httpValueLocation
	}

	// Auto-detect based on metric type
	switch normalizeKEDAKeyword(scalingMetric) {
	case "query":
		// Trino keeps recently finished/failed queries in /v1/query, so count only
		// non-terminal states for scale-from-zero.
		return `#(state!="FINISHED")#|#(state!="FAILED")#|#`
	case "memory":
		// Metrics-api memory extraction if a custom endpoint is used.
		return `trino_memory_allocated_bytes\{[^}]*\}\s+([0-9.]+)`
	case "cpu":
		// Metrics-api CPU extraction if a custom endpoint is used.
		return `container_cpu_usage_seconds_total\{[^}]*\}\s+([0-9.]+)`
	default:
		return "#"
	}
}

func buildHTTPFormat(scalingMetric, httpEndpoint string) string {
	if normalizeKEDAKeyword(scalingMetric) == "query" && strings.HasSuffix(strings.TrimSpace(httpEndpoint), "/v1/query") {
		return "json"
	}
	return "prometheus"
}

func normalizeKEDAKeyword(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isKEDAResourceMetric(metric string) bool {
	switch normalizeKEDAKeyword(metric) {
	case "memory", "cpu":
		return true
	default:
		return false
	}
}

// buildHTTPScalerConfig builds HTTP endpoint URL and value location for HTTP scaler
// Returns: (endpointURL, valueLocation)
// NOTE: For query-based scaling, coordinator aggregates queries across all workers.
// Memory/CPU scaling uses KEDA resource scalers and should not call this helper.
func buildHTTPScalerConfig(xtrinode *analyticsv1.XTrinode, releaseName, scalingMetric, httpEndpoint, httpValueLocation string) (endpoint, valueLocation string) {
	if isKEDAResourceMetric(scalingMetric) {
		return "", ""
	}
	endpoint = buildHTTPScalerEndpoint(xtrinode, releaseName, scalingMetric, httpEndpoint)
	valueLocation = buildHTTPValueLocation(scalingMetric, httpValueLocation)
	return endpoint, valueLocation
}

// buildScaledObjectSpec builds the ScaledObject spec with basic configuration
func buildScaledObjectSpec(scaledObjectName, deploymentName string, xtrinode *analyticsv1.XTrinode, minReplicas, maxReplicas *int32, scalerType, scalingMetric, threshold, prometheusServer, prometheusQuery, httpEndpoint, httpValueLocation, httpFormat, authName string) *kedav1alpha1.ScaledObject {
	return &kedav1alpha1.ScaledObject{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "keda.sh/v1alpha1",
			Kind:       "ScaledObject",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaledObjectName,
			Namespace: xtrinode.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "xtrinode",
				"app.kubernetes.io/instance":   xtrinode.Name,
				"app.kubernetes.io/component":  "keda",
				"app.kubernetes.io/managed-by": "xtrinode-operator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: analyticsv1.GroupVersion.String(),
					Kind:       "XTrinode",
					Name:       xtrinode.Name,
					UID:        xtrinode.UID,
					Controller: func() *bool { b := true; return &b }(),
				},
			},
		},
		Spec: kedav1alpha1.ScaledObjectSpec{
			ScaleTargetRef: &kedav1alpha1.ScaleTarget{
				Name: deploymentName, // Trino Helm chart creates worker Deployment
				Kind: "Deployment",
			},
			MinReplicaCount: minReplicas,
			MaxReplicaCount: maxReplicas,
			Triggers:        buildKEDATriggers(scalerType, scalingMetric, threshold, prometheusServer, prometheusQuery, httpEndpoint, httpValueLocation, httpFormat, authName),
		},
	}
}

// applyKEDAAdvancedConfig applies advanced KEDA configuration (cooldown periods, fallback)
func applyKEDAAdvancedConfig(scaledObject *kedav1alpha1.ScaledObject, xtrinode *analyticsv1.XTrinode) {
	if xtrinode.Spec.KEDA == nil {
		return
	}

	// Apply cooldown periods
	if xtrinode.Spec.KEDA.ScaleDownCooldown != nil || xtrinode.Spec.KEDA.ScaleUpCooldown != nil {
		applyKEDACooldownConfig(scaledObject, xtrinode.Spec.KEDA)
	}

	// Apply fallback configuration
	applyKEDAFallbackConfig(scaledObject, xtrinode)
}

// applyKEDACooldownConfig applies cooldown period configuration
func applyKEDACooldownConfig(scaledObject *kedav1alpha1.ScaledObject, kedaSpec *analyticsv1.KEDASpec) {
	behavior := &autoscalingv2.HorizontalPodAutoscalerBehavior{}

	if kedaSpec.ScaleDownCooldown != nil {
		scaledObject.Spec.CooldownPeriod = int32Ptr(int32(kedaSpec.ScaleDownCooldown.Seconds()))
		behavior.ScaleDown = &autoscalingv2.HPAScalingRules{
			StabilizationWindowSeconds: int32Ptr(int32(kedaSpec.ScaleDownCooldown.Seconds())),
		}
	}

	if kedaSpec.ScaleUpCooldown != nil {
		behavior.ScaleUp = &autoscalingv2.HPAScalingRules{
			StabilizationWindowSeconds: int32Ptr(int32(kedaSpec.ScaleUpCooldown.Seconds())),
		}
	}

	scaledObject.Spec.Advanced = &kedav1alpha1.AdvancedConfig{
		HorizontalPodAutoscalerConfig: &kedav1alpha1.HorizontalPodAutoscalerConfig{
			Behavior: behavior,
		},
	}
}

// applyKEDAFallbackConfig applies fallback configuration from valuesOverlay
func applyKEDAFallbackConfig(scaledObject *kedav1alpha1.ScaledObject, xtrinode *analyticsv1.XTrinode) {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{})
	if !ok {
		return
	}

	keda, ok := server["keda"].(map[string]interface{})
	if !ok {
		return
	}

	fallback, ok := keda["fallback"].(map[string]interface{})
	if !ok {
		return
	}

	fallbackConfig := extractFallbackConfig(fallback)
	if fallbackConfig != nil {
		scaledObject.Spec.Fallback = fallbackConfig
	}
}

func buildMetricsAPIAuthName(releaseName string) string {
	return releaseName + "-keda-metrics-auth"
}

func shouldUseTrinoQueryAuth(scalerType, scalingMetric, httpEndpoint string, xtrinode *analyticsv1.XTrinode) bool {
	return scalerType == "http" &&
		normalizeKEDAKeyword(scalingMetric) == "query" &&
		strings.TrimSpace(httpEndpoint) == config.BuildCoordinatorQueryAPIURL(xtrinode.Name, xtrinode.Namespace)
}

func ensureMetricsAPIAuth(ctx context.Context, cli client.Client, scheme *runtime.Scheme, xtrinode *analyticsv1.XTrinode, authName string) error {
	labels := map[string]string{
		"app.kubernetes.io/name":       "xtrinode",
		"app.kubernetes.io/instance":   xtrinode.Name,
		"app.kubernetes.io/component":  "keda-auth",
		"app.kubernetes.io/managed-by": "xtrinode-operator",
	}
	ownerReferences := []metav1.OwnerReference{
		{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
			Name:       xtrinode.Name,
			UID:        xtrinode.UID,
			Controller: func() *bool { b := true; return &b }(),
		},
	}

	authData, authRefs := buildMetricsAPIAuthDataAndRefs(xtrinode, authName)

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            authName,
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			OwnerReferences: ownerReferences,
		},
		Type: corev1.SecretTypeOpaque,
		Data: authData,
	}
	if err := applyKEDAObject(ctx, cli, scheme, secret); err != nil {
		return fmt.Errorf("failed to create/update KEDA metrics-api auth Secret: %w", err)
	}

	triggerAuthentication := &kedav1alpha1.TriggerAuthentication{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "keda.sh/v1alpha1",
			Kind:       "TriggerAuthentication",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            authName,
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			OwnerReferences: ownerReferences,
		},
		Spec: kedav1alpha1.TriggerAuthenticationSpec{
			SecretTargetRef: authRefs,
		},
	}
	if err := applyKEDAObject(ctx, cli, scheme, triggerAuthentication); err != nil {
		return fmt.Errorf("failed to create/update KEDA metrics-api TriggerAuthentication: %w", err)
	}

	return nil
}

func buildMetricsAPIAuthDataAndRefs(xtrinode *analyticsv1.XTrinode, authName string) (map[string][]byte, []kedav1alpha1.AuthSecretTargetRef) {
	ref := func(parameter, name, key string) kedav1alpha1.AuthSecretTargetRef {
		return kedav1alpha1.AuthSecretTargetRef(kedav1alpha1.AuthTargetRef{
			Parameter: parameter,
			Name:      name,
			Key:       key,
		})
	}

	if controlauth.HasPasswordSecret(xtrinode) && controlauth.HTTPAuthenticationConfigured(xtrinode) {
		passwordRef := xtrinode.Spec.TrinoControlAuth.PasswordSecret
		data := map[string][]byte{
			"authModes":    []byte("basic,apiKey"),
			"username":     []byte(controlauth.Username(xtrinode)),
			"apiKey":       []byte(controlauth.ForwardedProtoHTTPS),
			"method":       []byte("header"),
			"keyParamName": []byte(controlauth.ForwardedProtoHeader),
		}
		refs := []kedav1alpha1.AuthSecretTargetRef{
			ref("authModes", authName, "authModes"),
			ref("username", authName, "username"),
			ref("password", passwordRef.Name, passwordRef.Key),
			ref("apiKey", authName, "apiKey"),
			ref("method", authName, "method"),
			ref("keyParamName", authName, "keyParamName"),
		}
		return data, refs
	}

	data := map[string][]byte{
		"authModes":    []byte("apiKey"),
		"apiKey":       []byte("xtrinode-keda"),
		"method":       []byte("header"),
		"keyParamName": []byte(config.TrinoUserHeader),
	}
	refs := []kedav1alpha1.AuthSecretTargetRef{
		ref("authModes", authName, "authModes"),
		ref("apiKey", authName, "apiKey"),
		ref("method", authName, "method"),
		ref("keyParamName", authName, "keyParamName"),
	}
	return data, refs
}

func applyKEDAObject(ctx context.Context, cli client.Client, scheme *runtime.Scheme, obj client.Object) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK for %T: %w", obj, err)
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)
	return cli.Patch(ctx, obj, client.Apply, client.FieldOwner("xtrinode-operator"), client.ForceOwnership)
}

func cleanupMetricsAPIAuth(ctx context.Context, cli client.Client, releaseName, namespace string, log logr.Logger) error {
	authName := buildMetricsAPIAuthName(releaseName)

	triggerAuthentication := &kedav1alpha1.TriggerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authName,
			Namespace: namespace,
		},
	}
	if err := cli.Delete(ctx, triggerAuthentication); err != nil && !k8serrors.IsNotFound(err) {
		log.Error(err, "failed to delete stale KEDA metrics-api TriggerAuthentication", "name", authName, "namespace", namespace)
		return fmt.Errorf("failed to delete stale KEDA metrics-api TriggerAuthentication: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authName,
			Namespace: namespace,
		},
	}
	if err := cli.Delete(ctx, secret); err != nil && !k8serrors.IsNotFound(err) {
		log.Error(err, "failed to delete stale KEDA metrics-api auth Secret", "name", authName, "namespace", namespace)
		return fmt.Errorf("failed to delete stale KEDA metrics-api auth Secret: %w", err)
	}

	return nil
}

// ParseInt32 parses a numeric value to int32 (handles int, int64, float64)
func ParseInt32(val interface{}) (int32, bool) {
	switch v := val.(type) {
	case int:
		return int32(v), true
	case int32:
		return v, true
	case int64:
		return int32(v), true
	case float64:
		return int32(v), true
	case float32:
		return int32(v), true
	default:
		return 0, false
	}
}

// extractFallbackConfig extracts fallback configuration from valuesOverlay map
func extractFallbackConfig(fallback map[string]interface{}) *kedav1alpha1.Fallback {
	fallbackConfig := &kedav1alpha1.Fallback{}

	if failureThreshold, ok := ParseInt32(fallback["failureThreshold"]); ok {
		fallbackConfig.FailureThreshold = failureThreshold
	}

	if replicas, ok := ParseInt32(fallback["replicas"]); ok {
		fallbackConfig.Replicas = replicas
	}

	if fallbackConfig.FailureThreshold > 0 && fallbackConfig.Replicas > 0 {
		return fallbackConfig
	}

	return nil
}

// buildKEDATriggers builds the appropriate KEDA trigger based on scaler type.
// The "http" scaler type is kept as the XTrinode API's bare-metal mode, but it
// maps memory/cpu to KEDA's built-in resource scalers because upstream KEDA
// does not provide a built-in ScaledObject trigger named "http".
func buildKEDATriggers(scalerType, scalingMetric, threshold, prometheusServer, prometheusQuery, httpEndpoint, httpValueLocation, httpFormat, authName string) []kedav1alpha1.ScaleTriggers {
	if scalerType == "http" {
		switch normalizeKEDAKeyword(scalingMetric) {
		case "memory", "cpu":
			return []kedav1alpha1.ScaleTriggers{
				{
					Type:       normalizeKEDAKeyword(scalingMetric),
					MetricType: "Utilization",
					Metadata: map[string]string{
						"value": threshold,
					},
				},
			}
		}

		// Query-based bare-metal scaling uses KEDA's metrics-api scaler against
		// the configured HTTP endpoint. Custom httpValueLocation is recommended
		// because Trino metric names vary by version.
		trigger := kedav1alpha1.ScaleTriggers{
			Type: "metrics-api",
			Metadata: map[string]string{
				"url":           httpEndpoint,
				"format":        httpFormat,
				"valueLocation": httpValueLocation,
				"targetValue":   threshold,
				"unsafeSsl":     "false",
			},
		}
		if authName != "" {
			trigger.AuthenticationRef = &kedav1alpha1.AuthenticationRef{Name: authName}
		}
		return []kedav1alpha1.ScaleTriggers{trigger}
	}

	// Prometheus scaler.
	return []kedav1alpha1.ScaleTriggers{
		{
			Type: "prometheus",
			Metadata: map[string]string{
				"serverAddress": prometheusServer,
				"query":         prometheusQuery,
				"threshold":     threshold,
			},
		},
	}
}

// Helper function to create int32 pointer
func int32Ptr(i int32) *int32 {
	return &i
}
