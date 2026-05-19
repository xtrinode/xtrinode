package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/external"
	"github.com/xtrinode/xtrinode/internal/retry"
)

// ensureFinalizer ensures the finalizer is present on the XTrinode
func (r *XTrinodeReconciler) ensureFinalizer(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	if !controllerutil.ContainsFinalizer(xtrinode, FinalizerName) {
		controllerutil.AddFinalizer(xtrinode, FinalizerName)
		if err := retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
			func() error {
				key := client.ObjectKeyFromObject(xtrinode)
				return r.Get(ctx, key, xtrinode)
			},
			func() error {
				// Re-check finalizer after refresh
				if !controllerutil.ContainsFinalizer(xtrinode, FinalizerName) {
					controllerutil.AddFinalizer(xtrinode, FinalizerName)
				}
				return r.Update(ctx, xtrinode)
			},
		); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// Old annotation handling functions removed - replaced by ProcessCommands() in commands.go

func (r *XTrinodeReconciler) finalize(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	if !controllerutil.ContainsFinalizer(xtrinode, FinalizerName) {
		return ctrl.Result{}, nil
	}

	// DRAIN-BEFORE-REMOVAL STATE MACHINE
	// Step 0: Set backend to DRAINING state (if not already done)
	// Check annotation to track drain state
	if xtrinode.Annotations == nil {
		xtrinode.Annotations = make(map[string]string)
	}
	drainStartedAt, drainStarted := xtrinode.Annotations["xtrinode.analytics.xtrinode.io/drain-started-at"]

	if !drainStarted {
		// First time in finalizer - initiate drain
		log.Info("Initiating backend drain before removal", "xtrinode", xtrinode.Name)
		if err := external.CallWithTimeout(ctx, config.GatewayTimeout, func(ctx context.Context) error {
			return r.GatewayService.DrainRoute(ctx, xtrinode)
		}); err != nil {
			log.Error(err, "failed to drain gateway route")
			r.EventRecorder.Warningf(xtrinode, events.ReasonDrainStarted, "Failed to start gateway route drain: %v", err)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, fmt.Errorf("failed to start gateway route drain: %w", err)
		}

		err := retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
			func() error {
				return r.Get(ctx, client.ObjectKeyFromObject(xtrinode), xtrinode)
			},
			func() error {
				if xtrinode.Annotations == nil {
					xtrinode.Annotations = make(map[string]string)
				}
				xtrinode.Annotations["xtrinode.analytics.xtrinode.io/drain-started-at"] = time.Now().Format(time.RFC3339)
				return r.Update(ctx, xtrinode)
			},
		)
		if err != nil {
			log.Error(err, "failed to update drain annotation")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		log.Info("Backend set to DRAINING, waiting for drain condition", "xtrinode", xtrinode.Name)
		r.EventRecorder.Normal(xtrinode, events.ReasonDrainStarted, "Gateway route set to DRAINING state, waiting 5 minutes for active connections to complete")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Step 1: Check drain condition (time-based: 5 minutes)
	drainStartTime, err := time.Parse(time.RFC3339, drainStartedAt)
	if err != nil {
		log.Error(err, "failed to parse drain start time, proceeding with deletion")
	} else {
		elapsed := time.Since(drainStartTime)
		drainDuration := 5 * time.Minute // Match DrainRoute policy
		if elapsed < drainDuration {
			remaining := drainDuration - elapsed
			log.Info("Drain condition not met, waiting",
				"xtrinode", xtrinode.Name,
				"elapsed", elapsed,
				"remaining", remaining)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	log.Info("Drain condition met, proceeding with deletion", "xtrinode", xtrinode.Name)
	r.EventRecorder.Normal(xtrinode, events.ReasonDrainCompleted, "Drain period completed, proceeding with resource cleanup")

	// Step 2: Check if queries are running before deletion (graceful shutdown)
	safeToDelete, err := r.GracefulShutdownService.CheckQueriesBeforeScaleDown(ctx, xtrinode, log)
	if err != nil {
		log.Error(err, "failed to check queries before deletion")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	if !safeToDelete {
		log.Info("Queries still running, waiting before deletion", "xtrinode", xtrinode.Name)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Step 3: Wait for pods to terminate gracefully (if any are terminating)
	if err := r.GracefulShutdownService.WaitForPodTermination(ctx, xtrinode, log); err != nil {
		log.Info("Pods still terminating, waiting", "xtrinode", xtrinode.Name, "error", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Step 4: Clean up resources (Kubernetes owner references handle most cleanup automatically)
	r.EventRecorder.Normal(xtrinode, events.ReasonCleanupStarted, "Starting cleanup of all XTrinode resources (gateway, KEDA, Trino, node pool)")
	if err := r.cleanupResources(ctx, xtrinode, log); err != nil {
		r.EventRecorder.Warningf(xtrinode, events.ReasonCleanupStarted, "Resource cleanup failed: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	r.EventRecorder.Normal(xtrinode, events.ReasonCleanupCompleted, "Resource cleanup completed")

	if err := r.reconcileNamespaceGuardrailsAfterDelete(ctx, xtrinode, log); err != nil {
		log.Error(err, "failed to reconcile namespace guardrails after XTrinode deletion")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Remove finalizer
	if err := r.removeFinalizer(ctx, xtrinode, log); err != nil {
		return ctrl.Result{}, err
	}
	r.EventRecorder.Normal(xtrinode, events.ReasonFinalizerRemoved, "Finalizer removed from XTrinode")

	// Record finalization complete event
	r.EventRecorder.Normal(xtrinode, events.ReasonFinalized, events.FormatMessage("XTrinode finalization complete, all resources cleaned up"))

	return ctrl.Result{}, nil
}

// cleanupResources cleans up all XTrinode resources.
// Gateway route cleanup is critical because it is stored inside the shared route
// ConfigMap and is not garbage-collected by Kubernetes owner references.
func (r *XTrinodeReconciler) cleanupResources(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	var criticalErr error

	// Gateway route (has owner reference, but explicit cleanup ensures it's removed)
	if err := r.GatewayService.DeregisterRoute(ctx, xtrinode); err != nil {
		log.Error(err, "failed to deregister gateway route")
		criticalErr = fmt.Errorf("failed to deregister gateway route: %w", err)
	}

	// KEDA ScaledObject (has owner reference, but explicit cleanup ensures it's removed)
	// Only delete if KEDA was enabled (might not exist if KEDA was disabled)
	if isKEDAEnabled(xtrinode) {
		if err := r.KEDAService.DeleteScaledObject(ctx, xtrinode, log); err != nil {
			log.Error(err, "failed to delete KEDA ScaledObject")
			// Continue with cleanup even if KEDA deletion fails
		}
	}

	// Trino resources (Deployments, Services, ConfigMaps, ServiceAccount)
	r.cleanupTrinoResources(ctx, xtrinode, log)

	// Node pool (has owner reference, but explicit cleanup ensures it's removed)
	if err := r.NodePoolAdapter.DeleteNodePool(ctx, xtrinode); err != nil {
		log.Error(err, "failed to delete node pool")
		// Continue with cleanup even if node pool deletion fails
	}

	// Note: Catalog ConfigMaps are managed by XTrinodeCatalog controller via owner references
	// They will be automatically deleted when XTrinodeCatalog CRDs are deleted
	return criticalErr
}

// cleanupTrinoResources deletes Trino resources
func (r *XTrinodeReconciler) cleanupTrinoResources(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) {
	// Use last-known catalogs from status so finalizer cleanup includes catalog-related resources.
	catalogs := xtrinode.Status.ObservedCatalogs
	if catalogs == nil {
		catalogs = []string{}
	}

	log.Info("Cleaning up Trino resources", "catalogs", catalogs)

	// Get operator version for resource set building (needed for revision computation)
	resourceSet, err := r.TrinoResourcesService.BuildTrinoResourceSet(ctx, xtrinode, catalogs, r.OperatorVersion)
	if err != nil {
		log.Error(err, "failed to build resource set for deletion")
		// Continue with cleanup even if resource set build fails
		return
	}

	if err := r.TrinoResourcesService.DeleteTrinoResources(ctx, resourceSet); err != nil {
		log.Error(err, "failed to delete Trino resources")
		// Continue with cleanup even if resource deletion fails
	}
}

// removeFinalizer removes the finalizer from the XTrinode
func (r *XTrinodeReconciler) removeFinalizer(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	controllerutil.RemoveFinalizer(xtrinode, FinalizerName)
	return retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
		func() error {
			key := client.ObjectKeyFromObject(xtrinode)
			return r.Get(ctx, key, xtrinode)
		},
		func() error {
			// Re-check finalizer after refresh
			if controllerutil.ContainsFinalizer(xtrinode, FinalizerName) {
				controllerutil.RemoveFinalizer(xtrinode, FinalizerName)
			}
			return r.Update(ctx, xtrinode)
		},
	)
}
