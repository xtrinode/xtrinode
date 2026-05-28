// +kubebuilder:object:generate=true
package v1

//go:generate controller-gen object paths=./...

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// XTrinodeSpec defines the desired state of a XTrinode runtime
type XTrinodeSpec struct {
	// Size preset: xs|s|m|l|xl
	Size string `json:"size"`

	// MaxWorkers is the fixed worker count when autoscaling is disabled and the maximum when autoscaling is enabled.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=500
	MaxWorkers *int32 `json:"maxWorkers,omitempty"`

	// MinWorkers is the minimum number of worker replicas (normally 0)
	// +kubebuilder:validation:Minimum=0
	MinWorkers *int32 `json:"minWorkers,omitempty"`

	// Suspended indicates if the runtime is suspended
	Suspended bool `json:"suspended,omitempty"`

	// AutoSuspendAfter is the idle duration before auto-suspend
	// +kubebuilder:default="5m"
	AutoSuspendAfter *metav1.Duration `json:"autoSuspendAfter,omitempty"`

	// WakeMinWorkers is the number of workers to pre-warm on resume
	WakeMinWorkers *int32 `json:"wakeMinWorkers,omitempty"`

	// WakeTTL is how long to keep the pre-warm floor after resume
	WakeTTL *metav1.Duration `json:"wakeTTL,omitempty"`

	// Resources overrides the selected size preset's pod resources.
	// If omitted, resources come from spec.size.
	Resources *RuntimeResourcesSpec `json:"resources,omitempty"`

	// Placement configures scheduler constraints for coordinator and worker pods.
	// The top-level fields apply to both roles unless role-specific fields override them.
	Placement *PlacementSpec `json:"placement,omitempty"`

	// NodePool configuration
	NodePool *NodePoolSpec `json:"nodePool,omitempty"`

	// Routing configuration
	Routing *RoutingSpec `json:"routing,omitempty"`

	// CatalogSelector selects XTrinodeCatalogs to use for this XTrinode
	// Uses label selector to find matching XTrinodeCatalog CRDs
	// Example: {matchLabels: {team: "data-eng"}}
	// If not specified, no catalogs are mounted
	CatalogSelector *metav1.LabelSelector `json:"catalogSelector,omitempty"`

	// ResourceGroupsProfile is the ConfigMap name for resource groups (team-provided)
	ResourceGroupsProfile string `json:"resourceGroupsProfile,omitempty"`

	// CustomConfigMaps is a list of ConfigMap names to mount for coordinator customization
	// Teams can provide ConfigMaps with custom Trino configuration
	// These ConfigMaps will be mounted as volumes in the coordinator pod
	CustomConfigMaps []string `json:"customConfigMaps,omitempty"`

	// Limits for sessions and resource groups
	Limits *LimitsSpec `json:"limits,omitempty"`

	// ValuesOverlay is a privileged escape hatch for chart-shaped Trino customization.
	// Native resource builders read selected keys and may turn them into pod security,
	// volume, image, environment, networking, ConfigMap, Secret, and Trino settings.
	// Do not expose this field as tenant-safe input without additional admission policy.
	// Uses apiextensionsv1.JSON to support arbitrary JSON structures in CRDs.
	ValuesOverlay *apiextensionsv1.JSON `json:"valuesOverlay,omitempty"`

	// TrinoControlAuth configures credentials used by the operator and worker lifecycle hooks
	// for internal Trino control-plane HTTP APIs such as /v1/query and /v1/info/state.
	TrinoControlAuth *TrinoControlAuthSpec `json:"trinoControlAuth,omitempty"`

	// FaultTolerantExecution configures Trino fault-tolerant execution.
	FaultTolerantExecution *FaultTolerantExecutionSpec `json:"faultTolerantExecution,omitempty"`

	// KEDA configuration for autoscaling
	KEDA *KEDASpec `json:"keda,omitempty"`

	// TLS configuration for secure communication
	TLS *TLSSpec `json:"tls,omitempty"`

	// HelmChartConfig contains additional Helm chart configuration
	// These fields map directly to Trino Helm chart values
	HelmChartConfig *HelmChartConfigSpec `json:"helmChartConfig,omitempty"`

	// OperatorNodePoolDefaults allows configuring operator-level defaults for node pools
	// These defaults apply to all XTrinodes in the cluster unless overridden per-XTrinode
	// If not specified, uses operator defaults from config.go
	// +optional
	OperatorNodePoolDefaults *OperatorNodePoolDefaultsSpec `json:"operatorNodePoolDefaults,omitempty"`

	// RolloutPolicy controls deployment rollout behavior (revision history limit, rolling update strategy)
	// This is rollout mechanics, not versioning - versioning is handled at XTrinode level via revision
	// If not specified, uses operator defaults
	// +optional
	RolloutPolicy *RolloutPolicySpec `json:"rolloutPolicy,omitempty"`
}

// FaultTolerantExecutionSpec configures Trino retry execution behavior.
type FaultTolerantExecutionSpec struct {
	// RetryPolicy is rendered as Trino retry-policy.
	// Supported values: TASK, QUERY. Defaults to TASK.
	RetryPolicy string `json:"retryPolicy,omitempty"`

	// ExchangeManager configures exchange-manager.properties.
	// If omitted, XTrinode renders a filesystem exchange manager using /tmp/trino-exchange.
	ExchangeManager *ExchangeManagerSpec `json:"exchangeManager,omitempty"`
}

// ExchangeManagerSpec configures Trino exchange-manager.properties.
type ExchangeManagerSpec struct {
	// Enabled controls whether exchange-manager.properties is rendered.
	// Defaults to true. TASK retry policy requires it to remain enabled.
	Enabled *bool `json:"enabled,omitempty"`

	// Name is rendered as exchange-manager.name. Defaults to filesystem.
	Name string `json:"name,omitempty"`

	// BaseDirectories is rendered as exchange.base-directories.
	// Values are joined with commas, matching Trino's expected property format.
	BaseDirectories []string `json:"baseDirectories,omitempty"`

	// Properties contains additional exchange manager properties.
	// Use dedicated fields for exchange-manager.name and exchange.base-directories.
	Properties map[string]string `json:"properties,omitempty"`
}

// TLSSpec defines TLS configuration for Trino
type TLSSpec struct {
	// ServerSecretClass is the SecretClass name for server TLS certificates (client-to-coordinator)
	// SecretClass provides certificates via external-secrets operator or cert-manager
	// Secret will be mounted at /etc/trino/tls/server
	ServerSecretClass string `json:"serverSecretClass,omitempty"`

	// InternalSecretClass is the SecretClass name for internal TLS certificates (coordinator-to-worker)
	// SecretClass provides certificates via external-secrets operator or cert-manager
	// Secret will be mounted at /etc/trino/tls/internal
	InternalSecretClass string `json:"internalSecretClass,omitempty"`
}

// KEDASpec defines KEDA autoscaling configuration
type KEDASpec struct {
	// Enabled enables KEDA autoscaling
	// If false or nil, KEDA ScaledObject will not be created and workers use a fixed replica count.
	// If true but no scaler/metric/query/endpoint is configured, workers still use fixed replicas.
	// Default: false
	Enabled *bool `json:"enabled,omitempty"`

	// ScalerType defines how KEDA fetches metrics
	// Options: "prometheus" (recommended with gateway query metrics), "http" (direct endpoint mode)
	// Default when KEDA is active: "prometheus" if prometheusQuery/prometheusServer is set, otherwise "http"
	ScalerType string `json:"scalerType,omitempty"`

	// ScalingMetric defines what metric to use for scaling
	// Options: "query" (gateway inflight-query based for Prometheus, coordinator query-state based for HTTP), "memory" (memory-based), "cpu" (CPU-based)
	// Default when KEDA is active: "query" for prometheus, otherwise "memory"
	ScalingMetric string `json:"scalingMetric,omitempty"`

	// Threshold is the scaling threshold
	// For query-based: queries per worker (default: "1")
	// For memory-based: memory usage percentage per worker (default: "80")
	// For CPU-based: CPU usage percentage per worker (default: "80")
	Threshold *string `json:"threshold,omitempty"`

	// PrometheusServer is the Prometheus server address (only used if scalerType="prometheus")
	// Default: operator default or "http://prometheus-operated.monitoring.svc.cluster.local:9090"
	PrometheusServer *string `json:"prometheusServer,omitempty"`

	// PrometheusQuery is a custom Prometheus query (only used if scalerType="prometheus")
	// Overrides the built-in default for the selected scalingMetric.
	// Placeholders: {releaseName}, {namespace}, {xtrinodeName}
	// Default query scaling uses xtrinode_gateway_inflight_queries{namespace="{namespace}",xtrinode="{xtrinodeName}"}
	PrometheusQuery *string `json:"prometheusQuery,omitempty"`

	// HTTPEndpoint is the HTTP endpoint to query for metrics (only used if scalerType="http")
	// Options:
	// - "coordinator" (default): Query Trino coordinator /v1/query for scalingMetric="query", otherwise /metrics
	// - "jmx": Query JMX exporter /metrics endpoint (requires JMX exporter enabled)
	// - Custom URL: Full URL to metrics endpoint
	// Default: "coordinator" - queries http://trino-{name}.{namespace}.svc:8080
	HTTPEndpoint *string `json:"httpEndpoint,omitempty"`

	// HTTPValueLocation is the JSONPath or regex to extract metric value from HTTP response
	// For Prometheus format metrics: Use regex like "trino_query_queued.*? ([0-9.]+)"
	// For JSON format: Use JSONPath like "$.queries.queued"
	// Default: Auto-detected based on metric type and endpoint
	HTTPValueLocation *string `json:"httpValueLocation,omitempty"`

	// ScaleDownCooldown is the cooldown period before scaling down
	// Default: KEDA default (300s)
	ScaleDownCooldown *metav1.Duration `json:"scaleDownCooldown,omitempty"`

	// ScaleUpCooldown is the cooldown period before scaling up
	// Default: KEDA default (0s)
	ScaleUpCooldown *metav1.Duration `json:"scaleUpCooldown,omitempty"`

	// JMXExporter enables JMX exporter sidecar for additional metrics
	// If enabled, JMX exporter will be deployed as sidecar container
	JMXExporter *JMXExporterSpec `json:"jmxExporter,omitempty"`
}

// JMXExporterSpec defines JMX exporter sidecar configuration
type JMXExporterSpec struct {
	// Enabled enables JMX exporter sidecar
	Enabled bool `json:"enabled,omitempty"`

	// Image is the JMX exporter image (default: "bitnami/jmx-exporter@sha256:7c0014b7e1d736faec9760a89727389ba1ba7ad920c764417167abecfb7fd032")
	Image string `json:"image,omitempty"`

	// Port is the port JMX exporter listens on (default: 5556)
	Port *int32 `json:"port,omitempty"`

	// ConfigMap is the ConfigMap name containing JMX exporter configuration
	// If not specified, uses default configuration
	ConfigMap string `json:"configMap,omitempty"`
}

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

// RolloutPolicySpec defines deployment rollout policy (not versioning - versioning is at XTrinode level)
type RolloutPolicySpec struct {
	// RevisionHistoryLimit is the number of old ReplicaSets to retain
	// This is rollout mechanics, not versioning - XTrinode revision is the version
	// If not specified, uses operator default (10)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit,omitempty"`

	// RollingUpdateStrategy configures the rolling update strategy
	// If not specified, uses Kubernetes defaults (maxSurge=25%, maxUnavailable=25%)
	// +optional
	RollingUpdateStrategy *RollingUpdateStrategySpec `json:"rollingUpdateStrategy,omitempty"`
}

// RollingUpdateStrategySpec defines the rolling update strategy for deployments
type RollingUpdateStrategySpec struct {
	// MaxSurge is the maximum number of pods that can be created above the desired replica count
	// Can be an absolute number (e.g., 5) or a percentage (e.g., 10%)
	// If not specified, defaults to 25%
	// +optional
	MaxSurge *intstr.IntOrString `json:"maxSurge,omitempty"`

	// MaxUnavailable is the maximum number of pods that can be unavailable during the update
	// Can be an absolute number (e.g., 5) or a percentage (e.g., 10%)
	// If not specified, defaults to 25%
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
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

// RoutingSpec defines gateway routing configuration for dedicated runtime routes and shared pools.
type RoutingSpec struct {
	// RoutingGroup organizes backend membership for load balancing
	// - Set to runtime name for dedicated runtimes (e.g., "runtimeA")
	// - Set to "shared" for shared pools where multiple runtimes share backends
	// If not specified, defaults to runtime name (XTrinode.Name)
	// The routing group is used internally to group backends together
	RoutingGroup string `json:"routingGroup,omitempty"`

	// HostnameDomain is the domain suffix for auto-generated hostnames
	// If specified, hostname will be auto-generated as: {runtimeName}.{hostnameDomain}
	// Example: hostnameDomain="trino-gw.company.com" → hostname="runtimeA.trino-gw.company.com"
	// If not specified, no hostname routing is configured
	HostnameDomain string `json:"hostnameDomain,omitempty"`

	// Hostname for explicit hostname-based routing (overrides auto-generation)
	// If specified, this exact hostname will be used instead of auto-generation
	// Example: "dummy.trino.company.com"
	Hostname string `json:"hostname,omitempty"`

	// Header for header-based routing (e.g., "X-Trino-XTrinode=dummy")
	// Optional: Use this for header-based routing in addition to hostname
	Header string `json:"header,omitempty"`

	// Default indicates if this is the default runtime (fallback route)
	// Only one runtime should be marked as default
	Default bool `json:"default,omitempty"`

	// CapacityUnits overrides the routing capacity derived from the resolved runtime shape.
	// +kubebuilder:validation:Minimum=1
	// +optional
	CapacityUnits *int32 `json:"capacityUnits,omitempty"`
}

// RuntimeResourcesSpec configures coordinator and worker pod resources.
type RuntimeResourcesSpec struct {
	// Coordinator overrides coordinator pod resources from the size preset.
	// +optional
	Coordinator *corev1.ResourceRequirements `json:"coordinator,omitempty"`

	// Worker overrides worker pod resources from the size preset.
	// +optional
	Worker *corev1.ResourceRequirements `json:"worker,omitempty"`
}

// PlacementSpec configures scheduler constraints for runtime pods.
type PlacementSpec struct {
	// NodeSelector applies to both coordinator and worker pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// ExistingNodePool is a provider-specific convenience that expands to the
	// provider's standard node-pool label. Raw Kubernetes placement remains the
	// canonical low-level API.
	// +optional
	ExistingNodePool *ExistingNodePoolPlacementSpec `json:"existingNodePool,omitempty"`

	// Tolerations apply to both coordinator and worker pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity applies to both coordinator and worker pods.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Coordinator overrides placement for coordinator pods.
	// +optional
	Coordinator *RolePlacementSpec `json:"coordinator,omitempty"`

	// Worker overrides placement for worker pods.
	// +optional
	Worker *RolePlacementSpec `json:"worker,omitempty"`
}

// ExistingNodePoolPlacementSpec targets a pre-existing provider node pool.
type ExistingNodePoolPlacementSpec struct {
	// Provider is the cluster provider whose node-pool label should be used.
	// +kubebuilder:validation:Enum=azure;aws;gcp
	Provider string `json:"provider"`

	// Name is the provider node-pool name.
	Name string `json:"name"`
}

// RolePlacementSpec configures scheduler constraints for one Trino role.
type RolePlacementSpec struct {
	// NodeSelector selects nodes for this role.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allow this role to schedule onto tainted nodes.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity configures Kubernetes affinity for this role.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// TopologySpreadConstraints spread pods for this role across topology domains.
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// LimitsSpec defines resource limits
type LimitsSpec struct {
	// HardConcurrencyPerGroup is the hard concurrency limit per RG
	HardConcurrencyPerGroup *int32 `json:"hardConcurrencyPerGroup,omitempty"`

	// MaxQueuedPerGroup is the max queued queries per RG
	MaxQueuedPerGroup *int32 `json:"maxQueuedPerGroup,omitempty"`

	// Session limits
	Session *SessionLimits `json:"session,omitempty"`
}

// SessionLimits defines per-session limits
type SessionLimits struct {
	// MaxQueryMemory is the max memory per query
	MaxQueryMemory string `json:"maxQueryMemory,omitempty"`

	// MaxTotalMemoryPerNode is the max total memory per node
	MaxTotalMemoryPerNode string `json:"maxTotalMemoryPerNode,omitempty"`
}

// HelmChartConfigSpec defines additional Helm chart configuration
// These fields map directly to Trino Helm chart values
type HelmChartConfigSpec struct {
	// AccessControl configuration for system access control
	AccessControl *AccessControlSpec `json:"accessControl,omitempty"`

	// SecretMounts allows mounting secrets as files on all nodes
	SecretMounts []SecretMountSpec `json:"secretMounts,omitempty"`

	// ImagePullSecrets for pulling images from private registries
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccount configuration
	ServiceAccount *ServiceAccountSpec `json:"serviceAccount,omitempty"`

	// Ingress configuration for external access
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// NetworkPolicy configuration for network isolation
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`

	// ServiceMonitor configuration for Prometheus monitoring
	ServiceMonitor *ServiceMonitorSpec `json:"serviceMonitor,omitempty"`

	// Coordinator-specific configuration
	Coordinator *CoordinatorHelmConfigSpec `json:"coordinator,omitempty"`

	// Worker-specific configuration
	Worker *WorkerHelmConfigSpec `json:"worker,omitempty"`

	// Environment variables for all pods
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Environment variables from ConfigMaps/Secrets
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// InitContainers that run before the main container
	InitContainers map[string][]corev1.Container `json:"initContainers,omitempty"`
}

// TrinoControlAuthSpec defines the internal Trino credential used by operator lifecycle calls.
type TrinoControlAuthSpec struct {
	// Username is the Trino user used for internal lifecycle requests.
	// Defaults to xtrinode-operator when omitted.
	Username string `json:"username,omitempty"`

	// PasswordSecret references a Secret key containing the control user's password.
	// The Secret must exist in the same namespace as the XTrinode.
	PasswordSecret *corev1.SecretKeySelector `json:"passwordSecret"`
}

// AccessControlSpec defines system access control configuration
type AccessControlSpec struct {
	// Type: "configmap" or "properties"
	Type string `json:"type"`
	// RefreshPeriod for configmap type (e.g., "60s")
	RefreshPeriod string `json:"refreshPeriod,omitempty"`
	// ConfigFile name for configmap type
	ConfigFile string `json:"configFile,omitempty"`
	// Rules file contents for configmap type
	Rules map[string]string `json:"rules,omitempty"`
	// Properties string for properties type
	Properties string `json:"properties,omitempty"`
}

// SecretMountSpec defines a secret mount configuration
type SecretMountSpec struct {
	// Name of the mount
	Name string `json:"name"`
	// SecretName is the name of the secret
	SecretName string `json:"secretName"`
	// Path where the secret will be mounted
	Path string `json:"path"`
	// SubPath within the secret (optional)
	SubPath string `json:"subPath,omitempty"`
}

// ServiceAccountSpec defines service account configuration
type ServiceAccountSpec struct {
	// Create specifies whether to create a service account (default: true)
	// +optional
	Create *bool `json:"create,omitempty"`
	// Name overrides the default service account name (default: trino-{name})
	Name string `json:"name,omitempty"`
	// Annotations to add to the service account
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IngressSpec defines ingress configuration
type IngressSpec struct {
	// Enabled enables ingress
	Enabled bool `json:"enabled,omitempty"`
	// ClassName is the ingress class name
	ClassName string `json:"className,omitempty"`
	// Annotations for ingress
	Annotations map[string]string `json:"annotations,omitempty"`
	// Hosts configuration
	Hosts []IngressHostSpec `json:"hosts,omitempty"`
	// TLS configuration
	TLS []IngressTLSSpec `json:"tls,omitempty"`
}

// IngressHostSpec defines an ingress host
type IngressHostSpec struct {
	// Host name
	Host string `json:"host"`
	// Paths configuration
	Paths []IngressPathSpec `json:"paths,omitempty"`
}

// IngressPathSpec defines an ingress path
type IngressPathSpec struct {
	// Path pattern
	Path string `json:"path"`
	// PathType: Prefix, Exact, or ImplementationSpecific
	PathType string `json:"pathType,omitempty"`
}

// IngressTLSSpec defines TLS configuration for ingress
type IngressTLSSpec struct {
	// SecretName for TLS certificate
	SecretName string `json:"secretName"`
	// Hosts list
	Hosts []string `json:"hosts,omitempty"`
}

// NetworkPolicySpec defines network policy configuration
type NetworkPolicySpec struct {
	// Enabled enables network policy
	Enabled bool `json:"enabled,omitempty"`
	// Ingress rules
	Ingress []NetworkPolicyIngressSpec `json:"ingress,omitempty"`
	// Egress rules
	Egress []NetworkPolicyEgressSpec `json:"egress,omitempty"`
}

// NetworkPolicyIngressSpec defines ingress rules for network policy
type NetworkPolicyIngressSpec struct {
	// From defines sources
	From []NetworkPolicyPeerSpec `json:"from,omitempty"`
	// Ports defines allowed ports
	Ports []NetworkPolicyPortSpec `json:"ports,omitempty"`
}

// NetworkPolicyEgressSpec defines egress rules for network policy
type NetworkPolicyEgressSpec struct {
	// To defines destinations
	To []NetworkPolicyPeerSpec `json:"to,omitempty"`
	// Ports defines allowed ports
	Ports []NetworkPolicyPortSpec `json:"ports,omitempty"`
}

// NetworkPolicyPeerSpec defines a network policy peer
type NetworkPolicyPeerSpec struct {
	// PodSelector selects pods
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`
	// NamespaceSelector selects namespaces
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	// IPBlock defines IP ranges
	IPBlock *networkingv1.IPBlock `json:"ipBlock,omitempty"`
}

// NetworkPolicyPortSpec defines a network policy port
type NetworkPolicyPortSpec struct {
	// Protocol: TCP, UDP, or SCTP
	Protocol *corev1.Protocol `json:"protocol,omitempty"`
	// Port number or name
	Port *intstr.IntOrString `json:"port,omitempty"`
}

// ServiceMonitorSpec defines ServiceMonitor configuration for Prometheus
type ServiceMonitorSpec struct {
	// Enabled enables ServiceMonitor
	Enabled bool `json:"enabled,omitempty"`
	// APIVersion for ServiceMonitor (default: monitoring.coreos.com/v1)
	APIVersion string `json:"apiVersion,omitempty"`
	// Labels for ServiceMonitor selection
	Labels map[string]string `json:"labels,omitempty"`
	// Interval for scraping
	Interval string `json:"interval,omitempty"`
	// ScrapeTimeout for scraping (e.g. "10s")
	ScrapeTimeout string `json:"scrapeTimeout,omitempty"`
	// Coordinator-specific ServiceMonitor config
	Coordinator *ServiceMonitorRoleSpec `json:"coordinator,omitempty"`
	// Worker-specific ServiceMonitor config
	Worker *ServiceMonitorRoleSpec `json:"worker,omitempty"`
}

// ServiceMonitorRoleSpec defines ServiceMonitor config for coordinator/worker
type ServiceMonitorRoleSpec struct {
	// Enabled enables ServiceMonitor for this role
	Enabled bool `json:"enabled,omitempty"`
	// Labels for ServiceMonitor selection
	Labels map[string]string `json:"labels,omitempty"`
	// ScrapeTimeout for scraping (overrides global, e.g. "10s")
	ScrapeTimeout string `json:"scrapeTimeout,omitempty"`
}

// CoordinatorHelmConfigSpec defines coordinator-specific Helm chart configuration
type CoordinatorHelmConfigSpec struct {
	// SecretMounts allows mounting secrets as files on coordinator
	SecretMounts []SecretMountSpec `json:"secretMounts,omitempty"`
	// AdditionalConfigFiles placed in the default configuration directory
	AdditionalConfigFiles map[string]string `json:"additionalConfigFiles,omitempty"`
}

// WorkerHelmConfigSpec defines worker-specific Helm chart configuration
type WorkerHelmConfigSpec struct {
	// GracefulShutdown configuration
	GracefulShutdown *GracefulShutdownSpec `json:"gracefulShutdown,omitempty"`
	// SecretMounts allows mounting secrets as files on workers
	SecretMounts []SecretMountSpec `json:"secretMounts,omitempty"`
	// AdditionalConfigFiles placed in the default configuration directory
	AdditionalConfigFiles map[string]string `json:"additionalConfigFiles,omitempty"`
	// TopologySpreadConstraints for spreading workers across nodes/zones
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// GracefulShutdownSpec defines graceful shutdown configuration
type GracefulShutdownSpec struct {
	// Enabled enables graceful shutdown
	Enabled bool `json:"enabled,omitempty"`
	// GracePeriodSeconds is the grace period in seconds
	// terminationGracePeriodSeconds must be >= 2x gracePeriodSeconds
	GracePeriodSeconds int64 `json:"gracePeriodSeconds,omitempty"`
}

// XTrinodeStatus defines the observed state of a XTrinode
type XTrinodeStatus struct {
	// Phase is the current phase: Reconciling|Ready|Suspended|Error
	Phase string `json:"phase,omitempty"`

	// CoordinatorURL is the URL of the coordinator
	CoordinatorURL string `json:"coordinatorURL,omitempty"`

	// Workers is the current number of worker replicas
	Workers int32 `json:"workers,omitempty"`

	// LastActivity is the timestamp of the last query
	LastActivity *metav1.Time `json:"lastActivity,omitempty"`

	// CurrentRevision is the broad desired-state hash computed from spec and operatorVersion.
	// It is stamped on resource metadata for convergence and debugging.
	// Pod rollouts are controlled by the component rollout hashes below.
	// +optional
	CurrentRevision string `json:"currentRevision,omitempty"`

	// ObservedGeneration tracks the Kubernetes generation that was last reconciled
	// Used to detect when spec changes
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// BaseRevision is the stable identity hash (spec + operatorVersion)
	// Used for resource naming and debugging, not for rollout decisions
	// Does NOT include catalogs - those are tracked separately via rollout hashes
	// +optional
	BaseRevision string `json:"baseRevision,omitempty"`

	// CoordinatorRolloutHash is the rendered-content hash that triggers coordinator pod rollouts.
	// Includes the rendered coordinator pod template plus catalog, access control,
	// session property, and Secret content digests.
	// Changes to this hash trigger coordinator Deployment rollout
	// +optional
	CoordinatorRolloutHash string `json:"coordinatorRolloutHash,omitempty"`

	// WorkerRolloutHash is the rendered-content hash that triggers worker pod rollouts.
	// Includes the rendered worker pod template plus access control, session property,
	// Secret content, and mounted catalog content digests.
	// Changes to this hash trigger worker Deployment rollout
	// +optional
	WorkerRolloutHash string `json:"workerRolloutHash,omitempty"`

	// ObservedCatalogs is the list of catalogs from last successful reconcile
	// Used to preserve catalogs during discovery failures and finalizer cleanup
	// +optional
	ObservedCatalogs []string `json:"observedCatalogs,omitempty"`

	// ObservedCatalogDigest is the content hash of catalog ConfigMaps
	// Used to detect catalog changes that should trigger rollouts
	// +optional
	ObservedCatalogDigest string `json:"observedCatalogDigest,omitempty"`

	// ObservedAccessControlDigest is the content hash of access control ConfigMaps
	// +optional
	ObservedAccessControlDigest string `json:"observedAccessControlDigest,omitempty"`

	// ObservedSessionPropsDigest is the content hash of session properties ConfigMap
	// +optional
	ObservedSessionPropsDigest string `json:"observedSessionPropsDigest,omitempty"`

	// ObservedRuntimeShape is the compact resolved runtime shape from the latest
	// successful resolution.
	// +optional
	ObservedRuntimeShape *ObservedRuntimeShapeStatus `json:"observedRuntimeShape,omitempty"`

	// Wake tracks the ephemeral wake window state after resume
	// When set, KEDA minReplicas is temporarily raised to wake.minWorkers
	// After wake.expiresAt, this field is cleared and KEDA returns to normal min=0
	// This prevents flapping between controller and KEDA scaling
	// +optional
	Wake *WakeState `json:"wake,omitempty"`

	// Conditions is a list of conditions
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ObservedRuntimeShapeStatus exposes the resolved runtime shape used by
// guardrails, route capacity, pod rendering, and resume ranking.
const ObservedRuntimeShapeStatusVersion = "v1"

type ObservedRuntimeShapeStatus struct {
	// Version is the schema version for this compact observed shape.
	Version string `json:"version,omitempty"`

	// Hash is a stable hash of the resolved runtime shape.
	Hash string `json:"hash,omitempty"`

	// Preset is the size preset used as the base shape.
	Preset string `json:"preset,omitempty"`

	// AutoscalingMode is fixed, keda, or hpa.
	AutoscalingMode string `json:"autoscalingMode,omitempty"`

	// Coordinator contains resolved coordinator resources.
	Coordinator ObservedRuntimeResourcesStatus `json:"coordinator,omitempty"`

	// Worker contains resolved worker resources.
	Worker ObservedRuntimeResourcesStatus `json:"worker,omitempty"`

	// Workers contains the resolved worker counts used by shape consumers.
	Workers ObservedRuntimeWorkersStatus `json:"workers,omitempty"`

	// CapacityUnits is the routing and resume capacity weight.
	CapacityUnits int32 `json:"capacityUnits,omitempty"`

	// NodePool summarizes node-pool provisioning and scheduling binding intent.
	NodePool ObservedRuntimeNodePoolStatus `json:"nodePool,omitempty"`
}

// ObservedRuntimeResourcesStatus exposes resolved Kubernetes resources.
type ObservedRuntimeResourcesStatus struct {
	// Requests are resolved resource requests.
	// +optional
	Requests corev1.ResourceList `json:"requests,omitempty"`

	// Limits are resolved resource limits.
	// +optional
	Limits corev1.ResourceList `json:"limits,omitempty"`
}

// ObservedRuntimeWorkersStatus exposes resolved worker count semantics.
type ObservedRuntimeWorkersStatus struct {
	// Fixed is set for fixed-replica runtimes.
	// +optional
	Fixed *int32 `json:"fixed,omitempty"`

	// Min is the minimum autoscaled worker count, or the fixed count for fixed runtimes.
	Min int32 `json:"min,omitempty"`

	// Max is the maximum autoscaled worker count, or the fixed count for fixed runtimes.
	Max int32 `json:"max,omitempty"`

	// Quota is the worker count used for namespace guardrails.
	Quota int32 `json:"quota,omitempty"`

	// Capacity is the worker count used for default route capacity.
	Capacity int32 `json:"capacity,omitempty"`
}

// ObservedRuntimeNodePoolStatus exposes resolved node-pool intent.
type ObservedRuntimeNodePoolStatus struct {
	// ProvisioningRequested is true when spec.nodePool is set.
	ProvisioningRequested bool `json:"provisioningRequested,omitempty"`

	// Provider is the node-pool provider.
	Provider string `json:"provider,omitempty"`

	// ProviderMode is managed or self-managed when configured.
	ProviderMode string `json:"providerMode,omitempty"`

	// Name is the resolved node-pool name.
	Name string `json:"name,omitempty"`

	// SchedulePods is true when runtime pods are bound to the managed pool.
	SchedulePods bool `json:"schedulePods,omitempty"`

	// DeletionPolicy is the resolved managed node-pool deletion policy.
	DeletionPolicy string `json:"deletionPolicy,omitempty"`
}

// WakeState tracks ephemeral wake window after resume
type WakeState struct {
	// MinWorkers is the temporary minimum worker count during wake window
	MinWorkers int32 `json:"minWorkers"`

	// ExpiresAt is when the wake window expires and minWorkers returns to 0
	ExpiresAt metav1.Time `json:"expiresAt"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tn
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.spec.size`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Workers",type=integer,JSONPath=`.status.workers`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// XTrinode is the Schema for the xtrinodes API
type XTrinode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   XTrinodeSpec   `json:"spec,omitempty"`
	Status XTrinodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// XTrinodeList contains a list of XTrinode
type XTrinodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []XTrinode `json:"items"`
}

// GetValuesOverlayMap converts ValuesOverlay from apiextensionsv1.JSON to map[string]interface{}
// Returns nil if ValuesOverlay is nil or empty
// This helper method allows the codebase to continue using map[string]interface{} semantics
func (s *XTrinodeSpec) GetValuesOverlayMap() map[string]interface{} {
	if s.ValuesOverlay == nil || len(s.ValuesOverlay.Raw) == 0 {
		return nil
	}
	var result map[string]interface{}
	if err := json.Unmarshal(s.ValuesOverlay.Raw, &result); err != nil {
		return nil
	}
	return result
}

func init() {
	SchemeBuilder.Register(&XTrinode{}, &XTrinodeList{})
}
