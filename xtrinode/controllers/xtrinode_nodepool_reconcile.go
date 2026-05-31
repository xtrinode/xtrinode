package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
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

// reconcileNodePoolBlocking ensures node pool exists and waits for nodes to be ready
// This is used when node pool is explicitly requested (spec.nodePool != nil)
// Returns: (result, error) - result may contain requeue information
func (r *XTrinodeReconciler) reconcileNodePoolBlocking(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Check if node pool resource already exists to avoid duplicate event recording.
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

	// Ensure node pool resource exists.
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
	setNodePoolFitCondition(xtrinode)
	if isFirstProvision {
		metrics.NodePoolProvisioned.WithLabelValues(xtrinode.Namespace, xtrinode.Name, nodePool.Provider).Inc()
	}

	// Wait for nodes to be ready before continuing reconciliation.
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

	// Node-pool readiness requirement is satisfied; for scale-to-zero pools this
	// can mean the node-pool resource exists before any node has been created.
	log.Info("Node pool readiness requirement is satisfied", "xtrinode", xtrinode.Name)
	r.EventRecorder.Normalf(xtrinode, events.ReasonNodePoolReady, "Node pool readiness requirement is satisfied for provider %s", nodePool.Provider)
	return ctrl.Result{}, nil
}

// reconcileRemovedNodePool retains the last observed provider node-pool
// resources when spec.nodePool is removed from an existing runtime.
func (r *XTrinodeReconciler) reconcileRemovedNodePool(ctx context.Context, xtrinode *analyticsv1.XTrinode) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	retained, ok, err := xtrinodeWithRemovedObservedNodePool(xtrinode)
	if !ok {
		return ctrl.Result{}, nil
	}
	if err != nil {
		wrapped := fmt.Errorf("cannot retain removed node pool: %w", err)
		log.Error(wrapped, "failed to reconstruct removed node pool from observed status")
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolReady, metav1.ConditionFalse, status.ConditionReasonNodePoolFailed, wrapped.Error())
		r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolRetainFailed, "Failed to retain node pool after spec.nodePool removal: %v", err)
		if updateErr := r.updateStatus(ctx, xtrinode, log); updateErr != nil {
			log.Error(updateErr, "unable to update XTrinode status after node pool retention failure")
		}
		return ctrl.Result{RequeueAfter: config.NodePoolProvisioningErrorRequeueInterval}, wrapped
	}

	err = external.CallWithTimeout(ctx, config.NodePoolTimeout, func(ctx context.Context) error {
		return r.NodePoolAdapter.RetainNodePool(ctx, retained)
	})
	if err != nil {
		log.Error(err, "failed to retain removed node pool")
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolReady, metav1.ConditionFalse, status.ConditionReasonNodePoolFailed, fmt.Sprintf("Failed to retain removed node pool: %v", err))
		r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolRetainFailed, "Failed to retain node pool after spec.nodePool removal: %v", err)
		if updateErr := r.updateStatus(ctx, xtrinode, log); updateErr != nil {
			log.Error(updateErr, "unable to update XTrinode status after node pool retention failure")
		}
		return ctrl.Result{RequeueAfter: getNodePoolErrorRequeueInterval(retained.Spec.NodePool)}, err
	}

	if err := setObservedRuntimeShapeStatus(xtrinode); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to refresh observed runtime shape after node pool retention: %w", err)
	}
	status.SetCondition(xtrinode, status.ConditionTypeNodePoolReady, metav1.ConditionTrue, events.ReasonNodePoolRetained, "Node pool retained because spec.nodePool was removed")
	if err := r.updateStatus(ctx, xtrinode, log); err != nil {
		log.Error(err, "unable to update XTrinode status after node pool retention")
		return ctrl.Result{}, err
	}

	r.EventRecorder.Normalf(
		xtrinode,
		events.ReasonNodePoolRetained,
		"Node pool %q retained because spec.nodePool was removed",
		getNodePoolName(retained.Spec.NodePool, xtrinode.Name),
	)
	log.Info("Retained removed node pool", "nodePool", getNodePoolName(retained.Spec.NodePool, xtrinode.Name), "provider", retained.Spec.NodePool.Provider)
	return ctrl.Result{}, nil
}

func xtrinodeWithRemovedObservedNodePool(xtrinode *analyticsv1.XTrinode) (*analyticsv1.XTrinode, bool, error) {
	if xtrinode.Spec.NodePool != nil || xtrinode.Status.ObservedRuntimeShape == nil {
		return nil, false, nil
	}
	observed := &xtrinode.Status.ObservedRuntimeShape.NodePool
	if !observed.ProvisioningRequested {
		return nil, false, nil
	}

	nodePool, err := nodePoolSpecFromObservedStatus(observed)
	if err != nil {
		return nil, true, err
	}
	retained := xtrinode.DeepCopy()
	retained.Spec.NodePool = nodePool
	return retained, true, nil
}

func nodePoolSpecFromObservedStatus(observed *analyticsv1.ObservedRuntimeNodePoolStatus) (*analyticsv1.NodePoolSpec, error) {
	if observed.Provider == "" {
		return nil, fmt.Errorf("last observed node-pool provider is empty")
	}
	return &analyticsv1.NodePoolSpec{
		Provider:       observed.Provider,
		ProviderMode:   observed.ProviderMode,
		Name:           observed.Name,
		SchedulePods:   observed.SchedulePods,
		DeletionPolicy: analyticsv1.NodePoolDeletionPolicyRetain,
	}, nil
}

// waitForNodePoolReady checks whether the node-pool readiness requirement is satisfied.
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

	diagnostic, diagnosticErr := r.nodePoolProvisioningFailureDiagnostic(ctx, xtrinode, res)
	if diagnosticErr != nil {
		log.Error(diagnosticErr, "failed to inspect node pool provisioning diagnostics")
	}
	if diagnostic != nil {
		message := diagnostic.statusMessage()
		log.Info("Node pool provisioning reported failure", "xtrinode", xtrinode.Name, "message", message)
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolReady, metav1.ConditionFalse, status.ConditionReasonNodePoolFailed, message)
		r.EventRecorder.Warningf(xtrinode, events.ReasonNodePoolProvisionFailed, "Node pool provisioning reported failure: %s", message)
		metrics.NodePoolProvisionFailed.WithLabelValues(xtrinode.Namespace, xtrinode.Name, nodePool.Provider).Inc()
		if updateErr := r.updateStatus(ctx, xtrinode, log); updateErr != nil {
			return false, ctrl.Result{}, updateErr
		}
		return false, ctrl.Result{RequeueAfter: getNodePoolErrorRequeueInterval(nodePool)}, nil
	}

	// For minNodes > 0, wait for at least minNodes to be ready. For minNodes == 0,
	// the configurable required replica count defaults to 0 so scale-to-zero pools
	// do not block Trino resource creation before pending pods can trigger scale-up.
	requiredReplicas := requiredReadyReplicasForNodePool(xtrinode)
	if requiredReplicas == 0 {
		log.Info("Node pool readiness satisfied without ready replicas",
			"xtrinode", xtrinode.Name,
			"requiredReplicas", requiredReplicas)
		return true, ctrl.Result{}, nil
	}

	// Check status.readyReplicas only when at least one ready replica is required.
	st, found, _ := unstructured.NestedMap(res.Object, "status") //nolint:errcheck // best-effort status check; errors are non-critical
	if !found {
		requeueAfter := getNodePoolStatusNotAvailableRequeueInterval(nodePool)
		log.Info("Node pool status not available yet, requeuing", "name", nodePoolName, "requeueAfter", requeueAfter)
		return false, ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	readyReplicas, _, _ := unstructured.NestedInt64(st, "readyReplicas") //nolint:errcheck // best-effort status field extraction; defaults to 0 on error
	replicas, _, _ := unstructured.NestedInt64(st, "replicas")           //nolint:errcheck // best-effort status field extraction; defaults to 0 on error

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
