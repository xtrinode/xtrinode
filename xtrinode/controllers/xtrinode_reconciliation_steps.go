package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/pkg/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ReconciliationStep represents a single step in the reconciliation pipeline
type ReconciliationStep interface {
	// Execute performs the reconciliation step
	// Returns: result (for requeue), shouldContinue (false to stop pipeline), error
	Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (ctrl.Result, bool, error)
	// Name returns the name of the step for logging
	Name() string
}

// ReconciliationState holds state that flows between reconciliation steps
type ReconciliationState struct {
	EffectiveCatalogs []string
	Log               logr.Logger
}

// ReconciliationPipeline executes reconciliation steps sequentially
type ReconciliationPipeline struct {
	steps []ReconciliationStep
}

// NewReconciliationPipeline creates a new reconciliation pipeline with all steps
// Step order is conditionally determined based on XTrinode spec:
// - If nodePool is configured: NodePool step comes BEFORE Trino Resources
// - If nodePool is not configured: NodePool step comes AFTER Trino Resources (and will skip)
func NewReconciliationPipeline(reconciler *XTrinodeReconciler, xtrinode *analyticsv1.XTrinode) *ReconciliationPipeline {
	steps := []ReconciliationStep{
		&reconcileCatalogsStep{reconciler: reconciler},
		&reconcileNamespaceGuardrailsStep{reconciler: reconciler},
	}

	// Conditionally order node pool step based on configuration
	if xtrinode.Spec.NodePool != nil {
		// Node pool configured: place BEFORE Trino Resources
		steps = append(steps, &reconcileNodePoolStep{reconciler: reconciler}, &reconcileTrinoResourcesStep{reconciler: reconciler})
	} else {
		// No node pool: place AFTER Trino Resources (will skip)
		steps = append(steps, &reconcileTrinoResourcesStep{reconciler: reconciler}, &reconcileNodePoolStep{reconciler: reconciler})
	}

	// Remaining steps (order is fixed)
	// WakeTTL MUST run before KEDA: if wake has expired, clear status.wake first so KEDA
	// reads minReplicas=0 on this reconcile rather than waiting for a second pass.
	steps = append(steps,
		&reconcileWakeTTLStep{reconciler: reconciler},
		&reconcileKEDAStep{reconciler: reconciler},
		&waitForRuntimeReadyStep{reconciler: reconciler},
		&reconcileAutoSuspendStep{reconciler: reconciler},
	)

	return &ReconciliationPipeline{
		steps: steps,
	}
}

// Execute runs all steps in the pipeline sequentially
// Stops early if a step returns shouldContinue=false or an error
func (p *ReconciliationPipeline) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (ctrl.Result, error) {
	state := &ReconciliationState{
		Log:               log,
		EffectiveCatalogs: []string{},
	}

	for _, step := range p.steps {
		log.V(1).Info("Executing reconciliation step", "step", step.Name())

		// Track step execution time
		startTime := time.Now()
		result, shouldContinue, err := step.Execute(ctx, xtrinode, state)
		duration := time.Since(startTime).Seconds()

		// Record step duration metric
		metrics.ReconcileStepDuration.WithLabelValues(
			xtrinode.Namespace,
			xtrinode.Name,
			step.Name(),
		).Observe(duration)

		if err != nil {
			log.Error(err, "reconciliation step failed", "step", step.Name())

			// Record step error metrics
			metrics.ReconcileStepErrors.WithLabelValues(
				xtrinode.Namespace,
				xtrinode.Name,
				step.Name(),
			).Inc()
			metrics.ReconcileStepTotal.WithLabelValues(
				xtrinode.Namespace,
				xtrinode.Name,
				step.Name(),
				"error",
			).Inc()

			// Record error event - get reconciler from step (all steps have reconciler field)
			if reconcilerStep, ok := step.(interface{ getReconciler() *XTrinodeReconciler }); ok {
				reconcilerStep.getReconciler().EventRecorder.Warningf(xtrinode, events.ReasonReconcileError, "Step %s failed: %v", step.Name(), err)
			}
			return result, err
		}

		// Record successful step execution
		metrics.ReconcileStepTotal.WithLabelValues(
			xtrinode.Namespace,
			xtrinode.Name,
			step.Name(),
			"success",
		).Inc()

		if !shouldContinue {
			log.Info("Reconciliation step requested early stop", "step", step.Name())
			return result, nil
		}
		if result.RequeueAfter > 0 {
			log.Info("Reconciliation step requested requeue", "step", step.Name(), "requeueAfter", result.RequeueAfter)
			return result, nil
		}
	}

	return ctrl.Result{}, nil
}

// Pipeline step implementations

type reconcileCatalogsStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileCatalogsStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileCatalogsStep) Name() string {
	return "reconcileCatalogs"
}

func (s *reconcileCatalogsStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	state.Log.V(1).Info("Executing step", "step", s.Name())

	// Non-critical step - errors logged but reconciliation continues
	effectiveCatalogs, err := s.reconciler.reconcileCatalogs(ctx, xtrinode)
	if err != nil {
		// Error already logged in reconcileCatalogs
		effectiveCatalogs = []string{} // Continue with empty catalogs
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonCatalogSyncFailed, "Catalog discovery failed, continuing with empty catalogs: %v", err)

		// Record catalog sync error metric
		metrics.CatalogSyncErrors.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
	} else {
		state.Log.V(1).Info("Step completed", "step", s.Name(), "catalogs", len(effectiveCatalogs))
	}

	// Update catalog count metric
	metrics.CatalogsDiscovered.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(float64(len(effectiveCatalogs)))

	state.EffectiveCatalogs = effectiveCatalogs
	return ctrl.Result{}, true, nil
}

type reconcileNamespaceGuardrailsStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileNamespaceGuardrailsStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileNamespaceGuardrailsStep) Name() string {
	return "reconcileNamespaceGuardrails"
}

func (s *reconcileNamespaceGuardrailsStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	state.Log.V(1).Info("Executing step", "step", s.Name())

	// Critical step - errors stop reconciliation
	if err := s.reconciler.ensureNamespaceGuardrails(ctx, xtrinode); err != nil {
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonStepFailed, "Namespace guardrails failed: %v", err)
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, s.reconciler.Client, s.reconciler.Status(), xtrinode, state.Log, status.ConditionReasonNamespaceFailed, fmt.Sprintf("Failed to ensure namespace guardrails: %v", err), s.reconciler.EventRecorder)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, false, err
	}
	state.Log.V(1).Info("Step completed", "step", s.Name())
	return ctrl.Result{}, true, nil
}

type reconcileTrinoResourcesStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileTrinoResourcesStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileTrinoResourcesStep) Name() string {
	return "reconcileTrinoResources"
}

func (s *reconcileTrinoResourcesStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	state.Log.V(1).Info("Executing step", "step", s.Name(), "catalogs", len(state.EffectiveCatalogs))

	// Critical step - errors stop reconciliation
	if err := s.reconciler.reconcileTrinoResources(ctx, xtrinode, state.EffectiveCatalogs); err != nil {
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonResourceApplyFailed, "Failed to apply Trino resources: %v", err)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, false, err
	}
	state.Log.V(1).Info("Step completed", "step", s.Name())
	return ctrl.Result{}, true, nil
}

type reconcileNodePoolStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileNodePoolStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileNodePoolStep) Name() string {
	return "reconcileNodePool"
}

func (s *reconcileNodePoolStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	// Early return if node pool not configured
	if xtrinode.Spec.NodePool == nil {
		state.Log.V(1).Info("Step skipped - no node pool configured", "step", s.Name())
		return ctrl.Result{}, true, nil
	}

	state.Log.V(1).Info("Executing step", "step", s.Name(), "provider", xtrinode.Spec.NodePool.Provider)

	// Critical step - errors stop reconciliation (node pool explicitly requested)
	result, err = s.reconciler.reconcileNodePoolBlocking(ctx, xtrinode)
	if err != nil {
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonStepFailed, "Node pool provisioning failed: %v", err)
		return result, false, err
	}

	// If requeue requested, stop pipeline and requeue
	if result.RequeueAfter > 0 {
		state.Log.Info("Node pool not ready yet, requeuing", "xtrinode", xtrinode.Name, "requeueAfter", result.RequeueAfter)
		s.reconciler.EventRecorder.Normalf(xtrinode, events.ReasonNodePoolProvisioning, "Node pool provisioning in progress, requeue in %v", result.RequeueAfter)
		return result, false, nil
	}

	state.Log.V(1).Info("Step completed", "step", s.Name())
	return ctrl.Result{}, true, nil
}

type reconcileKEDAStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileKEDAStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileKEDAStep) Name() string {
	return "reconcileKEDA"
}

func (s *reconcileKEDAStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	if !isKEDAEnabled(xtrinode) {
		kedaDisabledMessage := "KEDA disabled; fixed worker replicas are used"
		if isNativeHPAEnabled(xtrinode) {
			kedaDisabledMessage = "KEDA disabled; native HPA worker autoscaling is configured"
		}
		state.Log.V(1).Info("KEDA disabled - cleaning up any stale ScaledObject", "step", s.Name(), "workerScaling", kedaDisabledMessage)
		if cleanupErr := s.reconciler.KEDAService.DeleteScaledObject(ctx, xtrinode, state.Log); cleanupErr != nil {
			state.Log.V(1).Info("Ignored stale KEDA cleanup error while KEDA is disabled", "error", cleanupErr)
		}
		status.SetCondition(xtrinode, status.ConditionTypeKEDAReady, metav1.ConditionFalse, "KEDADisabled", kedaDisabledMessage)
		return ctrl.Result{}, true, nil
	}

	state.Log.V(1).Info("Executing step", "step", s.Name())

	// Critical step - errors stop reconciliation
	if err := s.reconciler.reconcileKEDA(ctx, xtrinode); err != nil {
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonKEDAError, "KEDA operation failed: %v", err)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, false, err
	}
	state.Log.V(1).Info("Step completed", "step", s.Name())
	return ctrl.Result{}, true, nil
}

type reconcileGatewayStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileGatewayStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileGatewayStep) Name() string {
	return "reconcileGateway"
}

func (s *reconcileGatewayStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	state.Log.V(1).Info("Executing step", "step", s.Name())

	// Critical step - errors stop reconciliation
	if err := s.reconciler.reconcileGateway(ctx, xtrinode); err != nil {
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonGatewayError, "Gateway operation failed: %v", err)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, false, err
	}
	state.Log.V(1).Info("Step completed", "step", s.Name())
	return ctrl.Result{}, true, nil
}

type waitForRuntimeReadyStep struct {
	reconciler *XTrinodeReconciler
}

func (s *waitForRuntimeReadyStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *waitForRuntimeReadyStep) Name() string {
	return "waitForRuntimeReady"
}

func (s *waitForRuntimeReadyStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	state.Log.V(1).Info("Executing step", "step", s.Name())

	readiness, err := s.reconciler.checkTrinoRuntimeReady(ctx, xtrinode)
	if err != nil {
		s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonStepFailed, "Runtime readiness check failed: %v", err)
		return ctrl.Result{RequeueAfter: config.RuntimeReadinessRequeueInterval}, false, err
	}
	s.reconciler.syncSchedulingCondition(ctx, xtrinode, state.Log)

	if !readiness.Ready {
		if err := s.reconciler.syncPendingGatewayRoute(ctx, xtrinode, readiness); err != nil {
			s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonGatewayError, "Failed to publish pending gateway route: %v", err)
			return ctrl.Result{RequeueAfter: config.RuntimeReadinessRequeueInterval}, false, err
		}
		if err := s.reconciler.markRuntimeNotReady(ctx, xtrinode, readiness, state.Log); err != nil {
			return ctrl.Result{RequeueAfter: config.RuntimeReadinessRequeueInterval}, false, err
		}
		state.Log.Info("Runtime not ready yet, requeuing", "reason", readiness.Reason, "message", readiness.Message, "requeueAfter", config.RuntimeReadinessRequeueInterval)
		return ctrl.Result{RequeueAfter: config.RuntimeReadinessRequeueInterval}, false, nil
	}

	state.Log.V(1).Info("Step completed", "step", s.Name())
	return ctrl.Result{}, true, nil
}

type reconcileWakeTTLStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileWakeTTLStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileWakeTTLStep) Name() string {
	return "reconcileWakeTTL"
}

func (s *reconcileWakeTTLStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	// Skip if no active wake window in status
	if xtrinode.Status.Wake == nil {
		return ctrl.Result{}, true, nil
	}

	state.Log.V(1).Info("Executing step", "step", s.Name(), "expiresAt", xtrinode.Status.Wake.ExpiresAt)

	err = s.reconciler.reconcileWakeTTL(ctx, xtrinode)
	if err != nil {
		state.Log.Error(err, "failed to reconcile WakeTTL")
	}

	// Do not return RequeueAfter here; it would cause the pipeline to exit.
	// early and skip KEDA, gateway, and autosuspend steps while wake is active.
	// The requeue for wake expiration is handled by calculateRequeueInterval() at
	// the end of the reconcile loop, which already reads status.Wake.ExpiresAt.
	if xtrinode.Status.Wake != nil {
		remaining := time.Until(xtrinode.Status.Wake.ExpiresAt.Time)
		if remaining > 0 {
			state.Log.V(1).Info("Wake window still active, pipeline continues", "remaining", remaining)
		}
	}

	return ctrl.Result{}, true, nil
}

type reconcileAutoSuspendStep struct {
	reconciler *XTrinodeReconciler
}

func (s *reconcileAutoSuspendStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func (s *reconcileAutoSuspendStep) Name() string {
	return "reconcileAutoSuspend"
}

func (s *reconcileAutoSuspendStep) Execute(ctx context.Context, xtrinode *analyticsv1.XTrinode, state *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	// Skip if auto-suspend not configured or already suspended
	if xtrinode.Spec.AutoSuspendAfter == nil || xtrinode.Spec.Suspended {
		return ctrl.Result{}, true, nil
	}

	state.Log.V(1).Info("Executing step", "step", s.Name(), "idleAfter", xtrinode.Spec.AutoSuspendAfter.Duration)

	// Non-critical step - errors logged but reconciliation continues
	suspended, err := s.reconciler.reconcileAutoSuspend(ctx, xtrinode)
	if err != nil {
		if strings.Contains(err.Error(), "failed to request auto-suspend") {
			state.Log.Error(err, "failed to set auto-suspend annotation - suspend request lost, will retry", "step", s.Name())
			s.reconciler.EventRecorder.Warningf(xtrinode, events.ReasonAutoSuspendFailed, "Failed to set auto-suspend annotation: %v (will retry)", err)
			metrics.AutoSuspendChecks.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "failed").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, true, nil
		}
		// Non-critical errors - continue
		metrics.AutoSuspendChecks.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "failed").Inc()
		return ctrl.Result{}, true, nil
	}
	if suspended {
		// State transition: emit real event
		s.reconciler.EventRecorder.Normal(xtrinode, events.ReasonAutoSuspendTriggered, "Auto-suspend triggered - XTrinode has been idle")
		metrics.AutoSuspendChecks.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "triggered").Inc()
		return ctrl.Result{RequeueAfter: time.Second}, false, nil
	}
	state.Log.V(1).Info("Auto-suspend skipped - XTrinode is active", "step", s.Name())
	metrics.AutoSuspendChecks.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "skipped").Inc()
	return ctrl.Result{}, true, nil
}
