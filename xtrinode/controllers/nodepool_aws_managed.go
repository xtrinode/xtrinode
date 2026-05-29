package controllers

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// ensureAWSManagedMachinePool creates or updates AWS managed machine pool (EKS node group)
// Creates both MachinePool (CAPI core) and AWSManagedMachinePool (CAPA provider)
func (n *NodePoolAdapter) ensureAWSManagedMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	nodePool := xtrinode.Spec.NodePool
	clusterName := n.getClusterName(ctx, xtrinode)
	poolName := getNodePoolName(nodePool, xtrinode.Name)

	// Get defaults
	defaults := getNodePoolDefaults(nodePool, xtrinode)

	resourceExists, err := managedMachinePoolExists(n.client, ctx, poolName, xtrinode.Namespace)
	if err != nil {
		return fmt.Errorf("failed to check if MachinePool exists: %w", err)
	}

	// Validate required fields before creation
	if validationErr := validateNodePoolForCreation(nodePool, resourceExists); validationErr != nil {
		return fmt.Errorf("nodepool validation failed: %w", validationErr)
	}

	// Step 1: Create/update AWSManagedMachinePool (provider infra CRD)
	infraPool, err := newManagedInfrastructurePool("aws", poolName, xtrinode.Namespace, clusterName, xtrinode)
	if err != nil {
		return err
	}

	// Set instance type (required)
	if nodePool.AWS == nil || nodePool.AWS.InstanceType == "" {
		return fmt.Errorf("aws.instanceType is required for AWS managed pools")
	}
	if err := unstructured.SetNestedField(infraPool.Object, nodePool.AWS.InstanceType, "spec", "instanceType"); err != nil {
		return fmt.Errorf("failed to set instanceType: %w", err)
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

	// Apply availability zones if specified
	if len(nodePool.Zones) > 0 {
		zones := make([]interface{}, len(nodePool.Zones))
		for i, zone := range nodePool.Zones {
			zones[i] = zone
		}
		if err := unstructured.SetNestedSlice(infraPool.Object, zones, "spec", "availabilityZones"); err != nil {
			return fmt.Errorf("failed to set availability zones: %w", err)
		}
	}

	// Apply node labels (native EKS support)
	if labels := effectiveNodePoolLabels(nodePool, poolName); len(labels) > 0 {
		nodeLabels := make(map[string]interface{})
		for k, v := range labels {
			nodeLabels[k] = v
		}
		if err := unstructured.SetNestedMap(infraPool.Object, nodeLabels, "spec", "labels"); err != nil {
			return fmt.Errorf("failed to set node labels: %w", err)
		}
	}

	// Apply node taints (native EKS support)
	if len(nodePool.NodeTaints) > 0 {
		taints := convertTaintsToUnstructuredSlice(nodePool.NodeTaints)
		if err := unstructured.SetNestedSlice(infraPool.Object, taints, "spec", "taints"); err != nil {
			return fmt.Errorf("failed to set node taints: %w", err)
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

	// Apply AWSManagedMachinePool
	if err := applyUnstructured(n.client, ctx, infraPool); err != nil {
		return fmt.Errorf("failed to apply AWSManagedMachinePool: %w", err)
	}

	// Step 2: Build and apply MachinePool (CAPI core) using shared helper
	if err := buildAndApplyManagedMachinePool(n.client, ctx, &managedMachinePoolConfig{
		PoolName:          poolName,
		Namespace:         xtrinode.Namespace,
		ClusterName:       clusterName,
		XTrinode:          xtrinode,
		Defaults:          defaults,
		ResourceExists:    resourceExists,
		InfraAPIVer:       "infrastructure.cluster.x-k8s.io/v1beta2",
		InfraKind:         "AWSManagedMachinePool",
		KubernetesVersion: nodePool.KubernetesVersion,
	}); err != nil {
		return err
	}

	n.log.Info("AWS managed machine pool ensured", "name", poolName)
	return nil
}
