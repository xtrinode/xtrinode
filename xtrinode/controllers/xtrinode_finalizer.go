package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/external"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/pkg/metrics"
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

func (r *XTrinodeReconciler) finalize(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	if !controllerutil.ContainsFinalizer(xtrinode, FinalizerName) {
		return ctrl.Result{}, nil
	}

	// DRAIN-BEFORE-REMOVAL STATE MACHINE
	// Check annotation to track drain state
	if xtrinode.Annotations == nil {
		xtrinode.Annotations = make(map[string]string)
	}
	drainStartedAt, drainStarted := xtrinode.Annotations[config.DrainStartedAtAnnotation]

	if !drainStarted {
		// First time in finalizer - initiate drain
		log.Info("Initiating backend drain before removal", "xtrinode", xtrinode.Name)
		if err := external.CallWithTimeout(ctx, config.GatewayTimeout, func(ctx context.Context) error {
			return r.GatewayService.DrainRoute(ctx, xtrinode)
		}); err != nil {
			log.Error(err, "failed to drain gateway route")
			r.EventRecorder.Warningf(xtrinode, events.ReasonDrainStarted, "Failed to start gateway route drain: %v", err)
			metrics.XTrinodeDrainFailures.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "route_update_error").Inc()
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
				xtrinode.Annotations[config.DrainStartedAtAnnotation] = time.Now().Format(time.RFC3339)
				return r.Update(ctx, xtrinode)
			},
		)
		if err != nil {
			log.Error(err, "failed to update drain annotation")
			metrics.XTrinodeDrainFailures.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "annotation_update_error").Inc()
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		metrics.XTrinodeDrainActive.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(1)
		log.Info("Backend set to DRAINING, waiting for drain condition", "xtrinode", xtrinode.Name)
		r.EventRecorder.Normal(xtrinode, events.ReasonDrainStarted, "Gateway route set to DRAINING state, waiting for active queries to complete")
		return ctrl.Result{RequeueAfter: r.drainRequeueInterval()}, nil
	}

	metrics.XTrinodeDrainActive.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(1)
	drainComplete, drainResult, drainElapsed := r.queryAwareDrainComplete(ctx, xtrinode, drainStartedAt, log)
	if !drainComplete {
		return ctrl.Result{RequeueAfter: r.drainRequeueInterval()}, nil
	}

	drainMarked, err := r.markDrainComplete(ctx, xtrinode, drainResult, log)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("XTrinode no longer exists while marking drain completion, treating finalization as complete",
				"xtrinode", xtrinode.Name,
				"result", drainResult)
			metrics.XTrinodeDrainActive.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(0)
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to update drain completion annotation")
		metrics.XTrinodeDrainFailures.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "completion_annotation_update_error").Inc()
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}
	if drainMarked {
		log.Info("Drain condition met, proceeding with deletion", "xtrinode", xtrinode.Name, "result", drainResult, "elapsed", drainElapsed)
		r.EventRecorder.Normalf(xtrinode, events.ReasonDrainCompleted, "Drain completed via %s, proceeding with resource cleanup", drainResult)
		metrics.XTrinodeDrainDuration.WithLabelValues(xtrinode.Namespace, xtrinode.Name, drainResult).Observe(drainElapsed.Seconds())
	} else {
		log.Info("Drain already marked complete, continuing deletion", "xtrinode", xtrinode.Name, "result", xtrinode.Annotations[config.DrainResultAnnotation])
	}

	// Wait for pods to terminate gracefully if any are terminating.
	if err := r.GracefulShutdownService.WaitForPodTermination(ctx, xtrinode, log); err != nil {
		log.Info("Pods still terminating, waiting", "xtrinode", xtrinode.Name, "error", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Clean up resources. Kubernetes owner references handle most cleanup automatically.
	r.EventRecorder.Normal(xtrinode, events.ReasonCleanupStarted, "Starting cleanup of all XTrinode resources (gateway, KEDA, Trino, node pool)")
	if err := r.cleanupResources(ctx, xtrinode, log); err != nil {
		r.EventRecorder.Warningf(xtrinode, events.ReasonCleanupStarted, "Resource cleanup failed: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	metrics.XTrinodeDrainActive.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(0)
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

func (r *XTrinodeReconciler) queryAwareDrainComplete(ctx context.Context, xtrinode *analyticsv1.XTrinode, drainStartedAt string, log logr.Logger) (complete bool, result string, elapsed time.Duration) {
	drainDuration := r.drainDuration()
	drainStartTime, err := time.Parse(time.RFC3339, drainStartedAt)
	if err != nil {
		log.Error(err, "failed to parse drain start time, using elapsed drain fallback")
		metrics.XTrinodeDrainFailures.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "invalid_start_time").Inc()
		drainStartTime = time.Now().Add(-drainDuration)
	}

	elapsed = time.Since(drainStartTime)
	if elapsed < 0 {
		elapsed = 0
	}
	fallbackReady := elapsed >= drainDuration

	safeToDelete, checkErr := r.GracefulShutdownService.CheckQueriesBeforeScaleDown(ctx, xtrinode, log)
	if checkErr != nil {
		metrics.XTrinodeDrainFailures.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "query_check_error").Inc()
		if fallbackReady {
			log.Info("Query-aware drain check failed after fallback window, proceeding by elapsed drain time",
				"xtrinode", xtrinode.Name,
				"elapsed", elapsed,
				"fallbackWindow", drainDuration,
				"error", checkErr)
			return true, "time_fallback", elapsed
		}
		log.Info("Query-aware drain check failed before fallback window, waiting",
			"xtrinode", xtrinode.Name,
			"elapsed", elapsed,
			"remaining", drainDuration-elapsed,
			"error", checkErr)
		return false, "query_check_error", elapsed
	}

	if safeToDelete {
		return true, "query_complete", elapsed
	}

	log.Info("Queries still running, waiting before deletion",
		"xtrinode", xtrinode.Name,
		"elapsed", elapsed,
		"fallbackWindow", drainDuration)
	return false, "active_queries", elapsed
}

func (r *XTrinodeReconciler) markDrainComplete(ctx context.Context, xtrinode *analyticsv1.XTrinode, result string, log logr.Logger) (bool, error) {
	completedAt := time.Now().Format(time.RFC3339)
	marked := false
	err := retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
		func() error {
			return r.Get(ctx, client.ObjectKeyFromObject(xtrinode), xtrinode)
		},
		func() error {
			if xtrinode.Annotations == nil {
				xtrinode.Annotations = make(map[string]string)
			}
			if xtrinode.Annotations[config.DrainCompletedAtAnnotation] != "" {
				return nil
			}
			xtrinode.Annotations[config.DrainCompletedAtAnnotation] = completedAt
			xtrinode.Annotations[config.DrainResultAnnotation] = result
			marked = true
			return r.Update(ctx, xtrinode)
		},
	)
	return marked, err
}

func (r *XTrinodeReconciler) drainDuration() time.Duration {
	if r.DrainDuration <= 0 {
		return config.GatewayDrainDuration
	}
	return r.DrainDuration
}

func (r *XTrinodeReconciler) drainRequeueInterval() time.Duration {
	if r.DrainRequeueInterval <= 0 {
		return config.GatewayDrainRequeueInterval
	}
	return r.DrainRequeueInterval
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

	// Node pool (has owner reference, but explicit cleanup ensures it follows the configured policy)
	if xtrinode.Spec.NodePool != nil {
		switch nodePoolDeletionPolicy(xtrinode.Spec.NodePool) {
		case analyticsv1.NodePoolDeletionPolicyRetain:
			log.Info("Retaining node pool during XTrinode cleanup", "xtrinode", xtrinode.Name, "nodePool", xtrinode.Spec.NodePool.Name)
			if err := r.NodePoolAdapter.RetainNodePool(ctx, xtrinode); err != nil {
				log.Error(err, "failed to retain node pool")
				r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolRetainFailed, "Failed to retain node pool: %v", err)
				criticalErr = errors.Join(criticalErr, fmt.Errorf("failed to retain node pool: %w", err))
			} else {
				r.EventRecorder.Normal(xtrinode, events.ReasonNodePoolRetained, "Node pool retained by spec.nodePool.deletionPolicy=Retain")
			}
		case analyticsv1.NodePoolDeletionPolicyScaleToZero:
			log.Info("Scaling node pool to zero during XTrinode cleanup", "xtrinode", xtrinode.Name, "nodePool", xtrinode.Spec.NodePool.Name)
			r.EventRecorder.Normal(xtrinode, events.ReasonNodePoolScaleToZeroStarted, "Scaling node pool to zero by spec.nodePool.deletionPolicy=ScaleToZero")
			if err := r.NodePoolAdapter.ScaleNodePoolMinNodes(ctx, xtrinode, 0); err != nil {
				log.Error(err, "failed to scale node pool to zero")
				r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolScaleToZeroFailed, "Failed to scale node pool to zero: %v", err)
				criticalErr = errors.Join(criticalErr, fmt.Errorf("failed to scale node pool to zero: %w", err))
			} else if err := r.NodePoolAdapter.RetainNodePool(ctx, xtrinode); err != nil {
				log.Error(err, "failed to retain scaled-to-zero node pool")
				r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolRetainFailed, "Failed to retain scaled-to-zero node pool: %v", err)
				criticalErr = errors.Join(criticalErr, fmt.Errorf("failed to retain scaled-to-zero node pool: %w", err))
			} else {
				r.EventRecorder.Normal(xtrinode, events.ReasonNodePoolScaledToZero, "Node pool scaled to zero and retained by spec.nodePool.deletionPolicy=ScaleToZero")
			}
		default:
			r.EventRecorder.Normal(xtrinode, events.ReasonNodePoolDeletionStarted, "Deleting node pool by spec.nodePool.deletionPolicy=Delete")
			if err := r.NodePoolAdapter.DeleteNodePool(ctx, xtrinode); err != nil {
				log.Error(err, "failed to delete node pool")
				r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolDeleteFailed, "Failed to delete node pool: %v", err)
				// Continue with cleanup even if node pool deletion fails
			} else {
				r.EventRecorder.Normal(xtrinode, events.ReasonNodePoolDeleted, "Node pool deletion completed")
			}
		}
	} else if retained, ok, err := xtrinodeWithRemovedObservedNodePool(xtrinode); ok {
		if err != nil {
			log.Error(err, "failed to reconstruct removed node pool from observed status during cleanup")
			r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolRetainFailed, "Failed to retain removed node pool: %v", err)
			criticalErr = errors.Join(criticalErr, fmt.Errorf("failed to retain removed node pool: %w", err))
		} else {
			nodePoolName := getNodePoolName(retained.Spec.NodePool, xtrinode.Name)
			log.Info("Retaining removed node pool during XTrinode cleanup", "xtrinode", xtrinode.Name, "nodePool", nodePoolName)
			if err := r.NodePoolAdapter.RetainNodePool(ctx, retained); err != nil {
				log.Error(err, "failed to retain removed node pool")
				r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolRetainFailed, "Failed to retain removed node pool: %v", err)
				criticalErr = errors.Join(criticalErr, fmt.Errorf("failed to retain removed node pool: %w", err))
			} else {
				r.EventRecorder.Normal(xtrinode, events.ReasonNodePoolRetained, "Node pool retained because spec.nodePool was removed before deletion")
			}
		}
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

	// Build the last-known resource set so deletion includes revisioned resources.
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
