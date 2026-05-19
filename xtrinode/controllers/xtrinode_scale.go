package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

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
