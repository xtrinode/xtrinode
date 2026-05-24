package controllers

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// ensureAzureManagedMachinePool creates or updates Azure managed machine pool (AKS node pool)
// Creates both MachinePool (CAPI core) and AzureManagedMachinePool (CAPZ provider)
func (n *NodePoolAdapter) ensureAzureManagedMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	nodePool := xtrinode.Spec.NodePool
	clusterName := n.getClusterName(ctx, xtrinode)
	poolName := getNodePoolName(nodePool, xtrinode.Name)

	// Get defaults
	defaults := getNodePoolDefaults(nodePool, xtrinode)

	// Check if MachinePool already exists to gate replica setting
	existingCheck := &unstructured.Unstructured{}
	existingCheck.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	existingCheck.SetName(poolName)
	existingCheck.SetNamespace(xtrinode.Namespace)
	resourceExists, err := checkResourceExists(n.client, ctx, existingCheck)
	if err != nil {
		return fmt.Errorf("failed to check if MachinePool exists: %w", err)
	}

	// Validate required fields before creation
	if err := validateNodePoolForCreation(nodePool, resourceExists); err != nil {
		return fmt.Errorf("nodepool validation failed: %w", err)
	}

	// Step 1: Create/update AzureManagedMachinePool (provider infra CRD)
	infraPool := &unstructured.Unstructured{}
	infraPool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "AzureManagedMachinePool",
	})
	infraPool.SetName(poolName)
	infraPool.SetNamespace(xtrinode.Namespace)

	// Set cluster label (CAPI standard for cluster association)
	labels := infraPool.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["cluster.x-k8s.io/cluster-name"] = clusterName
	infraPool.SetLabels(labels)

	// CAPI MachinePool must become the controller owner for managed infra pools.
	if err := setXTrinodeNonControllerOwnerReference(infraPool, xtrinode); err != nil {
		return err
	}

	// Set VM size (required)
	if nodePool.Azure == nil || nodePool.Azure.VMSize == "" {
		return fmt.Errorf("azure.vmSize is required for Azure managed pools")
	}
	if err := unstructured.SetNestedField(infraPool.Object, nodePool.Azure.VMSize, "spec", "sku"); err != nil {
		return fmt.Errorf("failed to set sku: %w", err)
	}

	// Set mode (System or User) - default to User for workload pools
	mode := "User"
	if err := unstructured.SetNestedField(infraPool.Object, mode, "spec", "mode"); err != nil {
		return fmt.Errorf("failed to set mode: %w", err)
	}

	// Set scaling configuration
	scalingConfig := map[string]interface{}{
		"minSize": defaults.MinNodes,
		"maxSize": defaults.MaxNodes,
	}
	if err := unstructured.SetNestedMap(infraPool.Object, scalingConfig, "spec", "scaling"); err != nil {
		return fmt.Errorf("failed to set scaling config: %w", err)
	}

	// Set Kubernetes version if specified
	if nodePool.KubernetesVersion != "" {
		if err := unstructured.SetNestedField(infraPool.Object, nodePool.KubernetesVersion, "spec", "version"); err != nil {
			return fmt.Errorf("failed to set version: %w", err)
		}
	}

	// Apply node labels (native AKS support)
	if labels := effectiveNodePoolLabels(nodePool, poolName); len(labels) > 0 {
		nodeLabels := make(map[string]interface{})
		for k, v := range labels {
			nodeLabels[k] = v
		}
		if err := unstructured.SetNestedMap(infraPool.Object, nodeLabels, "spec", "nodeLabels"); err != nil {
			return fmt.Errorf("failed to set node labels: %w", err)
		}
	}

	// Apply node taints (native AKS support)
	if len(nodePool.NodeTaints) > 0 {
		taints := convertTaintsToUnstructuredSlice(nodePool.NodeTaints)
		if err := unstructured.SetNestedSlice(infraPool.Object, taints, "spec", "taints"); err != nil {
			return fmt.Errorf("failed to set node taints: %w", err)
		}
	}

	// Apply availability zones (multi-zone support)
	if len(nodePool.Zones) > 0 {
		zones := make([]interface{}, len(nodePool.Zones))
		for i, zone := range nodePool.Zones {
			zones[i] = zone
		}
		if err := unstructured.SetNestedSlice(infraPool.Object, zones, "spec", "availabilityZones"); err != nil {
			return fmt.Errorf("failed to set availability zones: %w", err)
		}
	}

	// Apply resource tags (cloud provider tags, not node labels)
	if len(nodePool.ResourceTags) > 0 {
		tags := make(map[string]interface{})
		for k, v := range nodePool.ResourceTags {
			tags[k] = v
		}
		if err := unstructured.SetNestedMap(infraPool.Object, tags, "spec", "additionalTags"); err != nil {
			return fmt.Errorf("failed to set additional tags: %w", err)
		}
	}

	// Apply AzureManagedMachinePool
	if err := applyUnstructured(n.client, ctx, infraPool); err != nil {
		return fmt.Errorf("failed to apply AzureManagedMachinePool: %w", err)
	}

	// Step 2: Build and apply MachinePool (CAPI core) using shared helper
	if err := buildAndApplyManagedMachinePool(n.client, ctx, &managedMachinePoolConfig{
		PoolName:          poolName,
		Namespace:         xtrinode.Namespace,
		ClusterName:       clusterName,
		XTrinode:          xtrinode,
		Defaults:          defaults,
		ResourceExists:    resourceExists,
		InfraAPIVer:       "infrastructure.cluster.x-k8s.io/v1beta1",
		InfraKind:         "AzureManagedMachinePool",
		KubernetesVersion: nodePool.KubernetesVersion,
	}); err != nil {
		return err
	}

	n.log.Info("Azure managed machine pool ensured", "name", poolName, "zones", len(nodePool.Zones))
	return nil
}
