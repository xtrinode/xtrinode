package controllers

import (
	"fmt"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// validateNodePoolForCreation validates that all required fields are present for creating new CAPI resources
// This is called before attempting to create MachineDeployment/MachinePool resources
func validateNodePoolForCreation(nodePool *analyticsv1.NodePoolSpec, resourceExists bool) error {
	if resourceExists {
		// Resource already exists, no creation validation needed
		return nil
	}

	// Validate provider-specific fields
	if err := validateProviderFields(nodePool); err != nil {
		return err
	}

	// For self-managed clusters, require kubernetesVersion and bootstrapConfigRef
	if nodePool.ProviderMode == "self-managed" || nodePool.ProviderMode == "" {
		if len(nodePool.NodeTaints) > 0 {
			return fmt.Errorf("spec.nodePool.nodeTaints is not supported for self-managed clusters; configure nodeRegistration.taints in spec.nodePool.bootstrapConfigRef instead")
		}

		// KubernetesVersion is required for creating new MachineDeployments
		if nodePool.KubernetesVersion == "" {
			return fmt.Errorf("spec.nodePool.kubernetesVersion is required for creating new node pools (e.g., 'v1.28.0')")
		}

		// BootstrapConfigRef is required for self-managed clusters
		if nodePool.BootstrapConfigRef == nil {
			return fmt.Errorf("spec.nodePool.bootstrapConfigRef is required for self-managed clusters (must reference a KubeadmConfigTemplate or similar)")
		}

		// Validate bootstrapConfigRef has required fields
		if nodePool.BootstrapConfigRef.APIVersion == "" || nodePool.BootstrapConfigRef.Kind == "" || nodePool.BootstrapConfigRef.Name == "" {
			return fmt.Errorf("spec.nodePool.bootstrapConfigRef must have apiVersion, kind, and name")
		}
	}

	// For managed clusters, bootstrapConfigRef should NOT be set (managed by cloud provider)
	if nodePool.ProviderMode == "managed" {
		if nodePool.KubernetesVersion == "" {
			return fmt.Errorf("spec.nodePool.kubernetesVersion is required for managed clusters")
		}

		if nodePool.BootstrapConfigRef != nil {
			return fmt.Errorf("spec.nodePool.bootstrapConfigRef should not be set for managed clusters (managed by cloud provider)")
		}
	}

	return nil
}

// validateProviderFields validates provider-specific required fields
func validateProviderFields(nodePool *analyticsv1.NodePoolSpec) error {
	switch nodePool.Provider {
	case "azure":
		if nodePool.Azure == nil || nodePool.Azure.VMSize == "" {
			return fmt.Errorf("spec.nodePool.azure.vmSize is required when provider is azure")
		}
	case "aws":
		if nodePool.AWS == nil || nodePool.AWS.InstanceType == "" {
			return fmt.Errorf("spec.nodePool.aws.instanceType is required when provider is aws")
		}
	case "gcp":
		if nodePool.GCP == nil || nodePool.GCP.MachineType == "" {
			return fmt.Errorf("spec.nodePool.gcp.machineType is required when provider is gcp")
		}
	default:
		return fmt.Errorf("unsupported provider: %s (must be azure, aws, or gcp)", nodePool.Provider)
	}
	return nil
}

// isAutoscalerEnabled returns true if Cluster Autoscaler is managing this node pool
func isAutoscalerEnabled(nodePool *analyticsv1.NodePoolSpec) bool {
	// Default to true (assume autoscaler is enabled unless explicitly disabled)
	if nodePool.AutoscalerEnabled == nil {
		return true
	}
	return *nodePool.AutoscalerEnabled
}
