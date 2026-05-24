package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/sizing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestJMXExporterUsesSeparateTrinoJMXPort(t *testing.T) {
	preset := sizing.Presets["s"]
	exporterPort := int32(6666)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			KEDA: &analyticsv1.KEDASpec{
				JMXExporter: &analyticsv1.JMXExporterSpec{
					Enabled: true,
					Port:    &exporterPort,
				},
			},
		},
	}

	configMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	assert.Contains(t, configMap.Data["jvm.config"], "-Dcom.sun.management.jmxremote.rmi.port=9081")
	assert.Contains(t, configMap.Data["config.properties"], "jmx.rmiregistry.port=9080")
	assert.Contains(t, configMap.Data["config.properties"], "jmx.rmiserver.port=9081")
	assert.NotContains(t, configMap.Data["config.properties"], "jmx.rmiregistry.port=6666")

	jmxConfigMap := BuildJMXExporterConfigMap(xtrinode, "worker")
	require.NotNil(t, jmxConfigMap)
	assert.Contains(t, jmxConfigMap.Data["jmx-exporter-config.yaml"], "hostPort: localhost:9080")

	container := buildJMXExporterContainer(xtrinode, "worker")
	require.Len(t, container.Ports, 1)
	assert.Equal(t, config.DefaultJMXExporterImage, container.Image)
	assert.Equal(t, exporterPort, container.Ports[0].ContainerPort)
	assert.NotEqual(t, int32(config.TrinoJMXPort), container.Ports[0].ContainerPort)
}

func TestValuesOverlayJMXExporterBuildsChartAlignedResources(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"jmx": map[string]interface{}{
					"enabled":      true,
					"registryPort": int64(19080),
					"serverPort":   int64(19081),
					"exporter": map[string]interface{}{
						"enabled": true,
						"image":   "example.com/jmx-exporter:test",
						"port":    int64(15556),
					},
				},
			}),
		},
	}

	configMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	assert.Contains(t, configMap.Data["jvm.config"], "-Dcom.sun.management.jmxremote.rmi.port=19081")
	assert.Contains(t, configMap.Data["config.properties"], "jmx.rmiregistry.port=19080")
	assert.Contains(t, configMap.Data["config.properties"], "jmx.rmiserver.port=19081")

	jmxConfigMap := BuildJMXExporterConfigMap(xtrinode, "worker")
	require.NotNil(t, jmxConfigMap)
	assert.Contains(t, jmxConfigMap.Data["jmx-exporter-config.yaml"], "hostPort: localhost:19080")

	deployment, err := BuildWorkerDeployment(xtrinode, "trino-test-trino-worker-rev", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.Len(t, deployment.Spec.Template.Spec.Containers, 2)
	assert.True(t, hasContainerPort(&deployment.Spec.Template.Spec.Containers[0], "jmx-registry", 19080))
	assert.True(t, hasContainerPort(&deployment.Spec.Template.Spec.Containers[0], "jmx-server", 19081))
	assert.Equal(t, "jmx-exporter", deployment.Spec.Template.Spec.Containers[1].Name)
	assert.Equal(t, int32(15556), deployment.Spec.Template.Spec.Containers[1].Ports[0].ContainerPort)
}
