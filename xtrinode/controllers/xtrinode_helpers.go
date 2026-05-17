package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/catalog"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/internal/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// isKEDAEnabled checks if KEDA is explicitly enabled for the XTrinode.
// Default: disabled; worker replicas are fixed unless spec.keda.enabled=true.
func isKEDAEnabled(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.KEDA == nil || xtrinode.Spec.KEDA.Enabled == nil {
		return false
	}
	return *xtrinode.Spec.KEDA.Enabled && hasKEDAMetricConfig(xtrinode.Spec.KEDA)
}

func hasKEDAMetricConfig(k *analyticsv1.KEDASpec) bool {
	return k.ScalerType != "" ||
		k.ScalingMetric != "" ||
		(k.PrometheusServer != nil && *k.PrometheusServer != "") ||
		(k.PrometheusQuery != nil && *k.PrometheusQuery != "") ||
		(k.HTTPEndpoint != nil && *k.HTTPEndpoint != "")
}

func nodePoolProvisionedMessage(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.NodePool == nil {
		return "No node pool requested"
	}
	if xtrinode.Spec.NodePool.ClusterName != "" {
		return fmt.Sprintf("Node pool provisioned for CAPI cluster %q; runtime pods are reconciled by the operator cluster unless XTrinode is installed in the workload cluster", xtrinode.Spec.NodePool.ClusterName)
	}
	return "Node pool provisioned successfully"
}

func trinoResourcesAppliedMessage(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.NodePool != nil && xtrinode.Spec.NodePool.ClusterName != "" {
		return fmt.Sprintf("Trino resources applied in operator cluster namespace %q; spec.nodePool provisions capacity for CAPI cluster %q and does not move runtime resources cross-cluster", xtrinode.Namespace, xtrinode.Spec.NodePool.ClusterName)
	}
	return "Trino resources applied successfully"
}

// getNodePoolErrorRequeueInterval returns the requeue interval for node pool provisioning errors
func getNodePoolErrorRequeueInterval(nodePool *analyticsv1.NodePoolSpec) time.Duration {
	if nodePool != nil && nodePool.ErrorRequeueInterval != nil {
		return nodePool.ErrorRequeueInterval.Duration
	}
	return config.NodePoolProvisioningErrorRequeueInterval
}

// getNodePoolResourceNotFoundRequeueInterval returns the requeue interval when resource is not found
func getNodePoolResourceNotFoundRequeueInterval(nodePool *analyticsv1.NodePoolSpec) time.Duration {
	if nodePool != nil && nodePool.ResourceNotFoundRequeueInterval != nil {
		return nodePool.ResourceNotFoundRequeueInterval.Duration
	}
	return config.NodePoolResourceNotFoundRequeueInterval
}

// getNodePoolStatusNotAvailableRequeueInterval returns the requeue interval when status is not available
func getNodePoolStatusNotAvailableRequeueInterval(nodePool *analyticsv1.NodePoolSpec) time.Duration {
	if nodePool != nil && nodePool.StatusNotAvailableRequeueInterval != nil {
		return nodePool.StatusNotAvailableRequeueInterval.Duration
	}
	return config.NodePoolStatusNotAvailableRequeueInterval
}

// getNodePoolNoNodesReadyRequeueInterval returns the requeue interval when no nodes are ready
func getNodePoolNoNodesReadyRequeueInterval(nodePool *analyticsv1.NodePoolSpec) time.Duration {
	if nodePool != nil && nodePool.NoNodesReadyRequeueInterval != nil {
		return nodePool.NoNodesReadyRequeueInterval.Duration
	}
	return config.NodePoolNoNodesReadyRequeueInterval
}

// getNodePoolNodesReadyRequeueInterval returns the requeue interval when some nodes are ready
func getNodePoolNodesReadyRequeueInterval(nodePool *analyticsv1.NodePoolSpec) time.Duration {
	if nodePool != nil && nodePool.NodesReadyRequeueInterval != nil {
		return nodePool.NodesReadyRequeueInterval.Duration
	}
	return config.NodePoolNodesReadyRequeueInterval
}

// getNodePoolMinRequiredReplicasWhenMinNodesZero returns the minimum required replicas when minNodes=0
func getNodePoolMinRequiredReplicasWhenMinNodesZero(nodePool *analyticsv1.NodePoolSpec) int64 {
	if nodePool != nil && nodePool.MinRequiredReplicasWhenMinNodesZero != nil {
		return int64(*nodePool.MinRequiredReplicasWhenMinNodesZero)
	}
	return config.NodePoolMinRequiredReplicasWhenMinNodesZero
}

// getNodePoolProvisioningTimeout returns the maximum time to wait for node pool provisioning
// This can be used in the future to implement timeout checking
func getNodePoolProvisioningTimeout(nodePool *analyticsv1.NodePoolSpec) time.Duration {
	if nodePool != nil && nodePool.ProvisioningTimeout != nil {
		return nodePool.ProvisioningTimeout.Duration
	}
	return config.NodePoolProvisioningTimeout
}

// catalogConfigMapToXTrinodes maps a catalog ConfigMap to all XTrinodes in the same namespace
// This allows dynamic catalog discovery - when a catalog ConfigMap is created/updated/deleted,
// all XTrinodes in that namespace are reconciled to pick up the change
func catalogConfigMapToXTrinodes(cli client.Client, ctx context.Context, obj client.Object, log logr.Logger) []reconcile.Request {
	// Find all XTrinodes in the same namespace and enqueue them for reconciliation
	xtrinodeList := &analyticsv1.XTrinodeList{}
	if err := cli.List(ctx, xtrinodeList, client.InNamespace(obj.GetNamespace())); err != nil {
		log.Error(err, "failed to list XTrinodes for ConfigMap watch",
			"configMap", obj.GetName(),
			"namespace", obj.GetNamespace())
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(xtrinodeList.Items))
	for i := range xtrinodeList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&xtrinodeList.Items[i]),
		})
	}
	return requests
}

// isCatalogConfigMap checks if a ConfigMap is a catalog ConfigMap
func isCatalogConfigMap(obj client.Object) bool {
	return strings.HasPrefix(obj.GetName(), config.CatalogConfigMapPrefix)
}

func gatewayConfigMapToXTrinodes(cli client.Client, ctx context.Context, obj client.Object, log logr.Logger) []reconcile.Request {
	xtrinodeList := &analyticsv1.XTrinodeList{}
	if err := cli.List(ctx, xtrinodeList); err != nil {
		log.Error(err, "failed to list XTrinodes for gateway ConfigMap watch",
			"configMap", obj.GetName(),
			"namespace", obj.GetNamespace())
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(xtrinodeList.Items))
	for i := range xtrinodeList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&xtrinodeList.Items[i]),
		})
	}
	return requests
}

func isGatewayRouteConfigMap(obj client.Object) bool {
	return obj.GetNamespace() == config.GatewayConfigMapNamespace &&
		obj.GetName() == config.GatewayConfigMapName
}

func namespaceGuardrailToXTrinodes(cli client.Client, ctx context.Context, obj client.Object, log logr.Logger) []reconcile.Request {
	xtrinodeList := &analyticsv1.XTrinodeList{}
	if err := cli.List(ctx, xtrinodeList, client.InNamespace(obj.GetNamespace())); err != nil {
		log.Error(err, "failed to list XTrinodes for namespace guardrail watch",
			"name", obj.GetName(),
			"namespace", obj.GetNamespace())
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(xtrinodeList.Items))
	for i := range xtrinodeList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&xtrinodeList.Items[i]),
		})
	}
	return requests
}

func isNamespaceResourceQuota(obj client.Object) bool {
	return obj.GetName() == namespaceResourceQuotaName
}

func isNamespaceLimitRange(obj client.Object) bool {
	return obj.GetName() == namespaceLimitRangeName
}

func serviceMonitorToXTrinodes(_ client.Client, _ context.Context, obj client.Object, _ logr.Logger) []reconcile.Request {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.APIVersion != analyticsv1.GroupVersion.String() || owner.Kind != "XTrinode" || owner.Name == "" {
			continue
		}
		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{
				Name:      owner.Name,
				Namespace: obj.GetNamespace(),
			},
		}}
	}
	return []reconcile.Request{}
}

// externalConfigMapToXTrinodes maps user-provided ConfigMaps that are mounted
// by XTrinodes to those runtimes so content changes refresh rollout hashes.
func externalConfigMapToXTrinodes(cli client.Client, ctx context.Context, obj client.Object, log logr.Logger) []reconcile.Request {
	xtrinodeList := &analyticsv1.XTrinodeList{}
	if err := cli.List(ctx, xtrinodeList, client.InNamespace(obj.GetNamespace())); err != nil {
		log.Error(err, "failed to list XTrinodes for external ConfigMap watch",
			"configMap", obj.GetName(),
			"namespace", obj.GetNamespace())
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(xtrinodeList.Items))
	for i := range xtrinodeList.Items {
		xtrinode := &xtrinodeList.Items[i]
		if !xtrinodeReferencesConfigMap(xtrinode, obj.GetName()) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(xtrinode),
		})
	}
	return requests
}

func xtrinodeReferencesConfigMap(xtrinode *analyticsv1.XTrinode, configMapName string) bool {
	if xtrinode.Spec.ResourceGroupsProfile == configMapName {
		return true
	}
	if jmxExporterReferencesConfigMap(xtrinode, configMapName) {
		return true
	}
	for _, customConfigMap := range xtrinode.Spec.CustomConfigMaps {
		if customConfigMap == configMapName {
			return true
		}
	}

	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return false
	}
	return overlayMountsReferenceName(valuesMap, "configMounts", "configMap", configMapName) ||
		overlayRoleMountsReferenceName(valuesMap, "configMounts", "configMap", configMapName) ||
		overlayEnvFromReferencesName(valuesMap, "configMapRef", configMapName) ||
		overlayAdditionalVolumesReferenceName(valuesMap, "configMap", "name", configMapName)
}

func jmxExporterReferencesConfigMap(xtrinode *analyticsv1.XTrinode, configMapName string) bool {
	return xtrinode.Spec.KEDA != nil &&
		xtrinode.Spec.KEDA.JMXExporter != nil &&
		xtrinode.Spec.KEDA.JMXExporter.Enabled &&
		xtrinode.Spec.KEDA.JMXExporter.ConfigMap == configMapName
}

// secretToXTrinodes maps a changed Secret to XTrinodes whose selected catalogs
// or mounted Secret dependencies reference that Secret.
func secretToXTrinodes(cli client.Client, ctx context.Context, obj client.Object, log logr.Logger) []reconcile.Request {
	xtrinodeList := &analyticsv1.XTrinodeList{}
	if err := cli.List(ctx, xtrinodeList, client.InNamespace(obj.GetNamespace())); err != nil {
		log.Error(err, "failed to list XTrinodes for Secret watch", "namespace", obj.GetNamespace())
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(xtrinodeList.Items))
	for i := range xtrinodeList.Items {
		xtrinode := &xtrinodeList.Items[i]
		if tlsReferencesSecret(xtrinode, obj.GetName()) || secretMountsReferenceSecret(xtrinode, obj.GetName()) {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(xtrinode),
			})
			continue
		}

		envVars, err := catalog.ExtractCatalogSecretReferences(ctx, cli, xtrinode, log)
		if err != nil {
			log.Error(err, "failed to inspect catalog secret references for Secret watch",
				"namespace", obj.GetNamespace(),
				"xtrinode", xtrinode.Name)
			continue
		}
		if !catalogEnvVarsReferenceSecret(envVars, obj.GetName()) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(xtrinode),
		})
	}

	return requests
}

func tlsReferencesSecret(xtrinode *analyticsv1.XTrinode, secretName string) bool {
	return xtrinode.Spec.TLS != nil &&
		(xtrinode.Spec.TLS.ServerSecretClass == secretName ||
			xtrinode.Spec.TLS.InternalSecretClass == secretName)
}

func secretMountsReferenceSecret(xtrinode *analyticsv1.XTrinode, secretName string) bool {
	if xtrinode.Spec.HelmChartConfig != nil {
		if secretMountListReferencesSecret(xtrinode.Spec.HelmChartConfig.SecretMounts, secretName) {
			return true
		}
		if xtrinode.Spec.HelmChartConfig.Coordinator != nil &&
			secretMountListReferencesSecret(xtrinode.Spec.HelmChartConfig.Coordinator.SecretMounts, secretName) {
			return true
		}
		if xtrinode.Spec.HelmChartConfig.Worker != nil &&
			secretMountListReferencesSecret(xtrinode.Spec.HelmChartConfig.Worker.SecretMounts, secretName) {
			return true
		}
	}

	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return false
	}
	return overlayMountsReferenceName(valuesMap, "secretMounts", "secretName", secretName) ||
		overlayRoleMountsReferenceName(valuesMap, "secretMounts", "secretName", secretName) ||
		overlayEnvFromReferencesName(valuesMap, "secretRef", secretName) ||
		overlayAdditionalVolumesReferenceName(valuesMap, "secret", "secretName", secretName) ||
		overlayAdditionalVolumesReferenceName(valuesMap, "secret", "name", secretName)
}

func secretMountListReferencesSecret(secretMounts []analyticsv1.SecretMountSpec, secretName string) bool {
	for _, secretMount := range secretMounts {
		if secretMount.SecretName == secretName {
			return true
		}
	}
	return false
}

func overlayMountsReferenceName(valuesMap map[string]interface{}, listKey, refKey, targetName string) bool {
	return overlayMountListReferencesName(valuesMap[listKey], refKey, targetName)
}

func overlayRoleMountsReferenceName(valuesMap map[string]interface{}, listKey, refKey, targetName string) bool {
	for _, role := range []string{"coordinator", "worker"} {
		roleMap, ok := valuesMap[role].(map[string]interface{})
		if !ok {
			continue
		}
		if overlayMountListReferencesName(roleMap[listKey], refKey, targetName) {
			return true
		}
	}
	return false
}

func overlayMountListReferencesName(raw interface{}, refKey, targetName string) bool {
	mounts, ok := raw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range mounts {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if ref, ok := itemMap[refKey].(string); ok && ref == targetName {
			return true
		}
	}
	return false
}

func overlayEnvFromReferencesName(valuesMap map[string]interface{}, refKey, targetName string) bool {
	envFromList, ok := valuesMap["envFrom"].([]interface{})
	if !ok {
		return false
	}
	for _, item := range envFromList {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if nestedMapReferencesName(itemMap, refKey, "name", targetName) {
			return true
		}
	}
	return false
}

func overlayAdditionalVolumesReferenceName(valuesMap map[string]interface{}, volumeKey, nameKey, targetName string) bool {
	for _, role := range []string{"coordinator", "worker"} {
		roleMap, ok := valuesMap[role].(map[string]interface{})
		if !ok {
			continue
		}
		additionalVolumes, ok := roleMap["additionalVolumes"].([]interface{})
		if !ok {
			continue
		}
		for _, item := range additionalVolumes {
			volumeMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if nestedMapReferencesName(volumeMap, volumeKey, nameKey, targetName) ||
				projectedVolumeReferencesName(volumeMap, volumeKey, nameKey, targetName) {
				return true
			}
		}
	}
	return false
}

func projectedVolumeReferencesName(volumeMap map[string]interface{}, volumeKey, nameKey, targetName string) bool {
	projectedMap, ok := volumeMap["projected"].(map[string]interface{})
	if !ok {
		return false
	}
	sources, ok := projectedMap["sources"].([]interface{})
	if !ok {
		return false
	}
	for _, source := range sources {
		sourceMap, ok := source.(map[string]interface{})
		if !ok {
			continue
		}
		if nestedMapReferencesName(sourceMap, volumeKey, nameKey, targetName) {
			return true
		}
	}
	return false
}

func nestedMapReferencesName(parent map[string]interface{}, childKey, nameKey, targetName string) bool {
	childMap, ok := parent[childKey].(map[string]interface{})
	if !ok {
		return false
	}
	name, ok := childMap[nameKey].(string)
	return ok && name == targetName
}

func catalogEnvVarsReferenceSecret(envVars []corev1.EnvVar, secretName string) bool {
	for _, envVar := range envVars {
		if envVar.ValueFrom == nil || envVar.ValueFrom.SecretKeyRef == nil {
			continue
		}
		if envVar.ValueFrom.SecretKeyRef.Name == secretName {
			return true
		}
	}
	return false
}

// updateStatusWithRetry updates the status of any Kubernetes object with retry logic for conflict handling
// CRITICAL: Gets into a FRESH object, then re-applies mutations, to avoid refresh wiping changes
// statusClient is the Status() client from controller-runtime (e.g., r.Status())
// newObj is a factory that creates a fresh empty object of the correct type (e.g., &XTrinode{} or &XTrinodeCatalog{})
func updateStatusWithRetry(
	ctx context.Context,
	cli client.Client,
	statusClient client.StatusWriter,
	key client.ObjectKey,
	log logr.Logger,
	newObj func() client.Object,
	mutateStatus func(client.Object) error,
) error {
	return retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
		func() error {
			// Get into a fresh object (not the caller's pointer)
			obj := newObj()
			return cli.Get(ctx, key, obj)
		},
		func() error {
			// Get fresh object for mutation
			obj := newObj()
			if err := cli.Get(ctx, key, obj); err != nil {
				return err
			}
			// Apply status mutations to fresh object
			if err := mutateStatus(obj); err != nil {
				return err
			}
			return statusClient.Update(ctx, obj)
		},
	)
}

// setXTrinodeErrorStatusAndUpdate sets error status and condition on a XTrinode, then updates it
// This is a helper to reduce duplication of error status update patterns
func setXTrinodeErrorStatusAndUpdate(
	ctx context.Context,
	cli client.Client,
	statusClient client.StatusWriter,
	xtrinode *analyticsv1.XTrinode,
	log logr.Logger,
	reason string,
	message string,
	eventRecorder events.Recorder,
) error {
	// Capture mutations to apply
	capturedPhase := "Error"
	xtrinode.Status.Phase = capturedPhase
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeError, metav1.ConditionTrue, reason, message, eventRecorder)
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeReady, metav1.ConditionFalse, reason, message, eventRecorder)
	status.SetCondition(xtrinode, status.ConditionTypeReconciling, metav1.ConditionFalse, reason, message)
	capturedConditions := xtrinode.Status.Conditions

	key := client.ObjectKeyFromObject(xtrinode)
	if err := updateStatusWithRetry(ctx, cli, statusClient, key, log,
		func() client.Object { return &analyticsv1.XTrinode{} },
		func(obj client.Object) error {
			t, ok := obj.(*analyticsv1.XTrinode)
			if !ok {
				return fmt.Errorf("unexpected object type %T", obj)
			}
			t.Status.Phase = capturedPhase
			t.Status.Conditions = capturedConditions
			return nil
		}); err != nil {
		log.Error(err, "failed to update status to Error")
		return err
	}
	return nil
}

// handleAnnotationRequest removed - replaced by ProcessCommands() in commands.go
