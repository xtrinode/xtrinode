package events

// Event reasons for XTrinode lifecycle
const (
	// Lifecycle events
	ReasonCreated   = "Created"
	ReasonUpdated   = "Updated"
	ReasonDeleted   = "Deleted"
	ReasonFinalized = "Finalized"

	// Reconciliation events
	ReasonReconciling       = "Reconciling"
	ReasonReconcileComplete = "ReconcileComplete"
	ReasonReconcileError    = "ReconcileError"
	ReasonReconcileRetry    = "ReconcileRetry"

	// Resource management events
	ReasonResourceCreated     = "ResourceCreated"
	ReasonResourceUpdated     = "ResourceUpdated"
	ReasonResourceDeleted     = "ResourceDeleted"
	ReasonResourceApplyFailed = "ResourceApplyFailed"
	ReasonResourceConflict    = "ResourceConflict"

	// Scaling events
	ReasonWorkersScaledUp   = "WorkersScaledUp"
	ReasonWorkersScaledDown = "WorkersScaledDown"
	ReasonCoordinatorScaled = "CoordinatorScaled"

	// Suspend/Resume events
	ReasonSuspended        = "Suspended"
	ReasonResumed          = "Resumed"
	ReasonSuspendRequested = "SuspendRequested"
	ReasonResumeRequested  = "ResumeRequested"
	ReasonSuspendFailed    = "SuspendFailed"
	ReasonResumeFailed     = "ResumeFailed"

	// KEDA events
	ReasonKEDACreated = "KEDACreated"
	ReasonKEDAUpdated = "KEDAUpdated"
	ReasonKEDADeleted = "KEDADeleted"
	ReasonKEDAError   = "KEDAError"
	ReasonKEDAScaled  = "KEDAScaled"

	// Node pool events
	ReasonNodePoolProvisioning       = "NodePoolProvisioning"
	ReasonNodePoolProvisioned        = "NodePoolProvisioned"
	ReasonNodePoolProvisionFailed    = "NodePoolProvisionFailed"
	ReasonNodePoolReady              = "NodePoolReady"
	ReasonNodePoolDeletionStarted    = "NodePoolDeletionStarted"
	ReasonNodePoolDeleted            = "NodePoolDeleted"
	ReasonNodePoolDeleteFailed       = "NodePoolDeleteFailed"
	ReasonNodePoolRetained           = "NodePoolRetained"
	ReasonNodePoolRetainFailed       = "NodePoolRetainFailed"
	ReasonNodePoolScaleToZeroStarted = "NodePoolScaleToZeroStarted"
	ReasonNodePoolScaledToZero       = "NodePoolScaledToZero"
	ReasonNodePoolScaleToZeroFailed  = "NodePoolScaleToZeroFailed"

	// Gateway events
	ReasonGatewayRouteRegistered   = "GatewayRouteRegistered"
	ReasonGatewayRouteUnregistered = "GatewayRouteUnregistered"
	ReasonGatewayError             = "GatewayError"

	// Catalog events
	ReasonCatalogsDiscovered = "CatalogsDiscovered"
	ReasonCatalogSyncFailed  = "CatalogSyncFailed"

	// Namespace and guardrails events
	ReasonNamespaceCreated           = "NamespaceCreated"
	ReasonNamespaceGuardrailsApplied = "NamespaceGuardrailsApplied"
	ReasonResourceQuotaApplied       = "ResourceQuotaApplied"
	ReasonLimitRangeApplied          = "LimitRangeApplied"
	ReasonNamespaceGuardrailsFailed  = "NamespaceGuardrailsFailed"

	// Trino resources events
	ReasonTrinoResourcesBuilding = "TrinoResourcesBuilding"
	ReasonTrinoResourcesApplied  = "TrinoResourcesApplied"
	ReasonTrinoResourcesFailed   = "TrinoResourcesFailed"
	ReasonCoordinatorDeployed    = "CoordinatorDeployed"
	ReasonWorkerDeployed         = "WorkerDeployed"
	ReasonServiceCreated         = "ServiceCreated"
	ReasonConfigMapApplied       = "ConfigMapApplied"

	// Auto-suspend events
	ReasonAutoSuspendChecking  = "AutoSuspendChecking"
	ReasonAutoSuspendTriggered = "AutoSuspendTriggered"
	ReasonAutoSuspendSkipped   = "AutoSuspendSkipped"
	ReasonAutoSuspendFailed    = "AutoSuspendFailed"

	// WakeTTL events
	ReasonWakeTTLExpired = "WakeTTLExpired"
	ReasonWakeTTLReset   = "WakeTTLReset"

	// Pipeline step events
	ReasonStepStarted   = "StepStarted"
	ReasonStepCompleted = "StepCompleted"
	ReasonStepFailed    = "StepFailed"
	ReasonStepSkipped   = "StepSkipped"

	// Finalizer events
	ReasonFinalizerAdded   = "FinalizerAdded"
	ReasonFinalizerRemoved = "FinalizerRemoved"
	ReasonDrainStarted     = "DrainStarted"
	ReasonDrainCompleted   = "DrainCompleted"
	ReasonCleanupStarted   = "CleanupStarted"
	ReasonCleanupCompleted = "CleanupCompleted"

	// Status events
	ReasonStatusUpdated = "StatusUpdated"
	ReasonPhaseChanged  = "PhaseChanged"

	// Condition transition events
	ReasonConditionReadyTrue      = "ConditionReadyTrue"      // Ready condition became True
	ReasonConditionReadyFalse     = "ConditionReadyFalse"     // Ready condition became False
	ReasonConditionErrorTrue      = "ConditionErrorTrue"      // Error condition became True
	ReasonConditionErrorFalse     = "ConditionErrorFalse"     // Error condition became False
	ReasonConditionSuspendedTrue  = "ConditionSuspendedTrue"  // Suspended condition became True
	ReasonConditionSuspendedFalse = "ConditionSuspendedFalse" // Suspended condition became False
)
