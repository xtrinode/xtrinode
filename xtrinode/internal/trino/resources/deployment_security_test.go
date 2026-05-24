package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestTrinoDeploymentsDisableServiceAccountTokenMount(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	coordinator, err := BuildCoordinatorDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotNil(t, coordinator.Spec.Template.Spec.AutomountServiceAccountToken)
	require.False(t, *coordinator.Spec.Template.Spec.AutomountServiceAccountToken)

	worker, err := BuildWorkerDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotNil(t, worker.Spec.Template.Spec.AutomountServiceAccountToken)
	require.False(t, *worker.Spec.Template.Spec.AutomountServiceAccountToken)
}

func TestTrinoDeploymentsEnableServiceAccountTokenForWorkloadIdentity(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				ServiceAccount: &analyticsv1.ServiceAccountSpec{
					Annotations: map[string]string{
						"iam.gke.io/gcp-service-account": "trino@example.iam.gserviceaccount.com",
					},
				},
			},
		},
	}

	coordinator, err := BuildCoordinatorDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotNil(t, coordinator.Spec.Template.Spec.AutomountServiceAccountToken)
	require.True(t, *coordinator.Spec.Template.Spec.AutomountServiceAccountToken)

	worker, err := BuildWorkerDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotNil(t, worker.Spec.Template.Spec.AutomountServiceAccountToken)
	require.True(t, *worker.Spec.Template.Spec.AutomountServiceAccountToken)
}
