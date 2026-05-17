package controllers

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/pkg/metrics"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodePoolAdapter handles node pool provisioning via CAPI or Crossplane
type NodePoolAdapter struct {
	client client.Client
	log    logr.Logger
}

// NewNodePoolAdapter creates a new NodePoolAdapter
func NewNodePoolAdapter(cli client.Client, log logr.Logger) *NodePoolAdapter {
	return &NodePoolAdapter{
		client: cli,
		log:    log,
	}
}

// EnsureNodePool ensures a node pool exists for the XTrinode
// Supports Azure CAPZ (MachinePool), AWS CAPA (MachineDeployment), GCP CAPG (MachineDeployment)
func (n *NodePoolAdapter) EnsureNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	if xtrinode.Spec.NodePool == nil {
		// No node pool configured, skip
		return nil
	}

	n.log.Info("Ensuring node pool", "xtrinode", xtrinode.Name, "provider", xtrinode.Spec.NodePool.Provider)

	// Validate provider-specific fields are set
	if err := validateProviderFields(xtrinode.Spec.NodePool); err != nil {
		return err
	}

	// Determine provider mode (managed vs self-managed)
	providerMode := xtrinode.Spec.NodePool.ProviderMode
	if providerMode == "" {
		providerMode = "self-managed"
	}

	var err error
	provider := xtrinode.Spec.NodePool.Provider

	// Route to appropriate implementation based on provider and mode
	switch provider {
	case "azure":
		if providerMode == "managed" {
			err = n.ensureAzureManagedMachinePool(ctx, xtrinode)
		} else {
			err = n.ensureAzureMachinePool(ctx, xtrinode)
		}
	case "aws":
		if providerMode == "managed" {
			err = n.ensureAWSManagedMachinePool(ctx, xtrinode)
		} else {
			err = n.ensureAWSMachineDeployment(ctx, xtrinode)
		}
	case "gcp":
		if providerMode == "managed" {
			err = n.ensureGCPManagedMachinePool(ctx, xtrinode)
		} else {
			err = n.ensureGCPMachineDeployment(ctx, xtrinode)
		}
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	if err != nil {
		metrics.NodePoolProvisionFailed.WithLabelValues(xtrinode.Namespace, xtrinode.Name, provider).Inc()
		return err
	}

	// Note: NodePoolProvisioned metric is emitted by the controller (reconcileNodePoolBlocking)
	// only on first creation, not on every reconcile.

	// Check if nodes are ready (non-blocking - just log status)
	n.checkNodePoolReadiness(ctx, xtrinode)

	return nil
}

// checkNodePoolReadiness checks if node pool nodes are ready
// This is a best-effort check - doesn't block reconciliation
func (n *NodePoolAdapter) checkNodePoolReadiness(ctx context.Context, xtrinode *analyticsv1.XTrinode) {
	nodePool := xtrinode.Spec.NodePool
	if nodePool == nil {
		return
	}

	// Get node pool name
	nodePoolName := getNodePoolName(nodePool, xtrinode.Name)

	var resource *unstructured.Unstructured
	var resourceKind string

	// Check resource type based on provider and mode
	// Managed AWS/GCP use MachinePool; self-managed AWS/GCP use MachineDeployment; Azure always uses MachinePool
	useMachinePool := isMachinePoolProvider(nodePool)
	resource = &unstructured.Unstructured{}
	resource.SetGroupVersionKind(getMachineResourceGVK(useMachinePool))
	if useMachinePool {
		resourceKind = "MachinePool"
	} else {
		resourceKind = "MachineDeployment"
	}

	err := n.client.Get(ctx, client.ObjectKey{
		Name:      nodePoolName,
		Namespace: xtrinode.Namespace,
	}, resource)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			n.log.Info("Node pool resource not found yet, nodes may not be ready", "kind", resourceKind, "name", nodePoolName)
		}
		return
	}

	// Check status
	//nolint:errcheck // best-effort status check; errors are non-critical
	status, found, _ := unstructured.NestedMap(resource.Object, "status")
	if found {
		//nolint:errcheck // best-effort status field extraction; defaults to 0 on error
		readyReplicas, _, _ := unstructured.NestedInt64(status, "readyReplicas")
		//nolint:errcheck // best-effort status field extraction; defaults to 0 on error
		replicas, _, _ := unstructured.NestedInt64(status, "replicas")

		if readyReplicas > 0 {
			n.log.Info("Node pool has ready nodes",
				"kind", resourceKind,
				"name", nodePoolName,
				"readyReplicas", readyReplicas,
				"replicas", replicas)
		} else {
			n.log.Info("Node pool nodes not ready yet",
				"kind", resourceKind,
				"name", nodePoolName,
				"readyReplicas", readyReplicas,
				"replicas", replicas)
		}
	} else {
		n.log.Info("Node pool status not available yet", "kind", resourceKind, "name", nodePoolName)
	}
}

// getClusterName gets the cluster name from XTrinode spec, ConfigMap, environment variable, or defaults
func (n *NodePoolAdapter) getClusterName(ctx context.Context, xtrinode *analyticsv1.XTrinode) string {
	nodePool := xtrinode.Spec.NodePool
	if nodePool == nil {
		return config.NodePoolDefaultClusterName
	}

	// Priority 1: XTrinode spec ClusterName field
	if nodePool.ClusterName != "" {
		n.log.Info("Using cluster name from XTrinode spec", "clusterName", nodePool.ClusterName)
		return nodePool.ClusterName
	}

	// Priority 2: ConfigMap in operator namespace
	configMap := &corev1.ConfigMap{}
	configMapName := config.NodePoolConfigMapName
	configMapNamespace := config.OperatorDefaultNamespace

	err := n.client.Get(ctx, client.ObjectKey{
		Name:      configMapName,
		Namespace: configMapNamespace,
	}, configMap)
	if err == nil {
		if clusterName := configMap.Data[config.NodePoolConfigMapClusterNameKey]; clusterName != "" {
			n.log.Info("Using cluster name from ConfigMap", "clusterName", clusterName)
			return clusterName
		}
	}

	// Priority 3: Environment variable
	if clusterName := os.Getenv(config.NodePoolEnvClusterName); clusterName != "" {
		n.log.Info("Using cluster name from environment variable", "clusterName", clusterName)
		return clusterName
	}

	// Priority 4: Try to infer from cluster context (check for Cluster resource)
	// This is a best-effort approach - CAPI clusters usually have a Cluster resource
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "Cluster",
	})

	// Try common cluster names
	for _, name := range config.NodePoolCommonClusterNames {
		err := n.client.Get(ctx, client.ObjectKey{
			Name:      name,
			Namespace: xtrinode.Namespace,
		}, cluster)
		if err == nil {
			n.log.Info("Found Cluster resource, using its name", "clusterName", name)
			return name
		}
	}

	// Fallback: Use default but log warning
	n.log.Info("Using default cluster name - consider setting CLUSTER_NAME env var or ConfigMap", "clusterName", config.NodePoolDefaultClusterName)
	return config.NodePoolDefaultClusterName
}

// DeleteNodePool deletes the node pool for a XTrinode
func (n *NodePoolAdapter) DeleteNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	if xtrinode.Spec.NodePool == nil {
		return nil
	}

	nodePool := xtrinode.Spec.NodePool
	n.log.Info("Deleting node pool", "xtrinode", xtrinode.Name, "provider", nodePool.Provider, "mode", nodePool.ProviderMode)

	providerMode := nodePool.ProviderMode
	if providerMode == "" {
		providerMode = "self-managed"
	}

	switch nodePool.Provider {
	case "azure":
		if providerMode == "managed" {
			return n.deleteAzureManagedMachinePool(ctx, xtrinode)
		}
		return n.deleteAzureMachinePool(ctx, xtrinode)
	case "aws":
		if providerMode == "managed" {
			return n.deleteAWSManagedMachinePool(ctx, xtrinode)
		}
		return n.deleteAWSMachineDeployment(ctx, xtrinode)
	case "gcp":
		if providerMode == "managed" {
			return n.deleteGCPManagedMachinePool(ctx, xtrinode)
		}
		return n.deleteGCPMachineDeployment(ctx, xtrinode)
	default:
		return fmt.Errorf("unsupported provider: %s", nodePool.Provider)
	}
}

// ensureAzureMachinePool creates or updates an Azure CAPZ MachinePool
func (n *NodePoolAdapter) ensureAzureMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	nodePool := xtrinode.Spec.NodePool
	machinePoolName := getNodePoolName(nodePool, xtrinode.Name)
	defaults := getNodePoolDefaults(nodePool, xtrinode)
	clusterName := n.getClusterName(ctx, xtrinode)

	// Validate cluster name is explicitly set (not inferred)
	if nodePool.ClusterName == "" {
		n.log.Info("WARNING: clusterName not set in nodePool spec, using discovered/default cluster name", "clusterName", clusterName, "xtrinode", xtrinode.Name)
	}

	// Check if MachinePool already exists
	existingCheck := buildBaseMachinePool(machinePoolName, xtrinode.Namespace, xtrinode)
	resourceExists, err := checkResourceExists(n.client, ctx, existingCheck)
	if err != nil {
		return fmt.Errorf("failed to check if MachinePool exists: %w", err)
	}

	// Validate required fields for creation
	if err := validateNodePoolForCreation(nodePool, resourceExists); err != nil {
		return fmt.Errorf("nodepool validation failed: %w", err)
	}

	templateName := machinePoolName + config.NodePoolTemplateSuffix

	// Build AzureMachinePool (infrastructure.cluster.x-k8s.io/v1beta1) FIRST
	// This prevents transient errors from MachinePool referencing non-existent infraRef
	azureMachinePool := buildBaseInfrastructureTemplate(
		getInfrastructureTemplateGVK("azure", true),
		templateName,
		xtrinode.Namespace,
		xtrinode,
	)

	// Set Azure-specific spec
	vmSize := getAzureVMSize(nodePool)
	if err := unstructured.SetNestedField(azureMachinePool.Object, vmSize, "spec", "template", "vmSize"); err != nil {
		return fmt.Errorf("failed to set vmSize: %w", err)
	}
	if err := unstructured.SetNestedField(azureMachinePool.Object, defaults.DiskSizeGB, "spec", "template", "osDisk", "diskSizeGB"); err != nil {
		return fmt.Errorf("failed to set osDiskSizeGB: %w", err)
	}
	// Set OS disk type
	osDiskType := getAzureOSDiskType(nodePool)
	if err := unstructured.SetNestedField(azureMachinePool.Object, osDiskType, "spec", "template", "osDisk", "managedDisk", "storageAccountType"); err != nil {
		return fmt.Errorf("failed to set osDiskType: %w", err)
	}

	// Spot configuration
	if nodePool.Spot != nil && nodePool.Spot.Enabled {
		if err := unstructured.SetNestedField(azureMachinePool.Object, true, "spec", "template", "spotVMOptions", "enabled"); err != nil {
			return fmt.Errorf("failed to set spot enabled: %w", err)
		}
		if nodePool.Spot.MaxPrice != "" {
			if err := unstructured.SetNestedField(azureMachinePool.Object, nodePool.Spot.MaxPrice, "spec", "template", "spotVMOptions", "maxPrice"); err != nil {
				return fmt.Errorf("failed to set spot maxPrice: %w", err)
			}
		}
	}

	// Zones (provider-specific)
	if err := applyZonesToMachineTemplate(azureMachinePool, nodePool.Zones, "azure"); err != nil {
		return err
	}

	// Resource tags (cloud provider tags)
	if err := applyLabelsToUnstructured(azureMachinePool, nodePool.ResourceTags, []string{"spec", "template", "additionalTags"}); err != nil {
		return err
	}

	// Apply AzureMachinePool FIRST
	if err := applyUnstructured(n.client, ctx, azureMachinePool); err != nil {
		return fmt.Errorf("failed to create/update AzureMachinePool: %w", err)
	}

	// Now build and apply MachinePool (cluster.x-k8s.io/v1beta1)
	machinePool := buildBaseMachinePool(machinePoolName, xtrinode.Namespace, xtrinode)

	// Set spec (only set replicas on initial creation, using resourceExists from above)
	setOnCreate := !resourceExists
	if err := setMachinePoolSpec(machinePool, clusterName, templateName, defaults.MinNodes, "AzureMachinePool", setOnCreate, nodePool.KubernetesVersion, nodePool.BootstrapConfigRef); err != nil {
		return err
	}

	// Apply failureDomain for zone placement (CAPI standard)
	if err := applyFailureDomainToMachine(machinePool, nodePool.Zones); err != nil {
		return err
	}

	// Cluster Autoscaler annotations (always update these)
	machinePool.SetAnnotations(buildAutoscalerAnnotations(defaults.MinNodes, defaults.MaxNodes))

	// Apply MachinePool
	if err := createOrApplyUnstructured(n.client, ctx, machinePool, resourceExists); err != nil {
		return fmt.Errorf("failed to create/update MachinePool: %w", err)
	}

	n.log.Info("Ensured Azure MachinePool", "name", machinePoolName, "minNodes", defaults.MinNodes, "maxNodes", defaults.MaxNodes)
	return nil
}

// ensureAWSMachineDeployment creates or updates an AWS CAPA MachineDeployment
func (n *NodePoolAdapter) ensureAWSMachineDeployment(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	nodePool := xtrinode.Spec.NodePool
	machineDeploymentName := getNodePoolName(nodePool, xtrinode.Name)
	defaults := getNodePoolDefaults(nodePool, xtrinode)
	clusterName := n.getClusterName(ctx, xtrinode)

	// Validate cluster name is explicitly set (not inferred)
	if nodePool.ClusterName == "" {
		n.log.Info("WARNING: clusterName not set in nodePool spec, using discovered/default cluster name", "clusterName", clusterName, "xtrinode", xtrinode.Name)
	}

	// Check if MachineDeployment already exists
	existingCheck := buildBaseMachineDeployment(machineDeploymentName, xtrinode.Namespace, xtrinode)
	resourceExists, err := checkResourceExists(n.client, ctx, existingCheck)
	if err != nil {
		return fmt.Errorf("failed to check if MachineDeployment exists: %w", err)
	}

	// Validate required fields for creation
	if err := validateNodePoolForCreation(nodePool, resourceExists); err != nil {
		return fmt.Errorf("nodepool validation failed: %w", err)
	}

	templateName := machineDeploymentName + config.NodePoolTemplateSuffix

	// Build AWSMachineTemplate (infrastructure.cluster.x-k8s.io/v1beta1)
	awsMachineTemplate := buildBaseInfrastructureTemplate(
		getInfrastructureTemplateGVK("aws", false),
		templateName,
		xtrinode.Namespace,
		xtrinode,
	)

	// Set AWS-specific spec
	instanceType := getAWSInstanceType(nodePool)
	if err := unstructured.SetNestedField(awsMachineTemplate.Object, instanceType, "spec", "template", "spec", "instanceType"); err != nil {
		return fmt.Errorf("failed to set instanceType: %w", err)
	}

	// Root volume configuration
	volumeType := getAWSVolumeType(nodePool)
	rootVolume := map[string]interface{}{
		"size": defaults.DiskSizeGB,
		"type": volumeType,
	}
	if err := unstructured.SetNestedMap(awsMachineTemplate.Object, rootVolume, "spec", "template", "spec", "rootVolume"); err != nil {
		return fmt.Errorf("failed to set rootVolume: %w", err)
	}

	// Spot configuration
	if nodePool.Spot != nil && nodePool.Spot.Enabled {
		spotMarketOptions := map[string]interface{}{
			"marketType": "spot",
		}
		if nodePool.Spot.MaxPrice != "" {
			spotMarketOptions["maxPrice"] = nodePool.Spot.MaxPrice
		}
		if err := unstructured.SetNestedMap(awsMachineTemplate.Object, spotMarketOptions, "spec", "template", "spec", "spotMarketOptions"); err != nil {
			return fmt.Errorf("failed to set spotMarketOptions: %w", err)
		}
	}

	// Availability zones (provider-specific)
	if err := applyZonesToMachineTemplate(awsMachineTemplate, nodePool.Zones, "aws"); err != nil {
		return err
	}

	// Resource tags (AWS resource tags, not Kubernetes node labels)
	if err := applyLabelsToUnstructured(awsMachineTemplate, nodePool.ResourceTags, []string{"spec", "template", "spec", "additionalTags"}); err != nil {
		return err
	}

	// Apply AWSMachineTemplate
	if err := applyUnstructured(n.client, ctx, awsMachineTemplate); err != nil {
		return fmt.Errorf("failed to create/update AWSMachineTemplate: %w", err)
	}

	// Build MachineDeployment (cluster.x-k8s.io/v1beta1)
	machineDeployment := buildBaseMachineDeployment(machineDeploymentName, xtrinode.Namespace, xtrinode)

	// Set spec (only set replicas on initial creation)
	setOnCreate := !resourceExists
	if err := setMachineDeploymentSpec(machineDeployment, clusterName, templateName, defaults.MinNodes, "AWSMachineTemplate", setOnCreate, nodePool.KubernetesVersion, nodePool.BootstrapConfigRef, "aws"); err != nil {
		return err
	}

	// Apply failureDomain for zone placement (CAPI standard)
	if err := applyFailureDomainToMachine(machineDeployment, nodePool.Zones); err != nil {
		return err
	}

	// Cluster Autoscaler annotations (always update these)
	machineDeployment.SetAnnotations(buildAutoscalerAnnotations(defaults.MinNodes, defaults.MaxNodes))

	// Apply MachineDeployment
	if err := createOrApplyUnstructured(n.client, ctx, machineDeployment, resourceExists); err != nil {
		return fmt.Errorf("failed to create/update MachineDeployment: %w", err)
	}

	n.log.Info("Ensured AWS MachineDeployment", "name", machineDeploymentName, "minNodes", defaults.MinNodes, "maxNodes", defaults.MaxNodes)
	return nil
}

// ensureGCPMachineDeployment creates or updates a GCP CAPG MachineDeployment
func (n *NodePoolAdapter) ensureGCPMachineDeployment(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	nodePool := xtrinode.Spec.NodePool
	machineDeploymentName := getNodePoolName(nodePool, xtrinode.Name)
	defaults := getNodePoolDefaults(nodePool, xtrinode)
	clusterName := n.getClusterName(ctx, xtrinode)

	// Validate cluster name is explicitly set (not inferred)
	if nodePool.ClusterName == "" {
		n.log.Info("WARNING: clusterName not set in nodePool spec, using discovered/default cluster name", "clusterName", clusterName, "xtrinode", xtrinode.Name)
	}

	// Check if MachineDeployment already exists
	existingCheck := buildBaseMachineDeployment(machineDeploymentName, xtrinode.Namespace, xtrinode)
	resourceExists, err := checkResourceExists(n.client, ctx, existingCheck)
	if err != nil {
		return fmt.Errorf("failed to check if MachineDeployment exists: %w", err)
	}

	// Validate required fields for creation
	if err := validateNodePoolForCreation(nodePool, resourceExists); err != nil {
		return fmt.Errorf("nodepool validation failed: %w", err)
	}

	templateName := machineDeploymentName + config.NodePoolTemplateSuffix

	// Build GCPMachineTemplate (infrastructure.cluster.x-k8s.io/v1beta1)
	gcpMachineTemplate := buildBaseInfrastructureTemplate(
		getInfrastructureTemplateGVK("gcp", false),
		templateName,
		xtrinode.Namespace,
		xtrinode,
	)

	// Set GCP-specific spec
	machineType := getGCPMachineType(nodePool)
	if err := unstructured.SetNestedField(gcpMachineTemplate.Object, machineType, "spec", "template", "spec", "machineType"); err != nil {
		return fmt.Errorf("failed to set machineType: %w", err)
	}

	// Root disk configuration
	diskType := getGCPDiskType(nodePool)
	disks := []interface{}{
		map[string]interface{}{
			"sizeGb": defaults.DiskSizeGB,
			"type":   diskType,
			"boot":   true,
		},
	}
	if err := unstructured.SetNestedSlice(gcpMachineTemplate.Object, disks, "spec", "template", "spec", "disks"); err != nil {
		return fmt.Errorf("failed to set disks: %w", err)
	}

	// Spot configuration (preemptible)
	if nodePool.Spot != nil && nodePool.Spot.Enabled {
		if err := unstructured.SetNestedField(gcpMachineTemplate.Object, true, "spec", "template", "spec", "preemptible"); err != nil {
			return fmt.Errorf("failed to set preemptible: %w", err)
		}
	}

	// Zones (provider-specific)
	if err := applyZonesToMachineTemplate(gcpMachineTemplate, nodePool.Zones, "gcp"); err != nil {
		return err
	}

	// Resource tags (GCP resource labels, not Kubernetes node labels)
	if err := applyLabelsToUnstructured(gcpMachineTemplate, nodePool.ResourceTags, []string{"spec", "template", "spec", "resourceLabels"}); err != nil {
		return err
	}

	// Apply GCPMachineTemplate
	if err := applyUnstructured(n.client, ctx, gcpMachineTemplate); err != nil {
		return fmt.Errorf("failed to create/update GCPMachineTemplate: %w", err)
	}

	// Build MachineDeployment (cluster.x-k8s.io/v1beta1)
	machineDeployment := buildBaseMachineDeployment(machineDeploymentName, xtrinode.Namespace, xtrinode)

	// Set spec (only set replicas on initial creation)
	setOnCreate := !resourceExists
	if err := setMachineDeploymentSpec(machineDeployment, clusterName, templateName, defaults.MinNodes, "GCPMachineTemplate", setOnCreate, nodePool.KubernetesVersion, nodePool.BootstrapConfigRef, "gcp"); err != nil {
		return err
	}

	// Apply failureDomain for zone placement (CAPI standard)
	if err := applyFailureDomainToMachine(machineDeployment, nodePool.Zones); err != nil {
		return err
	}

	// Cluster Autoscaler annotations (always update these)
	machineDeployment.SetAnnotations(buildAutoscalerAnnotations(defaults.MinNodes, defaults.MaxNodes))

	// Apply MachineDeployment
	if err := createOrApplyUnstructured(n.client, ctx, machineDeployment, resourceExists); err != nil {
		return fmt.Errorf("failed to create/update MachineDeployment: %w", err)
	}

	n.log.Info("Ensured GCP MachineDeployment", "name", machineDeploymentName, "minNodes", defaults.MinNodes, "maxNodes", defaults.MaxNodes)
	return nil
}

// deleteAzureMachinePool deletes the Azure self-managed MachinePool
func (n *NodePoolAdapter) deleteAzureMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return deleteNodePoolResources(n.client, ctx, xtrinode, n.log, "azure", true)
}

// deleteAWSMachineDeployment deletes the AWS self-managed MachineDeployment
func (n *NodePoolAdapter) deleteAWSMachineDeployment(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return deleteNodePoolResources(n.client, ctx, xtrinode, n.log, "aws", false)
}

// deleteGCPMachineDeployment deletes the GCP self-managed MachineDeployment
func (n *NodePoolAdapter) deleteGCPMachineDeployment(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return deleteNodePoolResources(n.client, ctx, xtrinode, n.log, "gcp", false)
}

// deleteAzureManagedMachinePool deletes the Azure managed MachinePool (AKS node pool)
func (n *NodePoolAdapter) deleteAzureManagedMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return deleteNodePoolManagedResources(n.client, ctx, xtrinode, n.log, "azure")
}

// deleteAWSManagedMachinePool deletes the AWS managed MachinePool (EKS node group)
func (n *NodePoolAdapter) deleteAWSManagedMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return deleteNodePoolManagedResources(n.client, ctx, xtrinode, n.log, "aws")
}

// deleteGCPManagedMachinePool deletes the GCP managed MachinePool (GKE node pool)
func (n *NodePoolAdapter) deleteGCPManagedMachinePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return deleteNodePoolManagedResources(n.client, ctx, xtrinode, n.log, "gcp")
}

// ScaleNodePoolMinNodes scales the node pool minNodes to the specified value
func (n *NodePoolAdapter) ScaleNodePoolMinNodes(ctx context.Context, xtrinode *analyticsv1.XTrinode, minNodes int32) error {
	if xtrinode.Spec.NodePool == nil {
		return nil // No node pool configured, nothing to scale
	}

	nodePool := xtrinode.Spec.NodePool
	nodePoolName := getNodePoolName(nodePool, xtrinode.Name)
	defaults := getNodePoolDefaults(nodePool, xtrinode)

	// Get current maxNodes (preserve it)
	maxNodes := defaults.MaxNodes

	n.log.Info("Scaling node pool minNodes", "xtrinode", xtrinode.Name, "minNodes", minNodes, "maxNodes", maxNodes)

	// Determine correct CAPI resource kind based on provider and mode
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(getMachineResourceGVK(isMachinePoolProvider(nodePool)))

	// Use retry logic to handle conflicts with other controllers (CA, CAPI)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy of resource
		err := n.client.Get(ctx, client.ObjectKey{
			Name:      nodePoolName,
			Namespace: xtrinode.Namespace,
		}, resource)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// Resource doesn't exist yet, nothing to scale
				n.log.Info("Node pool resource not found, cannot scale", "name", nodePoolName)
				return nil
			}
			return fmt.Errorf("failed to get node pool resource: %w", err)
		}

		// Capture base BEFORE mutations so MergeFrom computes a real diff
		base := resource.DeepCopy()

		// Check if autoscaler is enabled
		autoscalerEnabled := isAutoscalerEnabled(nodePool)

		// If autoscaler is enabled, only update annotations (let CA manage replicas)
		// If autoscaler is disabled, update both replicas and annotations
		if !autoscalerEnabled {
			// Update replicas only when autoscaler is disabled
			if err := unstructured.SetNestedField(resource.Object, int64(minNodes), "spec", "replicas"); err != nil {
				return fmt.Errorf("failed to set replicas: %w", err)
			}
		}

		// Always update Cluster Autoscaler annotations (this is the primary control mechanism)
		annotations := resource.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[config.NodePoolAutoscalerMinSizeAnnotation] = fmt.Sprintf("%d", minNodes)
		annotations[config.NodePoolAutoscalerMaxSizeAnnotation] = fmt.Sprintf("%d", maxNodes)
		resource.SetAnnotations(annotations)

		// Use Patch instead of Update to minimize conflicts
		patch := client.MergeFrom(base)
		if err := n.client.Patch(ctx, resource, patch); err != nil {
			return fmt.Errorf("failed to patch node pool: %w", err)
		}

		n.log.Info("Scaled node pool minNodes", "xtrinode", xtrinode.Name, "minNodes", minNodes, "autoscalerEnabled", autoscalerEnabled)
		return nil
	})
}
