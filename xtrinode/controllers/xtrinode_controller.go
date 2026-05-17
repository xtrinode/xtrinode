package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/external"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/internal/status"
	"github.com/xtrinode/xtrinode/internal/trino/controlendpoint"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

const (
	FinalizerName = "xtrinode.analytics.xtrinode.io/finalizer"

	namespaceResourceQuotaName = "xtrinode-namespace-quota"
	namespaceLimitRangeName    = "xtrinode-namespace-limits"
	guardrailScopeLabel        = "xtrinode.analytics.xtrinode.io/guardrail-scope"
	guardrailScopeNamespace    = "namespace"
	managedByLabel             = "app.kubernetes.io/managed-by"
	managedByXTrinodeOperator  = "xtrinode-operator"
)

// XTrinodeReconciler reconciles a XTrinode object
type XTrinodeReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	EventRecorder           events.Recorder                  // Event recorder for Kubernetes events (injected)
	NodePoolAdapter         NodePoolAdapterInterface         // Node pool adapter for provisioning (injected)
	GatewayService          GatewayServiceInterface          // Gateway service for route management (injected)
	KEDAService             KEDAServiceInterface             // KEDA service for autoscaling (injected)
	CatalogService          CatalogServiceInterface          // Catalog service for catalog discovery (injected)
	TrinoResourcesService   TrinoResourcesServiceInterface   // Trino resources service for resource management (injected)
	AutosuspendService      AutosuspendServiceInterface      // Autosuspend service for auto-suspend logic (injected)
	GracefulShutdownService GracefulShutdownServiceInterface // Graceful shutdown service for query checks (injected)
	OperatorVersion         string                           // Operator version for resource revisioning
}

// +kubebuilder:rbac:groups=analytics.xtrinode.io,resources=xtrinodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=analytics.xtrinode.io,resources=xtrinodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=analytics.xtrinode.io,resources=xtrinodes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=resourcequotas;limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses;networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keda.sh,resources=triggerauthentications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments;machinepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *XTrinodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconcile(ctx, req)
}

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

// reconcile performs the actual reconciliation logic
func (r *XTrinodeReconciler) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	startTime := time.Now()

	var xtrinode analyticsv1.XTrinode
	if err := r.Get(ctx, req.NamespacedName, &xtrinode); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch XTrinode", "namespacedName", req.NamespacedName)
		metrics.ReconcileTotal.WithLabelValues(req.Namespace, req.Name, "error").Inc()
		metrics.ReconcileErrors.WithLabelValues(req.Namespace, req.Name, "fetch_error").Inc()
		return ctrl.Result{}, err
	}

	// Defer metrics recording
	defer func() {
		duration := time.Since(startTime).Seconds()
		metrics.ReconcileDuration.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Observe(duration)
	}()

	// Record lifecycle events and metrics
	if xtrinode.Generation == 1 && xtrinode.Status.ObservedGeneration == 0 {
		// First time seeing this XTrinode - record Created event
		r.EventRecorder.Normal(&xtrinode, events.ReasonCreated, events.FormatMessage("XTrinode %s/%s created", xtrinode.Namespace, xtrinode.Name))
		metrics.XTrinodeCreated.WithLabelValues(xtrinode.Namespace, xtrinode.Spec.Size).Inc()
	} else if xtrinode.Generation > xtrinode.Status.ObservedGeneration {
		// Spec changed - record Updated event
		r.EventRecorder.Normal(&xtrinode, events.ReasonUpdated, events.FormatMessage("XTrinode spec updated (generation %d)", xtrinode.Generation))
	}

	// Handle deletion
	if xtrinode.DeletionTimestamp != nil {
		// The drain annotation is set during the first pass of finalize(), so its
		// absence indicates the first deletion pass.
		if xtrinode.Annotations == nil || xtrinode.Annotations["xtrinode.analytics.xtrinode.io/drain-started-at"] == "" {
			r.EventRecorder.Normal(&xtrinode, events.ReasonDeleted, events.FormatMessage("XTrinode deletion started, finalizing resources"))
			metrics.XTrinodeDeleted.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
		}
		result, err := r.finalize(ctx, &xtrinode)
		if err != nil {
			metrics.ReconcileTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "error").Inc()
			metrics.ReconcileErrors.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "finalize_error").Inc()
		} else {
			metrics.ReconcileTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "success").Inc()
		}
		return result, err
	}

	// Add finalizer
	if result, err := r.ensureFinalizer(ctx, &xtrinode); err != nil || result.RequeueAfter > 0 {
		if err == nil && result.RequeueAfter > 0 {
			// Finalizer was added, record event
			r.EventRecorder.Normal(&xtrinode, events.ReasonFinalizerAdded, "Finalizer added to XTrinode for cleanup coordination")
		}
		return result, err
	}

	// Process commands (annotation → spec conversion) - single intake point
	commands, err := r.ProcessCommands(ctx, &xtrinode)
	if err != nil {
		log.Error(err, "failed to process commands")
		metrics.ReconcileTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "error").Inc()
		metrics.ReconcileErrors.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "command_error").Inc()
		return ctrl.Result{}, err
	}
	if len(commands) > 0 {
		// Commands were processed and spec was updated - requeue to converge
		log.Info("Commands processed, requeuing to converge", "count", len(commands))
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Handle suspend/resume logic
	if xtrinode.Spec.Suspended {
		// XTrinode is suspended - disable KEDA first, then scale deployments
		// Note: Auto-resume is handled by gateway service (detects 503 and calls REST API)
		return r.reconcileSuspend(ctx, &xtrinode)
	}

	// XTrinode is not suspended - ensure it's resumed
	if resumeErr := r.reconcileResume(ctx, &xtrinode); resumeErr != nil {
		return ctrl.Result{RequeueAfter: 60 * time.Second}, resumeErr
	}

	// Update phase and conditions to Reconciling
	oldPhase := xtrinode.Status.Phase
	xtrinode.Status.Phase = "Reconciling"

	// Update state metrics
	r.updateStateMetrics(&xtrinode)

	status.SetCondition(&xtrinode, status.ConditionTypeReconciling, metav1.ConditionTrue, status.ConditionReasonReconciling, "Reconciling XTrinode")
	if updateErr := r.updateStatus(ctx, &xtrinode, log); updateErr != nil {
		log.Error(updateErr, "unable to update XTrinode status")
		return ctrl.Result{}, updateErr
	}

	// Record phase change if it changed
	if oldPhase != "Reconciling" && oldPhase != "" {
		r.EventRecorder.Normal(&xtrinode, events.ReasonPhaseChanged, events.FormatMessage("Phase changed from %s to Reconciling", oldPhase))
	}

	// Execute reconciliation pipeline
	// KEDA remains fixed-replica unless the XTrinode has an explicit scaler config.
	pipeline := NewReconciliationPipeline(r, &xtrinode)
	result, err := pipeline.Execute(ctx, &xtrinode, log)
	if err != nil {
		r.EventRecorder.Warningf(&xtrinode, events.ReasonReconcileError, "Reconciliation failed: %v", err)
		metrics.ReconcileTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "error").Inc()
		metrics.ReconcileErrors.WithLabelValues(xtrinode.Namespace, xtrinode.Name, "pipeline_error").Inc()
		return result, err
	}
	if result.RequeueAfter > 0 {
		return result, nil
	}

	if err := r.reconcileReadyGatewayRoute(ctx, &xtrinode); err != nil {
		return ctrl.Result{RequeueAfter: 60 * time.Second}, err
	}

	// Step 10: Update status to Ready
	if err := r.transitionToReady(ctx, &xtrinode, oldPhase, log); err != nil {
		return ctrl.Result{}, err
	}

	// Only requeue if we need periodic checks (autosuspend or WakeTTL)
	requeueAfter := r.calculateRequeueInterval(&xtrinode)
	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

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

	// Step 1: Check if queries are running before touching deployments
	safeToSuspend, err := r.GracefulShutdownService.CheckQueriesBeforeScaleDown(ctx, xtrinode, log)
	if err != nil {
		log.Error(err, "failed to check queries before suspend")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	if !safeToSuspend {
		log.Info("Queries still running, waiting before suspend", "xtrinode", xtrinode.Name)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Step 2: Scale down deployments + disable KEDA now that it is safe to do so
	if err := r.ensureSuspendedInvariants(ctx, xtrinode); err != nil {
		log.Error(err, "failed to scale down for suspend")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Step 3: Wait for pods to terminate gracefully
	if err := r.GracefulShutdownService.WaitForPodTermination(ctx, xtrinode, log); err != nil {
		log.Info("Pods still terminating, waiting before marking as suspended", "xtrinode", xtrinode.Name, "error", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Step 4: Mark as suspended in status
	currentPhase := status.Phase(xtrinode.Status.Phase)
	if err := currentPhase.TransitionTo(status.PhaseSuspended); err != nil {
		log.Error(err, "invalid phase transition", "from", currentPhase, "to", "Suspended")
	}

	r.markSuspendedStatus(xtrinode)

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
		condition.ObservedGeneration == xtrinode.Generation
}

func shouldProvisionNodePoolWhileSuspended(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.NodePool == nil || xtrinode.Spec.NodePool.ScaleDownOnSuspend == nil {
		return false
	}
	return !*xtrinode.Spec.NodePool.ScaleDownOnSuspend
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

func (r *XTrinodeReconciler) syncSuspendedGatewayRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return r.registerGatewayRoute(ctx, xtrinode)
}

// ensureSuspendedInvariants enforces suspended state: KEDA disabled, replicas=0, nodepool scaled down
// This provides self-healing if KEDA/HPA tries to scale up while suspended
func (r *XTrinodeReconciler) ensureSuspendedInvariants(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)

	// Get expected invariants for suspended state
	inv := status.GetInvariants(status.PhaseSuspended, xtrinode)

	// Step 1: Disable KEDA scaling if needed.
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

	// Step 2: Scale deployments to match invariants
	if err := r.scaleDeployments(ctx, xtrinode, inv.CoordReplicas, inv.MinWorkerReplicas); err != nil {
		log.Error(err, "failed to scale deployments to suspended state")
		r.EventRecorder.Warningf(xtrinode, events.ReasonSuspendFailed, "Failed to enforce suspended state: %v", err)
		return fmt.Errorf("failed to scale deployments: %w", err)
	}

	// Step 3: Scale node pool to match invariants
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

		// Step 0: Scale node pool back up if it was scaled down
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

		// Step 1: Scale coordinator (and workers if KEDA disabled) for resume
		if err := r.scaleForResume(ctx, xtrinode, wakeMinWorkers); err != nil {
			return err
		}

		// Step 2: Clear wake annotations after scaling succeeds so one-time wake
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

// scaleForResume scales coordinator (and workers when KEDA is disabled) during a resume transition.
// Records an error status update and event if scaling fails.
func (r *XTrinodeReconciler) scaleForResume(ctx context.Context, xtrinode *analyticsv1.XTrinode, wakeMinWorkers int32) error {
	log := ctrl.LoggerFrom(ctx)
	const coordReplicas = int32(1)
	if isKEDAEnabled(xtrinode) {
		// KEDA enabled — only scale coordinator, NEVER touch workers
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
	// KEDA disabled — controller scales both coordinator and workers
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
// IMPORTANT: Only scales coordinator - never touches workers when KEDA owns them
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

	return nil
}

// reconcileCatalogs handles catalog discovery and validation
func (r *XTrinodeReconciler) reconcileCatalogs(ctx context.Context, xtrinode *analyticsv1.XTrinode) ([]string, error) {
	log := ctrl.LoggerFrom(ctx)
	// Step 1: Get effective catalogs (explicit spec.catalogs or auto-discovered from ConfigMaps)
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

	// Step 2: Validate catalog ConfigMaps (teams provide their own ConfigMaps)
	// Validate both explicit and auto-discovered catalogs
	// Teams create ConfigMaps with catalog properties - operator just validates they exist
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

	// Get operator version for revision computation

	// Step 4: Build and apply Trino resources
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

// reconcileNodePoolBlocking ensures node pool exists and waits for nodes to be ready
// This is used when node pool is explicitly requested (spec.nodePool != nil)
// Returns: (result, error) - result may contain requeue information
func (r *XTrinodeReconciler) reconcileNodePoolBlocking(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Step 1: Check if node pool resource already exists to avoid duplicate event recording
	nodePool := xtrinode.Spec.NodePool
	if nodePool == nil {
		return ctrl.Result{}, nil
	}

	nodePoolName := getNodePoolName(nodePool, xtrinode.Name)
	checkResource := &unstructured.Unstructured{}
	checkResource.SetGroupVersionKind(getMachineResourceGVK(isMachinePoolProvider(nodePool)))

	// Only record provisioning event if resource doesn't exist yet (first time provisioning)
	isFirstProvision := false
	err := r.Get(ctx, client.ObjectKey{
		Name:      nodePoolName,
		Namespace: xtrinode.Namespace,
	}, checkResource)
	if k8serrors.IsNotFound(err) {
		// Resource doesn't exist - this is the first provisioning attempt
		isFirstProvision = true
		r.EventRecorder.Normalf(xtrinode, events.ReasonNodePoolProvisioning, "Node pool provisioning started for provider %s", nodePool.Provider)
	}

	// Step 2: Ensure node pool resource exists
	err = external.CallWithTimeout(ctx, config.NodePoolTimeout, func(ctx context.Context) error {
		return r.NodePoolAdapter.EnsureNodePool(ctx, xtrinode)
	})
	if err != nil {
		log.Error(err, "failed to ensure node pool")
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolReady, metav1.ConditionFalse, status.ConditionReasonNodePoolFailed, fmt.Sprintf("Failed: %v", err))
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonNodePoolFailed, fmt.Sprintf("Failed to ensure node pool: %v", err), r.EventRecorder)
		requeueAfter := getNodePoolErrorRequeueInterval(xtrinode.Spec.NodePool)
		return ctrl.Result{RequeueAfter: requeueAfter}, err
	}
	status.SetCondition(xtrinode, status.ConditionTypeNodePoolReady, metav1.ConditionTrue, "NodePoolProvisioned", nodePoolProvisionedMessage(xtrinode))
	if isFirstProvision {
		metrics.NodePoolProvisioned.WithLabelValues(xtrinode.Namespace, xtrinode.Name, nodePool.Provider).Inc()
	}

	// Step 3: Wait for nodes to be ready (blocking)
	ready, result, err := r.waitForNodePoolReady(ctx, xtrinode, log)
	if err != nil {
		log.Error(err, "failed to check node pool readiness")
		return result, err
	}

	if !ready {
		// Nodes not ready yet, requeue
		log.Info("Node pool nodes not ready yet, requeuing",
			"xtrinode", xtrinode.Name,
			"requeueAfter", result.RequeueAfter)
		return result, nil
	}

	// Nodes are ready - record success event
	log.Info("Node pool nodes are ready", "xtrinode", xtrinode.Name)
	r.EventRecorder.Normalf(xtrinode, events.ReasonNodePoolReady, "Node pool nodes are ready for provider %s", nodePool.Provider)
	return ctrl.Result{}, nil
}

// waitForNodePoolReady checks if node pool nodes are ready
// Returns: (ready bool, result ctrl.Result, error)
func (r *XTrinodeReconciler) waitForNodePoolReady(ctx context.Context, xtrinode *analyticsv1.XTrinode, log logr.Logger) (ready bool, result ctrl.Result, err error) {
	nodePool := xtrinode.Spec.NodePool
	if nodePool == nil {
		return true, ctrl.Result{}, nil // No node pool, consider ready
	}

	nodePoolName := getNodePoolName(nodePool, xtrinode.Name)

	// Get MachinePool/MachineDeployment based on provider and mode
	res := &unstructured.Unstructured{}
	res.SetGroupVersionKind(getMachineResourceGVK(isMachinePoolProvider(nodePool)))

	err = r.Get(ctx, client.ObjectKey{
		Name:      nodePoolName,
		Namespace: xtrinode.Namespace,
	}, res)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Resource not found yet, requeue
			requeueAfter := getNodePoolResourceNotFoundRequeueInterval(nodePool)
			log.Info("Node pool resource not found yet, requeuing", "name", nodePoolName, "requeueAfter", requeueAfter)
			return false, ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return false, ctrl.Result{}, err
	}

	// Check status.readyReplicas
	st, found, _ := unstructured.NestedMap(res.Object, "status") //nolint:errcheck // best-effort status check; errors are non-critical
	if !found {
		requeueAfter := getNodePoolStatusNotAvailableRequeueInterval(nodePool)
		log.Info("Node pool status not available yet, requeuing", "name", nodePoolName, "requeueAfter", requeueAfter)
		return false, ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	readyReplicas, _, _ := unstructured.NestedInt64(st, "readyReplicas") //nolint:errcheck // best-effort status field extraction; defaults to 0 on error
	replicas, _, _ := unstructured.NestedInt64(st, "replicas")           //nolint:errcheck // best-effort status field extraction; defaults to 0 on error

	// For minNodes > 0, wait for at least minNodes to be ready
	// For minNodes == 0, wait for at least configured minimum (default: 1) ready replica
	minNodes := int64(0)
	if nodePool.MinNodes != nil {
		minNodes = int64(*nodePool.MinNodes)
	}

	requiredReplicas := minNodes
	if requiredReplicas == 0 {
		requiredReplicas = getNodePoolMinRequiredReplicasWhenMinNodesZero(nodePool)
	}

	if readyReplicas >= requiredReplicas {
		log.Info("Node pool nodes are ready",
			"xtrinode", xtrinode.Name,
			"readyReplicas", readyReplicas,
			"requiredReplicas", requiredReplicas)
		return true, ctrl.Result{}, nil
	}

	log.Info("Node pool nodes not ready yet",
		"xtrinode", xtrinode.Name,
		"readyReplicas", readyReplicas,
		"requiredReplicas", requiredReplicas,
		"replicas", replicas)

	// Requeue with configurable intervals based on readiness
	var requeueAfter time.Duration
	if readyReplicas == 0 {
		requeueAfter = getNodePoolNoNodesReadyRequeueInterval(nodePool)
	} else {
		requeueAfter = getNodePoolNodesReadyRequeueInterval(nodePool)
	}

	return false, ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// reconcileKEDA ensures KEDA ScaledObject configuration
func (r *XTrinodeReconciler) reconcileKEDA(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)
	// Step 6: Ensure KEDA ScaledObject (after node pool so nodes can be ready).
	// KEDA is opt-in; fixed worker replicas are used unless spec.keda.enabled=true.
	if !isKEDAEnabled(xtrinode) {
		log.Info("KEDA disabled, using fixed worker count from deployment replicas", "xtrinode", xtrinode.Name)
		return nil
	}

	scaledObjectExists := false
	scaledObjectKey := client.ObjectKey{
		Name:      config.BuildScaledObjectName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}
	var existingScaledObject kedav1alpha1.ScaledObject
	if err := r.Get(ctx, scaledObjectKey, &existingScaledObject); err != nil {
		if !k8serrors.IsNotFound(err) {
			// Real error (RBAC, timeout, etc.) - return it
			log.Error(err, "failed to check ScaledObject existence")
			return fmt.Errorf("failed to check ScaledObject existence: %w", err)
		}
		// NotFound - this is expected for new ScaledObjects
		scaledObjectExists = false
	} else {
		scaledObjectExists = true
	}

	// Active wake windows temporarily raise the KEDA floor above spec defaults.
	var wakeMinWorkers int32
	if xtrinode.Status.Wake != nil && time.Now().Before(xtrinode.Status.Wake.ExpiresAt.Time) {
		wakeMinWorkers = xtrinode.Status.Wake.MinWorkers
	}

	err := external.CallWithTimeout(ctx, config.KEDATimeout, func(ctx context.Context) error {
		if wakeMinWorkers > 0 {
			log.Info("Using wake minWorkers for KEDA ScaledObject", "wakeMinWorkers", wakeMinWorkers)
			return r.KEDAService.EnableScaledObjectWithWakeMinWorkers(ctx, xtrinode, wakeMinWorkers, log)
		}
		return r.KEDAService.EnsureScaledObject(ctx, xtrinode, log)
	})
	if err != nil {
		log.Error(err, "failed to ensure KEDA ScaledObject")
		status.SetCondition(xtrinode, status.ConditionTypeKEDAReady, metav1.ConditionFalse, status.ConditionReasonKEDAScaleFailed, fmt.Sprintf("Failed: %v", err))
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonKEDAScaleFailed, fmt.Sprintf("Failed to ensure KEDA ScaledObject: %v", err), r.EventRecorder)
		return err
	}
	status.SetCondition(xtrinode, status.ConditionTypeKEDAReady, metav1.ConditionTrue, "KEDAConfigured", "KEDA ScaledObject configured successfully")

	// Record event based on whether ScaledObject was created or updated
	if !scaledObjectExists {
		r.EventRecorder.Normal(xtrinode, events.ReasonKEDACreated, "KEDA ScaledObject created for autoscaling")
	} else {
		r.EventRecorder.Normal(xtrinode, events.ReasonKEDAUpdated, "KEDA ScaledObject updated")
	}
	log.Info("KEDA ScaledObject created successfully", "xtrinode", xtrinode.Name)
	return nil
}

// reconcileGateway registers gateway route
func (r *XTrinodeReconciler) reconcileGateway(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)
	// Step 7: Register gateway route
	if err := r.registerGatewayRoute(ctx, xtrinode); err != nil {
		log.Error(err, "failed to register gateway route")
		status.SetCondition(xtrinode, status.ConditionTypeGatewayReady, metav1.ConditionFalse, status.ConditionReasonGatewayFailed, fmt.Sprintf("Failed: %v", err))
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonGatewayFailed, fmt.Sprintf("Failed to register gateway route: %v", err), r.EventRecorder)
		return err
	}
	status.SetCondition(xtrinode, status.ConditionTypeGatewayReady, metav1.ConditionTrue, "GatewayRegistered", "Gateway route registered successfully")
	routingGroup := "default"
	if xtrinode.Spec.Routing != nil && xtrinode.Spec.Routing.RoutingGroup != "" {
		routingGroup = xtrinode.Spec.Routing.RoutingGroup
	}
	r.EventRecorder.Normal(xtrinode, events.ReasonGatewayRouteRegistered, events.FormatMessage("Gateway route registered for routing group %s", routingGroup))
	return nil
}

func (r *XTrinodeReconciler) syncPendingGatewayRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode, readiness trinoRuntimeReadiness) error {
	log := ctrl.LoggerFrom(ctx)
	if err := r.registerGatewayRoute(ctx, xtrinode); err != nil {
		log.Error(err, "failed to register pending gateway route")
		status.SetCondition(xtrinode, status.ConditionTypeGatewayReady, metav1.ConditionFalse, status.ConditionReasonGatewayFailed, fmt.Sprintf("Failed: %v", err))
		//nolint:errcheck // best-effort status update; main error is already being returned
		_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonGatewayFailed, fmt.Sprintf("Failed to register pending gateway route: %v", err), r.EventRecorder)
		return err
	}
	status.SetCondition(
		xtrinode,
		status.ConditionTypeGatewayReady,
		metav1.ConditionFalse,
		status.ConditionReasonRuntimeNotReady,
		fmt.Sprintf("Gateway route held in RESUMING until Trino runtime is ready: %s", readiness.Message),
	)
	return nil
}

func (r *XTrinodeReconciler) registerGatewayRoute(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	return external.CallWithTimeout(ctx, config.GatewayTimeout, func(ctx context.Context) error {
		return r.GatewayService.RegisterRoute(ctx, xtrinode)
	})
}

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

// resetWakeTTLIfExpired removed - wake state now managed via status.wake field
// KEDA reads wake state from invariants which read from status.wake

// reconcileAutoSuspend checks auto-suspend conditions
func (r *XTrinodeReconciler) reconcileAutoSuspend(ctx context.Context, xtrinode *analyticsv1.XTrinode) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	// Step 9: Check auto-suspend conditions
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

// scaleCoordinatorOnly scales only the coordinator deployment
// Used for drift repair when KEDA may own worker scaling
func (r *XTrinodeReconciler) scaleCoordinatorOnly(ctx context.Context, xtrinode *analyticsv1.XTrinode, coordinatorReplicas int32) error {
	log := ctrl.LoggerFrom(ctx)
	return r.scaleDeployment(ctx, xtrinode, config.BuildCoordinatorDeploymentName(xtrinode.Name), coordinatorReplicas, "coordinator", log)
}

// scaleDeployments scales coordinator and worker deployments
// Only use when you own both (e.g., during suspend after disabling KEDA)
func (r *XTrinodeReconciler) scaleDeployments(ctx context.Context, xtrinode *analyticsv1.XTrinode, coordinatorReplicas, workerReplicas int32) error {
	log := ctrl.LoggerFrom(ctx)

	// Scale coordinator deployment
	if err := r.scaleDeployment(ctx, xtrinode, config.BuildCoordinatorDeploymentName(xtrinode.Name), coordinatorReplicas, "coordinator", log); err != nil {
		return err
	}

	// Scale worker deployment
	if err := r.scaleDeployment(ctx, xtrinode, config.BuildWorkerDeploymentName(xtrinode.Name), workerReplicas, "worker", log); err != nil {
		return err
	}

	return nil
}

// scaleDeployment scales a single deployment
func (r *XTrinodeReconciler) scaleDeployment(ctx context.Context, xtrinode *analyticsv1.XTrinode, deploymentName string, replicas int32, deploymentType string, log logr.Logger) error {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      deploymentName,
		Namespace: xtrinode.Namespace,
	}, deployment); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("Deployment not found, will be created on next reconciliation", "name", deploymentName, "type", deploymentType)
			return nil
		}
		return fmt.Errorf("failed to get %s deployment: %w", deploymentType, err)
	}

	// Track old replicas for event recording
	oldReplicas := int32(0)
	if deployment.Spec.Replicas != nil {
		oldReplicas = *deployment.Spec.Replicas
	}

	// Use the Scale subresource to avoid update conflicts with KEDA/HPA.
	if err := r.scaleDeploymentViaSubresource(ctx, deployment, replicas, log); err != nil {
		return fmt.Errorf("failed to scale %s deployment: %w", deploymentType, err)
	}
	log.Info("Scaled deployment", "type", deploymentType, "replicas", replicas)

	// Record scaling events and metrics for workers only
	if deploymentType == "worker" && oldReplicas != replicas {
		if replicas > oldReplicas {
			r.EventRecorder.Normalf(xtrinode, events.ReasonWorkersScaledUp, "Workers scaled from %d to %d replicas", oldReplicas, replicas)
			metrics.ScaleUpTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
		} else if replicas < oldReplicas {
			r.EventRecorder.Normalf(xtrinode, events.ReasonWorkersScaledDown, "Workers scaled from %d to %d replicas", oldReplicas, replicas)
			metrics.ScaleDownTotal.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Inc()
		}
		// Update current workers gauge
		metrics.WorkersCurrent.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(float64(replicas))
	}

	return nil
}

// scaleDeploymentViaSubresource scales a deployment using the Scale subresource
// This is the proper way to scale when autoscalers (KEDA/HPA) are involved
func (r *XTrinodeReconciler) scaleDeploymentViaSubresource(ctx context.Context, deployment *appsv1.Deployment, replicas int32, log logr.Logger) error {
	// Create Scale object with desired replicas
	scale := &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployment.Name,
			Namespace: deployment.Namespace,
		},
		Spec: autoscalingv1.ScaleSpec{
			Replicas: replicas,
		},
	}

	// Use SubResource("scale").Update to scale the deployment
	// This avoids conflicts with KEDA/HPA which also use the scale subresource
	if err := r.SubResource("scale").Update(ctx, deployment, client.WithSubResourceBody(scale)); err != nil {
		return fmt.Errorf("failed to update scale subresource: %w", err)
	}

	return nil
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
