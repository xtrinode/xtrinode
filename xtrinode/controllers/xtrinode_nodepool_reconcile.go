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
