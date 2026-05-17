package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/events"
)

// CatalogReconciliationStep represents a single step in the catalog reconciliation pipeline
type CatalogReconciliationStep interface {
	// Execute performs the reconciliation step
	// Returns: result (for requeue), shouldContinue (false to stop pipeline), error
	Execute(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, state *CatalogReconciliationState) (ctrl.Result, bool, error)
	// Name returns the name of the step for logging
	Name() string
}

// CatalogReconciliationState holds state that flows between reconciliation steps
type CatalogReconciliationState struct {
	ConfigMap *corev1.ConfigMap // Generated ConfigMap (set by generateConfigMapStep)
	Log       logr.Logger
}

// CatalogReconciliationPipeline executes catalog reconciliation steps sequentially
type CatalogReconciliationPipeline struct {
	steps []CatalogReconciliationStep
}

// NewCatalogReconciliationPipeline creates a new catalog reconciliation pipeline with all steps
func NewCatalogReconciliationPipeline(reconciler *XTrinodeCatalogReconciler) *CatalogReconciliationPipeline {
	return &CatalogReconciliationPipeline{
		steps: []CatalogReconciliationStep{
			&generateConfigMapStep{reconciler: reconciler},
			&ensureConfigMapStep{reconciler: reconciler},
			&updateStatusStep{reconciler: reconciler},
			// Note: XTrinode reconciliation is automatically triggered by ConfigMap watch
		},
	}
}

// Execute runs all steps in the pipeline sequentially
// Stops early if a step returns shouldContinue=false or an error
func (p *CatalogReconciliationPipeline) Execute(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, log logr.Logger) (ctrl.Result, error) {
	state := &CatalogReconciliationState{
		Log:       log,
		ConfigMap: nil, // Will be set by generateConfigMapStep
	}

	for _, step := range p.steps {
		log.V(1).Info("Executing catalog reconciliation step", "step", step.Name())
		result, shouldContinue, err := step.Execute(ctx, catalog, state)
		if err != nil {
			log.Error(err, "catalog reconciliation step failed", "step", step.Name())
			return result, err
		}
		if !shouldContinue {
			log.Info("Catalog reconciliation step requested early stop", "step", step.Name())
			return result, nil
		}
		if result.RequeueAfter > 0 {
			log.Info("Catalog reconciliation step requested requeue", "step", step.Name(), "requeueAfter", result.RequeueAfter)
			return result, nil
		}
	}

	return ctrl.Result{}, nil
}

// generateConfigMapStep generates the ConfigMap from XTrinodeCatalog spec
type generateConfigMapStep struct {
	reconciler *XTrinodeCatalogReconciler
}

func (s *generateConfigMapStep) Name() string {
	return "generateConfigMap"
}

func (s *generateConfigMapStep) Execute(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, state *CatalogReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	configMap, genErr := s.reconciler.generateConfigMap(catalog)
	if genErr != nil {
		s.reconciler.EventRecorder.Warningf(catalog, events.ReasonReconcileError, "Failed to generate ConfigMap: %v", genErr)
		handleResult, handleErr := s.reconciler.handleError(ctx, catalog, state.Log, genErr, "failed to generate ConfigMap")
		return handleResult, false, handleErr
	}

	state.ConfigMap = configMap
	return ctrl.Result{}, true, nil
}

// ensureConfigMapStep creates or updates the ConfigMap
type ensureConfigMapStep struct {
	reconciler *XTrinodeCatalogReconciler
}

func (s *ensureConfigMapStep) Name() string {
	return "ensureConfigMap"
}

func (s *ensureConfigMapStep) Execute(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, state *CatalogReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	if state.ConfigMap == nil {
		// Treat a nil ConfigMap as an invariant violation, not a valid path.
		invariantErr := fmt.Errorf("internal error: ConfigMap was not generated for XTrinodeCatalog %s/%s", catalog.Namespace, catalog.Name)
		handleResult, handleErr := s.reconciler.handleError(ctx, catalog, state.Log, invariantErr, "ConfigMap generation invariant violated")
		return handleResult, false, handleErr
	}

	if ensureErr := s.reconciler.ensureConfigMap(ctx, catalog, state.ConfigMap, state.Log); ensureErr != nil {
		s.reconciler.EventRecorder.Warningf(catalog, events.ReasonResourceApplyFailed, "Failed to ensure ConfigMap: %v", ensureErr)
		handleResult, handleErr := s.reconciler.handleError(ctx, catalog, state.Log, ensureErr, "failed to ensure ConfigMap")
		return handleResult, false, handleErr
	}

	return ctrl.Result{}, true, nil
}

// updateStatusStep updates the catalog status to Ready
type updateStatusStep struct {
	reconciler *XTrinodeCatalogReconciler
}

func (s *updateStatusStep) Name() string {
	return "updateStatus"
}

func (s *updateStatusStep) Execute(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, state *CatalogReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	if state.ConfigMap == nil {
		// Treat a nil ConfigMap as an invariant violation.
		invariantErr := fmt.Errorf("internal error: ConfigMap was not generated for XTrinodeCatalog %s/%s", catalog.Namespace, catalog.Name)
		state.Log.Error(invariantErr, "ConfigMap generation invariant violated")
		return ctrl.Result{}, false, invariantErr
	}

	if statusErr := s.reconciler.updateStatusToReady(ctx, catalog, state.ConfigMap.Name, state.Log); statusErr != nil {
		state.Log.Error(statusErr, "failed to update status")
		return ctrl.Result{}, false, statusErr
	}

	return ctrl.Result{}, true, nil
}

// Note: triggerXTrinodeReconciliationStep removed - redundant with ConfigMap watch.
// The XTrinode controller watches catalog ConfigMaps and automatically enqueues
// all XTrinodes in the namespace when a ConfigMap changes.
