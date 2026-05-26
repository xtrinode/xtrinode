package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildDeploymentsDoNotRenderDeniedOverlaySidecarsAndEnvFrom(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"envFrom": []interface{}{
					map[string]interface{}{
						"secretRef": map[string]interface{}{"name": "runtime-env"},
					},
				},
				"sidecarContainers": map[string]interface{}{
					"coordinator": []interface{}{
						map[string]interface{}{
							"name":  "sidecar",
							"image": "example.invalid/sidecar:latest",
						},
					},
					"worker": []interface{}{
						map[string]interface{}{
							"name":  "sidecar",
							"image": "example.invalid/sidecar:latest",
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

	require.Len(t, coordinator.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "trino-coordinator", coordinator.Spec.Template.Spec.Containers[0].Name)
	assert.Empty(t, coordinator.Spec.Template.Spec.Containers[0].EnvFrom)

	require.Len(t, worker.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "trino-worker", worker.Spec.Template.Spec.Containers[0].Name)
	assert.Empty(t, worker.Spec.Template.Spec.Containers[0].EnvFrom)
}

func TestBuildDeploymentRejectsDeniedOverlayContainerSecurityContext(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"containerSecurityContext": map[string]interface{}{
					"privileged": true,
				},
			}),
		},
	}

	_, err := BuildCoordinatorDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "privileged containers are not allowed")
}

func TestBuildDeploymentRejectsDeniedOverlayHostPathVolume(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"coordinator": map[string]interface{}{
					"additionalVolumes": []interface{}{
						map[string]interface{}{
							"name": "host",
							"hostPath": map[string]interface{}{
								"path": "/var/run/docker.sock",
							},
						},
					},
				},
			}),
		},
	}

	_, err := BuildCoordinatorDeployment(xtrinode, "test-config", nil, "rev", "hash", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hostPath volumes are not allowed")
}

func TestBuildCoordinatorServiceClampsDeniedExternalServiceTypes(t *testing.T) {
	for _, serviceType := range []string{"NodePort", "LoadBalancer"} {
		t.Run(serviceType, func(t *testing.T) {
			xtrinode := &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"service": map[string]interface{}{
							"type":     serviceType,
							"nodePort": int64(32000),
						},
						"coordinator": map[string]interface{}{
							"additionalExposedPorts": map[string]interface{}{
								"debug": map[string]interface{}{
									"servicePort": int64(9191),
									"port":        int64(9191),
									"nodePort":    int64(32001),
								},
							},
						},
					}),
				},
			}

			service := BuildCoordinatorService(xtrinode)
			assert.Equal(t, corev1.ServiceTypeClusterIP, service.Spec.Type)
			for _, port := range service.Spec.Ports {
				assert.Zero(t, port.NodePort)
			}
		})
	}
}
