package controllers

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// ensureGCPManagedMachinePool creates or updates GCP managed machine pool (GKE node pool)
// Creates both MachinePool (CAPI core) and GCPManagedMachinePool (CAPG provider)
func (n *NodePoolAdapter) ensureGCPManagedMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	nodePool := xtrinode.Spec.NodePool
	clusterName := n.getClusterName(ctx, xtrinode)
	poolName := getNodePoolName(nodePool, xtrinode.Name)

	// Get defaults
	defaults := getNodePoolDefaults(nodePool, xtrinode)
	machinePoolVersion := gcpManagedMachinePoolTemplateVersion(nodePool.KubernetesVersion)

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

	// Step 1: Create/update GCPManagedMachinePool (provider infra CRD)
	infraPool := &unstructured.Unstructured{}
	infraPool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "GCPManagedMachinePool",
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

	// Set machine type (required)
	if nodePool.GCP == nil || nodePool.GCP.MachineType == "" {
		return fmt.Errorf("gcp.machineType is required for GCP managed pools")
	}
	if err := unstructured.SetNestedField(infraPool.Object, nodePool.GCP.MachineType, "spec", "machineType"); err != nil {
		return fmt.Errorf("failed to set machineType: %w", err)
	}
	if err := unstructured.SetNestedField(infraPool.Object, poolName, "spec", "nodePoolName"); err != nil {
		return fmt.Errorf("failed to set nodePoolName: %w", err)
	}
	if nodePool.GCP.DiskType != "" {
		if err := unstructured.SetNestedField(infraPool.Object, nodePool.GCP.DiskType, "spec", "diskType"); err != nil {
			return fmt.Errorf("failed to set diskType: %w", err)
		}
	}
	if err := unstructured.SetNestedField(infraPool.Object, defaults.DiskSizeGB, "spec", "diskSizeGB"); err != nil {
		return fmt.Errorf("failed to set diskSizeGB: %w", err)
	}

	// Set scaling configuration (GCP uses different field names: minCount/maxCount)
	scalingConfig := map[string]interface{}{
		"minCount": defaults.MinNodes,
		"maxCount": defaults.MaxNodes,
	}
	if err := unstructured.SetNestedMap(infraPool.Object, scalingConfig, "spec", "scaling"); err != nil {
		return fmt.Errorf("failed to set scaling config: %w", err)
	}

	// Apply node labels (native GKE support)
	if labels := effectiveNodePoolLabels(nodePool, poolName); len(labels) > 0 {
		nodeLabels := make(map[string]interface{})
		for k, v := range labels {
			nodeLabels[k] = v
		}
		if err := unstructured.SetNestedMap(infraPool.Object, nodeLabels, "spec", "kubernetesLabels"); err != nil {
			return fmt.Errorf("failed to set node labels: %w", err)
		}
	}

	// Apply node taints (native GKE support)
	if len(nodePool.NodeTaints) > 0 {
		taints := convertTaintsToUnstructuredSlice(nodePool.NodeTaints)
		if err := unstructured.SetNestedSlice(infraPool.Object, taints, "spec", "kubernetesTaints"); err != nil {
			return fmt.Errorf("failed to set node taints: %w", err)
		}
	}

	// Apply node locations (zones) for multi-zone support
	if len(nodePool.Zones) > 0 {
		zones := make([]interface{}, len(nodePool.Zones))
		for i, zone := range nodePool.Zones {
			zones[i] = zone
		}
		if err := unstructured.SetNestedSlice(infraPool.Object, zones, "spec", "nodeLocations"); err != nil {
			return fmt.Errorf("failed to set node locations: %w", err)
		}
	}

	// Apply resource labels (GCP uses labels, not tags). GKE adds this
	// provisioning-model resource label by default; include it in desired state
	// so CAPG does not continuously try to remove it.
	resourceLabels := map[string]interface{}{
		"goog-gke-node-pool-provisioning-model": "on-demand",
	}
	for k, v := range nodePool.ResourceTags {
		resourceLabels[k] = v
	}
	if err := unstructured.SetNestedMap(infraPool.Object, resourceLabels, "spec", "additionalLabels"); err != nil {
		return fmt.Errorf("failed to set additional labels: %w", err)
	}

	// Apply GCPManagedMachinePool
	if err := applyUnstructured(n.client, ctx, infraPool); err != nil {
		return fmt.Errorf("failed to apply GCPManagedMachinePool: %w", err)
	}

	// Step 2: Build and apply MachinePool (CAPI core) using shared helper
	if err := buildAndApplyManagedMachinePool(n.client, ctx, &managedMachinePoolConfig{
		PoolName:                poolName,
		Namespace:               xtrinode.Namespace,
		ClusterName:             clusterName,
		XTrinode:                xtrinode,
		Defaults:                defaults,
		ResourceExists:          resourceExists,
		InfraAPIVer:             "infrastructure.cluster.x-k8s.io/v1beta1",
		InfraKind:               "GCPManagedMachinePool",
		KubernetesVersion:       machinePoolVersion,
		RemoveKubernetesVersion: nodePool.KubernetesVersion != "" && machinePoolVersion == "",
	}); err != nil {
		return err
	}

	n.log.Info("GCP managed machine pool ensured", "name", poolName, "zones", len(nodePool.Zones))
	return nil
}

func gcpManagedMachinePoolTemplateVersion(version string) string {
	if strings.Contains(version, "-gke.") {
		return version
	}
	return ""
}
