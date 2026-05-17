package config

import (
	"fmt"
	"time"
)

// Ports defines default port numbers used across the operator
const (
	// TrinoPortHTTP is the default HTTP port for Trino coordinator and workers
	TrinoPortHTTP = 8080

	// TrinoPortHTTPS is the default HTTPS port for Trino coordinator and workers
	TrinoPortHTTPS = 8443

	// JMXExporterPort is the default port for JMX exporter sidecar
	JMXExporterPort = 5556

	// TrinoJMXPort is the internal Trino JMX registry port scraped by the JMX exporter
	TrinoJMXPort = 9080

	// TrinoJMXServerPort is the internal Trino JMX RMI server port
	TrinoJMXServerPort = 9081

	// GatewayPort is the default port for the gateway service
	GatewayPort = 8080

	// APIServerPort is the default port for the control-plane REST API server.
	APIServerPort = 8081
)

// ImageDefaults defines default images for managed runtime containers.
const (
	// DefaultTrinoImageRepository is the upstream Trino image repository.
	DefaultTrinoImageRepository = "trinodb/trino"

	// DefaultTrinoImageTag is the managed Trino runtime tag, not an XTrinode
	// control-plane image tag. It matches the appVersion in upstream chart trino-1.42.2.
	DefaultTrinoImageTag = "480"

	// DefaultJMXExporterImage is the default JMX exporter sidecar image.
	// Pin the maintained Bitnami image by multi-arch digest because
	// Docker Hub does not publish stable semver tags for this repository.
	DefaultJMXExporterImage = "bitnami/jmx-exporter@sha256:7c0014b7e1d736faec9760a89727389ba1ba7ad920c764417167abecfb7fd032"
)

// ServiceNames defines service naming patterns
const (
	// ServiceNamePrefix is the prefix for Trino service names
	// Format: trino-{xtrinode-name}
	ServiceNamePrefix = "trino-"

	// CoordinatorServiceName returns the coordinator service name for a given XTrinode name
	// Usage: CoordinatorServiceName("dummy") -> "trino-dummy"
	CoordinatorServiceName = ServiceNamePrefix + "%s"

	// WorkerServiceName returns the worker service name for a given XTrinode name
	// Format: trino-{name}-worker
	WorkerServiceNameSuffix = "-worker"

	// MetricsServiceNameCoordinator returns the coordinator metrics service name
	// Format: trino-{name}-metrics
	MetricsServiceNameCoordinatorSuffix = "-metrics"

	// MetricsServiceNameWorker returns the worker metrics service name
	// Format: trino-{name}-worker-metrics
	MetricsServiceNameWorkerSuffix = "-worker-metrics"
)

// GatewayConfig defines gateway-related configuration
const (
	// GatewayConfigMapName is the name of the ConfigMap storing gateway routes
	GatewayConfigMapName = "trino-gateway-routes"

	// GatewayConfigMapNamespace is the default namespace where the gateway ConfigMap lives
	GatewayConfigMapNamespace = GatewayDefaultNamespace

	// GatewayConfigMapKey is the key in the ConfigMap storing route data
	GatewayConfigMapKey = "routes.yaml"

	// GatewayAuthSecretName is the default name of the Secret containing API keys
	GatewayAuthSecretName = "trino-gateway-api-keys"

	// GatewayAuthSecretKey is the default key in the Secret containing API keys
	GatewayAuthSecretKey = "api-keys"

	// GatewayDefaultRoutingGroupSeparator separates namespace and name in default dedicated routes.
	GatewayDefaultRoutingGroupSeparator = "--"
)

// DefaultDedicatedRoutingGroup returns the gateway route key for a default dedicated XTrinode route.
func DefaultDedicatedRoutingGroup(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return fmt.Sprintf("%s%s%s", namespace, GatewayDefaultRoutingGroupSeparator, name)
}

// OperatorConfig defines operator-related configuration
const (
	// OperatorDefaultNamespace is the default namespace where the operator and API server run
	OperatorDefaultNamespace = "xtrinode-system"

	// GatewayDefaultNamespace is the default namespace where the gateway runs
	GatewayDefaultNamespace = "xtrinode-gateway"

	// OperatorServiceName is the name of the operator service
	OperatorServiceName = "xtrinode-operator"

	// OperatorVersion is the version of the operator (used for revision computation)
	// This should be set at build time via ldflags: -X github.com/xtrinode/xtrinode/internal/config.OperatorVersion=...
	// Defaults to "dev" if not set
	OperatorVersion = "dev"

	// OperatorLeaderElectionID is the unique identifier for leader election
	OperatorLeaderElectionID = "xtrinode.analytics.xtrinode.io"

	// OperatorRESTConfigQPS is the QPS limit for the Kubernetes API client
	OperatorRESTConfigQPS = 50

	// OperatorRESTConfigBurst is the burst limit for the Kubernetes API client
	OperatorRESTConfigBurst = 100
)

// Timeouts defines timeout values used across the operator
const (
	// HTTPClientTimeout is the default timeout for HTTP client requests
	HTTPClientTimeout = 5 * time.Second

	// GatewayShutdownTimeout is the timeout for gateway graceful shutdown
	GatewayShutdownTimeout = 5 * time.Second

	// GatewayReadHeaderTimeout is the timeout for reading request headers (protects against Slowloris)
	GatewayReadHeaderTimeout = 5 * time.Second

	// GatewayReadTimeout is the total timeout for reading the entire request.
	// Keep disabled by default because Trino query clients can hold request/response
	// streams open longer than ordinary REST calls. ReadHeaderTimeout still protects
	// the listener from slow header attacks.
	GatewayReadTimeout = 0

	// GatewayWriteTimeout is the total timeout for writing the response.
	// Keep disabled by default so Trino long-polling and result streaming are not
	// interrupted by the gateway.
	GatewayWriteTimeout = 0

	// GatewayIdleTimeout is the keep-alive timeout for idle connections
	GatewayIdleTimeout = 60 * time.Second

	// GatewayRouteReloadInterval is the interval for reloading gateway routes from ConfigMap
	GatewayRouteReloadInterval = 5 * time.Second

	// GatewayAutoResumeCooldown is the minimum time between auto-resume attempts per XTrinode
	GatewayAutoResumeCooldown = 30 * time.Second

	// GatewayHealthCheckInterval is the interval for active health checks
	GatewayHealthCheckInterval = 5 * time.Second

	// GatewayHealthCheckTimeout is the timeout for individual health checks
	GatewayHealthCheckTimeout = 2 * time.Second

	// GatewayHealthCheckFailureThreshold is the number of consecutive failures before marking backend unhealthy
	GatewayHealthCheckFailureThreshold = 3

	// RuntimeReadinessRequeueInterval is how often the operator checks Trino runtime readiness
	// before marking an XTrinode Ready and exposing it as RUNNING through the gateway.
	RuntimeReadinessRequeueInterval = 5 * time.Second

	// Redis sticky routing configuration
	// GatewayRedisEnabled enables Redis for distributed sticky routing
	GatewayRedisEnabled = false

	// GatewayRedisURL is the Redis connection URL (e.g., redis://localhost:6379)
	GatewayRedisURL = ""

	// GatewayRedisPassword is the Redis password (optional)
	GatewayRedisPassword = ""

	// GatewayRedisDB is the Redis database number
	GatewayRedisDB = 0

	// GatewayRedisStickyTTL is the TTL for sticky routing entries in Redis
	GatewayRedisStickyTTL = 1 * time.Hour

	// GatewayRedisFallbackCacheSize is the LRU cache size for Redis fallback
	GatewayRedisFallbackCacheSize = 1000

	// GatewayRedisPingTimeout is the Redis connection test timeout
	GatewayRedisPingTimeout = 5 * time.Second

	// GatewayRedisTimeout is the timeout for Redis operations
	GatewayRedisTimeout = 1 * time.Second

	// API server configuration
	// GatewayAPIServerURL is the base API URL for the API server
	GatewayAPIServerURL = "http://xtrinode-api-server:8081/api/v1"

	// GatewayAPIServerTimeout is the timeout for API server calls
	GatewayAPIServerTimeout = 5 * time.Second

	// GatewayCircuitBreakerFailureThreshold is the number of failures before opening circuit
	GatewayCircuitBreakerFailureThreshold = 5

	// GatewayCircuitBreakerSuccessThreshold is the number of successes needed to close circuit (half-open)
	GatewayCircuitBreakerSuccessThreshold = 2

	// GatewayCircuitBreakerTimeout is the timeout before trying half-open state
	GatewayCircuitBreakerTimeout = 30 * time.Second

	// GatewayRateLimitCapacity is the number of requests allowed per window
	GatewayRateLimitCapacity = 100

	// GatewayRateLimitRefillRate is the refill rate for rate limiting (1 token per duration)
	GatewayRateLimitRefillRate = 1 * time.Second

	// GatewayRetryMaxRetries is the maximum number of retry attempts
	GatewayRetryMaxRetries = 3

	// GatewayRetryBaseDelay is the base delay between retries
	GatewayRetryBaseDelay = 100 * time.Millisecond

	// GatewayRetryMaxDelay is the maximum delay between retries
	GatewayRetryMaxDelay = 5 * time.Second

	// GatewayHTTPClientTimeout is the timeout for gateway HTTP client requests
	GatewayHTTPClientTimeout = 10 * time.Second

	// GatewayHTTPClientMaxIdleConns is the maximum number of idle connections across all hosts
	GatewayHTTPClientMaxIdleConns = 100

	// GatewayHTTPClientMaxIdleConnsPerHost is the maximum number of idle connections per host
	GatewayHTTPClientMaxIdleConnsPerHost = 10

	// GatewayHTTPClientIdleConnTimeout is the timeout for idle connections
	GatewayHTTPClientIdleConnTimeout = 90 * time.Second

	// HTTPTransportMaxIdleConns is the default maximum number of idle connections for HTTP transport
	HTTPTransportMaxIdleConns = 100

	// HTTPTransportMaxIdleConnsPerHost is the default maximum number of idle connections per host
	HTTPTransportMaxIdleConnsPerHost = 10

	// HTTPTransportIdleConnTimeout is the default timeout for idle connections
	HTTPTransportIdleConnTimeout = 90 * time.Second

	// WorkerPoolCleanupInterval is the interval for cleaning up stale mutexes in worker pool
	WorkerPoolCleanupInterval = 1 * time.Hour

	// MaxConcurrentReconciles is the maximum number of concurrent XTrinode reconciliations
	MaxConcurrentReconciles = 10

	// MaxConcurrentReconcilesCatalog is the maximum number of concurrent XTrinodeCatalog reconciliations
	MaxConcurrentReconcilesCatalog = 5

	// LeaderElectionEnabled controls whether leader election is enabled by default
	LeaderElectionEnabled = true
)

// Trino Resource Configuration
const (
	// MaxRevisionHistory is the maximum number of old ConfigMap revisions to keep
	MaxRevisionHistory = 3

	// FieldOwner is the field owner for server-side apply operations
	FieldOwner = "xtrinode-operator"

	// RevisionLabelKey is the label key for XTrinode revision
	RevisionLabelKey = "xtrinode.io/revision"

	// RevisionAnnotationKey is the annotation key for XTrinode revision
	RevisionAnnotationKey = "xtrinode.io/revision"

	// DefaultCoordinatorReplicas is the default number of coordinator replicas
	DefaultCoordinatorReplicas = 1

	// DefaultWorkerReplicas is the default number of worker replicas (when KEDA is disabled)
	DefaultWorkerReplicas = 2

	// DefaultGracefulShutdownSeconds is the default graceful shutdown period for workers
	DefaultGracefulShutdownSeconds = 120

	// DefaultCoordinatorPDBMinAvailable is the default minAvailable for coordinator PDB
	DefaultCoordinatorPDBMinAvailable = 1

	// DefaultWorkerPDBMaxUnavailable is the default maxUnavailable for worker PDB
	DefaultWorkerPDBMaxUnavailable = 1

	// DefaultHPAMinReplicas is the default minimum replicas for HPA
	DefaultHPAMinReplicas = 2

	// DefaultHPAMaxReplicas is the default maximum replicas for HPA
	DefaultHPAMaxReplicas = 5

	// DefaultHPACPUTargetPercentage is the default CPU target percentage for HPA
	DefaultHPACPUTargetPercentage = 50

	// DefaultHPAMemoryTargetPercentage is the default memory target percentage for HPA
	DefaultHPAMemoryTargetPercentage = 80
)

// Additional operator configuration
const (
	// LeaderElectionReleaseOnCancel enables faster leader handover on shutdown
	LeaderElectionReleaseOnCancel = true

	// HealthProbeBindAddress is the address for health/readiness probes
	HealthProbeBindAddress = ":8081"

	// APIServerReadTimeout is the read timeout for API server
	APIServerReadTimeout = 10 * time.Second

	// APIServerWriteTimeout is the write timeout for API server responses
	APIServerWriteTimeout = 30 * time.Second

	// APIServerShutdownTimeout is the timeout for API server graceful shutdown
	APIServerShutdownTimeout = 5 * time.Second

	// APIServerSuspendLeaseDuration is the K8s Lease duration for suspend operations
	APIServerSuspendLeaseDuration = 120 * time.Second

	// APIServerRetryAfterSeconds is the default Retry-After seconds for gated requests
	APIServerRetryAfterSeconds = 30

	// APIServerRequestTimeout is the default timeout for API requests
	APIServerRequestTimeout = 30 * time.Second

	// APIServerDefaultLeaseNamespace is the default namespace for K8s Lease objects
	APIServerDefaultLeaseNamespace = OperatorDefaultNamespace

	// APIServerDefaultLeaseHolderIdentity is the default identity for K8s Lease holder
	APIServerDefaultLeaseHolderIdentity = "xtrinode-api-server"

	// APIServerResumeLeaseDuration is the default K8s Lease duration for resume operations
	APIServerResumeLeaseDuration = 120 * time.Second

	// APIServerMaxLeaseNameLength is the maximum length for a Kubernetes Lease name (DNS-1123 subdomain)
	APIServerMaxLeaseNameLength = 63

	// APIServerLeasePrefix is the prefix for all resume/suspend leases
	APIServerLeasePrefix = "xtrinode-resume-"

	// APIServerMinRetryAfterSeconds is the minimum retry-after value for gated requests
	APIServerMinRetryAfterSeconds = 1

	// APIServerMaxRetryAfterSeconds is the maximum retry-after value for gated requests
	APIServerMaxRetryAfterSeconds = 120

	// APIServerDefaultAPIPath is the default base path for API endpoints
	APIServerDefaultAPIPath = "/api/v1"

	// DefaultLogLevel is the default log level
	DefaultLogLevel = "info"

	// GatewayShutdownWaitTimeout is the timeout for waiting for gateway to shut down
	GatewayShutdownWaitTimeout = 10 * time.Second

	// ReconcileRequeueIntervalAutosuspend is the requeue interval for autosuspend checks
	ReconcileRequeueIntervalAutosuspend = 60 * time.Second

	// ReconcileRequeueIntervalWakeTTL is the requeue interval for WakeTTL expiration checks
	ReconcileRequeueIntervalWakeTTL = 30 * time.Second

	// ReconcileRequeueIntervalSuspended is the requeue interval for suspended XTrinodes
	ReconcileRequeueIntervalSuspended = 300 * time.Second
)

// XTrinodeSizes defines valid runtime sizes and their ordering
// These are t-shirt sizing presets that map to specific resource allocations
// in the sizing package (pkg/sizing/sizing.go)
var (
	// ValidSizes is the set of allowed runtime sizes
	ValidSizes = map[string]bool{
		"xs": true, // Extra-small: Minimal resources for dev/testing
		"s":  true, // Small: Light production workloads
		"m":  true, // Medium: Standard production workloads
		"l":  true, // Large: Heavy analytical workloads
		"xl": true, // Extra-large: Enterprise-scale workloads
	}

	// SizeOrder defines the ordering of sizes for upgrade/downgrade detection
	// Used by validation logic to determine if a size change is an upgrade or downgrade
	SizeOrder = map[string]int{
		"xs": 1,
		"s":  2,
		"m":  3,
		"l":  4,
		"xl": 5,
	}

	// SizeList is an ordered list of valid sizes (for display/iteration)
	SizeList = []string{"xs", "s", "m", "l", "xl"}
)

// CloudProviders defines valid cloud providers for node pools
var (
	// ValidProviders is the set of allowed cloud providers
	ValidProviders = map[string]bool{
		"azure": true, // Microsoft Azure (AKS)
		"aws":   true, // Amazon Web Services (EKS)
		"gcp":   true, // Google Cloud Platform (GKE)
	}

	// ProviderList is an ordered list of valid providers (for display/iteration)
	ProviderList = []string{"azure", "aws", "gcp"}
)

// HTTPRetryDefaults defines default values for HTTP client retry logic
const (
	// HTTPRetryMaxRetries is the default maximum number of retry attempts
	HTTPRetryMaxRetries = 3

	// HTTPRetryBaseDelay is the default base delay for exponential backoff
	HTTPRetryBaseDelay = 100 * time.Millisecond

	// HTTPRetryMaxDelay is the default maximum delay cap for exponential backoff
	HTTPRetryMaxDelay = 2 * time.Second

	// HTTPRetryTimeout is the default per-request timeout
	HTTPRetryTimeout = 5 * time.Second

	// HTTPRetryHealthCheckMaxRetries is the maximum retries for health checks (fewer for fast failure)
	HTTPRetryHealthCheckMaxRetries = 2

	// HTTPRetryHealthCheckBaseDelay is the base delay for health check retries
	HTTPRetryHealthCheckBaseDelay = 50 * time.Millisecond

	// HTTPRetryHealthCheckMaxDelay is the max delay for health check retries
	HTTPRetryHealthCheckMaxDelay = 500 * time.Millisecond

	// HTTPRetryJitterPercent is the jitter percentage for retry delays (±25%)
	HTTPRetryJitterPercent = 0.25
)

// GracefulShutdownDefaults defines default values for graceful shutdown
const (
	// DefaultWorkerGracePeriodSeconds is the default grace period for worker pods (60 minutes)
	DefaultWorkerGracePeriodSeconds = 60 * 60

	// CoordinatorGracePeriodSeconds is the default termination grace period for coordinator pods (15 minutes)
	CoordinatorGracePeriodSeconds = 15 * 60

	// WorkerGracePeriodSeconds is the default termination grace period for worker pods (60 minutes)
	WorkerGracePeriodSeconds = 60 * 60

	// ShutdownGracePeriodSeconds is the Trino shutdown grace period (30 seconds)
	ShutdownGracePeriodSeconds = 30

	// ShutdownTimeoutMinutes is the Trino shutdown timeout (60 minutes)
	ShutdownTimeoutMinutes = 60
)

// KEDADefaults defines default values for KEDA autoscaling
const (
	// KEDADefaultMaxWorkers is the default maximum number of workers
	KEDADefaultMaxWorkers = 24

	// KEDADefaultMemoryThreshold is the default memory usage threshold percentage (as string)
	KEDADefaultMemoryThreshold = "80"

	// KEDADefaultCPUThreshold is the default CPU usage threshold percentage (as string)
	KEDADefaultCPUThreshold = "80"

	// KEDADefaultQueryThreshold is the default query threshold (queries per worker)
	KEDADefaultQueryThreshold = "1"
)

// AnnotationKeys defines annotation keys used for coordination between operator and API
const (
	// ResumeRequestedAnnotation indicates a resume request from REST API
	ResumeRequestedAnnotation = "xtrinode.analytics.xtrinode.io/resume-requested"

	// ResumeRequestedAtAnnotation timestamp when resume was requested
	ResumeRequestedAtAnnotation = "xtrinode.analytics.xtrinode.io/resume-requested-at"

	// SuspendRequestedAnnotation indicates a suspend request from REST API
	SuspendRequestedAnnotation = "xtrinode.analytics.xtrinode.io/suspend-requested"

	// SuspendRequestedAtAnnotation timestamp when suspend was requested
	SuspendRequestedAtAnnotation = "xtrinode.analytics.xtrinode.io/suspend-requested-at"

	// AutoSuspendRequestedAnnotation indicates an auto-suspend request
	AutoSuspendRequestedAnnotation = "xtrinode.analytics.xtrinode.io/auto-suspend-requested"

	// AutoSuspendRequestedAtAnnotation timestamp when auto-suspend was requested
	AutoSuspendRequestedAtAnnotation = "xtrinode.analytics.xtrinode.io/auto-suspend-requested-at"

	// WakeMinWorkersAnnotation specifies minimum workers for wake operation
	WakeMinWorkersAnnotation = "xtrinode.analytics.xtrinode.io/wake-min-workers"

	// WakeTTLAnnotation specifies TTL for wake operation
	WakeTTLAnnotation = "xtrinode.analytics.xtrinode.io/wake-ttl"

	// WakeTimeAnnotation timestamp when XTrinode was woken
	WakeTimeAnnotation = "xtrinode.analytics.xtrinode.io/wake-time"

	// CatalogUpdatedAnnotation indicates catalog was updated (triggers XTrinode reconciliation)
	CatalogUpdatedAnnotation = "xtrinode.analytics.xtrinode.io/catalog-updated"

	// ResumeLeaseUntilAnnotation stores RFC3339 timestamp until which resume operation is leased
	ResumeLeaseUntilAnnotation = "xtrinode.analytics.xtrinode.io/resume-lease-until"

	// SuspendLeaseUntilAnnotation stores RFC3339 timestamp until which suspend operation is leased
	SuspendLeaseUntilAnnotation = "xtrinode.analytics.xtrinode.io/suspend-lease-until"
)

// LabelKeys defines label keys used for resource organization
const (
	// ManagedLabel indicates a resource is managed by XTrinode operator
	ManagedLabel = "xtrinode.analytics.xtrinode.io/managed"

	// RuntimeLabel indicates the XTrinode runtime name
	RuntimeLabel = "xtrinode.analytics.xtrinode.io/runtime"
)

// HTTPPaths defines HTTP path constants
const (
	// HealthPath is the health check endpoint path
	HealthPath = "/health"

	// MetricsPath is the metrics endpoint path
	MetricsPath = "/metrics"

	// QueryAPIPath is the Trino query API endpoint path
	QueryAPIPath = "/v1/query"

	// StatementAPIPath is the Trino statement API endpoint path
	StatementAPIPath = "/v1/statement"
)

// TrinoHTTP defines headers and identities used for internal Trino API calls.
const (
	// TrinoUserHeader is required by Trino client/query API endpoints.
	TrinoUserHeader = "X-Trino-User"

	// TrinoOperatorUser is the identity used by operator-originated Trino API calls.
	TrinoOperatorUser = "xtrinode-operator"
)

// ServiceDefaults defines default service configuration values
const (
	// DefaultServiceType is the default Kubernetes service type
	DefaultServiceType = "ClusterIP"

	// HeadlessServiceClusterIP is the ClusterIP value for headless services
	HeadlessServiceClusterIP = "None"

	// DefaultBackendWeight is the default weight for load balancing backends
	DefaultBackendWeight = 100

	// PrometheusDefaultPort is the default Prometheus server port
	PrometheusDefaultPort = 9090

	// PrometheusDefaultURL is the default Prometheus server URL
	PrometheusDefaultURL = "http://prometheus-operated.monitoring.svc.cluster.local:9090"

	// CatalogConfigMapPrefix is the prefix for catalog ConfigMap names
	// Format: trino-catalog-{catalogName}
	CatalogConfigMapPrefix = "trino-catalog-"
)

// ReleaseNamePattern defines the pattern for release names
const (
	// ReleaseNamePrefix is the prefix for release names (used by KEDA)
	// Format: trino-{xtrinode-name}
	ReleaseNamePrefix = ServiceNamePrefix
)

// DNS patterns
const (
	// ServiceDNSFormat is the format for Kubernetes service DNS names
	// Format: {service-name}.{namespace}.svc.cluster.local:{port}
	ServiceDNSFormat = "%s.%s.svc.cluster.local:%d"

	// ServiceDNSFormatNoPort is the format for Kubernetes service DNS names without port
	// Format: {service-name}.{namespace}.svc.cluster.local
	ServiceDNSFormatNoPort = "%s.%s.svc.cluster.local"
)

// Helper functions for building service names and URLs

// BuildCoordinatorServiceName builds the coordinator service name for a XTrinode
func BuildCoordinatorServiceName(xtrinodeName string) string {
	return ServiceNamePrefix + xtrinodeName
}

// BuildWorkerServiceName builds the worker service name for a XTrinode
func BuildWorkerServiceName(xtrinodeName string) string {
	return ServiceNamePrefix + xtrinodeName + WorkerServiceNameSuffix
}

// BuildCoordinatorMetricsServiceName builds the coordinator metrics service name
func BuildCoordinatorMetricsServiceName(xtrinodeName string) string {
	return ServiceNamePrefix + xtrinodeName + MetricsServiceNameCoordinatorSuffix
}

// BuildWorkerMetricsServiceName builds the worker metrics service name
func BuildWorkerMetricsServiceName(xtrinodeName string) string {
	return ServiceNamePrefix + xtrinodeName + MetricsServiceNameWorkerSuffix
}

// BuildCoordinatorURL builds the coordinator service URL
func BuildCoordinatorURL(xtrinodeName, namespace string) string {
	serviceName := BuildCoordinatorServiceName(xtrinodeName)
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, namespace, TrinoPortHTTP)
}

// BuildCoordinatorURLWithPort builds the coordinator service URL with a custom port
func BuildCoordinatorURLWithPort(xtrinodeName, namespace string, port int) string {
	serviceName := BuildCoordinatorServiceName(xtrinodeName)
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, namespace, port)
}

// BuildAPIServerServiceURL builds the API server URL used by the gateway.
func BuildAPIServerServiceURL(namespace string) string {
	return fmt.Sprintf("http://xtrinode-api-server.%s.svc.cluster.local:%d%s", namespace, APIServerPort, APIServerDefaultAPIPath)
}

// BuildReleaseName builds the release name for a XTrinode (used by KEDA)
func BuildReleaseName(xtrinodeName string) string {
	return ServiceNamePrefix + xtrinodeName
}

// BuildScaledObjectName builds the ScaledObject name for a XTrinode
// This is the canonical name used by KEDA for autoscaling
func BuildScaledObjectName(xtrinodeName string) string {
	return BuildReleaseName(xtrinodeName) + "-workers"
}

// BuildCoordinatorDeploymentName builds the coordinator deployment name for a XTrinode
func BuildCoordinatorDeploymentName(xtrinodeName string) string {
	return BuildReleaseName(xtrinodeName) + "-coordinator"
}

// BuildWorkerDeploymentName builds the worker deployment name for a XTrinode
func BuildWorkerDeploymentName(xtrinodeName string) string {
	return BuildReleaseName(xtrinodeName) + "-worker"
}

// BuildCoordinatorMetricsURL builds the coordinator metrics URL
func BuildCoordinatorMetricsURL(xtrinodeName, namespace string) string {
	serviceName := BuildCoordinatorServiceName(xtrinodeName)
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", serviceName, namespace, TrinoPortHTTP, MetricsPath)
}

// BuildCoordinatorQueryAPIURL builds the coordinator query API URL.
func BuildCoordinatorQueryAPIURL(xtrinodeName, namespace string) string {
	serviceName := BuildCoordinatorServiceName(xtrinodeName)
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/v1/query", serviceName, namespace, TrinoPortHTTP)
}

// BuildJMXMetricsURL builds the JMX exporter metrics URL
func BuildJMXMetricsURL(xtrinodeName, namespace string, jmxPort int32) string {
	serviceName := BuildCoordinatorServiceName(xtrinodeName)
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", serviceName, namespace, jmxPort, MetricsPath)
}

// NodePoolDefaults defines default values for node pool configuration
const (
	// NodePoolDefaultMinNodes is the default minimum number of nodes
	NodePoolDefaultMinNodes = 0

	// NodePoolDefaultMaxNodes is the default maximum number of nodes
	NodePoolDefaultMaxNodes = 10

	// NodePoolDefaultDiskSizeGB is the default disk size in GB
	NodePoolDefaultDiskSizeGB = 128

	// NodePoolDefaultClusterName is the default cluster name if not specified
	NodePoolDefaultClusterName = "default"

	// NodePoolNameSuffix is the suffix appended to XTrinode name for node pool name
	NodePoolNameSuffix = "-pool"

	// NodePoolTemplateSuffix is the suffix appended to node pool name for template name
	NodePoolTemplateSuffix = "-template"
)

// NodePoolAPIVersions defines CAPI API versions
const (
	// NodePoolCAPIAPIVersion is the CAPI API version for MachineDeployment/MachinePool
	NodePoolCAPIAPIVersion = "cluster.x-k8s.io/v1beta1"

	// NodePoolInfrastructureAPIVersion is the infrastructure API version for provider templates
	NodePoolInfrastructureAPIVersion = "infrastructure.cluster.x-k8s.io/v1beta1"
)

// NodePoolVolumeTypes defines default volume types per provider
const (
	// NodePoolAzureOSDiskType is the default Azure OS disk type
	NodePoolAzureOSDiskType = "Premium_LRS"

	// NodePoolAWSVolumeType is the default AWS EBS volume type
	NodePoolAWSVolumeType = "gp3"

	// NodePoolGCPDiskType is the default GCP disk type
	NodePoolGCPDiskType = "pd-standard"
)

// NodePoolLabels defines common labels for node pool resources
const (
	// NodePoolManagedByLabel is the label indicating the resource is managed by xtrinode-operator
	NodePoolManagedByLabel = "app.kubernetes.io/managed-by"

	// NodePoolManagedByValue is the value for the managed-by label
	NodePoolManagedByValue = "xtrinode-operator"
)

// NodePoolAnnotations defines annotation keys for Cluster Autoscaler
const (
	// NodePoolAutoscalerMinSizeAnnotation is the annotation key for minimum node group size
	NodePoolAutoscalerMinSizeAnnotation = "cluster-autoscaler/node-group-min-size"

	// NodePoolAutoscalerMaxSizeAnnotation is the annotation key for maximum node group size
	NodePoolAutoscalerMaxSizeAnnotation = "cluster-autoscaler/node-group-max-size"
)

// NodePoolClusterDiscovery defines cluster name discovery configuration
const (
	// NodePoolConfigMapName is the name of the ConfigMap storing node pool configuration
	NodePoolConfigMapName = "xtrinode-operator-config"

	// NodePoolConfigMapClusterNameKey is the key in ConfigMap storing cluster name
	NodePoolConfigMapClusterNameKey = "clusterName"

	// NodePoolEnvClusterName is the environment variable name for cluster name
	NodePoolEnvClusterName = "CLUSTER_NAME"
)

// NodePoolCommonClusterNames defines common cluster names to try during discovery
var NodePoolCommonClusterNames = []string{"default", "cluster", "management-cluster"}

// NodePoolProvisioningTimeouts defines timeout values for node pool provisioning
const (
	// NodePoolProvisioningErrorRequeueInterval is the requeue interval when node pool provisioning fails
	NodePoolProvisioningErrorRequeueInterval = 60 * time.Second

	// NodePoolResourceNotFoundRequeueInterval is the requeue interval when node pool resource is not found yet
	NodePoolResourceNotFoundRequeueInterval = 10 * time.Second

	// NodePoolStatusNotAvailableRequeueInterval is the requeue interval when node pool status is not available yet
	NodePoolStatusNotAvailableRequeueInterval = 10 * time.Second

	// NodePoolNoNodesReadyRequeueInterval is the requeue interval when no nodes are ready yet
	NodePoolNoNodesReadyRequeueInterval = 30 * time.Second

	// NodePoolNodesReadyRequeueInterval is the requeue interval when some nodes are ready but waiting for more
	NodePoolNodesReadyRequeueInterval = 10 * time.Second

	// NodePoolProvisioningTimeout is the maximum time to wait for node pool provisioning before giving up
	// This is used to prevent infinite waiting if provisioning fails silently
	NodePoolProvisioningTimeout = 30 * time.Minute

	// NodePoolMinRequiredReplicasWhenMinNodesZero is the minimum number of ready replicas required when minNodes=0
	// This ensures at least one node is available for Cluster Autoscaler to work
	NodePoolMinRequiredReplicasWhenMinNodesZero = 1
)

// RolloutPolicyDefaults defines operator-level defaults for deployment rollout policy
// Note: Versioning is handled at XTrinode level via revision, these are rollout mechanics
const (
	// DeploymentDefaultRevisionHistoryLimit is the default number of old ReplicaSets to retain
	DeploymentDefaultRevisionHistoryLimit = 10

	// DeploymentDefaultMaxSurge is the default maxSurge for rolling updates (as percentage string)
	DeploymentDefaultMaxSurge = "25%"

	// DeploymentDefaultMaxUnavailable is the default maxUnavailable for rolling updates (as percentage string)
	DeploymentDefaultMaxUnavailable = "25%"
)

// RolloutPolicyConfigMapKeys defines ConfigMap keys for operator-level rollout policy
const (
	// DeploymentConfigMapRevisionHistoryLimitKey is the ConfigMap key for revision history limit
	DeploymentConfigMapRevisionHistoryLimitKey = "deploymentRevisionHistoryLimit"

	// DeploymentConfigMapMaxSurgeKey is the ConfigMap key for maxSurge
	DeploymentConfigMapMaxSurgeKey = "deploymentMaxSurge"

	// DeploymentConfigMapMaxUnavailableKey is the ConfigMap key for maxUnavailable
	DeploymentConfigMapMaxUnavailableKey = "deploymentMaxUnavailable"
)

// RolloutPolicyEnvVars defines environment variable names for operator-level rollout policy
const (
	// DeploymentEnvRevisionHistoryLimit is the environment variable name for revision history limit
	DeploymentEnvRevisionHistoryLimit = "DEPLOYMENT_REVISION_HISTORY_LIMIT"

	// DeploymentEnvMaxSurge is the environment variable name for maxSurge
	DeploymentEnvMaxSurge = "DEPLOYMENT_MAX_SURGE"

	// DeploymentEnvMaxUnavailable is the environment variable name for maxUnavailable
	DeploymentEnvMaxUnavailable = "DEPLOYMENT_MAX_UNAVAILABLE"
)

// ExternalOperationTimeouts defines timeout durations for external operations
const (
	// DefaultTimeout is the default timeout for external calls
	DefaultTimeout = 30 * time.Second

	// GatewayTimeout is the timeout for gateway operations
	GatewayTimeout = 10 * time.Second

	// NodePoolTimeout is the timeout for node pool operations
	NodePoolTimeout = 30 * time.Second

	// KEDATimeout is the timeout for KEDA operations
	KEDATimeout = 10 * time.Second
)
