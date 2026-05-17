package apiserver

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// resumeRequestsTotal tracks total resume requests by result
	resumeRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_api_resume_requests_total",
			Help: "Total number of resume requests by result type",
		},
		[]string{"result"}, // success, lease_blocked, error
	)

	// suspendRequestsTotal tracks total suspend requests by result
	suspendRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_api_suspend_requests_total",
			Help: "Total number of suspend requests by result type",
		},
		[]string{"result"}, // success, lease_blocked, error
	)

	// k8sUpdatesTotal tracks K8s API writes
	k8sUpdatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_api_k8s_updates_total",
			Help: "Total number of K8s API updates by operation",
		},
		[]string{"operation"}, // resume, suspend, create, delete
	)

	// leaseDurationSeconds tracks configured lease durations
	leaseDurationSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_api_lease_duration_seconds",
			Help: "Configured lease duration in seconds by operation",
		},
		[]string{"operation"}, // resume, suspend
	)

	// requestDuration tracks request latency
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xtrinode_api_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "result"},
	)

	// k8sLeaseAcquiredTotal tracks K8s Lease acquisitions for resume gating
	k8sLeaseAcquiredTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_api_k8s_lease_acquired_total",
			Help: "Total number of K8s Lease acquisitions by key type",
		},
		[]string{"key_type"}, // runtime, pool
	)

	// k8sLeaseGatedTotal tracks requests gated by K8s Lease
	k8sLeaseGatedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_api_k8s_lease_gated_total",
			Help: "Total number of requests gated by active K8s Lease",
		},
		[]string{"key_type"}, // runtime, pool
	)
)

// recordResumeRequest records a resume request metric
func recordResumeRequest(result string) {
	resumeRequestsTotal.WithLabelValues(result).Inc()
}

// recordSuspendRequest records a suspend request metric
func recordSuspendRequest(result string) {
	suspendRequestsTotal.WithLabelValues(result).Inc()
}

// recordK8sUpdate records a K8s API update
func recordK8sUpdate(operation string) {
	k8sUpdatesTotal.WithLabelValues(operation).Inc()
}

// setLeaseDuration sets the lease duration gauge for an operation
func setLeaseDuration(operation string, seconds float64) {
	leaseDurationSeconds.WithLabelValues(operation).Set(seconds)
}

// observeRequestDuration records request duration
func observeRequestDuration(operation, result string, seconds float64) {
	requestDuration.WithLabelValues(operation, result).Observe(seconds)
}

// recordK8sLeaseAcquired records a K8s Lease acquisition
func recordK8sLeaseAcquired(keyType string) {
	k8sLeaseAcquiredTotal.WithLabelValues(keyType).Inc()
}

// recordK8sLeaseGated records a request gated by K8s Lease
func recordK8sLeaseGated(keyType string) {
	k8sLeaseGatedTotal.WithLabelValues(keyType).Inc()
}
