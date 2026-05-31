package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/serverapply"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodePoolDefaults holds default values for node pool configuration
type NodePoolDefaults struct {
	MinNodes   int64
	MaxNodes   int64
	DiskSizeGB int64
}

// getNodePoolName returns the node pool name, using default if not specified
func getNodePoolName(nodePool *analyticsv1.NodePoolSpec, xtrinodeName string) string {
	if nodePool.Name != "" {
		return nodePool.Name
	}
	return xtrinodeName + config.NodePoolNameSuffix
}

func nodePoolDeletionPolicy(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool == nil || nodePool.DeletionPolicy == "" {
		return analyticsv1.NodePoolDeletionPolicyDelete
	}
	return nodePool.DeletionPolicy
}

func effectiveNodePoolLabels(nodePool *analyticsv1.NodePoolSpec, poolName string) map[string]string {
	if nodePool == nil {
		return nil
	}
	labels := make(map[string]string, len(nodePool.NodeLabels)+1)
	for key, value := range nodePool.NodeLabels {
		labels[key] = value
	}
	if nodePool.SchedulePods {
		labels[config.NodePoolSchedulingLabel] = poolName
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func managedMachinePoolExists(cli client.Client, ctx context.Context, name, namespace string) (bool, error) {
	existingCheck := buildUnstructuredForDeletion(getMachineResourceGVK(true), name, namespace)
	return checkResourceExists(cli, ctx, existingCheck)
}

func newManagedInfrastructurePool(provider, poolName, namespace, clusterName string, xtrinode *analyticsv1.XTrinode) (*unstructured.Unstructured, error) {
	infraPool := &unstructured.Unstructured{}
	infraPool.SetGroupVersionKind(getManagedInfrastructureGVK(provider))
	infraPool.SetName(poolName)
	infraPool.SetNamespace(namespace)
	infraPool.SetLabels(map[string]string{
		"cluster.x-k8s.io/cluster-name": clusterName,
	})

	// CAPI MachinePool must become the controller owner for managed infra pools.
	if err := setXTrinodeNonControllerOwnerReference(infraPool, xtrinode); err != nil {
		return nil, err
	}
	return infraPool, nil
}

// getNodePoolDefaults extracts default values from node pool spec
// Priority: Per-XTrinode spec → Operator-level defaults → Code defaults
func getNodePoolDefaults(nodePool *analyticsv1.NodePoolSpec, xtrinode *analyticsv1.XTrinode) NodePoolDefaults {
	// Start with code defaults (lowest priority)
	codeDefaults := config.NodePoolDefaultValuesFromEnv()
	defaults := NodePoolDefaults{
		MinNodes:   int64(codeDefaults.MinNodes),
		MaxNodes:   int64(codeDefaults.MaxNodes),
		DiskSizeGB: int64(codeDefaults.DiskSizeGB),
	}

	// Apply operator-level defaults (middle priority)
	if xtrinode.Spec.OperatorNodePoolDefaults != nil {
		if xtrinode.Spec.OperatorNodePoolDefaults.DefaultMinNodes != nil {
			defaults.MinNodes = int64(*xtrinode.Spec.OperatorNodePoolDefaults.DefaultMinNodes)
		}
		if xtrinode.Spec.OperatorNodePoolDefaults.DefaultMaxNodes != nil {
			defaults.MaxNodes = int64(*xtrinode.Spec.OperatorNodePoolDefaults.DefaultMaxNodes)
		}
		if xtrinode.Spec.OperatorNodePoolDefaults.DefaultOSDiskGB != nil {
			defaults.DiskSizeGB = int64(*xtrinode.Spec.OperatorNodePoolDefaults.DefaultOSDiskGB)
		}
	}

	// Apply per-XTrinode spec (highest priority)
	if nodePool.MinNodes != nil {
		defaults.MinNodes = int64(*nodePool.MinNodes)
	}
	if nodePool.MaxNodes != nil {
		defaults.MaxNodes = int64(*nodePool.MaxNodes)
	}
	if nodePool.OSDiskGB != nil {
		defaults.DiskSizeGB = int64(*nodePool.OSDiskGB)
	}

	return defaults
}

// buildCommonLabels builds common labels for node pool resources
func buildCommonLabels(xtrinodeName string) map[string]string {
	return map[string]string{
		config.NodePoolManagedByLabel: config.NodePoolManagedByValue,
		config.RuntimeLabel:           xtrinodeName,
	}
}

// buildOwnerReference builds owner reference for XTrinode
func buildOwnerReference(xtrinode *analyticsv1.XTrinode) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
			Name:       xtrinode.Name,
			UID:        xtrinode.UID,
			Controller: func() *bool { b := true; return &b }(),
		},
	}
}

func buildNonControllerOwnerReference(xtrinode *analyticsv1.XTrinode) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
			Name:       xtrinode.Name,
			UID:        xtrinode.UID,
		},
	}
}

// buildAutoscalerAnnotations builds Cluster Autoscaler annotations
func buildAutoscalerAnnotations(minNodes, maxNodes int64) map[string]string {
	return map[string]string{
		config.NodePoolAutoscalerMinSizeAnnotation: fmt.Sprintf("%d", minNodes),
		config.NodePoolAutoscalerMaxSizeAnnotation: fmt.Sprintf("%d", maxNodes),
	}
}

// buildBaseMachineDeployment builds the base MachineDeployment unstructured object
func buildBaseMachineDeployment(name, namespace string, xtrinode *analyticsv1.XTrinode) *unstructured.Unstructured {
	md := &unstructured.Unstructured{}
	md.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	})
	md.SetName(name)
	md.SetNamespace(namespace)
	md.SetLabels(buildCommonLabels(xtrinode.Name))
	md.SetOwnerReferences(buildOwnerReference(xtrinode))
	return md
}

// buildBaseMachinePool builds the base MachinePool unstructured object (for Azure)
func buildBaseMachinePool(name, namespace string, xtrinode *analyticsv1.XTrinode) *unstructured.Unstructured {
	mp := &unstructured.Unstructured{}
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	mp.SetName(name)
	mp.SetNamespace(namespace)
	mp.SetLabels(buildCommonLabels(xtrinode.Name))
	mp.SetOwnerReferences(buildOwnerReference(xtrinode))
	return mp
}

// buildBaseInfrastructureTemplate builds base infrastructure template with common fields
func buildBaseInfrastructureTemplate(gvk schema.GroupVersionKind, name, namespace string, xtrinode *analyticsv1.XTrinode) *unstructured.Unstructured {
	template := &unstructured.Unstructured{}
	template.SetGroupVersionKind(gvk)
	template.SetName(name)
	template.SetNamespace(namespace)
	template.SetLabels(buildCommonLabels(xtrinode.Name))
	template.SetOwnerReferences(buildOwnerReference(xtrinode))
	return template
}

// setMachineDeploymentSpec sets common MachineDeployment spec fields
// setOnCreate controls whether to set replicas (only on initial creation)
// provider is used to determine the correct infrastructure API version
func setMachineDeploymentSpec(md *unstructured.Unstructured, clusterName, templateName string, minNodes int64, infrastructureKind string, setOnCreate bool, kubernetesVersion string, bootstrapConfigRef *corev1.ObjectReference, provider string) error {
	// Only set replicas on initial creation to avoid conflicts with Cluster Autoscaler
	if setOnCreate {
		if err := unstructured.SetNestedField(md.Object, minNodes, "spec", "replicas"); err != nil {
			return fmt.Errorf("failed to set replicas: %w", err)
		}
	}

	if err := unstructured.SetNestedField(md.Object, clusterName, "spec", "clusterName"); err != nil {
		return fmt.Errorf("failed to set clusterName: %w", err)
	}

	// Set template.spec.clusterName (required by CAPI)
	if err := unstructured.SetNestedField(md.Object, clusterName, "spec", "template", "spec", "clusterName"); err != nil {
		return fmt.Errorf("failed to set template.spec.clusterName: %w", err)
	}

	// Set Kubernetes version (required for CAPI)
	if kubernetesVersion != "" {
		if err := unstructured.SetNestedField(md.Object, kubernetesVersion, "spec", "template", "spec", "version"); err != nil {
			return fmt.Errorf("failed to set version: %w", err)
		}
	}

	// Set selector.matchLabels (required by CAPI)
	matchLabels := map[string]interface{}{
		"cluster.x-k8s.io/cluster-name":    clusterName,
		"cluster.x-k8s.io/deployment-name": md.GetName(),
	}
	if err := unstructured.SetNestedMap(md.Object, matchLabels, "spec", "selector", "matchLabels"); err != nil {
		return fmt.Errorf("failed to set selector.matchLabels: %w", err)
	}

	// Set template.metadata.labels (must match selector)
	if err := unstructured.SetNestedMap(md.Object, matchLabels, "spec", "template", "metadata", "labels"); err != nil {
		return fmt.Errorf("failed to set template.metadata.labels: %w", err)
	}

	// Set infrastructureRef
	if err := unstructured.SetNestedField(md.Object, templateName, "spec", "template", "spec", "infrastructureRef", "name"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef name: %w", err)
	}
	// Use provider-specific API version (AWS uses v1beta2, others use v1beta1)
	apiVersion := getInfrastructureAPIVersion(provider)
	if err := unstructured.SetNestedField(md.Object, apiVersion, "spec", "template", "spec", "infrastructureRef", "apiVersion"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef apiVersion: %w", err)
	}
	if err := unstructured.SetNestedField(md.Object, infrastructureKind, "spec", "template", "spec", "infrastructureRef", "kind"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef kind: %w", err)
	}

	// Set bootstrap configRef (required for self-managed clusters)
	// Use provided bootstrapConfigRef if available, otherwise skip (for managed clusters)
	if bootstrapConfigRef != nil {
		bootstrapRef := map[string]interface{}{
			"apiVersion": bootstrapConfigRef.APIVersion,
			"kind":       bootstrapConfigRef.Kind,
			"name":       bootstrapConfigRef.Name,
		}
		if bootstrapConfigRef.Namespace != "" {
			bootstrapRef["namespace"] = bootstrapConfigRef.Namespace
		}
		if err := unstructured.SetNestedMap(md.Object, bootstrapRef, "spec", "template", "spec", "bootstrap", "configRef"); err != nil {
			return fmt.Errorf("failed to set bootstrap configRef: %w", err)
		}
	}

	return nil
}

// setMachinePoolSpec sets common MachinePool spec fields (for Azure)
// setOnCreate controls whether to set replicas (only on initial creation)
func setMachinePoolSpec(mp *unstructured.Unstructured, clusterName, templateName string, minNodes int64, infrastructureKind string, setOnCreate bool, kubernetesVersion string, bootstrapConfigRef *corev1.ObjectReference) error {
	// Only set replicas on initial creation, not on updates
	// This allows Cluster Autoscaler to manage replicas without operator interference
	if setOnCreate {
		if err := unstructured.SetNestedField(mp.Object, minNodes, "spec", "replicas"); err != nil {
			return fmt.Errorf("failed to set replicas: %w", err)
		}
	}

	if err := unstructured.SetNestedField(mp.Object, clusterName, "spec", "clusterName"); err != nil {
		return fmt.Errorf("failed to set clusterName: %w", err)
	}

	// Set template.spec.clusterName (required by CAPI)
	if err := unstructured.SetNestedField(mp.Object, clusterName, "spec", "template", "spec", "clusterName"); err != nil {
		return fmt.Errorf("failed to set template.spec.clusterName: %w", err)
	}

	// Set Kubernetes version (required for CAPI)
	if kubernetesVersion != "" {
		if err := unstructured.SetNestedField(mp.Object, kubernetesVersion, "spec", "template", "spec", "version"); err != nil {
			return fmt.Errorf("failed to set version: %w", err)
		}
	}

	if err := unstructured.SetNestedField(mp.Object, templateName, "spec", "template", "spec", "infrastructureRef", "name"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef name: %w", err)
	}
	if err := unstructured.SetNestedField(mp.Object, config.NodePoolInfrastructureAPIVersion, "spec", "template", "spec", "infrastructureRef", "apiVersion"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef apiVersion: %w", err)
	}
	if err := unstructured.SetNestedField(mp.Object, infrastructureKind, "spec", "template", "spec", "infrastructureRef", "kind"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef kind: %w", err)
	}

	// Set bootstrap configRef (required for self-managed clusters)
	if bootstrapConfigRef != nil {
		bootstrapRef := map[string]interface{}{
			"apiVersion": bootstrapConfigRef.APIVersion,
			"kind":       bootstrapConfigRef.Kind,
			"name":       bootstrapConfigRef.Name,
		}
		if bootstrapConfigRef.Namespace != "" {
			bootstrapRef["namespace"] = bootstrapConfigRef.Namespace
		}
		if err := unstructured.SetNestedMap(mp.Object, bootstrapRef, "spec", "template", "spec", "bootstrap", "configRef"); err != nil {
			return fmt.Errorf("failed to set bootstrap configRef: %w", err)
		}
	}

	return nil
}

// applyTaintsToUnstructured applies taints to an unstructured object at the specified path
// path is a slice of path segments, e.g., []string{"spec", "template", "taints"}
func applyTaintsToUnstructured(obj *unstructured.Unstructured, taints []corev1.Taint, path []string) error {
	if len(taints) == 0 {
		return nil
	}

	taintList := make([]interface{}, len(taints))
	for i, taint := range taints {
		taintMap := map[string]interface{}{
			"key":    taint.Key,
			"effect": string(taint.Effect),
		}
		if taint.Value != "" {
			taintMap["value"] = taint.Value
		}
		taintList[i] = taintMap
	}

	if err := unstructured.SetNestedSlice(obj.Object, taintList, path...); err != nil {
		return fmt.Errorf("failed to set taints at path %v: %w", path, err)
	}
	return nil
}

// applyLabelsToUnstructured applies labels to an unstructured object at the specified path
// path is a slice of path segments, e.g., []string{"spec", "template", "additionalTags"}
func applyLabelsToUnstructured(obj *unstructured.Unstructured, labels map[string]string, path []string) error {
	if len(labels) == 0 {
		return nil
	}

	labelMap := make(map[string]interface{})
	for k, v := range labels {
		labelMap[k] = v
	}

	if err := unstructured.SetNestedMap(obj.Object, labelMap, path...); err != nil {
		return fmt.Errorf("failed to set labels at path %v: %w", path, err)
	}
	return nil
}

// applyZonesToMachineTemplate applies zones to infrastructure template (provider-specific)
// This sets provider-specific zone fields on the infrastructure template
func applyZonesToMachineTemplate(obj *unstructured.Unstructured, zones []string, provider string) error {
	if len(zones) == 0 {
		return nil
	}

	switch provider {
	case "azure":
		// Azure uses availabilityZones as a slice
		if err := unstructured.SetNestedStringSlice(obj.Object, zones, "spec", "template", "availabilityZones"); err != nil {
			return fmt.Errorf("failed to set availabilityZones: %w", err)
		}
	case "aws":
		// AWS uses availabilityZone as a single string (first element)
		if err := unstructured.SetNestedField(obj.Object, zones[0], "spec", "template", "spec", "availabilityZone"); err != nil {
			return fmt.Errorf("failed to set availabilityZone: %w", err)
		}
	case "gcp":
		// GCP uses zone as a single string (first element)
		if err := unstructured.SetNestedField(obj.Object, zones[0], "spec", "template", "spec", "zone"); err != nil {
			return fmt.Errorf("failed to set zone: %w", err)
		}
	default:
		return fmt.Errorf("unsupported provider for zones: %s", provider)
	}
	return nil
}

// applyFailureDomainToMachine applies failureDomain to MachineDeployment/MachinePool
// This is the CAPI-standard way to specify zone placement
func applyFailureDomainToMachine(obj *unstructured.Unstructured, zones []string) error {
	if len(zones) == 0 {
		return nil
	}

	// Use first zone as failureDomain (CAPI will spread across zones if multiple are available)
	// For multi-zone deployments, you'd typically create multiple MachineDeployments
	if err := unstructured.SetNestedField(obj.Object, zones[0], "spec", "template", "spec", "failureDomain"); err != nil {
		return fmt.Errorf("failed to set failureDomain: %w", err)
	}
	return nil
}

// deleteUnstructuredResource deletes an unstructured Kubernetes resource
// Returns nil if resource is already deleted (NotFound error is ignored)
func deleteUnstructuredResource(cli client.Client, ctx context.Context, obj *unstructured.Unstructured, log logr.Logger, resourceKind, resourceName string) error {
	if err := cli.Delete(ctx, obj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info(fmt.Sprintf("%s already deleted", resourceKind), "name", resourceName)
			return nil
		}
		return fmt.Errorf("failed to delete %s: %w", resourceKind, err)
	}
	return nil
}

// buildUnstructuredForDeletion builds an unstructured object for deletion
func buildUnstructuredForDeletion(gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	return obj
}

// getAzureVMSize returns Azure VM size
func getAzureVMSize(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool.Azure != nil {
		return nodePool.Azure.VMSize
	}
	return ""
}

// getAzureOSDiskType returns Azure OS disk type, with fallback to operator default
func getAzureOSDiskType(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool.Azure != nil && nodePool.Azure.OSDiskType != "" {
		return nodePool.Azure.OSDiskType
	}
	return config.NodePoolAzureOSDiskType
}

// getAWSInstanceType returns AWS instance type
func getAWSInstanceType(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool.AWS != nil {
		return nodePool.AWS.InstanceType
	}
	return ""
}

// getAWSVolumeType returns AWS volume type, with fallback to operator default
func getAWSVolumeType(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool.AWS != nil && nodePool.AWS.VolumeType != "" {
		return nodePool.AWS.VolumeType
	}
	return config.NodePoolAWSVolumeType
}

// getGCPMachineType returns GCP machine type
func getGCPMachineType(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool.GCP != nil {
		return nodePool.GCP.MachineType
	}
	return ""
}

// getGCPDiskType returns GCP disk type, with fallback to operator default
func getGCPDiskType(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool.GCP != nil && nodePool.GCP.DiskType != "" {
		return nodePool.GCP.DiskType
	}
	return config.NodePoolGCPDiskType
}

// applyUnstructured applies an unstructured object using server-side apply.
// It avoids ForceOwnership so primary resources can keep externally owned scale
// fields such as spec.replicas when autoscalers are involved.
func applyUnstructured(cli client.Client, ctx context.Context, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()
	if gvk.Empty() {
		gv, err := schema.ParseGroupVersion(obj.GetAPIVersion())
		if err == nil {
			gvk = gv.WithKind(obj.GetKind())
		}
	}
	if !gvk.Empty() {
		obj.SetGroupVersionKind(gvk)
	}
	return serverapply.Unstructured(ctx, cli, obj, "xtrinode-operator", false)
}

// createOrApplyUnstructured creates primary CAPI resources on first reconcile so
// the operator does not keep server-side-apply ownership of spec.replicas.
// Later reconciles patch autoscaler annotations separately and apply only the
// remaining fields the operator should keep managing. This lets Cluster
// Autoscaler own replica changes without blocking min/max annotation updates.
func createOrApplyUnstructured(cli client.Client, ctx context.Context, obj *unstructured.Unstructured, resourceExists bool) error {
	if !resourceExists {
		if err := cli.Create(ctx, obj); err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				return err
			}
		} else {
			return nil
		}
	}

	applyObj := obj.DeepCopy()
	if err := patchUnstructuredAnnotations(cli, ctx, applyObj); err != nil {
		return err
	}
	applyObj.SetAnnotations(nil)
	return applyUnstructured(cli, ctx, applyObj)
}

func patchUnstructuredAnnotations(cli client.Client, ctx context.Context, obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	if len(annotations) == 0 {
		return nil
	}

	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(obj.GroupVersionKind())
	patchObj.SetName(obj.GetName())
	patchObj.SetNamespace(obj.GetNamespace())
	patchObj.SetAnnotations(annotations)

	if err := cli.Patch(ctx, patchObj, client.Merge); err != nil {
		return fmt.Errorf("failed to patch annotations: %w", err)
	}
	return nil
}

// checkResourceExists checks if a resource exists
func checkResourceExists(cli client.Client, ctx context.Context, obj *unstructured.Unstructured) (bool, error) {
	err := cli.Get(ctx, client.ObjectKey{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}, obj)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// validateProviderFields is now in nodepool_validation.go

// isMachinePoolProvider returns true if this provider+mode uses MachinePool instead of MachineDeployment
// Azure always uses MachinePool; AWS/GCP use MachinePool for managed mode, MachineDeployment for self-managed
func isMachinePoolProvider(nodePool *analyticsv1.NodePoolSpec) bool {
	if nodePool.Provider == "azure" {
		return true
	}
	// Managed AWS/GCP use MachinePool
	return nodePool.ProviderMode == "managed"
}

// getMachineResourceGVK returns the GroupVersionKind for MachinePool or MachineDeployment
func getMachineResourceGVK(isMachinePool bool) schema.GroupVersionKind {
	kind := "MachineDeployment"
	if isMachinePool {
		kind = "MachinePool"
	}
	return schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    kind,
	}
}

// getInfrastructureTemplateGVK returns the GroupVersionKind for provider-specific infrastructure templates
// Uses provider-specific API version: AWS uses v1beta2, others use v1beta1
func getInfrastructureTemplateGVK(provider string, isMachinePool bool) schema.GroupVersionKind {
	var kind string
	switch provider {
	case "azure":
		if isMachinePool {
			kind = "AzureMachinePool"
		} else {
			kind = "AzureMachineTemplate"
		}
	case "aws":
		kind = "AWSMachineTemplate"
	case "gcp":
		kind = "GCPMachineTemplate"
	default:
		kind = "MachineTemplate"
	}
	return schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: getInfrastructureAPIVersionShort(provider),
		Kind:    kind,
	}
}

// getInfrastructureAPIVersionShort returns just the version portion (e.g., "v1beta2") for a provider
func getInfrastructureAPIVersionShort(provider string) string {
	switch provider {
	case "aws":
		return "v1beta2"
	default:
		return "v1beta1"
	}
}

// deleteNodePoolResources deletes MachinePool/MachineDeployment and infrastructure template
// This is a common helper to reduce duplication across provider-specific deletion functions
func deleteNodePoolResources(
	cli client.Client,
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	log logr.Logger,
	provider string,
	isMachinePool bool,
) error {
	nodePool := xtrinode.Spec.NodePool
	resourceName := getNodePoolName(nodePool, xtrinode.Name)

	// Delete MachinePool/MachineDeployment
	machineResourceGVK := getMachineResourceGVK(isMachinePool)
	machineResourceKind := machineResourceGVK.Kind
	machineResource := buildUnstructuredForDeletion(machineResourceGVK, resourceName, xtrinode.Namespace)
	if err := deleteUnstructuredResource(cli, ctx, machineResource, log, machineResourceKind, resourceName); err != nil {
		return err
	}

	// Delete infrastructure template
	infrastructureGVK := getInfrastructureTemplateGVK(provider, isMachinePool)
	infrastructureKind := infrastructureGVK.Kind
	infrastructureName := resourceName + config.NodePoolTemplateSuffix
	infrastructureTemplate := buildUnstructuredForDeletion(infrastructureGVK, infrastructureName, xtrinode.Namespace)
	if err := deleteUnstructuredResource(cli, ctx, infrastructureTemplate, log, infrastructureKind, infrastructureName); err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Deleted %s %s", provider, machineResourceKind), "name", resourceName)
	return nil
}

func retainNodePoolResources(
	cli client.Client,
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	log logr.Logger,
	provider string,
	isMachinePool bool,
) error {
	nodePool := xtrinode.Spec.NodePool
	resourceName := getNodePoolName(nodePool, xtrinode.Name)

	machineResourceGVK := getMachineResourceGVK(isMachinePool)
	machineResource := buildUnstructuredForDeletion(machineResourceGVK, resourceName, xtrinode.Namespace)
	if err := removeXTrinodeOwnerReference(cli, ctx, machineResource, xtrinode, log); err != nil {
		return err
	}

	infrastructureGVK := getInfrastructureTemplateGVK(provider, isMachinePool)
	infrastructureName := resourceName + config.NodePoolTemplateSuffix
	infrastructureTemplate := buildUnstructuredForDeletion(infrastructureGVK, infrastructureName, xtrinode.Namespace)
	if err := removeXTrinodeOwnerReference(cli, ctx, infrastructureTemplate, xtrinode, log); err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Retained %s %s", provider, machineResourceGVK.Kind), "name", resourceName)
	return nil
}

// getInfrastructureAPIVersion returns the correct API version for a provider's infrastructure CRDs
// AWS uses v1beta2, while Azure and GCP use v1beta1
func getInfrastructureAPIVersion(provider string) string {
	switch provider {
	case "aws":
		return "infrastructure.cluster.x-k8s.io/v1beta2"
	default:
		return "infrastructure.cluster.x-k8s.io/v1beta1"
	}
}

// setXTrinodeOwnerReference sets owner reference to XTrinode resource
// Uses analyticsv1.GroupVersion and hardcoded Kind because controller-runtime
// does not populate TypeMeta on objects fetched via Get()
func setXTrinodeOwnerReference(obj *unstructured.Unstructured, xtrinode *analyticsv1.XTrinode) error {
	obj.SetOwnerReferences(buildOwnerReference(xtrinode))
	return nil
}

func setXTrinodeNonControllerOwnerReference(obj *unstructured.Unstructured, xtrinode *analyticsv1.XTrinode) error {
	obj.SetOwnerReferences(buildNonControllerOwnerReference(xtrinode))
	return nil
}

func removeXTrinodeOwnerReference(
	cli client.Client,
	ctx context.Context,
	obj *unstructured.Unstructured,
	xtrinode *analyticsv1.XTrinode,
	log logr.Logger,
) error {
	existing := obj.DeepCopy()
	if err := cli.Get(ctx, client.ObjectKeyFromObject(obj), existing); err != nil {
		if k8serrors.IsNotFound(err) {
			log.V(1).Info("Node-pool resource already absent while retaining", "kind", obj.GetKind(), "name", obj.GetName())
			return nil
		}
		return fmt.Errorf("failed to get node-pool resource %s/%s for retention: %w", obj.GetKind(), obj.GetName(), err)
	}

	ownerRefs := existing.GetOwnerReferences()
	filtered := ownerRefs[:0]
	removed := false
	for _, ref := range ownerRefs {
		if ownerReferenceMatchesXTrinode(&ref, xtrinode) {
			removed = true
			continue
		}
		filtered = append(filtered, ref)
	}
	if !removed {
		return nil
	}
	existing.SetOwnerReferences(filtered)
	if err := cli.Update(ctx, existing); err != nil {
		return fmt.Errorf("failed to remove XTrinode owner reference from %s/%s: %w", existing.GetKind(), existing.GetName(), err)
	}
	log.Info("Removed XTrinode owner reference from retained node-pool resource", "kind", existing.GetKind(), "name", existing.GetName())
	return nil
}

func ownerReferenceMatchesXTrinode(ref *metav1.OwnerReference, xtrinode *analyticsv1.XTrinode) bool {
	if ref.APIVersion != analyticsv1.GroupVersion.String() || ref.Kind != "XTrinode" || ref.Name != xtrinode.Name {
		return false
	}
	return xtrinode.UID == "" || ref.UID == "" || ref.UID == xtrinode.UID
}

// managedMachinePoolConfig holds the parameters for building a managed MachinePool (CAPI core)
type managedMachinePoolConfig struct {
	PoolName                string
	Namespace               string
	ClusterName             string
	XTrinode                *analyticsv1.XTrinode
	Defaults                NodePoolDefaults
	ResourceExists          bool
	InfraAPIVer             string // e.g. "infrastructure.cluster.x-k8s.io/v1beta1"
	InfraKind               string // e.g. "AzureManagedMachinePool"
	KubernetesVersion       string // e.g. "v1.28.0"
	RemoveKubernetesVersion bool
}

// buildAndApplyManagedMachinePool builds and applies the CAPI MachinePool for managed clusters.
// This is shared by Azure, AWS, and GCP managed pool implementations.
func buildAndApplyManagedMachinePool(cli client.Client, ctx context.Context, cfg *managedMachinePoolConfig) error {
	machinePool := &unstructured.Unstructured{}
	machinePool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	machinePool.SetName(cfg.PoolName)
	machinePool.SetNamespace(cfg.Namespace)

	// Set cluster label
	machinePool.SetLabels(map[string]string{
		"cluster.x-k8s.io/cluster-name": cfg.ClusterName,
	})

	// Set owner reference
	if err := setXTrinodeOwnerReference(machinePool, cfg.XTrinode); err != nil {
		return err
	}

	// Set cluster name
	if err := unstructured.SetNestedField(machinePool.Object, cfg.ClusterName, "spec", "clusterName"); err != nil {
		return fmt.Errorf("failed to set clusterName: %w", err)
	}

	// Set template.spec.clusterName (required by CAPI)
	if err := unstructured.SetNestedField(machinePool.Object, cfg.ClusterName, "spec", "template", "spec", "clusterName"); err != nil {
		return fmt.Errorf("failed to set template.spec.clusterName: %w", err)
	}

	// Set template.metadata.labels with cluster label (required for machine adoption by CAPI)
	templateLabels := map[string]interface{}{
		"cluster.x-k8s.io/cluster-name": cfg.ClusterName,
	}
	if err := unstructured.SetNestedMap(machinePool.Object, templateLabels, "spec", "template", "metadata", "labels"); err != nil {
		return fmt.Errorf("failed to set template.metadata.labels: %w", err)
	}

	// Only set replicas on initial creation to avoid conflicts with Cluster Autoscaler
	if !cfg.ResourceExists {
		if err := unstructured.SetNestedField(machinePool.Object, cfg.Defaults.MinNodes, "spec", "replicas"); err != nil {
			return fmt.Errorf("failed to set replicas: %w", err)
		}
	}

	// Set infrastructure reference
	infraRef := map[string]interface{}{
		"apiVersion": cfg.InfraAPIVer,
		"kind":       cfg.InfraKind,
		"name":       cfg.PoolName,
		"namespace":  cfg.Namespace,
	}
	if err := unstructured.SetNestedMap(machinePool.Object, infraRef, "spec", "template", "spec", "infrastructureRef"); err != nil {
		return fmt.Errorf("failed to set infrastructureRef: %w", err)
	}

	// Set Kubernetes version on the MachinePool template (CAPI standard location)
	if cfg.KubernetesVersion != "" {
		if err := unstructured.SetNestedField(machinePool.Object, cfg.KubernetesVersion, "spec", "template", "spec", "version"); err != nil {
			return fmt.Errorf("failed to set template.spec.version: %w", err)
		}
	}

	// For managed pools, bootstrap is empty (managed by cloud provider)
	bootstrapRef := map[string]interface{}{
		"dataSecretName": "",
	}
	if err := unstructured.SetNestedMap(machinePool.Object, bootstrapRef, "spec", "template", "spec", "bootstrap"); err != nil {
		return fmt.Errorf("failed to set bootstrap: %w", err)
	}

	// Set autoscaler annotations before creating/updating.
	// We use Create-or-Update instead of SSA (server-side apply) for the
	// MachinePool because SSA does not reliably preserve metadata.annotations
	// for unstructured objects across all client implementations.
	machinePool.SetAnnotations(buildAutoscalerAnnotations(cfg.Defaults.MinNodes, cfg.Defaults.MaxNodes))

	if err := createOrApplyUnstructured(cli, ctx, machinePool, cfg.ResourceExists); err != nil {
		if cfg.ResourceExists {
			return fmt.Errorf("failed to apply MachinePool: %w", err)
		}
		return fmt.Errorf("failed to create MachinePool: %w", err)
	}
	if cfg.RemoveKubernetesVersion {
		if err := removeMachinePoolTemplateVersion(cli, ctx, machinePool); err != nil {
			return fmt.Errorf("failed to remove MachinePool template version: %w", err)
		}
	}

	return nil
}

func removeMachinePoolTemplateVersion(cli client.Client, ctx context.Context, machinePool *unstructured.Unstructured) error {
	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(machinePool.GroupVersionKind())
	patchObj.SetName(machinePool.GetName())
	patchObj.SetNamespace(machinePool.GetNamespace())
	return cli.Patch(ctx, patchObj, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"template":{"spec":{"version":null}}}}`)))
}

// getManagedInfrastructureGVK returns the GVK for provider-specific managed infra CRDs
// (AWSManagedMachinePool, AzureManagedMachinePool, GCPManagedMachinePool)
func getManagedInfrastructureGVK(provider string) schema.GroupVersionKind {
	var kind, version string
	switch provider {
	case "azure":
		kind = "AzureManagedMachinePool"
		version = "v1beta1"
	case "aws":
		kind = "AWSManagedMachinePool"
		version = "v1beta2"
	case "gcp":
		kind = "GCPManagedMachinePool"
		version = "v1beta1"
	default:
		kind = "ManagedMachinePool"
		version = "v1beta1"
	}
	return schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: version,
		Kind:    kind,
	}
}

// deleteNodePoolManagedResources deletes a managed MachinePool and its provider-specific infra CRD
// Used for EKS (AWSManagedMachinePool), AKS (AzureManagedMachinePool), GKE (GCPManagedMachinePool)
func deleteNodePoolManagedResources(
	cli client.Client,
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	log logr.Logger,
	provider string,
) error {
	nodePool := xtrinode.Spec.NodePool
	resourceName := getNodePoolName(nodePool, xtrinode.Name)

	// Delete CAPI core MachinePool (all managed pools use MachinePool)
	machinePoolGVK := getMachineResourceGVK(true)
	machinePool := buildUnstructuredForDeletion(machinePoolGVK, resourceName, xtrinode.Namespace)
	if err := deleteUnstructuredResource(cli, ctx, machinePool, log, "MachinePool", resourceName); err != nil {
		return err
	}

	// Delete managed infra CRD (AWSManagedMachinePool / AzureManagedMachinePool / GCPManagedMachinePool)
	infraGVK := getManagedInfrastructureGVK(provider)
	infraResource := buildUnstructuredForDeletion(infraGVK, resourceName, xtrinode.Namespace)
	if err := deleteUnstructuredResource(cli, ctx, infraResource, log, infraGVK.Kind, resourceName); err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Deleted managed %s MachinePool", provider), "name", resourceName)
	return nil
}

func retainNodePoolManagedResources(
	cli client.Client,
	ctx context.Context,
	xtrinode *analyticsv1.XTrinode,
	log logr.Logger,
	provider string,
) error {
	nodePool := xtrinode.Spec.NodePool
	resourceName := getNodePoolName(nodePool, xtrinode.Name)

	machinePoolGVK := getMachineResourceGVK(true)
	machinePool := buildUnstructuredForDeletion(machinePoolGVK, resourceName, xtrinode.Namespace)
	if err := removeXTrinodeOwnerReference(cli, ctx, machinePool, xtrinode, log); err != nil {
		return err
	}

	infraGVK := getManagedInfrastructureGVK(provider)
	infraResource := buildUnstructuredForDeletion(infraGVK, resourceName, xtrinode.Namespace)
	if err := removeXTrinodeOwnerReference(cli, ctx, infraResource, xtrinode, log); err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Retained managed %s MachinePool", provider), "name", resourceName)
	return nil
}

// convertTaintsToUnstructuredSlice converts Kubernetes taints to unstructured slice for CAPI
func convertTaintsToUnstructuredSlice(taints []corev1.Taint) []interface{} {
	result := make([]interface{}, 0, len(taints))
	for _, taint := range taints {
		t := map[string]interface{}{
			"key":    taint.Key,
			"effect": string(taint.Effect),
		}
		if taint.Value != "" {
			t["value"] = taint.Value
		}
		result = append(result, t)
	}
	return result
}
