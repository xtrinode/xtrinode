package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	"github.com/xtrinode/xtrinode/internal/external"
	"github.com/xtrinode/xtrinode/internal/status"
)

// reconcileKEDA ensures KEDA ScaledObject configuration
func (r *XTrinodeReconciler) reconcileKEDA(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)
	// Ensure KEDA ScaledObject after node pool reconciliation so nodes can be ready.
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
		if isKEDAPlatformUnavailableError(err) {
			message := fmt.Sprintf("runtime KEDA is active, but the KEDA ScaledObject API is unavailable: %v", err)
			status.SetCondition(xtrinode, status.ConditionTypeKEDAReady, metav1.ConditionFalse, status.ConditionReasonKEDAPlatformMissing, message)
			//nolint:errcheck // best-effort status update; main error is already being returned
			_ = setXTrinodeErrorStatusAndUpdate(ctx, r.Client, r.Status(), xtrinode, log, status.ConditionReasonKEDAPlatformMissing, message, r.EventRecorder)
			return errors.New(message)
		}
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

func isKEDAPlatformUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if meta.IsNoMatchError(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no matches for kind") ||
		strings.Contains(message, "the server could not find the requested resource") ||
		(strings.Contains(message, "scaledobjects") && strings.Contains(message, "not found"))
}

// reconcileGateway registers gateway route
func (r *XTrinodeReconciler) reconcileGateway(ctx context.Context, xtrinode *analyticsv1.XTrinode) error {
	log := ctrl.LoggerFrom(ctx)
	// Register gateway route.
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
