package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/external"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

// reconcileSuspend handles suspending a XTrinode
func (r *XTrinodeReconciler) reconcileSuspend(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	alreadySuspended := xtrinode.Status.Phase == string(status.PhaseSuspended)

	if alreadySuspended {
		// Already in suspended phase: enforce invariants only (self-healing drift).
		// No query check needed - coordinator is already at 0 so there are no queries.
		if err := r.ensureSuspendedInvariants(ctx, xtrinode); err != nil {
			log.Error(err, "failed to enforce suspended state invariants")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		if err := r.syncSuspendedGatewayRoute(ctx, xtrinode); err != nil {
			log.Error(err, "failed to sync suspended gateway route")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		r.markSuspendedStatus(xtrinode)
		if err := setObservedRuntimeShapeStatus(xtrinode); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to resolve observed runtime shape while suspended: %w", err)
		}
		if result, err := r.reconcileSuspendedNodePool(ctx, xtrinode, log); err != nil || result.RequeueAfter > 0 {
			return result, err
		}
		if err := r.updateStatus(ctx, xtrinode, log); err != nil {
			log.Error(err, "unable to refresh suspended XTrinode status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: config.ReconcileRequeueIntervalSuspended}, nil
	}

	// Fresh transition to suspended: graceful shutdown first, THEN scale down.
	log.Info("Suspending XTrinode", "xtrinode", xtrinode.Name)

	// Clear wake state on suspend. A stale wake from the previous session
	// could have an ExpiresAt still in the future, which would incorrectly set
	// KEDA minReplicas on the next resume even if that resume has no wake params.
	if xtrinode.Status.Wake != nil {
		log.Info("Clearing active wake window on suspend", "minWorkers", xtrinode.Status.Wake.MinWorkers)
		xtrinode.Status.Wake = nil
	}
	if xtrinode.Status.Phase != string(status.PhaseSuspending) {
		currentPhase := status.Phase(xtrinode.Status.Phase)
		if err := currentPhase.TransitionTo(status.PhaseSuspending); err != nil {
			log.Error(err, "invalid phase transition", "from", currentPhase, "to", "Suspending")
		}
		xtrinode.Status.Phase = string(status.PhaseSuspending)
		r.updateStateMetrics(xtrinode)
		status.SetCondition(xtrinode, status.ConditionTypeReconciling, metav1.ConditionTrue, status.ConditionReasonReconciling, "Suspending XTrinode")
		if err := r.updateStatus(ctx, xtrinode, log); err != nil {
			log.Error(err, "unable to update XTrinode status to Suspending")
		}
	}

	// Publish PAUSED before drain checks so new queries stop selecting this backend
	// while existing queries are allowed to finish.
	if err := r.syncSuspendedGatewayRoute(ctx, xtrinode); err != nil {
		log.Error(err, "failed to sync suspended gateway route")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Check if queries are running before touching deployments.
	safeToSuspend, err := r.GracefulShutdownService.CheckQueriesBeforeScaleDown(ctx, xtrinode, log)
	if err != nil {
		log.Error(err, "failed to check queries before suspend")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	if !safeToSuspend {
		log.Info("Queries still running, waiting before suspend", "xtrinode", xtrinode.Name)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Scale down deployments and disable KEDA now that it is safe to do so.
	if err := r.ensureSuspendedInvariants(ctx, xtrinode); err != nil {
		log.Error(err, "failed to scale down for suspend")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Wait for pods to terminate gracefully.
	if err := r.GracefulShutdownService.WaitForPodTermination(ctx, xtrinode, log); err != nil {
		log.Info("Pods still terminating, waiting before marking as suspended", "xtrinode", xtrinode.Name, "error", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Mark as suspended in status.
	currentPhase := status.Phase(xtrinode.Status.Phase)
	if err := currentPhase.TransitionTo(status.PhaseSuspended); err != nil {
		log.Error(err, "invalid phase transition", "from", currentPhase, "to", "Suspended")
	}

	r.markSuspendedStatus(xtrinode)
	if err := setObservedRuntimeShapeStatus(xtrinode); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to resolve observed runtime shape while suspending: %w", err)
	}

	if result, err := r.reconcileSuspendedNodePool(ctx, xtrinode, log); err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	if err := r.updateStatus(ctx, xtrinode, log); err != nil {
		log.Error(err, "unable to update XTrinode status")
		return ctrl.Result{}, err
	}
	if err := r.syncSuspendedGatewayRoute(ctx, xtrinode); err != nil {
		log.Error(err, "failed to sync suspended gateway route")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	r.EventRecorder.Normal(xtrinode, events.ReasonSuspended, "XTrinode suspended, all deployments scaled to 0")
	metrics.XTrinodeSuspended.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()

	return ctrl.Result{RequeueAfter: config.ReconcileRequeueIntervalSuspended}, nil
}

func (r *XTrinodeReconciler) reconcileSuspendedNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (ctrl.Result, error) {
	if !shouldProvisionNodePoolWhileSuspended(xtrinode) {
		return ctrl.Result{}, nil
	}
	if suspendedNodePoolProvisioningCurrent(xtrinode) {
		needsRepair, err := r.suspendedNodePoolNeedsRepair(ctx, xtrinode)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !needsRepair {
			return ctrl.Result{}, nil
		}
	}

	result, err := r.reconcileNodePoolBlocking(ctx, xtrinode)
	if err != nil {
		return result, err
	}
	if result.RequeueAfter > 0 {
		status.SetCondition(
			xtrinode,
			status.ConditionTypeReconciling,
			metav1.ConditionTrue,
			status.ConditionReasonReconciling,
			"Provisioning node pool while Trino runtime remains suspended",
		)
		if updateErr := r.updateStatus(ctx, xtrinode, log); updateErr != nil {
			log.Error(updateErr, "unable to update suspended node pool provisioning status")
			return ctrl.Result{}, updateErr
		}
	}
	return result, nil
}

func (r *XTrinodeReconciler) suspendedNodePoolNeedsRepair(ctx context.Context, xtrinode *analyticsv1.XTrinode) (bool, error) {
	nodePool := xtrinode.Spec.NodePool
	if nodePool == nil || nodePool.Provider != "gcp" || nodePool.ProviderMode != "managed" {
		return false, nil
	}

	machinePool := &unstructured.Unstructured{}
	machinePool.SetGroupVersionKind(getMachineResourceGVK(true))
	if err := r.Get(ctx, client.ObjectKey{Name: getNodePoolName(nodePool, xtrinode.Name), Namespace: xtrinode.Namespace}, machinePool); err != nil {
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	version, found, _ := unstructured.NestedString(machinePool.Object, "spec", "template", "spec", "version") //nolint:errcheck // absence means no repair needed
	return found && version != "" && gcpManagedMachinePoolTemplateVersion(version) == "", nil
}

func suspendedNodePoolProvisioningCurrent(xtrinode *analyticsv1.XTrinode) bool {
	condition := status.GetCondition(xtrinode, status.ConditionTypeNodePoolReady)
	return condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == events.ReasonNodePoolReady &&
		condition.ObservedGeneration == xtrinode.Generation
}

func shouldProvisionNodePoolWhileSuspended(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.NodePool == nil || xtrinode.Spec.NodePool.ScaleDownOnSuspend == nil {
		return false
	}
	return !*xtrinode.Spec.NodePool.ScaleDownOnSuspend
}

func (r *XTrinodeReconciler) syncSuspendedGatewayRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return r.registerGatewayRoute(ctx, xtrinode)
}

// ensureSuspendedInvariants enforces suspended state: KEDA disabled, replicas=0, nodepool scaled down
// This provides self-healing if KEDA/HPA tries to scale up while suspended
func (r *XTrinodeReconciler) ensureSuspendedInvariants(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)

	// Get expected invariants for suspended state
	inv := status.GetInvariants(status.PhaseSuspended, xtrinode)

	// Disable KEDA scaling if needed.
	// If KEDA is still enabled and we scale deployments to 0, KEDA will immediately
	// fight us by scaling back up. Block the scale-down if KEDA disable fails with a
	// permanent error (anything other than "not found", which is already-gone and safe).
	if isKEDAEnabled(xtrinode) && !inv.KEDAEnabled {
		err := external.CallWithTimeout(ctx, config.KEDATimeout, func(ctx context.Context) error {
			return r.KEDAService.DisableScaledObject(ctx, xtrinode, log)
		})
		if err != nil && !k8serrors.IsNotFound(err) {
			// Permanent-looking failure (RBAC, timeout, etc.) - set a condition and bail.
			// Proceeding with scale-down while KEDA is active would cause a fight loop.
			msg := fmt.Sprintf("cannot enforce suspended state: failed to disable KEDA ScaledObject: %v", err)
			log.Error(err, "aborting suspend invariant enforcement - KEDA still active")
			r.EventRecorder.Warningf(xtrinode, events.ReasonSuspendFailed, "%s", msg)
			status.SetCondition(xtrinode, status.ConditionTypeSuspended, metav1.ConditionFalse,
				"KEDADisableFailed", msg)
			return fmt.Errorf("%s", msg)
		}
	}

	if err := r.deleteNativeHPAForSuspend(ctx, xtrinode, log); err != nil {
		msg := fmt.Sprintf("cannot enforce suspended state: failed to delete native HPA: %v", err)
		log.Error(err, "aborting suspend invariant enforcement - native HPA may still scale workers")
		r.EventRecorder.Warningf(xtrinode, events.ReasonSuspendFailed, "%s", msg)
		status.SetCondition(xtrinode, status.ConditionTypeSuspended, metav1.ConditionFalse,
			"NativeHPADeleteFailed", msg)
		return fmt.Errorf("%s", msg)
	}

	// Scale deployments to match invariants.
	if err := r.scaleDeployments(ctx, xtrinode, inv.CoordReplicas, inv.MinWorkerReplicas); err != nil {
		log.Error(err, "failed to scale deployments to suspended state")
		r.EventRecorder.Warningf(xtrinode, events.ReasonSuspendFailed, "Failed to enforce suspended state: %v", err)
		return fmt.Errorf("failed to scale deployments: %w", err)
	}

	// Scale node pool to match invariants.
	if inv.NodePoolMinNodes != nil {
		err := external.CallWithTimeout(ctx, config.NodePoolTimeout, func(ctx context.Context) error {
			return r.NodePoolAdapter.ScaleNodePoolMinNodes(ctx, xtrinode, *inv.NodePoolMinNodes)
		})
		if err != nil {
			log.Error(err, "failed to scale node pool for suspend", "targetMinNodes", *inv.NodePoolMinNodes)
			// Continue - not critical, pods are already scaled down
		}
	}

	return nil
}

func (r *XTrinodeReconciler) deleteNativeHPAForSuspend(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.BuildWorkerServiceName(xtrinode.Name),
			Namespace: xtrinode.Namespace,
		},
	}
	if err := r.Delete(ctx, hpa); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	log.Info("Deleted native HPA before suspend", "hpa", hpa.Name, "namespace", hpa.Namespace)
	return nil
}
