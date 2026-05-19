package controllers

import (
	"context"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

// reconcileWakeTTL checks status.wake expiration and clears it when expired.
// CRITICAL: persists the cleared wake state immediately via a status update so that
// the KEDA step (which runs next in the same reconcile) reads Wake==nil and sets
// minReplicas=0 without needing a second reconcile pass.
func (r *XTrinodeReconciler) reconcileWakeTTL(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)

	if xtrinode.Status.Wake == nil {
		return nil
	}

	now := time.Now()
	if now.Before(xtrinode.Status.Wake.ExpiresAt.Time) {
		remaining := xtrinode.Status.Wake.ExpiresAt.Sub(now)
		log.V(1).Info("Wake window still active", "remaining", remaining)
		metrics.WakeTTLRemaining.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(remaining.Seconds())
		return nil
	}

	log.Info("Wake window expired, clearing status.wake",
		"minWorkers", xtrinode.Status.Wake.MinWorkers,
		"expiredAt", xtrinode.Status.Wake.ExpiresAt.Time)

	xtrinode.Status.Wake = nil
	metrics.WakeTTLRemaining.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(0)

	// Persist the cleared wake immediately so KEDA reads Wake==nil in this same reconcile.
	if err := r.updateStatus(ctx, xtrinode, log); err != nil {
		// Non-fatal: invariants also check ExpiresAt as a secondary guard.
		// Return the error so the step logs it; pipeline will continue.
		return fmt.Errorf("wake expired but failed to persist cleared status.wake: %w", err)
	}
	return nil
}

// reconcileAutoSuspend checks auto-suspend conditions
func (r *XTrinodeReconciler) reconcileAutoSuspend(ctx context.Context, xtrinode *analyticsv1.XTrinode) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	// Note: This checks idle time, but actual query activity should be monitored
	// via Prometheus metrics in a separate goroutine or controller
	if xtrinode.Spec.AutoSuspendAfter != nil && !xtrinode.Spec.Suspended {
		suspended, err := r.AutosuspendService.AutoSuspendIfNeeded(ctx, xtrinode, log)
		if err != nil {
			log.Error(err, "failed to check auto-suspend")
			// Continue even if auto-suspend check fails
			return false, err
		}
		return suspended, nil
	}
	return false, nil
}
