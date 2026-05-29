package controllers

import (
	"context"
	"fmt"
	"strings"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxNodePoolProvisioningDiagnosticMessageLen = 900

type nodePoolProvisioningDiagnostic struct {
	sourceKind    string
	sourceName    string
	conditionType string
	reason        string
	message       string
}

func (d *nodePoolProvisioningDiagnostic) statusMessage() string {
	source := strings.TrimSpace(d.sourceKind + " " + d.sourceName)
	parts := make([]string, 0, 3)
	if d.conditionType != "" {
		parts = append(parts, "condition="+d.conditionType)
	}
	if d.reason != "" {
		parts = append(parts, "reason="+d.reason)
	}
	if d.message != "" {
		parts = append(parts, d.message)
	}
	if len(parts) == 0 {
		return truncateNodePoolProvisioningDiagnostic(source)
	}
	return truncateNodePoolProvisioningDiagnostic(source + " reported provisioning failure: " + strings.Join(parts, "; "))
}

func (r *XTrinodeReconciler) nodePoolProvisioningFailureDiagnostic(
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	machineResource *unstructured.Unstructured,
) (*nodePoolProvisioningDiagnostic, error) {
	if diagnostic, ok := provisioningFailureFromObject(machineResource); ok {
		return &diagnostic, nil
	}

	if infrastructureResource, ok, err := r.getNodePoolInfrastructureResource(ctx, xtrinode); err != nil {
		return nil, err
	} else if ok {
		if diagnostic, found := provisioningFailureFromObject(infrastructureResource); found {
			return &diagnostic, nil
		}
	}

	relatedResources, err := r.relatedNodePoolProvisioningResources(ctx, xtrinode, machineResource)
	if err != nil {
		return nil, err
	}
	for i := range relatedResources {
		if diagnostic, ok := provisioningFailureFromObject(relatedResources[i]); ok {
			return &diagnostic, nil
		}
	}
	return nil, nil
}

func (r *XTrinodeReconciler) getNodePoolInfrastructureResource(ctx context.Context, xtrinode *analyticsv1.XTrinode) (*unstructured.Unstructured, bool, error) {
	nodePool := xtrinode.Spec.NodePool
	if nodePool == nil {
		return nil, false, nil
	}

	nodePoolName := getNodePoolName(nodePool, xtrinode.Name)
	infrastructureName := nodePoolName + config.NodePoolTemplateSuffix
	infrastructureGVK := getInfrastructureTemplateGVK(nodePool.Provider, isMachinePoolProvider(nodePool))
	if nodePool.ProviderMode == "managed" {
		infrastructureName = nodePoolName
		infrastructureGVK = getManagedInfrastructureGVK(nodePool.Provider)
	}

	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(infrastructureGVK)
	err := r.Get(ctx, client.ObjectKey{Name: infrastructureName, Namespace: xtrinode.Namespace}, resource)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to inspect node-pool infrastructure resource %s/%s: %w", infrastructureGVK.Kind, infrastructureName, err)
	}
	return resource, true, nil
}

func (r *XTrinodeReconciler) relatedNodePoolProvisioningResources(
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	machineResource *unstructured.Unstructured,
) ([]*unstructured.Unstructured, error) {
	machineSets, err := r.nodePoolMachineSets(ctx, xtrinode, machineResource)
	if err != nil {
		return nil, err
	}
	machines, err := r.nodePoolMachines(ctx, xtrinode, machineResource, machineSets)
	if err != nil {
		return nil, err
	}

	resources := make([]*unstructured.Unstructured, 0, len(machineSets)+len(machines)*2)
	resources = append(resources, machineSets...)
	resources = append(resources, machines...)
	for _, machine := range machines {
		infrastructureResource, ok, err := r.machineInfrastructureResource(ctx, machine)
		if err != nil {
			return resources, err
		}
		if ok {
			resources = append(resources, infrastructureResource)
		}
	}
	return resources, nil
}

func (r *XTrinodeReconciler) nodePoolMachineSets(
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	machineResource *unstructured.Unstructured,
) ([]*unstructured.Unstructured, error) {
	if machineResource.GetKind() != "MachineDeployment" {
		return nil, nil
	}

	machineSetList := newNodePoolUnstructuredList(machineSetGVK())
	if err := r.List(ctx, machineSetList, client.InNamespace(xtrinode.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list node-pool MachineSets: %w", err)
	}

	machineSets := make([]*unstructured.Unstructured, 0)
	for i := range machineSetList.Items {
		machineSet := &machineSetList.Items[i]
		if hasOwnerReferenceToObject(machineSet, machineResource) {
			machineSets = append(machineSets, machineSet.DeepCopy())
		}
	}
	return machineSets, nil
}

func (r *XTrinodeReconciler) nodePoolMachines(
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	machineResource *unstructured.Unstructured,
	machineSets []*unstructured.Unstructured,
) ([]*unstructured.Unstructured, error) {
	machineList := newNodePoolUnstructuredList(machineGVK())
	if err := r.List(ctx, machineList, client.InNamespace(xtrinode.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list node-pool Machines: %w", err)
	}

	machines := make([]*unstructured.Unstructured, 0)
	for i := range machineList.Items {
		machine := &machineList.Items[i]
		if hasOwnerReferenceToObject(machine, machineResource) || hasOwnerReferenceToAnyObject(machine, machineSets) {
			machines = append(machines, machine.DeepCopy())
		}
	}
	return machines, nil
}

func (r *XTrinodeReconciler) machineInfrastructureResource(ctx context.Context, machine *unstructured.Unstructured) (*unstructured.Unstructured, bool, error) {
	ref, found, err := unstructured.NestedMap(machine.Object, "spec", "infrastructureRef")
	if err != nil || !found {
		return nil, false, nil
	}

	apiVersion, ok := ref["apiVersion"].(string)
	if !ok || apiVersion == "" {
		return nil, false, nil
	}
	kind, ok := ref["kind"].(string)
	if !ok || kind == "" {
		return nil, false, nil
	}
	name, ok := ref["name"].(string)
	if !ok || name == "" {
		return nil, false, nil
	}
	namespace, _ := ref["namespace"].(string) //nolint:errcheck // optional field; absence falls back to the Machine namespace
	if namespace == "" {
		namespace = machine.GetNamespace()
	}
	groupVersion, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, false, nil
	}

	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(groupVersion.WithKind(kind))
	err = r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, resource)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to inspect Machine infrastructure resource %s/%s: %w", kind, name, err)
	}
	return resource, true, nil
}

func provisioningFailureFromObject(obj *unstructured.Unstructured) (nodePoolProvisioningDiagnostic, bool) {
	statusMap, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return nodePoolProvisioningDiagnostic{}, false
	}

	if diagnostic, ok := objectFailureFieldsDiagnostic(obj, statusMap); ok {
		return diagnostic, true
	}
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return nodePoolProvisioningDiagnostic{}, false
	}
	for _, rawCondition := range conditions {
		condition, ok := rawCondition.(map[string]interface{})
		if !ok || !conditionReportsProvisioningFailure(condition) {
			continue
		}
		return nodePoolProvisioningDiagnostic{
			sourceKind:    obj.GetKind(),
			sourceName:    obj.GetName(),
			conditionType: conditionString(condition, "type"),
			reason:        conditionString(condition, "reason"),
			message:       conditionString(condition, "message"),
		}, true
	}
	return nodePoolProvisioningDiagnostic{}, false
}

func objectFailureFieldsDiagnostic(obj *unstructured.Unstructured, statusMap map[string]interface{}) (nodePoolProvisioningDiagnostic, bool) {
	reason := stringField(statusMap, "failureReason")
	message := stringField(statusMap, "failureMessage")
	if reason == "" && message == "" {
		return nodePoolProvisioningDiagnostic{}, false
	}
	return nodePoolProvisioningDiagnostic{
		sourceKind: obj.GetKind(),
		sourceName: obj.GetName(),
		reason:     reason,
		message:    message,
	}, true
}

func conditionReportsProvisioningFailure(condition map[string]interface{}) bool {
	conditionStatus := strings.ToLower(conditionString(condition, "status"))
	conditionType := conditionString(condition, "type")
	severity := strings.ToLower(conditionString(condition, "severity"))
	conditionTypeText := strings.ToLower(conditionType)
	detailText := strings.ToLower(conditionString(condition, "reason") + " " + conditionString(condition, "message"))

	if conditionStatus == "true" && containsAnySchedulingPhrase(conditionTypeText+" "+detailText, "failed", "failure", "error") {
		return true
	}
	if severity == "error" && conditionStatus != "true" {
		return true
	}
	if conditionStatus == "false" || conditionStatus == "unknown" {
		return containsNodePoolFailurePhrase(detailText)
	}
	return false
}

func containsNodePoolFailurePhrase(text string) bool {
	return containsAnySchedulingPhrase(
		text,
		"fail",
		"error",
		"invalid",
		"denied",
		"forbidden",
		"unauthoriz",
		"quota",
		"capacity",
		"insufficient",
		"exceeded",
		"not found",
		"notfound",
		"timeout",
		"timed out",
		"unable",
		"cannot",
	)
}

func conditionString(condition map[string]interface{}, key string) string {
	return stringField(condition, key)
}

func stringField(fields map[string]interface{}, key string) string {
	value, ok := fields[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func machineGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "Machine"}
}

func machineSetGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachineSet"}
}

func newNodePoolUnstructuredList(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	return list
}

func hasOwnerReferenceToAnyObject(obj *unstructured.Unstructured, owners []*unstructured.Unstructured) bool {
	for _, owner := range owners {
		if hasOwnerReferenceToObject(obj, owner) {
			return true
		}
	}
	return false
}

func hasOwnerReferenceToObject(obj, owner *unstructured.Unstructured) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.APIVersion != owner.GetAPIVersion() || ref.Kind != owner.GetKind() || ref.Name != owner.GetName() {
			continue
		}
		if ref.UID != "" && owner.GetUID() != "" && ref.UID != owner.GetUID() {
			continue
		}
		return true
	}
	return false
}

func truncateNodePoolProvisioningDiagnostic(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) <= maxNodePoolProvisioningDiagnosticMessageLen {
		return message
	}
	return string(runes[:maxNodePoolProvisioningDiagnosticMessageLen]) + "..."
}
