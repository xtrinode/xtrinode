package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestBuildDeploymentsIgnoreValuesOverlayRolloutFields(t *testing.T) {
	revisionHistoryLimit := int32(4)
	maxSurge := intstr.FromString("30%")
	maxUnavailable := intstr.FromInt32(1)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			RolloutPolicy: &analyticsv1.RolloutPolicySpec{
				RevisionHistoryLimit: &revisionHistoryLimit,
				RollingUpdateStrategy: &analyticsv1.RollingUpdateStrategySpec{
					MaxSurge:       &maxSurge,
					MaxUnavailable: &maxUnavailable,
				},
			},
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"coordinator": map[string]interface{}{
					"deployment": map[string]interface{}{
						"strategy": map[string]interface{}{
							"type": "Recreate",
							"rollingUpdate": map[string]interface{}{
								"maxSurge":       "90%",
								"maxUnavailable": "90%",
							},
						},
						"revisionHistoryLimit":    int64(1),
						"progressDeadlineSeconds": int64(123),
						"annotations": map[string]interface{}{
							"allowed": "coordinator",
						},
					},
				},
				"worker": map[string]interface{}{
					"deployment": map[string]interface{}{
						"strategy": map[string]interface{}{
							"type": "Recreate",
							"rollingUpdate": map[string]interface{}{
								"maxSurge":       "90%",
								"maxUnavailable": "90%",
							},
						},
						"revisionHistoryLimit":    int64(1),
						"progressDeadlineSeconds": int64(124),
						"annotations": map[string]interface{}{
							"allowed": "worker",
						},
					},
				},
			}),
		},
	}

	coordinator, err := BuildCoordinatorDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	worker, err := BuildWorkerDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)

	assertDeploymentUsesTypedRolloutPolicy(t, coordinator, revisionHistoryLimit, maxSurge, maxUnavailable)
	assert.Equal(t, "coordinator", coordinator.Annotations["allowed"])
	require.NotNil(t, coordinator.Spec.ProgressDeadlineSeconds)
	assert.Equal(t, int32(123), *coordinator.Spec.ProgressDeadlineSeconds)

	assertDeploymentUsesTypedRolloutPolicy(t, worker, revisionHistoryLimit, maxSurge, maxUnavailable)
	assert.Equal(t, "worker", worker.Annotations["allowed"])
	require.NotNil(t, worker.Spec.ProgressDeadlineSeconds)
	assert.Equal(t, int32(124), *worker.Spec.ProgressDeadlineSeconds)
}

func assertDeploymentUsesTypedRolloutPolicy(
	t *testing.T,
	deployment *appsv1.Deployment,
	revisionHistoryLimit int32,
	maxSurge intstr.IntOrString,
	maxUnavailable intstr.IntOrString,
) {
	t.Helper()

	require.NotNil(t, deployment.Spec.RevisionHistoryLimit)
	assert.Equal(t, revisionHistoryLimit, *deployment.Spec.RevisionHistoryLimit)
	assert.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, deployment.Spec.Strategy.Type)
	require.NotNil(t, deployment.Spec.Strategy.RollingUpdate)
	require.NotNil(t, deployment.Spec.Strategy.RollingUpdate.MaxSurge)
	require.NotNil(t, deployment.Spec.Strategy.RollingUpdate.MaxUnavailable)
	assert.Equal(t, maxSurge, *deployment.Spec.Strategy.RollingUpdate.MaxSurge)
	assert.Equal(t, maxUnavailable, *deployment.Spec.Strategy.RollingUpdate.MaxUnavailable)
}
