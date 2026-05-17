package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// Reconciliation metrics
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_reconcile_total",
			Help: "Total number of XTrinode reconciliations",
		},
		[]string{"namespace", "name", "result"},
	)

	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xtrinode_reconcile_duration_seconds",
			Help:    "Duration of XTrinode reconciliations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "name"},
	)

	// Lifecycle metrics
	XTrinodeCreated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_created_total",
			Help: "Total number of XTrinodes created",
		},
		[]string{"namespace", "size"},
	)

	XTrinodeSuspended = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_suspended_total",
			Help: "Total number of XTrinodes suspended",
		},
		[]string{"namespace", "name"},
	)

	XTrinodeResumed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_resumed_total",
			Help: "Total number of XTrinodes resumed",
		},
		[]string{"namespace", "name"},
	)

	XTrinodeDeleted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_deleted_total",
			Help: "Total number of XTrinodes deleted",
		},
		[]string{"namespace", "name"},
	)

	// Scaling metrics
	WorkersCurrent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_workers_current",
			Help: "Current number of workers for a XTrinode",
		},
		[]string{"namespace", "name"},
	)

	WorkersDesired = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_workers_desired",
			Help: "Desired number of workers for a XTrinode",
		},
		[]string{"namespace", "name"},
	)

	ScaleUpTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_scale_up_total",
			Help: "Total number of scale-up events",
		},
		[]string{"namespace", "name"},
	)

	ScaleDownTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_scale_down_total",
			Help: "Total number of scale-down events",
		},
		[]string{"namespace", "name"},
	)

	// Gateway metrics
	GatewayRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of gateway requests",
		},
		[]string{"routing_group", "status_code"},
	)

	Gateway503Total = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_503_total",
			Help: "Total number of 503 responses from backends",
		},
		[]string{"routing_group"},
	)

	GatewayAutoResumeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_auto_resume_total",
			Help: "Total number of auto-resume events",
		},
		[]string{"routing_group"},
	)

	GatewayRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Duration of gateway requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"routing_group"},
	)

	// KEDA metrics
	KEDAScaledObjectCreated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_keda_scaledobject_created_total",
			Help: "Total number of KEDA ScaledObjects created",
		},
		[]string{"namespace", "name"},
	)

	KEDAScaledObjectDeleted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_keda_scaledobject_deleted_total",
			Help: "Total number of KEDA ScaledObjects deleted",
		},
		[]string{"namespace", "name"},
	)

	// Node pool metrics
	NodePoolProvisioned = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_nodepool_provisioned_total",
			Help: "Total number of node pools provisioned",
		},
		[]string{"namespace", "name", "provider"},
	)

	NodePoolProvisionFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_nodepool_provision_failed_total",
			Help: "Total number of node pool provision failures",
		},
		[]string{"namespace", "name", "provider"},
	)

	// Error metrics
	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_reconcile_errors_total",
			Help: "Total number of reconciliation errors",
		},
		[]string{"namespace", "name", "error_type"},
	)

	// Resource state metrics (gauges for current state)
	XTrinodeState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_state",
			Help: "Current state of XTrinode (0=Unknown, 1=Reconciling, 2=Ready, 3=Suspended, 4=Error)",
		},
		[]string{"namespace", "name", "phase"},
	)

	XTrinodeCondition = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_condition",
			Help: "XTrinode condition status (0=False, 1=True, 2=Unknown)",
		},
		[]string{"namespace", "name", "condition_type"},
	)

	// Pipeline step metrics
	ReconcileStepDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xtrinode_reconcile_step_duration_seconds",
			Help:    "Duration of individual reconciliation steps",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"namespace", "name", "step"},
	)

	ReconcileStepErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_reconcile_step_errors_total",
			Help: "Total errors per reconciliation step",
		},
		[]string{"namespace", "name", "step"},
	)

	ReconcileStepTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_reconcile_step_total",
			Help: "Total executions per reconciliation step",
		},
		[]string{"namespace", "name", "step", "result"},
	)

	// Auto-suspend metrics
	AutoSuspendChecks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_autosuspend_checks_total",
			Help: "Total auto-suspend checks performed",
		},
		[]string{"namespace", "name", "result"},
	)

	AutoSuspendIdleTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_autosuspend_idle_seconds",
			Help: "Current idle time in seconds",
		},
		[]string{"namespace", "name"},
	)

	// Catalog metrics
	CatalogsDiscovered = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_catalogs_discovered",
			Help: "Number of catalogs discovered for XTrinode",
		},
		[]string{"namespace", "name"},
	)

	CatalogSyncErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_catalog_sync_errors_total",
			Help: "Total catalog synchronization errors",
		},
		[]string{"namespace", "name"},
	)

	// WakeTTL metrics
	WakeTTLExpired = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_wakettl_expired_total",
			Help: "Total WakeTTL expirations",
		},
		[]string{"namespace", "name"},
	)

	WakeTTLRemaining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_wakettl_remaining_seconds",
			Help: "Remaining time until WakeTTL expires (seconds)",
		},
		[]string{"namespace", "name"},
	)
)

func init() {
	// Register all metrics with controller-runtime metrics registry
	metrics.Registry.MustRegister(
		// Reconciliation metrics
		ReconcileTotal,
		ReconcileDuration,
		ReconcileErrors,
		// Lifecycle metrics
		XTrinodeCreated,
		XTrinodeSuspended,
		XTrinodeResumed,
		XTrinodeDeleted,
		// Scaling metrics
		WorkersCurrent,
		WorkersDesired,
		ScaleUpTotal,
		ScaleDownTotal,
		// Gateway metrics
		GatewayRequestsTotal,
		Gateway503Total,
		GatewayAutoResumeTotal,
		GatewayRequestDuration,
		// KEDA metrics
		KEDAScaledObjectCreated,
		KEDAScaledObjectDeleted,
		// Node pool metrics
		NodePoolProvisioned,
		NodePoolProvisionFailed,
		// Resource state metrics
		XTrinodeState,
		XTrinodeCondition,
		// Pipeline step metrics
		ReconcileStepDuration,
		ReconcileStepErrors,
		ReconcileStepTotal,
		// Auto-suspend metrics
		AutoSuspendChecks,
		AutoSuspendIdleTime,
		// Catalog metrics
		CatalogsDiscovered,
		CatalogSyncErrors,
		// WakeTTL metrics
		WakeTTLExpired,
		WakeTTLRemaining,
	)
}
