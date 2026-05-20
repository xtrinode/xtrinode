package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestSharedCollectorsRegistered(t *testing.T) {
	seedSharedCollectors()

	families, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather shared metrics registry: %v", err)
	}

	got := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		got[family.GetName()] = family
	}

	expected := []string{
		"xtrinode_reconcile_total",
		"xtrinode_reconcile_duration_seconds",
		"xtrinode_created_total",
		"xtrinode_suspended_total",
		"xtrinode_resumed_total",
		"xtrinode_deleted_total",
		"xtrinode_drain_active",
		"xtrinode_drain_duration_seconds",
		"xtrinode_drain_failures_total",
		"xtrinode_workers_current",
		"xtrinode_workers_desired",
		"xtrinode_scale_up_total",
		"xtrinode_scale_down_total",
		"gateway_requests_total",
		"gateway_503_total",
		"gateway_auto_resume_total",
		"gateway_request_duration_seconds",
		"xtrinode_keda_scaledobject_created_total",
		"xtrinode_keda_scaledobject_deleted_total",
		"xtrinode_nodepool_provisioned_total",
		"xtrinode_nodepool_provision_failed_total",
		"xtrinode_reconcile_errors_total",
		"xtrinode_state",
		"xtrinode_condition",
		"xtrinode_reconcile_step_duration_seconds",
		"xtrinode_reconcile_step_errors_total",
		"xtrinode_reconcile_step_total",
		"xtrinode_autosuspend_checks_total",
		"xtrinode_autosuspend_idle_seconds",
		"xtrinode_catalogs_discovered",
		"xtrinode_catalog_sync_errors_total",
		"xtrinode_wakettl_expired_total",
		"xtrinode_wakettl_remaining_seconds",
	}

	for _, name := range expected {
		family, ok := got[name]
		if !ok {
			t.Fatalf("expected metric family %q to be registered", name)
		}
		if len(family.GetMetric()) == 0 {
			t.Fatalf("expected metric family %q to be gatherable", name)
		}
	}
}

func seedSharedCollectors() {
	const (
		namespace    = "metrics-test"
		xtrinodeName = "runtime"
		result       = "success"
		routingGroup = "rg"
	)

	ReconcileTotal.WithLabelValues(namespace, xtrinodeName, result).Add(0)
	ReconcileDuration.WithLabelValues(namespace, xtrinodeName).Observe(0)

	XTrinodeCreated.WithLabelValues(namespace, "s").Add(0)
	XTrinodeSuspended.WithLabelValues(namespace, xtrinodeName).Add(0)
	XTrinodeResumed.WithLabelValues(namespace, xtrinodeName).Add(0)
	XTrinodeDeleted.WithLabelValues(namespace, xtrinodeName).Add(0)
	XTrinodeDrainActive.WithLabelValues(namespace, xtrinodeName).Set(0)
	XTrinodeDrainDuration.WithLabelValues(namespace, xtrinodeName, "query_complete").Observe(0)
	XTrinodeDrainFailures.WithLabelValues(namespace, xtrinodeName, "query_check_error").Add(0)

	WorkersCurrent.WithLabelValues(namespace, xtrinodeName).Set(0)
	WorkersDesired.WithLabelValues(namespace, xtrinodeName).Set(0)
	ScaleUpTotal.WithLabelValues(namespace, xtrinodeName).Add(0)
	ScaleDownTotal.WithLabelValues(namespace, xtrinodeName).Add(0)

	GatewayRequestsTotal.WithLabelValues(routingGroup, "200").Add(0)
	Gateway503Total.WithLabelValues(routingGroup).Add(0)
	GatewayAutoResumeTotal.WithLabelValues(routingGroup).Add(0)
	GatewayRequestDuration.WithLabelValues(routingGroup).Observe(0)

	KEDAScaledObjectCreated.WithLabelValues(namespace, xtrinodeName).Add(0)
	KEDAScaledObjectDeleted.WithLabelValues(namespace, xtrinodeName).Add(0)

	NodePoolProvisioned.WithLabelValues(namespace, xtrinodeName, "gcp").Add(0)
	NodePoolProvisionFailed.WithLabelValues(namespace, xtrinodeName, "gcp").Add(0)

	ReconcileErrors.WithLabelValues(namespace, xtrinodeName, "test").Add(0)
	XTrinodeState.WithLabelValues(namespace, xtrinodeName, "Ready").Set(2)
	XTrinodeCondition.WithLabelValues(namespace, xtrinodeName, "Ready").Set(1)

	ReconcileStepDuration.WithLabelValues(namespace, xtrinodeName, "test-step").Observe(0)
	ReconcileStepErrors.WithLabelValues(namespace, xtrinodeName, "test-step").Add(0)
	ReconcileStepTotal.WithLabelValues(namespace, xtrinodeName, "test-step", result).Add(0)

	AutoSuspendChecks.WithLabelValues(namespace, xtrinodeName, "skipped").Add(0)
	AutoSuspendIdleTime.WithLabelValues(namespace, xtrinodeName).Set(0)

	CatalogsDiscovered.WithLabelValues(namespace, xtrinodeName).Set(0)
	CatalogSyncErrors.WithLabelValues(namespace, xtrinodeName).Add(0)

	WakeTTLExpired.WithLabelValues(namespace, xtrinodeName).Add(0)
	WakeTTLRemaining.WithLabelValues(namespace, xtrinodeName).Set(0)
}
