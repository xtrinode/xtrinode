package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/sizing"
)

func TestResourceGroupsAreCoordinatorOnly(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"resourceGroups": map[string]interface{}{
					"type":                 "configmap",
					"resourceGroupsConfig": `{"rootGroups":[]}`,
				},
			}),
		},
	}

	coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	assert.Contains(t, coordinatorConfigMap.Data, "resource-groups.properties")

	workerConfigMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	assert.NotContains(t, workerConfigMap.Data, "resource-groups.properties")

	assert.NotNil(t, BuildResourceGroupsConfigMapCoordinator(xtrinode))
	assert.Nil(t, BuildResourceGroupsConfigMapWorker(xtrinode))

	coordinatorDeployment, err := BuildCoordinatorDeployment(xtrinode, "trino-test-trino-coordinator-rev", nil, "rev", "hash", nil)
	require.NoError(t, err)
	assert.Equal(t, "trino-test-trino-resource-groups-volume-coordinator", configMapVolumeName(coordinatorDeployment.Spec.Template.Spec.Volumes, "resource-groups-volume"))

	workerDeployment, err := BuildWorkerDeployment(xtrinode, "trino-test-trino-worker-rev", nil, "rev", "hash", nil)
	require.NoError(t, err)
	assert.Empty(t, configMapVolumeName(workerDeployment.Spec.Template.Spec.Volumes, "resource-groups-volume"))
}

func TestResourceGroupsProfileMountsUserConfigMap(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:                  "s",
			ResourceGroupsProfile: "bi-friendly",
		},
	}

	configMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	assert.Contains(t, configMap.Data, "resource-groups.properties")
	assert.Nil(t, BuildResourceGroupsConfigMapCoordinator(xtrinode))

	deployment, err := BuildCoordinatorDeployment(xtrinode, "trino-test-trino-coordinator-rev", nil, "rev", "hash", nil)
	require.NoError(t, err)
	assert.Equal(t, "bi-friendly", configMapVolumeName(deployment.Spec.Template.Spec.Volumes, "resource-groups-volume"))
}

func configMapVolumeName(volumes []corev1.Volume, volumeName string) string {
	for i := range volumes {
		volume := &volumes[i]
		if volume.Name == volumeName && volume.ConfigMap != nil {
			return volume.ConfigMap.Name
		}
	}
	return ""
}
