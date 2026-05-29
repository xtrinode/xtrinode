package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodePoolSpec defines node pool configuration
const (
	// NodePoolDeletionPolicyDelete deletes provider node-pool resources with the XTrinode.
	NodePoolDeletionPolicyDelete = "Delete"
	// NodePoolDeletionPolicyRetain leaves provider node-pool resources in place during finalizer cleanup.
	NodePoolDeletionPolicyRetain = "Retain"
	// NodePoolDeletionPolicyScaleToZero scales provider node-pool resources to zero and then retains them.
	NodePoolDeletionPolicyScaleToZero = "ScaleToZero"
)

type NodePoolSpec struct {
	// Name of the node pool
	Name string `json:"name"`

	// Provider: azure|aws|gcp
	Provider string `json:"provider"`

	// MinNodes is the minimum number of nodes
	MinNodes *int32 `json:"minNodes,omitempty"`

	// MaxNodes is the maximum number of nodes
	MaxNodes *int32 `json:"maxNodes,omitempty"`

	// Zones for the node pool
	Zones []string `json:"zones,omitempty"`

	// OSDiskGB is the OS disk size in GB (common across all providers)
	OSDiskGB *int32 `json:"osDiskGB,omitempty"`

	// Spot configuration (common across all providers)
	Spot *SpotSpec `json:"spot,omitempty"`

	// NodeLabels are Kubernetes node labels applied to managed node pools for scheduling.
	// For self-managed clusters, configure nodeRegistration.kubeletExtraArgs.node-labels
	// in the referenced bootstrap template instead.
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// SchedulePods binds coordinator and worker placement to this managed node pool.
	// When true, XTrinode applies a stable node-pool label to managed pools and
	// adds the matching nodeSelector to the resolved runtime placement.
	// +optional
	SchedulePods bool `json:"schedulePods,omitempty"`

	// NodeTaints are Kubernetes node taints applied to managed node pools for scheduling.
	// For self-managed clusters, configure nodeRegistration.taints in the referenced
	// bootstrap template instead.
	// +optional
	NodeTaints []corev1.Taint `json:"nodeTaints,omitempty"`

	// ResourceTags are cloud provider resource tags/labels (AWS tags, GCP labels, Azure tags)
	// These are metadata on the cloud resources, not Kubernetes node labels
	// +optional
	ResourceTags map[string]string `json:"resourceTags,omitempty"`

	// Prewarm configuration
	Prewarm *PrewarmSpec `json:"prewarm,omitempty"`

	// ProvisioningTimeout is the maximum time to wait for node pool provisioning before giving up
	// If not specified, uses operator default (30 minutes)
	// +optional
	ProvisioningTimeout *metav1.Duration `json:"provisioningTimeout,omitempty"`

	// ResourceNotFoundRequeueInterval is the requeue interval when node pool resource is not found yet
	// If not specified, uses operator default (10 seconds)
	// +optional
	ResourceNotFoundRequeueInterval *metav1.Duration `json:"resourceNotFoundRequeueInterval,omitempty"`

	// StatusNotAvailableRequeueInterval is the requeue interval when node pool status is not available yet
	// If not specified, uses operator default (10 seconds)
	// +optional
	StatusNotAvailableRequeueInterval *metav1.Duration `json:"statusNotAvailableRequeueInterval,omitempty"`

	// NoNodesReadyRequeueInterval is the requeue interval when no nodes are ready yet
	// If not specified, uses operator default (30 seconds)
	// +optional
	NoNodesReadyRequeueInterval *metav1.Duration `json:"noNodesReadyRequeueInterval,omitempty"`

	// NodesReadyRequeueInterval is the requeue interval when some nodes are ready but waiting for more
	// If not specified, uses operator default (10 seconds)
	// +optional
	NodesReadyRequeueInterval *metav1.Duration `json:"nodesReadyRequeueInterval,omitempty"`

	// ErrorRequeueInterval is the requeue interval when node pool provisioning fails
	// If not specified, uses operator default (60 seconds)
	// +optional
	ErrorRequeueInterval *metav1.Duration `json:"errorRequeueInterval,omitempty"`

	// MinRequiredReplicasWhenMinNodesZero is the minimum number of ready replicas required when minNodes=0
	// This ensures at least one node is available for Cluster Autoscaler to work
	// If not specified, uses operator default (1)
	// +optional
	MinRequiredReplicasWhenMinNodesZero *int32 `json:"minRequiredReplicasWhenMinNodesZero,omitempty"`

	// ClusterName is the CAPI cluster name to use for node pool provisioning
	// REQUIRED in production - operator will error if not set and discovery fails
	// If not specified, operator will attempt discovery from ConfigMap, environment variable, or default
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// KubernetesVersion is the Kubernetes version for the nodes (e.g., "v1.28.0")
	// Required for CAPI MachineDeployment/MachinePool creation
	// If not specified, operator will error - cannot create new node pools without version
	// +optional
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`

	// BootstrapConfigRef references the bootstrap config template for self-managed clusters
	// Required for creating new MachineDeployments in self-managed CAPI clusters
	// Example: {apiVersion: "bootstrap.cluster.x-k8s.io/v1beta1", kind: "KubeadmConfigTemplate", name: "worker-bootstrap"}
	// Not needed for managed clusters (EKS/AKS/GKE managed node groups)
	// +optional
	BootstrapConfigRef *corev1.ObjectReference `json:"bootstrapConfigRef,omitempty"`

	// ProviderMode specifies whether this is a managed or self-managed cluster
	// Options: "managed" (EKS/AKS/GKE managed node groups), "self-managed" (CAPI-managed)
	// If not specified, operator will attempt to detect based on existing resources
	// Recommended to set explicitly to avoid ambiguity
	// +kubebuilder:validation:Enum=managed;self-managed
	// +optional
	ProviderMode string `json:"providerMode,omitempty"`

	// AutoscalerEnabled indicates whether Cluster Autoscaler is managing this node pool
	// If true, operator will NOT set spec.replicas on updates (only on creation)
	// If false, operator will manage replicas directly
	// Default: true (assume autoscaler is enabled)
	// +optional
	AutoscalerEnabled *bool `json:"autoscalerEnabled,omitempty"`

	// ScaleDownOnSuspend controls whether node pool should scale down to 0 when XTrinode is suspended
	// If true, minNodes will be set to 0 on suspend and restored on resume
	// If false, node pool minNodes remains unchanged during suspend/resume
	// Default: true (scale down on suspend for cost savings)
	// +optional
	ScaleDownOnSuspend *bool `json:"scaleDownOnSuspend,omitempty"`

	// DeletionPolicy controls what happens to managed provider node-pool resources
	// when this XTrinode is deleted.
	// Defaults to Delete when omitted.
	// +kubebuilder:validation:Enum=Delete;Retain;ScaleToZero
	// +optional
	DeletionPolicy string `json:"deletionPolicy,omitempty"`

	// Azure-specific configuration
	// +optional
	Azure *AzureNodePoolSpec `json:"azure,omitempty"`

	// AWS-specific configuration
	// +optional
	AWS *AWSNodePoolSpec `json:"aws,omitempty"`

	// GCP-specific configuration
	// +optional
	GCP *GCPNodePoolSpec `json:"gcp,omitempty"`
}

// AzureNodePoolSpec defines Azure-specific node pool configuration
type AzureNodePoolSpec struct {
	// VMSize is the Azure VM size (e.g., Standard_D8as_v5)
	// Required when provider is azure
	VMSize string `json:"vmSize"`

	// OSDiskType is the Azure OS disk type (e.g., Premium_LRS, Standard_LRS, PremiumV2_LRS)
	// If not specified, uses operator default (Premium_LRS)
	// +optional
	OSDiskType string `json:"osDiskType,omitempty"`
}

// AWSNodePoolSpec defines AWS-specific node pool configuration
type AWSNodePoolSpec struct {
	// InstanceType is the AWS EC2 instance type (e.g., m5.xlarge)
	// Required when provider is aws
	InstanceType string `json:"instanceType"`

	// VolumeType is the AWS EBS volume type (e.g., gp3, gp2, io1)
	// If not specified, uses operator default (gp3)
	// +optional
	VolumeType string `json:"volumeType,omitempty"`
}

// GCPNodePoolSpec defines GCP-specific node pool configuration
type GCPNodePoolSpec struct {
	// MachineType is the GCP machine type (e.g., n1-standard-4)
	// Required when provider is gcp
	MachineType string `json:"machineType"`

	// DiskType is the GCP disk type (e.g., pd-standard, pd-ssd, pd-balanced)
	// If not specified, uses operator default (pd-standard)
	// +optional
	DiskType string `json:"diskType,omitempty"`
}

// OperatorNodePoolDefaultsSpec defines operator-level defaults for node pools
// These defaults apply cluster-wide unless overridden per-XTrinode
type OperatorNodePoolDefaultsSpec struct {
	// DefaultMinNodes is the default minimum number of nodes for all XTrinodes
	// If not specified, uses operator default (0)
	// +optional
	DefaultMinNodes *int32 `json:"defaultMinNodes,omitempty"`

	// DefaultMaxNodes is the default maximum number of nodes for all XTrinodes
	// If not specified, uses operator default (10)
	// +optional
	DefaultMaxNodes *int32 `json:"defaultMaxNodes,omitempty"`

	// DefaultOSDiskGB is the default OS disk size in GB for all XTrinodes
	// If not specified, uses operator default (128)
	// +optional
	DefaultOSDiskGB *int32 `json:"defaultOSDiskGB,omitempty"`
}

// SpotSpec defines spot instance configuration
type SpotSpec struct {
	// Enabled indicates if spot instances are enabled
	Enabled bool `json:"enabled,omitempty"`

	// MaxPrice is the maximum price for spot instances
	MaxPrice string `json:"maxPrice,omitempty"`
}

// PrewarmSpec defines pre-warming configuration
type PrewarmSpec struct {
	// Nodes is the number of nodes to pre-warm
	Nodes *int32 `json:"nodes,omitempty"`

	// TTL is how long to keep the pre-warm floor
	TTL *metav1.Duration `json:"ttl,omitempty"`
}
