package controllers

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
	"github.com/xtrinode/xtrinode/internal/status"
)

func (r *XTrinodeReconciler) reconcileNamespaceGuardrailsAfterDelete(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	limits, err := r.calculateNamespaceGuardrailLimits(ctx, xtrinode)
	if err != nil {
		return fmt.Errorf("failed to calculate namespace guardrails after delete: %w", err)
	}

	if limits.RuntimeCount == 0 {
		return r.deleteNamespaceGuardrailResources(ctx, xtrinode.Namespace, log)
	}

	if err := r.ensureResourceQuota(ctx, xtrinode, limits.MaxCPU, limits.MaxMemory, log); err != nil {
		return err
	}
	if err := r.ensureLimitRange(ctx, xtrinode, limits.WorkerCPURequest, limits.WorkerMemoryRequest, limits.WorkerCPULimit, limits.WorkerMemoryLimit, log); err != nil {
		return err
	}

	log.Info(
		"Reconciled namespace guardrails after XTrinode deletion",
		"namespace", xtrinode.Namespace,
		"runtimes", limits.RuntimeCount,
		"cpu", limits.MaxCPU.String(),
		"memory", limits.MaxMemory.String(),
	)
	return nil
}

func (r *XTrinodeReconciler) deleteNamespaceGuardrailResources(ctx context.Context, namespace string, log logr.Logger) error {
	resourceQuota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespaceResourceQuotaName,
			Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, resourceQuota); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace ResourceQuota %s/%s: %w", namespace, namespaceResourceQuotaName, err)
	}

	limitRange := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespaceLimitRangeName,
			Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, limitRange); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace LimitRange %s/%s: %w", namespace, namespaceLimitRangeName, err)
	}

	log.Info("Deleted namespace guardrails after final XTrinode deletion", "namespace", namespace)
	return nil
}

// ensureNamespaceGuardrails ensures namespace guardrails (namespace, ResourceQuota, LimitRange)
func (r *XTrinodeReconciler) ensureNamespaceGuardrails(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Ensuring namespace guardrails", "namespace", xtrinode.Namespace)

	limits, err := r.calculateNamespaceGuardrailLimits(ctx, xtrinode)
	if err != nil {
		return err
	}

	if err := r.ensureNamespaceWithLabels(ctx, xtrinode, log); err != nil {
		return err
	}

	if err := r.ensureResourceQuota(ctx, xtrinode, limits.MaxCPU, limits.MaxMemory, log); err != nil {
		return err
	}
	r.EventRecorder.Normalf(
		xtrinode,
		events.ReasonResourceQuotaApplied,
		"Namespace ResourceQuota applied (runtimes: %d, CPU: %s, Memory: %s)",
		limits.RuntimeCount,
		limits.MaxCPU.String(),
		limits.MaxMemory.String(),
	)

	if err := r.ensureLimitRange(ctx, xtrinode, limits.WorkerCPURequest, limits.WorkerMemoryRequest, limits.WorkerCPULimit, limits.WorkerMemoryLimit, log); err != nil {
		return err
	}
	r.EventRecorder.Normal(xtrinode, events.ReasonLimitRangeApplied, "Namespace LimitRange applied for container resource defaults")
	r.EventRecorder.Normal(xtrinode, events.ReasonNamespaceGuardrailsApplied, "Namespace guardrails applied successfully")

	status.SetCondition(xtrinode, status.ConditionTypeGuardrailsReady, metav1.ConditionTrue, "GuardrailsApplied", "Namespace guardrails applied successfully")
	return nil
}

type namespaceGuardrailLimits struct {
	MaxCPU              resource.Quantity
	MaxMemory           resource.Quantity
	WorkerCPURequest    resource.Quantity
	WorkerMemoryRequest resource.Quantity
	WorkerCPULimit      resource.Quantity
	WorkerMemoryLimit   resource.Quantity
	RuntimeCount        int
}

func (r *XTrinodeReconciler) calculateNamespaceGuardrailLimits(ctx context.Context, current *analyticsv1.XTrinode) (namespaceGuardrailLimits, error) {
	xtrinodes, err := r.listNamespaceXTrinodes(ctx, current)
	if err != nil {
		return namespaceGuardrailLimits{}, err
	}

	limits := namespaceGuardrailLimits{
		MaxCPU:    resource.MustParse("0"),
		MaxMemory: resource.MustParse("0"),
	}
	for i := range xtrinodes {
		xtrinode := &xtrinodes[i]
		shape, err := runtimeshape.Resolve(xtrinode)
		if err != nil {
			return namespaceGuardrailLimits{}, fmt.Errorf("failed to resolve runtime shape for XTrinode %s/%s: %w", xtrinode.Namespace, xtrinode.Name, err)
		}
		maxCPU, maxMemory := shapeQuotaLimits(xtrinode, shape)
		workerCPURequest := resourceFromList(shape.Worker.Requests, corev1.ResourceCPU)
		workerMemoryRequest := resourceFromList(shape.Worker.Requests, corev1.ResourceMemory)
		workerCPULimit := resourceFromList(shape.Worker.Limits, corev1.ResourceCPU)
		workerMemoryLimit := resourceFromList(shape.Worker.Limits, corev1.ResourceMemory)

		limits.MaxCPU.Add(maxCPU)
		limits.MaxMemory.Add(maxMemory)
		limits.WorkerCPURequest = maxQuantity(limits.WorkerCPURequest, workerCPURequest)
		limits.WorkerMemoryRequest = maxQuantity(limits.WorkerMemoryRequest, workerMemoryRequest)
		limits.WorkerCPULimit = maxQuantity(limits.WorkerCPULimit, workerCPULimit)
		limits.WorkerMemoryLimit = maxQuantity(limits.WorkerMemoryLimit, workerMemoryLimit)
	}
	limits.RuntimeCount = len(xtrinodes)

	return limits, nil
}

func shapeQuotaLimits(xtrinode *analyticsv1.XTrinode, shape *runtimeshape.ResolvedRuntimeShape) (maxCPU, maxMemory resource.Quantity) {
	coordinatorQuotaPods := int32(1) + rolloutSurgeReplicas(xtrinode, "coordinator", 1)
	workerQuotaPods := shape.QuotaWorkers + rolloutSurgeReplicas(xtrinode, "worker", shape.QuotaWorkers)

	maxCPU = resourceFromList(shape.Coordinator.Limits, corev1.ResourceCPU)
	maxCPU.SetMilli(int64(coordinatorQuotaPods) * maxCPU.MilliValue())
	workerCPULimit := resourceFromList(shape.Worker.Limits, corev1.ResourceCPU)
	workerCPULimitScaled := workerCPULimit.DeepCopy()
	workerCPULimitScaled.SetMilli(int64(workerQuotaPods) * workerCPULimitScaled.MilliValue())
	maxCPU.Add(workerCPULimitScaled)

	maxMemory = resourceFromList(shape.Coordinator.Limits, corev1.ResourceMemory)
	maxMemory.Set(int64(coordinatorQuotaPods) * maxMemory.Value())
	workerMemoryLimit := resourceFromList(shape.Worker.Limits, corev1.ResourceMemory)
	workerMemoryLimitScaled := workerMemoryLimit.DeepCopy()
	workerMemoryLimitScaled.Set(int64(workerQuotaPods) * workerMemoryLimitScaled.Value())
	maxMemory.Add(workerMemoryLimitScaled)
	return maxCPU, maxMemory
}

func rolloutSurgeReplicas(xtrinode *analyticsv1.XTrinode, role string, replicas int32) int32 {
	if replicas <= 0 {
		return 0
	}
	maxSurge, hasSurge := rolloutMaxSurge(xtrinode, role)
	if !hasSurge {
		return 0
	}
	value, err := intstr.GetScaledValueFromIntOrPercent(maxSurge, int(replicas), true)
	if err != nil || value < 0 {
		return 0
	}
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(value)
}

func rolloutMaxSurge(xtrinode *analyticsv1.XTrinode, role string) (*intstr.IntOrString, bool) {
	maxSurge := intstr.FromString("25%")
	hasSurge := true

	if xtrinode.Spec.RolloutPolicy != nil && xtrinode.Spec.RolloutPolicy.RollingUpdateStrategy != nil {
		if configured := xtrinode.Spec.RolloutPolicy.RollingUpdateStrategy.MaxSurge; configured != nil {
			maxSurge = *configured
		}
	}

	valuesOverlay := xtrinode.Spec.GetValuesOverlayMap()
	roleConfig, ok := valuesOverlay[role].(map[string]interface{})
	if !ok {
		return &maxSurge, hasSurge
	}
	deploymentConfig, ok := roleConfig["deployment"].(map[string]interface{})
	if !ok {
		return &maxSurge, hasSurge
	}
	strategyConfig, ok := deploymentConfig["strategy"].(map[string]interface{})
	if !ok {
		return &maxSurge, hasSurge
	}
	if strategyType, hasType := strategyConfig["type"].(string); hasType && strings.EqualFold(strategyType, string(appsv1.RecreateDeploymentStrategyType)) {
		return nil, false
	}
	rollingUpdate, ok := strategyConfig["rollingUpdate"].(map[string]interface{})
	if !ok {
		return &maxSurge, hasSurge
	}
	if configured, ok := intOrStringFromOverlay(rollingUpdate["maxSurge"]); ok {
		maxSurge = configured
	}
	return &maxSurge, hasSurge
}

func intOrStringFromOverlay(value interface{}) (intstr.IntOrString, bool) {
	switch typed := value.(type) {
	case nil:
		return intstr.IntOrString{}, false
	case int:
		return intstr.FromInt(typed), true
	case int32:
		return intstr.FromInt32(typed), true
	case int64:
		if typed < 0 || typed > math.MaxInt32 {
			return intstr.IntOrString{}, false
		}
		return intstr.FromInt32(int32(typed)), true
	case float64:
		if typed < 0 || typed > math.MaxInt32 || typed != math.Trunc(typed) {
			return intstr.IntOrString{}, false
		}
		return intstr.FromInt32(int32(typed)), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return intstr.IntOrString{}, false
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 32); err == nil {
			return intstr.FromInt32(int32(parsed)), true
		}
		return intstr.Parse(trimmed), true
	default:
		return intstr.IntOrString{}, false
	}
}

func resourceFromList(list corev1.ResourceList, name corev1.ResourceName) resource.Quantity {
	if quantity, ok := list[name]; ok {
		return quantity.DeepCopy()
	}
	return resource.MustParse("0")
}

func (r *XTrinodeReconciler) listNamespaceXTrinodes(ctx context.Context, current *analyticsv1.XTrinode) ([]analyticsv1.XTrinode, error) {
	var list analyticsv1.XTrinodeList
	if err := r.List(ctx, &list, client.InNamespace(current.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list XTrinodes in namespace %s for guardrail aggregation: %w", current.Namespace, err)
	}

	xtrinodes := make([]analyticsv1.XTrinode, 0, len(list.Items)+1)
	seen := make(map[string]struct{}, len(list.Items)+1)
	for i := range list.Items {
		item := list.Items[i]
		if item.DeletionTimestamp != nil {
			continue
		}
		seen[item.Name] = struct{}{}
		xtrinodes = append(xtrinodes, *item.DeepCopy())
	}
	if _, ok := seen[current.Name]; !ok && current.DeletionTimestamp == nil {
		xtrinodes = append(xtrinodes, *current.DeepCopy())
	}

	return xtrinodes, nil
}

func maxQuantity(current, candidate resource.Quantity) resource.Quantity {
	if current.Cmp(candidate) >= 0 {
		return current
	}
	return candidate.DeepCopy()
}

// ensureNamespaceWithLabels ensures namespace exists and has required labels
func (r *XTrinodeReconciler) ensureNamespaceWithLabels(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: xtrinode.Namespace,
		},
	}
	if err := r.ensureNamespace(ctx, namespace, log); err != nil {
		return err
	}

	namespaceKey := client.ObjectKeyFromObject(namespace)
	if err := retry.OnConflictWithRefresh(ctx, retry.FastConfig(), log,
		func() error {
			return r.Get(ctx, namespaceKey, namespace)
		},
		func() error {
			if namespace.Labels == nil {
				namespace.Labels = make(map[string]string)
			}
			namespace.Labels[config.ManagedLabel] = "true"
			namespace.Labels[guardrailScopeLabel] = guardrailScopeNamespace
			delete(namespace.Labels, config.RuntimeLabel)
			return r.Update(ctx, namespace)
		},
	); err != nil {
		log.Error(err, "failed to update namespace labels")
	}
	return nil
}

// ensureResourceQuota creates or updates ResourceQuota for the namespace
func (r *XTrinodeReconciler) ensureResourceQuota(ctx context.Context, xtrinode *analyticsv1.XTrinode, maxCPU, maxMemory resource.Quantity, log logr.Logger) error {
	resourceQuota := buildNamespaceResourceQuota(xtrinode.Namespace, maxCPU, maxMemory)
	gvk, err := apiutil.GVKForObject(resourceQuota, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK for ResourceQuota: %w", err)
	}
	resourceQuota.GetObjectKind().SetGroupVersionKind(gvk)
	if err := r.Patch(ctx, resourceQuota, client.Apply, client.FieldOwner("xtrinode-operator"), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to create/update ResourceQuota: %w", err)
	}
	log.Info("Ensured namespace ResourceQuota", "namespace", xtrinode.Namespace, "name", resourceQuota.Name, "cpu", maxCPU.String(), "memory", maxMemory.String())
	return nil
}

// ensureLimitRange creates or updates LimitRange for the namespace
func (r *XTrinodeReconciler) ensureLimitRange(ctx context.Context, xtrinode *analyticsv1.XTrinode, workerCPUReq, workerMemReq, workerCPULim, workerMemLim resource.Quantity, log logr.Logger) error {
	limitRange := buildNamespaceLimitRange(xtrinode.Namespace, workerCPUReq, workerMemReq, workerCPULim, workerMemLim)
	gvk, err := apiutil.GVKForObject(limitRange, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK for LimitRange: %w", err)
	}
	limitRange.GetObjectKind().SetGroupVersionKind(gvk)
	if err := r.Patch(ctx, limitRange, client.Apply, client.FieldOwner("xtrinode-operator"), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to create/update LimitRange: %w", err)
	}
	log.Info("Ensured namespace LimitRange", "namespace", xtrinode.Namespace, "name", limitRange.Name)
	return nil
}

func buildNamespaceResourceQuota(namespace string, maxCPU, maxMemory resource.Quantity) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespaceResourceQuotaName,
			Namespace: namespace,
			Labels:    namespaceGuardrailLabels(),
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceCPU:    maxCPU,
				corev1.ResourceMemory: maxMemory,
			},
		},
	}
}

func buildNamespaceLimitRange(namespace string, workerCPUReq, workerMemReq, workerCPULim, workerMemLim resource.Quantity) *corev1.LimitRange {
	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespaceLimitRangeName,
			Namespace: namespace,
			Labels:    namespaceGuardrailLabels(),
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{
					Type: corev1.LimitTypeContainer,
					Default: corev1.ResourceList{
						corev1.ResourceCPU:    workerCPULim,
						corev1.ResourceMemory: workerMemLim,
					},
					DefaultRequest: corev1.ResourceList{
						corev1.ResourceCPU:    workerCPUReq,
						corev1.ResourceMemory: workerMemReq,
					},
					Max: corev1.ResourceList{
						corev1.ResourceCPU:    workerCPULim,
						corev1.ResourceMemory: workerMemLim,
					},
				},
			},
		},
	}
}

func namespaceGuardrailLabels() map[string]string {
	return map[string]string{
		managedByLabel:      managedByXTrinodeOperator,
		config.ManagedLabel: "true",
		guardrailScopeLabel: guardrailScopeNamespace,
	}
}

// ensureNamespace ensures the namespace exists
func (r *XTrinodeReconciler) ensureNamespace(ctx context.Context, namespace *corev1.Namespace, log logr.Logger) error {
	err := r.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)
	if err == nil {
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace: %w", err)
	}

	if err := r.Create(ctx, namespace); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}
	log.Info("Created namespace", "namespace", namespace.Name)
	return nil
}
