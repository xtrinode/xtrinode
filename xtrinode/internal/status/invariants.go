package status

import (
	"time"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// PhaseInvariants defines the expected state for a lifecycle phase.
type PhaseInvariants struct {
	KEDAEnabled     bool
	KEDAMinReplicas int32
	KEDAMaxReplicas int32

	CoordReplicas     int32
	MinWorkerReplicas int32

	// NodePoolMinNodes is nil when the phase has no strict node pool floor.
	NodePoolMinNodes *int32

	GatewayEnabled bool
}

// GetInvariants returns the expected invariants for a given phase.
func GetInvariants(phase Phase, xtrinode *analyticsv1.XTrinode) PhaseInvariants {
	switch phase {
	case PhaseSuspended:
		return getSuspendedInvariants(xtrinode)
	case PhaseReady:
		return getReadyInvariants(xtrinode)
	case PhaseSuspending:
		return getSuspendingInvariants(xtrinode)
	case PhaseResuming:
		return getResumingInvariants(xtrinode)
	case PhaseReconciling:
		return getReconcilingInvariants(xtrinode)
	default:
		return PhaseInvariants{}
	}
}

func getSuspendedInvariants(xtrinode *analyticsv1.XTrinode) PhaseInvariants {
	inv := PhaseInvariants{
		KEDAEnabled:       false,
		KEDAMinReplicas:   0,
		KEDAMaxReplicas:   0,
		CoordReplicas:     0,
		MinWorkerReplicas: 0,
		GatewayEnabled:    true,
	}

	if xtrinode.Spec.NodePool != nil {
		scaleDownOnSuspend := true
		if xtrinode.Spec.NodePool.ScaleDownOnSuspend != nil {
			scaleDownOnSuspend = *xtrinode.Spec.NodePool.ScaleDownOnSuspend
		}
		if scaleDownOnSuspend {
			zero := int32(0)
			inv.NodePoolMinNodes = &zero
		}
	}

	return inv
}

func getReadyInvariants(xtrinode *analyticsv1.XTrinode) PhaseInvariants {
	inv := PhaseInvariants{
		KEDAEnabled:    isKEDAEnabled(xtrinode),
		CoordReplicas:  1,
		GatewayEnabled: true,
	}

	if inv.KEDAEnabled {
		inv.KEDAMinReplicas = 0
		inv.MinWorkerReplicas = 0

		if xtrinode.Status.Wake != nil && time.Now().Before(xtrinode.Status.Wake.ExpiresAt.Time) {
			inv.KEDAMinReplicas = xtrinode.Status.Wake.MinWorkers
		}

		if xtrinode.Spec.MaxWorkers != nil {
			inv.KEDAMaxReplicas = *xtrinode.Spec.MaxWorkers
		} else {
			inv.KEDAMaxReplicas = 10
		}
	} else if xtrinode.Spec.MaxWorkers != nil {
		inv.MinWorkerReplicas = *xtrinode.Spec.MaxWorkers
	}

	if xtrinode.Spec.NodePool != nil && xtrinode.Spec.NodePool.MinNodes != nil {
		minNodes := *xtrinode.Spec.NodePool.MinNodes
		inv.NodePoolMinNodes = &minNodes
	}

	return inv
}

func getSuspendingInvariants(xtrinode *analyticsv1.XTrinode) PhaseInvariants {
	return getSuspendedInvariants(xtrinode)
}

func getResumingInvariants(xtrinode *analyticsv1.XTrinode) PhaseInvariants {
	return getReadyInvariants(xtrinode)
}

func getReconcilingInvariants(xtrinode *analyticsv1.XTrinode) PhaseInvariants {
	return getReadyInvariants(xtrinode)
}

func isKEDAEnabled(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.KEDA == nil || xtrinode.Spec.KEDA.Enabled == nil {
		return false
	}
	return *xtrinode.Spec.KEDA.Enabled && hasKEDAMetricConfig(xtrinode.Spec.KEDA)
}

func hasKEDAMetricConfig(k *analyticsv1.KEDASpec) bool {
	return k.ScalerType != "" ||
		k.ScalingMetric != "" ||
		(k.PrometheusServer != nil && *k.PrometheusServer != "") ||
		(k.PrometheusQuery != nil && *k.PrometheusQuery != "") ||
		(k.HTTPEndpoint != nil && *k.HTTPEndpoint != "")
}
