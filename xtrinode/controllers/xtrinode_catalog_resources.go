package controllers

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/status"
)

// reconcileCatalogs handles catalog discovery and validation
func (r *XTrinodeReconciler) reconcileCatalogs(ctx context.Context, xtrinode *analyticsv1.XTrinode) ([]string, error) {
	log := ctrl.LoggerFrom(ctx)
	// Get effective catalogs selected by spec.catalogSelector. Raw catalog ConfigMaps
	// are watched for drift, but catalog membership is selector-driven.
	effectiveCatalogs, err := r.CatalogService.GetEffectiveCatalogs(ctx, xtrinode, log)
	if err != nil {
		log.Error(err, "failed to get effective catalogs")
		r.EventRecorder.Warning(xtrinode, events.ReasonCatalogSyncFailed, events.FormatMessage("Failed to discover catalogs: %v", err))

		// Preserve last-known catalogs from status during transient discovery failures.
		if len(xtrinode.Status.ObservedCatalogs) > 0 {
			log.Info("Using last-known catalogs from status due to discovery failure",
				"catalogs", xtrinode.Status.ObservedCatalogs)
			effectiveCatalogs = xtrinode.Status.ObservedCatalogs
		} else {
			// No previous catalogs - use empty list
			effectiveCatalogs = []string{}
		}
	} else if len(effectiveCatalogs) > 0 {
		// Record successful catalog discovery
		r.EventRecorder.Normalf(xtrinode, events.ReasonCatalogsDiscovered, "Discovered %d catalog(s): %s", len(effectiveCatalogs), strings.Join(effectiveCatalogs, ", "))
	}

	// Validate selected catalog ConfigMaps. Teams create XTrinodeCatalog resources;
	// that controller owns the generated ConfigMaps and this reconciler only verifies them.
	if err := r.CatalogService.ValidateCatalogConfigMaps(ctx, xtrinode, effectiveCatalogs, log); err != nil {
		log.Error(err, "failed to validate catalog ConfigMaps")
		r.EventRecorder.Warning(xtrinode, events.ReasonCatalogSyncFailed, events.FormatMessage("Failed to validate catalog ConfigMaps: %v", err))
		// Don't fail reconciliation - teams may create ConfigMaps later
		// Trino will work without catalogs until ConfigMaps are created
	}

	return effectiveCatalogs, nil
}

// reconcileTrinoResources builds and applies Trino resources
func (r *XTrinodeReconciler) reconcileTrinoResources(ctx context.Context, xtrinode *analyticsv1.XTrinode, effectiveCatalogs []string) error {
	log := ctrl.LoggerFrom(ctx)

	// Build and apply Trino resources using the operator version for revision computation.
	resourceSet, err := r.TrinoResourcesService.BuildTrinoResourceSet(ctx, xtrinode, effectiveCatalogs, r.OperatorVersion)
	if err != nil {
		log.Error(err, "failed to build Trino resources")
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonResourceBuildFailed, "failed to build service", r.EventRecorder)
		return err
	}

	// Apply all resources using server-side apply
	if err := r.TrinoResourcesService.ApplyTrinoResources(ctx, xtrinode, resourceSet); err != nil {
		log.Error(err, "failed to apply Trino resources")
		status.SetCondition(xtrinode, status.ConditionTypeTrinoResourcesReady, metav1.ConditionFalse, status.ConditionReasonResourceApplyFailed, fmt.Sprintf("Failed: %v", err))
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonResourceApplyFailed, "failed to apply coordinator deployment", r.EventRecorder)
		return err
	}
	status.SetCondition(xtrinode, status.ConditionTypeTrinoResourcesReady, metav1.ConditionTrue, "ResourcesApplied", trinoResourcesAppliedMessage(xtrinode))

	// Update status with current revision and observed generation
	// This tracks the XTrinode revision that was successfully applied
	currentRevision := r.TrinoResourcesService.GetXTrinodeRevision(xtrinode, r.OperatorVersion, effectiveCatalogs)
	xtrinode.Status.CurrentRevision = currentRevision
	xtrinode.Status.ObservedGeneration = xtrinode.Generation

	// Track rollout information in status for operational visibility.
	if resourceSet.CoordinatorDeployment != nil {
		if hash, ok := resourceSet.CoordinatorDeployment.Spec.Template.Annotations["trino.io/rollout-hash-coordinator"]; ok {
			xtrinode.Status.CoordinatorRolloutHash = hash
		}
	}
	if resourceSet.WorkerDeployment != nil {
		if hash, ok := resourceSet.WorkerDeployment.Spec.Template.Annotations["trino.io/rollout-hash-worker"]; ok {
			xtrinode.Status.WorkerRolloutHash = hash
		}
	}

	// Track observed catalogs for use in discovery failures and finalizer cleanup
	xtrinode.Status.ObservedCatalogs = effectiveCatalogs

	log.Info("Applied Trino resources successfully",
		"xtrinode", xtrinode.Name,
		"baseRevision", currentRevision,
		"coordRevision", xtrinode.Status.CoordinatorRolloutHash,
		"workerRevision", xtrinode.Status.WorkerRolloutHash,
		"catalogs", effectiveCatalogs)
	return nil
}
