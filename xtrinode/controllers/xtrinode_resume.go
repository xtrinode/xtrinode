package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/external"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

// reconcileResume handles resuming a XTrinode
func (r *XTrinodeReconciler) reconcileResume(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)

	// First-time resume from a suspended/suspending state, or from a queued
	// wake command whose status phase has not caught up to the suspend yet.
	if shouldRunResumeTransition(xtrinode) {
		log.Info("Resuming XTrinode", "xtrinode", xtrinode.Name, "phase", xtrinode.Status.Phase)

		// Parse wake parameters: annotations (one-time override) or spec (defaults)
		wakeMinWorkers, wakeTTL := parseWakeParams(xtrinode, log)

		// Set status.wake (ephemeral wake window state)
		// This is the single source of truth for wake behavior
		if wakeMinWorkers > 0 {
			expiresAt := metav1.NewTime(time.Now().Add(wakeTTL))
			xtrinode.Status.Wake = &analyticsv1.WakeState{
				MinWorkers: wakeMinWorkers,
				ExpiresAt:  expiresAt,
			}
			log.Info("Set wake window", "minWorkers", wakeMinWorkers, "expiresAt", expiresAt.Time)
		} else if xtrinode.Status.Wake != nil {
			// Clear any stale wake from a previous session before calculating
			// the KEDA floor for this resume.
			log.Info("Clearing stale wake state from previous session")
			xtrinode.Status.Wake = nil
		}
		if xtrinode.Status.Phase != string(status.PhaseResuming) {
			currentPhase := status.Phase(xtrinode.Status.Phase)
			if err := currentPhase.TransitionTo(status.PhaseResuming); err != nil {
				log.Error(err, "invalid phase transition", "from", currentPhase, "to", "Resuming")
			}
			xtrinode.Status.Phase = string(status.PhaseResuming)
			r.updateStateMetrics(xtrinode)
			status.SetCondition(xtrinode, status.ConditionTypeReconciling, metav1.ConditionTrue, status.ConditionReasonReconciling, "Resuming XTrinode")
			if err := r.updateStatus(ctx, xtrinode, log); err != nil {
				log.Error(err, "unable to update XTrinode status to Resuming")
			}
		}

		// Scale node pool back up if it was scaled down.
		if xtrinode.Spec.NodePool != nil {
			scaleDownOnSuspend := true // Default: scale down
			if xtrinode.Spec.NodePool.ScaleDownOnSuspend != nil {
				scaleDownOnSuspend = *xtrinode.Spec.NodePool.ScaleDownOnSuspend
			}
			if scaleDownOnSuspend {
				// Restore original minNodes directly from CRD spec (source of truth)
				originalMinNodes := int32(0)
				if xtrinode.Spec.NodePool.MinNodes != nil {
					originalMinNodes = *xtrinode.Spec.NodePool.MinNodes
				}

				// Scale node pool back to original minNodes from CRD spec
				err := external.CallWithTimeout(ctx, config.NodePoolTimeout, func(ctx context.Context) error {
					return r.NodePoolAdapter.ScaleNodePoolMinNodes(ctx, xtrinode, originalMinNodes)
				})
				if err != nil {
					log.Error(err, "failed to scale node pool up on resume")
					// Continue - not critical, will retry on next reconciliation
				} else {
					log.Info("Scaled node pool back up on resume", "xtrinode", xtrinode.Name, "minNodes", originalMinNodes)
				}
			}
		}

		// Scale coordinator, and seed workers only when the selected scaler needs
		// a non-zero target before it can take ownership.
		if err := r.scaleForResume(ctx, xtrinode, wakeMinWorkers); err != nil {
			return err
		}

		// Clear wake annotations after scaling succeeds so one-time wake
		// overrides survive retries when scaling fails.
		if xtrinode.Annotations != nil &&
			(xtrinode.Annotations[config.WakeMinWorkersAnnotation] != "" ||
				xtrinode.Annotations[config.WakeTTLAnnotation] != "") {

			// Patch refreshes the object from the server before status is persisted,
			// so preserve in-memory wake state across the metadata patch.
			savedWake := xtrinode.Status.Wake
			savedLastActivity := xtrinode.Status.LastActivity

			base := xtrinode.DeepCopy()
			delete(xtrinode.Annotations, config.WakeMinWorkersAnnotation)
			delete(xtrinode.Annotations, config.WakeTTLAnnotation)
			if patchErr := r.Patch(ctx, xtrinode, client.MergeFrom(base)); patchErr != nil {
				log.Error(patchErr, "failed to patch wake annotations - they may be reused on next resume")
				// Non-fatal: resume proceeds; worst case we reuse same wake params once more
			}

			// Restore in-memory status fields that were overwritten by the Patch
			// server response (status hasn't been persisted yet at this point)
			xtrinode.Status.Wake = savedWake
			xtrinode.Status.LastActivity = savedLastActivity
		}

		// Reset LastActivity to now on resume. Without this, the stale
		// pre-suspend LastActivity includes the entire suspend duration in idle time,
		// causing immediate auto-suspend on the first reconcile after resume when no
		// wake window is configured (wakeMinWorkers=0).
		now := metav1.Now()
		xtrinode.Status.LastActivity = &now

		// Update status - clear Suspended condition and set Ready to True
		status.SetConditionWithEvents(xtrinode, status.ConditionTypeSuspended, metav1.ConditionFalse, status.ConditionReasonNotSuspended, "XTrinode is not suspended", r.EventRecorder)
		status.SetConditionWithEvents(xtrinode, status.ConditionTypeReady, metav1.ConditionTrue, status.ConditionReasonAllComponentsReady, "XTrinode resumed", r.EventRecorder)

		// Update condition metrics
		r.updateConditionMetrics(xtrinode)

		// Record resume event and metrics
		wakeTTLDuration := time.Duration(0)
		if xtrinode.Status.Wake != nil {
			wakeTTLDuration = time.Until(xtrinode.Status.Wake.ExpiresAt.Time)
		}
		r.EventRecorder.Normalf(xtrinode, events.ReasonResumed, "XTrinode resumed with wake window (minWorkers: %d, TTL: %v)", wakeMinWorkers, wakeTTLDuration)
		metrics.XTrinodeResumed.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
	}

	// Always enforce resumed state when not suspended for drift repair.
	// This ensures coordinator >= 1 even if someone manually scaled it to 0.
	if !xtrinode.Spec.Suspended {
		if err := r.ensureResumedInvariants(ctx, xtrinode); err != nil {
			log.Error(err, "failed to enforce resumed state invariants")
			return err
		}
	}

	return nil
}

func shouldRunResumeTransition(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Status.Phase == status.PhaseSuspended.String() ||
		xtrinode.Status.Phase == status.PhaseSuspending.String() {
		return true
	}
	return hasWakeCommandAnnotations(xtrinode)
}

func hasWakeCommandAnnotations(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Annotations == nil {
		return false
	}
	return xtrinode.Annotations[config.WakeMinWorkersAnnotation] != "" ||
		xtrinode.Annotations[config.WakeTTLAnnotation] != ""
}

// parseWakeParams reads wake parameters from annotations (one-time override) then falls back to spec defaults.
func parseWakeParams(xtrinode *analyticsv1.XTrinode, log logr.Logger) (wakeMinWorkers int32, wakeTTL time.Duration) {
	wakeMinWorkers = 0
	wakeTTL = 5 * time.Minute

	// Track annotation presence separately from values because 0 and 5m are
	// valid explicit inputs as well as defaults.
	workersFromAnnotation := false
	ttlFromAnnotation := false

	if xtrinode.Annotations != nil {
		if wakeMinStr := xtrinode.Annotations[config.WakeMinWorkersAnnotation]; wakeMinStr != "" {
			if val, err := strconv.ParseInt(wakeMinStr, 10, 32); err == nil && val >= 0 {
				wakeMinWorkers = int32(val)
				workersFromAnnotation = true
				log.Info("Using wake parameters from command", "wakeMinWorkers", wakeMinWorkers)
			}
		}
		if wakeTTLStr := xtrinode.Annotations[config.WakeTTLAnnotation]; wakeTTLStr != "" {
			if duration, err := time.ParseDuration(wakeTTLStr); err == nil && duration >= 0 {
				wakeTTL = duration
				ttlFromAnnotation = true
				log.Info("Using wake TTL from command", "wakeTTL", wakeTTL)
			}
		}
	}
	if !workersFromAnnotation && xtrinode.Spec.WakeMinWorkers != nil {
		wakeMinWorkers = *xtrinode.Spec.WakeMinWorkers
	}
	if !ttlFromAnnotation && xtrinode.Spec.WakeTTL != nil {
		wakeTTL = xtrinode.Spec.WakeTTL.Duration
	}
	return wakeMinWorkers, wakeTTL
}

// scaleForResume scales the coordinator during a resume transition.
// Worker scale remains externally owned, except that native HPA cannot recover
// a Deployment whose scale target is already zero, so the operator seeds it to
// the configured native HPA floor once.
// Records an error status update and event if scaling fails.
func (r *XTrinodeReconciler) scaleForResume(ctx context.Context, xtrinode *analyticsv1.XTrinode, wakeMinWorkers int32) error {
	log := ctrl.LoggerFrom(ctx)
	const coordReplicas = int32(1)
	if isKEDAEnabled(xtrinode) {
		// Autoscaler enabled - only scale coordinator, never touch workers.
		if err := r.scaleCoordinatorOnly(ctx, xtrinode, coordReplicas); err != nil {
			log.Error(err, "failed to scale coordinator for resume")
			r.EventRecorder.Warningf(xtrinode, events.ReasonResumeFailed, "Failed to resume XTrinode: %v", err)
			if updateErr := setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonResumeFailed, fmt.Sprintf("Failed to resume XTrinode: %v", err), r.EventRecorder); updateErr != nil {
				log.Error(updateErr, "failed to update error status")
			}
			return err
		}
		return nil
	}
	if isNativeHPAEnabled(xtrinode) {
		if err := r.scaleCoordinatorOnly(ctx, xtrinode, coordReplicas); err != nil {
			log.Error(err, "failed to scale coordinator for resume")
			r.EventRecorder.Warningf(xtrinode, events.ReasonResumeFailed, "Failed to resume XTrinode: %v", err)
			if updateErr := setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonResumeFailed, fmt.Sprintf("Failed to resume XTrinode: %v", err), r.EventRecorder); updateErr != nil {
				log.Error(updateErr, "failed to update error status")
			}
			return err
		}
		if err := r.ensureNativeHPAWorkerFloor(ctx, xtrinode); err != nil {
			log.Error(err, "failed to seed native HPA worker floor for resume")
			r.EventRecorder.Warningf(xtrinode, events.ReasonResumeFailed, "Failed to resume XTrinode: %v", err)
			if updateErr := setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonResumeFailed, fmt.Sprintf("Failed to resume XTrinode: %v", err), r.EventRecorder); updateErr != nil {
				log.Error(updateErr, "failed to update error status")
			}
			return err
		}
		return nil
	}
	// No autoscaler - controller scales both coordinator and workers.
	if err := r.scaleDeployments(ctx, xtrinode, coordReplicas, wakeMinWorkers); err != nil {
		log.Error(err, "failed to resume XTrinode")
		r.EventRecorder.Warningf(xtrinode, events.ReasonResumeFailed, "Failed to resume XTrinode: %v", err)
		if updateErr := setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonResumeFailed, fmt.Sprintf("Failed to resume XTrinode: %v", err), r.EventRecorder); updateErr != nil {
			log.Error(updateErr, "failed to update error status")
		}
		return err
	}
	return nil
}

// ensureResumedInvariants enforces that coordinator is running when not suspended
// This provides self-healing if deployments drift to 0 replicas
// IMPORTANT: Only scales coordinator, plus a native HPA worker bootstrap when
// the target is zero. KEDA can scale from zero and remains untouched.
func (r *XTrinodeReconciler) ensureResumedInvariants(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)

	// Get expected invariants for ready/resumed state
	inv := status.GetInvariants(status.PhaseReady, xtrinode)

	// Check coordinator deployment
	coordDeployment := &appsv1.Deployment{}
	coordKey := client.ObjectKey{
		Name:      config.BuildCoordinatorDeploymentName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}

	if err := r.Get(ctx, coordKey, coordDeployment); err != nil {
		if k8serrors.IsNotFound(err) {
			// Deployment doesn't exist yet - will be created in reconciliation pipeline
			return nil
		}
		return fmt.Errorf("failed to get coordinator deployment: %w", err)
	}

	// Ensure coordinator matches expected replicas from invariants
	currentReplicas := int32(0)
	if coordDeployment.Spec.Replicas != nil {
		currentReplicas = *coordDeployment.Spec.Replicas
	}

	if currentReplicas != inv.CoordReplicas {
		log.Info("Coordinator replicas drifted - restoring to expected state",
			"xtrinode", xtrinode.Name,
			"current", currentReplicas,
			"expected", inv.CoordReplicas)
		// Only scale coordinator - NEVER touch workers when KEDA may own them
		if err := r.scaleCoordinatorOnly(ctx, xtrinode, inv.CoordReplicas); err != nil {
			return fmt.Errorf("failed to restore coordinator replicas: %w", err)
		}
		r.EventRecorder.Normalf(xtrinode, events.ReasonReconcileComplete,
			"Recovered from drift: coordinator scaled from %d to %d", currentReplicas, inv.CoordReplicas)
	}

	if err := r.ensureNativeHPAWorkerFloor(ctx, xtrinode); err != nil {
		return err
	}

	return nil
}

func (r *XTrinodeReconciler) ensureNativeHPAWorkerFloor(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	floor, ok := nativeHPARequiredWorkers(xtrinode)
	if !ok || floor <= 0 {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	workerDeployment := &appsv1.Deployment{}
	workerKey := client.ObjectKey{
		Name:      config.BuildWorkerDeploymentName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}
	if err := r.Get(ctx, workerKey, workerDeployment); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get worker deployment: %w", err)
	}

	currentReplicas := int32(0)
	if workerDeployment.Spec.Replicas != nil {
		currentReplicas = *workerDeployment.Spec.Replicas
	}
	if currentReplicas > 0 {
		return nil
	}

	log.Info("Native HPA worker target is zero - seeding configured floor",
		"xtrinode", xtrinode.Name,
		"current", currentReplicas,
		"floor", floor)
	if err := r.scaleDeployment(ctx, xtrinode, workerDeployment.Name, floor, "worker", log); err != nil {
		return fmt.Errorf("failed to seed native HPA worker floor: %w", err)
	}
	return nil
}
