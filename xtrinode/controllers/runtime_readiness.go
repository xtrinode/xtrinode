package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/internal/trino/controlendpoint"
	trinoresources "github.com/xtrinode/xtrinode/internal/trino/resources"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

func (r *XTrinodeReconciler) transitionToReady(ctx context.Context, xtrinode *analyticsv1.XTrinode, oldPhase string, log logr.Logger) error {
	currentPhase := status.Phase(xtrinode.Status.Phase)
	if err := currentPhase.TransitionTo(status.PhaseReady); err != nil {
		log.Error(err, "invalid phase transition", "from", currentPhase, "to", "Ready")
	}
	xtrinode.Status.Phase = string(status.PhaseReady)

	r.updateStateMetrics(xtrinode)

	xtrinode.Status.CoordinatorURL = controlendpoint.CoordinatorURL(xtrinode)

	workerDeployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      config.BuildWorkerDeploymentName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}, workerDeployment); err == nil {
		if workerDeployment.Status.ReadyReplicas > 0 {
			xtrinode.Status.Workers = workerDeployment.Status.ReadyReplicas
		} else {
			xtrinode.Status.Workers = workerDeployment.Status.Replicas
		}
	} else {
		xtrinode.Status.Workers = 0
	}

	if xtrinode.Spec.MaxWorkers != nil {
		metrics.WorkersDesired.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(float64(*xtrinode.Spec.MaxWorkers))
	}

	status.SetConditionWithEvents(xtrinode, status.ConditionTypeReady, metav1.ConditionTrue, status.ConditionReasonAllComponentsReady, "All components ready", r.EventRecorder)
	setSchedulingReadyConditions(xtrinode)
	status.SetCondition(xtrinode, status.ConditionTypeReconciling, metav1.ConditionFalse, status.ConditionReasonReconciling, "Reconciliation complete")
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeSuspended, metav1.ConditionFalse, status.ConditionReasonNotSuspended, "XTrinode is not suspended", r.EventRecorder)
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeError, metav1.ConditionFalse, status.ConditionReasonNoError, "No errors", r.EventRecorder)

	r.updateConditionMetrics(xtrinode)

	if err := r.updateStatus(ctx, xtrinode, log); err != nil {
		log.Error(err, "unable to update XTrinode status")
		metrics.ReconcileTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "error").Inc()
		metrics.ReconcileErrors.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "status_update_error").Inc()
		return err
	}

	if oldPhase != "Ready" {
		r.EventRecorder.Normal(xtrinode, events.ReasonPhaseChanged, events.FormatMessage("Phase changed from %s to Ready", oldPhase))
	}
	r.EventRecorder.Normal(xtrinode, events.ReasonReconcileComplete, "Reconciliation completed successfully")
	metrics.ReconcileTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "success").Inc()

	return nil
}

func (r *XTrinodeReconciler) syncSchedulingCondition(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) {
	pods := &corev1.PodList{}
	selector := trinoresources.TrinoSelectorLabels(xtrinode, "")
	if err := r.List(ctx, pods, client.InNamespace(xtrinode.Namespace), client.MatchingLabels(selector)); err != nil {
		log.Error(err, "failed to list runtime pods for scheduling condition")
		setSchedulingUnknownConditions(xtrinode, fmt.Sprintf("Failed to inspect runtime pod scheduling: %v", err))
		return
	}

	deployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, deployments, client.InNamespace(xtrinode.Namespace), client.MatchingLabels(selector)); err != nil {
		log.Error(err, "failed to list runtime deployments for scheduling condition")
		setSchedulingUnknownConditions(xtrinode, fmt.Sprintf("Failed to inspect runtime deployment scheduling: %v", err))
		return
	}

	blockers := schedulingBlockerSet{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		for _, condition := range pod.Status.Conditions {
			if condition.Type != corev1.PodScheduled || condition.Status != corev1.ConditionFalse {
				continue
			}
			reason := strings.TrimSpace(condition.Reason)
			if reason == "" {
				reason = "PodNotScheduled"
			}
			message := strings.TrimSpace(condition.Message)
			if message == "" {
				message = "pod is not scheduled"
			}
			component := pod.Labels[trinoresources.AppComponentLabel]
			if component == "" {
				component = "runtime"
			}
			blockers.add(reason, message, fmt.Sprintf("%s pod %s: %s: %s", component, pod.Name, reason, message))
		}
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		for _, condition := range deployment.Status.Conditions {
			if condition.Type != appsv1.DeploymentReplicaFailure || condition.Status != corev1.ConditionTrue {
				continue
			}
			reason := strings.TrimSpace(condition.Reason)
			if reason == "" {
				reason = "ReplicaFailure"
			}
			message := strings.TrimSpace(condition.Message)
			if message == "" {
				message = "deployment cannot create runtime pods"
			}
			component := deployment.Labels[trinoresources.AppComponentLabel]
			if component == "" {
				component = "runtime"
			}
			blockers.add(reason, message, fmt.Sprintf("%s deployment %s: %s: %s", component, deployment.Name, reason, message))
		}
	}
	r.addClusterSchedulingDiagnostics(ctx, &blockers, pods.Items, log)
	if blockers.hasAny() {
		setSchedulingBlockedConditions(xtrinode, &blockers)
		return
	}
	setSchedulingReadyConditions(xtrinode)
}

type schedulingBlockerSet struct {
	all       []string
	placement []string
	taints    []string
	quota     []string
	capacity  []string
}

func (b *schedulingBlockerSet) add(reason, message, summary string) {
	b.all = append(b.all, summary)
	classification := classifySchedulingBlocker(reason, message)
	if classification.placement {
		b.placement = append(b.placement, summary)
	}
	if classification.taints {
		b.taints = append(b.taints, summary)
	}
	if classification.quota {
		b.quota = append(b.quota, summary)
	}
	if classification.capacity {
		b.capacity = append(b.capacity, summary)
	}
}

func (b *schedulingBlockerSet) hasAny() bool {
	return len(b.all) > 0
}

type schedulingBlockerClassification struct {
	placement bool
	taints    bool
	quota     bool
	capacity  bool
}

func classifySchedulingBlocker(reason, message string) schedulingBlockerClassification {
	text := strings.ToLower(reason + " " + message)
	return schedulingBlockerClassification{
		placement: containsAnySchedulingPhrase(text,
			"node affinity",
			"node selector",
			"nodeselector",
			"didn't match pod",
			"did not match pod",
			"didn't match node selector",
			"did not match node selector",
		),
		taints: containsAnySchedulingPhrase(text, "taint", "tolerat"),
		quota:  containsAnySchedulingPhrase(text, "quota", "resourcequota", "exceeded quota"),
		capacity: containsAnySchedulingPhrase(text,
			"insufficient cpu",
			"insufficient memory",
			"insufficient ephemeral-storage",
			"insufficient pods",
			"too many pods",
			"max node group size",
		),
	}
}

func containsAnySchedulingPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func setSchedulingReadyConditions(xtrinode *analyticsv1.XTrinode) {
	status.SetCondition(xtrinode, status.ConditionTypeSchedulingReady, metav1.ConditionTrue, status.ConditionReasonSchedulingReady, "Runtime pods are scheduled or no scheduling blockers are currently reported")
	status.SetCondition(xtrinode, status.ConditionTypePlacementReady, metav1.ConditionTrue, status.ConditionReasonPlacementReady, "No placement selector blockers are currently reported")
	status.SetCondition(xtrinode, status.ConditionTypeTaintsReady, metav1.ConditionTrue, status.ConditionReasonTaintsReady, "No taint or toleration blockers are currently reported")
	status.SetCondition(xtrinode, status.ConditionTypeQuotaReady, metav1.ConditionTrue, status.ConditionReasonQuotaReady, "No namespace quota blockers are currently reported")
	status.SetCondition(xtrinode, status.ConditionTypeCapacityReady, metav1.ConditionTrue, status.ConditionReasonCapacityReady, "No node capacity blockers are currently reported")
}

func setSchedulingUnknownConditions(xtrinode *analyticsv1.XTrinode, message string) {
	status.SetCondition(xtrinode, status.ConditionTypeSchedulingReady, metav1.ConditionUnknown, status.ConditionReasonSchedulingUnknown, message)
	status.SetCondition(xtrinode, status.ConditionTypePlacementReady, metav1.ConditionUnknown, status.ConditionReasonPlacementUnknown, message)
	status.SetCondition(xtrinode, status.ConditionTypeTaintsReady, metav1.ConditionUnknown, status.ConditionReasonTaintsUnknown, message)
	status.SetCondition(xtrinode, status.ConditionTypeQuotaReady, metav1.ConditionUnknown, status.ConditionReasonQuotaUnknown, message)
	status.SetCondition(xtrinode, status.ConditionTypeCapacityReady, metav1.ConditionUnknown, status.ConditionReasonCapacityUnknown, message)
}

func setSchedulingBlockedConditions(xtrinode *analyticsv1.XTrinode, blockers *schedulingBlockerSet) {
	status.SetCondition(xtrinode, status.ConditionTypeSchedulingReady, metav1.ConditionFalse, status.ConditionReasonSchedulingBlocked, strings.Join(blockers.all, "; "))
	setSchedulingCategoryCondition(xtrinode, status.ConditionTypePlacementReady, status.ConditionReasonPlacementReady, status.ConditionReasonPlacementBlocked, blockers.placement, "No placement selector blockers are currently reported")
	setSchedulingCategoryCondition(xtrinode, status.ConditionTypeTaintsReady, status.ConditionReasonTaintsReady, status.ConditionReasonTaintsBlocked, blockers.taints, "No taint or toleration blockers are currently reported")
	setSchedulingCategoryCondition(xtrinode, status.ConditionTypeQuotaReady, status.ConditionReasonQuotaReady, status.ConditionReasonQuotaBlocked, blockers.quota, "No namespace quota blockers are currently reported")
	setSchedulingCategoryCondition(xtrinode, status.ConditionTypeCapacityReady, status.ConditionReasonCapacityReady, status.ConditionReasonCapacityBlocked, blockers.capacity, "No node capacity blockers are currently reported")
}

func setSchedulingCategoryCondition(xtrinode *analyticsv1.XTrinode, conditionType, readyReason, blockedReason string, blockers []string, readyMessage string) {
	if len(blockers) == 0 {
		status.SetCondition(xtrinode, conditionType, metav1.ConditionTrue, readyReason, readyMessage)
		return
	}
	status.SetCondition(xtrinode, conditionType, metav1.ConditionFalse, blockedReason, strings.Join(blockers, "; "))
}

type trinoRuntimeReadiness struct {
	Ready                    bool
	Reason                   string
	Message                  string
	CoordinatorReadyReplicas int32
	WorkerReadyReplicas      int32
	RequiredWorkers          int32
}

func (r *XTrinodeReconciler) checkTrinoRuntimeReady(ctx context.Context, xtrinode *analyticsv1.XTrinode) (trinoRuntimeReadiness, error) {
	coordinator := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      config.BuildCoordinatorDeploymentName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}, coordinator); err != nil {
		if k8serrors.IsNotFound(err) {
			return runtimeNotReady("CoordinatorDeploymentMissing", "Coordinator deployment does not exist yet"), nil
		}
		return trinoRuntimeReadiness{}, fmt.Errorf("failed to get coordinator deployment: %w", err)
	}

	result := trinoRuntimeReadiness{
		CoordinatorReadyReplicas: coordinator.Status.ReadyReplicas,
	}

	if reason, message, ready := deploymentRolloutReady(coordinator, "Coordinator", 1); !ready {
		result.Ready = false
		result.Reason = reason
		result.Message = message
		return result, nil
	}
	if coordinator.Status.ReadyReplicas < 1 || coordinator.Status.AvailableReplicas < 1 {
		result.Ready = false
		result.Reason = "CoordinatorDeploymentNotReady"
		result.Message = fmt.Sprintf("Coordinator deployment is not ready: readyReplicas=%d availableReplicas=%d", coordinator.Status.ReadyReplicas, coordinator.Status.AvailableReplicas)
		return result, nil
	}

	endpointsReady, err := r.serviceHasReadyEndpoints(ctx, xtrinode.Namespace, config.BuildCoordinatorServiceName(xtrinode.Name))
	if err != nil {
		return trinoRuntimeReadiness{}, fmt.Errorf("failed to check coordinator service endpoints: %w", err)
	}
	if !endpointsReady {
		result.Ready = false
		result.Reason = "CoordinatorEndpointsNotReady"
		result.Message = "Coordinator service has no ready HTTP endpoints"
		return result, nil
	}

	worker := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      config.BuildWorkerDeploymentName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}, worker); err != nil {
		if k8serrors.IsNotFound(err) {
			requiredWorkers := requiredReadyWorkers(xtrinode, nil)
			result.RequiredWorkers = requiredWorkers
			if requiredWorkers > 0 {
				result.Ready = false
				result.Reason = "WorkerDeploymentMissing"
				result.Message = fmt.Sprintf("Worker deployment does not exist but %d ready worker(s) are required", requiredWorkers)
				return result, nil
			}
			result.Ready = true
			result.Reason = status.ConditionReasonRuntimeReady
			result.Message = "Coordinator deployment and service endpoints are ready; no workers are required"
			return result, nil
		}
		return trinoRuntimeReadiness{}, fmt.Errorf("failed to get worker deployment: %w", err)
	}

	requiredWorkers := requiredReadyWorkers(xtrinode, worker)
	result.WorkerReadyReplicas = worker.Status.ReadyReplicas
	result.RequiredWorkers = requiredWorkers
	if requiredWorkers > 0 {
		if reason, message, ready := deploymentRolloutReady(worker, "Worker", requiredWorkers); !ready {
			result.Ready = false
			result.Reason = reason
			result.Message = message
			return result, nil
		}
		if worker.Status.ReadyReplicas < requiredWorkers || worker.Status.AvailableReplicas < requiredWorkers {
			result.Ready = false
			result.Reason = "WorkerDeploymentNotReady"
			result.Message = fmt.Sprintf("Worker deployment is below the required floor: readyReplicas=%d availableReplicas=%d required=%d", worker.Status.ReadyReplicas, worker.Status.AvailableReplicas, requiredWorkers)
			return result, nil
		}
	}

	result.Ready = true
	result.Reason = status.ConditionReasonRuntimeReady
	result.Message = fmt.Sprintf("Coordinator is ready with service endpoints; workers ready=%d required=%d", worker.Status.ReadyReplicas, requiredWorkers)
	return result, nil
}

func runtimeNotReady(reason, message string) trinoRuntimeReadiness {
	return trinoRuntimeReadiness{
		Ready:   false,
		Reason:  reason,
		Message: message,
	}
}

func requiredReadyWorkers(xtrinode *analyticsv1.XTrinode, worker *appsv1.Deployment) int32 {
	if xtrinode.Status.Wake != nil && time.Now().Before(xtrinode.Status.Wake.ExpiresAt.Time) && xtrinode.Status.Wake.MinWorkers > 0 {
		return xtrinode.Status.Wake.MinWorkers
	}
	if xtrinode.Spec.MinWorkers != nil && *xtrinode.Spec.MinWorkers > 0 {
		return *xtrinode.Spec.MinWorkers
	}
	if isKEDAEnabled(xtrinode) {
		return 0
	}
	if hpaMinReplicas, ok := nativeHPARequiredWorkers(xtrinode); ok {
		return hpaMinReplicas
	}
	if worker != nil && worker.Spec.Replicas != nil && *worker.Spec.Replicas > 0 {
		return *worker.Spec.Replicas
	}
	return 0
}

func nativeHPARequiredWorkers(xtrinode *analyticsv1.XTrinode) (int32, bool) {
	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return 0, false
	}
	server, ok := valuesMap["server"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	autoscaling, ok := server["autoscaling"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	enabled, ok := autoscaling["enabled"].(bool)
	if !ok || !enabled {
		return 0, false
	}

	cpuTarget := int32(config.DefaultHPACPUTargetPercentage)
	if parsed, ok := parseOverlayInt32(autoscaling["targetCPUUtilizationPercentage"]); ok {
		cpuTarget = parsed
	} else if cpuTargetStr, ok := autoscaling["targetCPUUtilizationPercentage"].(string); ok && cpuTargetStr == "" {
		cpuTarget = 0
	}
	memoryTarget := int32(config.DefaultHPAMemoryTargetPercentage)
	if parsed, ok := parseOverlayInt32(autoscaling["targetMemoryUtilizationPercentage"]); ok {
		memoryTarget = parsed
	} else if memoryTargetStr, ok := autoscaling["targetMemoryUtilizationPercentage"].(string); ok && memoryTargetStr == "" {
		memoryTarget = 0
	}
	if cpuTarget <= 0 && memoryTarget <= 0 {
		return 0, false
	}

	if minReplicas, ok := parseOverlayInt32(autoscaling["minReplicas"]); ok {
		return minReplicas, true
	}
	return int32(config.DefaultHPAMinReplicas), true
}

func isNativeHPAEnabled(xtrinode *analyticsv1.XTrinode) bool {
	_, ok := nativeHPARequiredWorkers(xtrinode)
	return ok
}

func parseOverlayInt32(val interface{}) (int32, bool) {
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

func deploymentRolloutReady(deployment *appsv1.Deployment, component string, requiredUpdatedReplicas int32) (reason, message string, ready bool) {
	if deployment.Status.ObservedGeneration < deployment.Generation {
		return component + "RolloutPending",
			fmt.Sprintf("%s deployment rollout is pending: observedGeneration=%d generation=%d", component, deployment.Status.ObservedGeneration, deployment.Generation),
			false
	}

	if condition := deploymentCondition(deployment, appsv1.DeploymentProgressing); condition != nil && condition.Status == corev1.ConditionFalse {
		return component + "RolloutFailed",
			fmt.Sprintf("%s deployment rollout failed: reason=%s message=%s", component, condition.Reason, condition.Message),
			false
	}

	if requiredUpdatedReplicas > 0 && deployment.Status.UpdatedReplicas < requiredUpdatedReplicas {
		return component + "RolloutPending",
			fmt.Sprintf("%s deployment current revision is not ready: updatedReplicas=%d required=%d", component, deployment.Status.UpdatedReplicas, requiredUpdatedReplicas),
			false
	}

	return "", "", true
}

func deploymentCondition(deployment *appsv1.Deployment, conditionType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range deployment.Status.Conditions {
		if deployment.Status.Conditions[i].Type == conditionType {
			return &deployment.Status.Conditions[i]
		}
	}
	return nil
}

func (r *XTrinodeReconciler) serviceHasReadyEndpoints(ctx context.Context, namespace, name string) (bool, error) {
	endpointSlices := &discoveryv1.EndpointSliceList{}
	if err := r.List(
		ctx,
		endpointSlices,
		client.InNamespace(namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: name},
	); err != nil {
		return false, err
	}

	for i := range endpointSlices.Items {
		endpointSlice := &endpointSlices.Items[i]
		if !endpointSliceHasHTTPPort(endpointSlice) {
			continue
		}
		for j := range endpointSlice.Endpoints {
			if endpointSliceEndpointReady(&endpointSlice.Endpoints[j]) {
				return true, nil
			}
		}
	}
	return false, nil
}

func endpointSliceHasHTTPPort(endpointSlice *discoveryv1.EndpointSlice) bool {
	if len(endpointSlice.Ports) == 0 {
		return true
	}
	for _, port := range endpointSlice.Ports {
		if port.Name != nil && *port.Name == "http" {
			return true
		}
		if port.Port != nil && *port.Port == config.TrinoPortHTTP {
			return true
		}
	}
	return false
}

func endpointSliceEndpointReady(endpoint *discoveryv1.Endpoint) bool {
	if len(endpoint.Addresses) == 0 {
		return false
	}
	if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
		return false
	}
	return true
}

func (r *XTrinodeReconciler) markRuntimeNotReady(ctx context.Context, xtrinode *analyticsv1.XTrinode, readiness trinoRuntimeReadiness, log logr.Logger) error {
	xtrinode.Status.CoordinatorURL = controlendpoint.CoordinatorURL(xtrinode)
	xtrinode.Status.Workers = readiness.WorkerReadyReplicas
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeReady, metav1.ConditionFalse, status.ConditionReasonRuntimeNotReady, readiness.Message, r.EventRecorder)
	status.SetCondition(xtrinode, status.ConditionTypeReconciling, metav1.ConditionTrue, status.ConditionReasonReconciling, "Waiting for Trino runtime readiness")
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeSuspended, metav1.ConditionFalse, status.ConditionReasonNotSuspended, "XTrinode is not suspended", r.EventRecorder)
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeError, metav1.ConditionFalse, status.ConditionReasonNoError, "No errors", r.EventRecorder)
	r.updateConditionMetrics(xtrinode)
	return r.updateStatus(ctx, xtrinode, log)
}

func (r *XTrinodeReconciler) reconcileReadyGatewayRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	readyForRoute := xtrinode.DeepCopy()
	readyForRoute.Status.Phase = string(status.PhaseReady)
	if err := r.reconcileGateway(ctx, readyForRoute); err != nil {
		return err
	}
	if gatewayCondition := status.GetCondition(readyForRoute, status.ConditionTypeGatewayReady); gatewayCondition != nil {
		status.SetCondition(xtrinode, gatewayCondition.Type, gatewayCondition.Status, gatewayCondition.Reason, gatewayCondition.Message)
	}
	return nil
}
