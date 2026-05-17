package gateway

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var gatewayMetricFactory = promauto.With(crmetrics.Registry)

var (
	// Redis sticky routing metrics
	gatewayRedisOperationsTotal = gatewayMetricFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_redis_operations_total",
			Help: "Total number of Redis operations by type and result",
		},
		[]string{"operation", "result"}, // set/get/delete, success/error
	)

	gatewayRedisHitsTotal = gatewayMetricFactory.NewCounter(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_redis_hits_total",
			Help: "Total number of Redis cache hits for sticky routing",
		},
	)

	gatewayRedisMissesTotal = gatewayMetricFactory.NewCounter(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_redis_misses_total",
			Help: "Total number of Redis cache misses for sticky routing",
		},
	)

	gatewayRedisErrorsTotal = gatewayMetricFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_redis_errors_total",
			Help: "Total number of Redis errors by operation",
		},
		[]string{"operation"}, // set/get/delete/marshal/unmarshal
	)

	gatewayRedisFallbackTotal = gatewayMetricFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_redis_fallback_total",
			Help: "Total number of times Redis fallback was triggered",
		},
		[]string{"reason"}, // connection_error/timeout/etc
	)

	gatewayFallbackCacheHitsTotal = gatewayMetricFactory.NewCounter(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_fallback_cache_hits_total",
			Help: "Total number of fallback cache hits (when Redis unavailable)",
		},
	)

	gatewayFallbackCacheMissesTotal = gatewayMetricFactory.NewCounter(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_fallback_cache_misses_total",
			Help: "Total number of fallback cache misses",
		},
	)

	// API server resume call metrics
	gatewayResumeAPICallsTotal = gatewayMetricFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xtrinode_gateway_resume_api_calls_total",
			Help: "Total number of resume API calls to API server by status",
		},
		[]string{"status"}, // 202/503/error
	)

	gatewayResumeAPICallDuration = gatewayMetricFactory.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "xtrinode_gateway_resume_api_call_duration_seconds",
			Help:    "Duration of resume API calls to API server",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0},
		},
	)

	gatewayInflightQueries = gatewayMetricFactory.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "xtrinode_gateway_inflight_queries",
			Help: "Current non-terminal Trino queries observed by the gateway, labeled by XTrinode and state",
		},
		[]string{"namespace", "xtrinode", "routing_group", "state"},
	)
)
