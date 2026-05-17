package status

import (
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestReadyInvariants_DefaultKEDADisabled(t *testing.T) {
	inv := GetInvariants(PhaseReady, &analyticsv1.XTrinode{})

	require.False(t, inv.KEDAEnabled)
	require.Zero(t, inv.KEDAMinReplicas)
	require.Zero(t, inv.KEDAMaxReplicas)
}

func TestReadyInvariants_KEDARequiresEnabledAndMetricConfig(t *testing.T) {
	enabled := true

	withoutMetric := GetInvariants(PhaseReady, &analyticsv1.XTrinode{
		Spec: analyticsv1.XTrinodeSpec{
			KEDA: &analyticsv1.KEDASpec{Enabled: &enabled},
		},
	})
	require.False(t, withoutMetric.KEDAEnabled)

	withMetric := GetInvariants(PhaseReady, &analyticsv1.XTrinode{
		Spec: analyticsv1.XTrinodeSpec{
			KEDA: &analyticsv1.KEDASpec{
				Enabled:       &enabled,
				ScalerType:    "prometheus",
				ScalingMetric: "query",
			},
		},
	})
	require.True(t, withMetric.KEDAEnabled)
}
