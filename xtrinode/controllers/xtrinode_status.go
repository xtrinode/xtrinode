package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/internal/trino/controlendpoint"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

// updateStatus updates the XTrinode status with retry logic for conflict handling
// Captures current status state and reapplies it after refresh to avoid losing changes
func (r *XTrinodeReconciler) updateStatus(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	// Capture ENTIRE status to reapply after refresh
	// This prevents losing fields like CoordinatorURL, Workers, CurrentRevision, ObservedCatalogs, etc.
	capturedStatus := xtrinode.Status.DeepCopy()
	key := client.ObjectKeyFromObject(xtrinode)

	return updateStatusWithRetry(ctx, r.Client, r.Status(), key, log,
		func() client.Object { return &analyticsv1.XTrinode{} },
		func(obj client.Object) error {
			t, ok := obj.(*analyticsv1.XTrinode)
			if !ok {
				return fmt.Errorf("unexpected object type %T", obj)
			}
			freshLastActivity := t.Status.LastActivity
			// Reapply entire captured status
			t.Status = *capturedStatus
			if freshLastActivity != nil &&
				(t.Status.LastActivity == nil || freshLastActivity.After(t.Status.LastActivity.Time)) {
				t.Status.LastActivity = freshLastActivity
			}
			return nil
		})
}

func (r *XTrinodeReconciler) markSuspendedStatus(xtrinode *analyticsv1.XTrinode) {
	xtrinode.Status.Phase = string(status.PhaseSuspended)
	xtrinode.Status.ObservedGeneration = xtrinode.Generation
	xtrinode.Status.CoordinatorURL = controlendpoint.CoordinatorURL(xtrinode)
	xtrinode.Status.Workers = 0

	r.updateStateMetrics(xtrinode)
	status.SetCondition(xtrinode, status.ConditionTypeSuspended, metav1.ConditionTrue, status.ConditionReasonSuspended, "XTrinode suspended")
	status.SetCondition(xtrinode, status.ConditionTypeReady, metav1.ConditionFalse, status.ConditionReasonSuspended, "XTrinode is suspended")
	status.SetCondition(xtrinode, status.ConditionTypeReconciling, metav1.ConditionFalse, status.ConditionReasonSuspended, "XTrinode is suspended")
	status.SetCondition(xtrinode, status.ConditionTypeKEDAReady, metav1.ConditionFalse, "KEDADisabled", "KEDA disabled while suspended")
	status.SetConditionWithEvents(xtrinode, status.ConditionTypeError, metav1.ConditionFalse, status.ConditionReasonNoError, "No errors", r.EventRecorder)
	r.updateConditionMetrics(xtrinode)
}

// calculateRequeueInterval determines if periodic reconciliation is needed
// Returns 0 if no requeue needed, otherwise returns the requeue interval
func (r *XTrinodeReconciler) calculateRequeueInterval(xtrinode *analyticsv1.XTrinode) time.Duration {
	var minInterval time.Duration = 0

	// Check if autosuspend is configured
	if xtrinode.Spec.AutoSuspendAfter != nil && !xtrinode.Spec.Suspended {
		// Requeue periodically to check for autosuspend
		minInterval = config.ReconcileRequeueIntervalAutosuspend
	}

	// Check if there's an ACTIVE wake window (status.wake exists)
	// Only requeue when wake window is active, not just because spec has wake params
	if xtrinode.Status.Wake != nil {
		remaining := time.Until(xtrinode.Status.Wake.ExpiresAt.Time)
		if remaining > 0 {
			// Wake window is active - requeue to check expiry
			if minInterval == 0 || remaining < minInterval {
				minInterval = remaining
			}
		}
	}

	return minInterval
}

// updateStateMetrics updates Prometheus metrics for XTrinode state
func (r *XTrinodeReconciler) updateStateMetrics(xtrinode *analyticsv1.XTrinode) {
	// Map phase to numeric value for gauge
	var stateValue float64
	switch xtrinode.Status.Phase {
	case "Reconciling":
		stateValue = 1
	case "Ready":
		stateValue = 2
	case "Suspended":
		stateValue = 3
	case "Error":
		stateValue = 4
	default:
		stateValue = 0 // Unknown
	}

	// Set state gauge with phase label
	metrics.XTrinodeState.WithLabelValues(
		xtrinode.Namespace,
		xtrinode.Name,
		xtrinode.Status.Phase,
	).Set(stateValue)
}

// updateConditionMetrics updates Prometheus metrics for XTrinode conditions
func (r *XTrinodeReconciler) updateConditionMetrics(xtrinode *analyticsv1.XTrinode) {
	// Update condition metrics for each condition type
	conditionTypes := []string{
		status.ConditionTypeReady,
		status.ConditionTypeReconciling,
		status.ConditionTypeSuspended,
		status.ConditionTypeError,
	}

	for _, condType := range conditionTypes {
		cond := status.GetCondition(xtrinode, condType)
		var value float64
		if cond == nil {
			value = 2 // Unknown
		} else {
			switch cond.Status {
			case metav1.ConditionTrue:
				value = 1
			case metav1.ConditionFalse:
				value = 0
			default:
				value = 2 // Unknown
			}
		}

		metrics.XTrinodeCondition.WithLabelValues(
			xtrinode.Namespace,
			xtrinode.Name,
			condType,
		).Set(value)
	}
}
