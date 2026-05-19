package controllers

import (
	"context"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/status"
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
