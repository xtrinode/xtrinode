package resources

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/sizing"
)

const (
	chartGoldenRevision    = "golden-revision"
	chartGoldenRolloutHash = "golden-rollout"
	chartGoldenNamespace   = "default"
)

func TestChartGoldenCoreDefaults(t *testing.T) {
	xtrinode := chartGoldenXTrinode("golden-defaults", nil)
	preset := sizing.Presets["s"]

	coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)
	workerConfigMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)

	assert.Equal(t, []string{"config.properties", "jvm.config", "log.properties", "node.properties"}, chartGoldenDataKeys(coordinatorConfigMap.Data))
	assert.Equal(t, []string{"config.properties", "jvm.config", "log.properties", "node.properties"}, chartGoldenDataKeys(workerConfigMap.Data))

	chartGoldenAssertProperties(t, coordinatorConfigMap.Data["config.properties"], map[string]string{
		"coordinator":                        "true",
		"node-scheduler.include-coordinator": "false",
		"http-server.http.port":              "8080",
		"discovery.uri":                      "http://localhost:8080",
	})
	chartGoldenAssertProperties(t, workerConfigMap.Data["config.properties"], map[string]string{
		"coordinator":           "false",
		"http-server.http.port": "8080",
		"discovery.uri":         fmt.Sprintf("http://%s:8080", config.BuildCoordinatorServiceName(xtrinode.Name)),
	})
	chartGoldenAssertMissingProperties(t, workerConfigMap.Data["config.properties"], "node-scheduler.include-coordinator")

	coordinatorDeployment, err := BuildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)
	workerDeployment, err := BuildWorkerDeployment(
		xtrinode,
		&preset,
		workerConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)

	coordinatorContainer := chartGoldenContainer(t, coordinatorDeployment, "trino-coordinator")
	workerContainer := chartGoldenContainer(t, workerDeployment, "trino-worker")
	assert.Equal(t, map[string]int32{"http": config.TrinoPortHTTP}, chartGoldenContainerPorts(coordinatorContainer))
	assert.Equal(t, map[string]int32{"http": config.TrinoPortHTTP}, chartGoldenContainerPorts(workerContainer))

	assert.Equal(t, "configmap:"+coordinatorConfigMap.Name, chartGoldenVolumeSource(coordinatorDeployment.Spec.Template.Spec.Volumes, "config-volume"))
	assert.Equal(t, "configmap:trino-golden-defaults-schemas-volume-coordinator", chartGoldenVolumeSource(coordinatorDeployment.Spec.Template.Spec.Volumes, "schemas-volume"))
	assert.Equal(t, "configmap:"+workerConfigMap.Name, chartGoldenVolumeSource(workerDeployment.Spec.Template.Spec.Volumes, "config-volume"))
	assert.Equal(t, "configmap:trino-golden-defaults-schemas-volume-worker", chartGoldenVolumeSource(workerDeployment.Spec.Template.Spec.Volumes, "schemas-volume"))

	assert.Equal(t, "disabled", coordinatorDeployment.Spec.Template.Labels["trino.io/network-policy-protection"])
	assert.Equal(t, "disabled", workerDeployment.Spec.Template.Labels["trino.io/network-policy-protection"])

	coordinatorService := BuildCoordinatorService(xtrinode)
	workerService := BuildWorkerService(xtrinode)
	assert.Equal(t, map[string]chartGoldenServicePort{
		"http": {Port: config.TrinoPortHTTP, TargetPort: "http"},
	}, chartGoldenServicePorts(coordinatorService))
	assert.Equal(t, map[string]chartGoldenServicePort{
		"http": {Port: config.TrinoPortHTTP, TargetPort: "http"},
	}, chartGoldenServicePorts(workerService))
	assert.Equal(t, corev1.ClusterIPNone, workerService.Spec.ClusterIP)
}

func TestChartGoldenServicePortOverride(t *testing.T) {
	xtrinode := chartGoldenXTrinode("golden-port", map[string]interface{}{
		"service": map[string]interface{}{
			"port": int64(8181),
		},
		"worker": map[string]interface{}{
			"gracefulShutdown": map[string]interface{}{
				"enabled":            true,
				"gracePeriodSeconds": int64(17),
			},
		},
	})
	xtrinode.Spec.HelmChartConfig = &analyticsv1.HelmChartConfigSpec{
		Ingress: &analyticsv1.IngressSpec{
			Enabled: true,
			Hosts: []analyticsv1.IngressHostSpec{
				{
					Host: "trino.example.com",
					Paths: []analyticsv1.IngressPathSpec{
						{Path: "/", PathType: "Prefix"},
					},
				},
			},
		},
	}
	preset := sizing.Presets["s"]

	coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)
	workerConfigMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)

	chartGoldenAssertProperties(t, coordinatorConfigMap.Data["config.properties"], map[string]string{
		"http-server.http.port": "8181",
		"discovery.uri":         "http://localhost:8181",
	})
	chartGoldenAssertProperties(t, workerConfigMap.Data["config.properties"], map[string]string{
		"http-server.http.port": "8181",
		"discovery.uri":         fmt.Sprintf("http://%s:8181", config.BuildCoordinatorServiceName(xtrinode.Name)),
		"shutdown.grace-period": "17s",
	})

	coordinatorDeployment, err := BuildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)
	workerDeployment, err := BuildWorkerDeployment(
		xtrinode,
		&preset,
		workerConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, int32(8181), chartGoldenContainerPorts(chartGoldenContainer(t, coordinatorDeployment, "trino-coordinator"))["http"])
	workerContainer := chartGoldenContainer(t, workerDeployment, "trino-worker")
	assert.Equal(t, int32(8181), chartGoldenContainerPorts(workerContainer)["http"])
	require.NotNil(t, workerContainer.Lifecycle)
	require.NotNil(t, workerContainer.Lifecycle.PreStop)
	require.NotNil(t, workerContainer.Lifecycle.PreStop.Exec)
	assert.Contains(t, strings.Join(workerContainer.Lifecycle.PreStop.Exec.Command, " "), "http://localhost:8181/v1/info/state")

	assert.Equal(t, int32(8181), chartGoldenServicePorts(BuildCoordinatorService(xtrinode))["http"].Port)
	assert.Equal(t, int32(8181), chartGoldenServicePorts(BuildWorkerService(xtrinode))["http"].Port)

	ingress := BuildIngress(xtrinode)
	require.NotNil(t, ingress)
	require.Len(t, ingress.Spec.Rules, 1)
	require.NotNil(t, ingress.Spec.Rules[0].HTTP)
	require.Len(t, ingress.Spec.Rules[0].HTTP.Paths, 1)
	require.NotNil(t, ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service)
	assert.Equal(t, int32(8181), ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number)
}

func TestChartGoldenAuthSecrets(t *testing.T) {
	const (
		passwordAuthFixture = "admin:hashed-password" // #nosec G101 -- test fixture, not a real credential.
		groupsAuthFixture   = "admin:admin"           // #nosec G101 -- test fixture, not a real credential.
	)

	xtrinode := chartGoldenXTrinode("golden-auth", map[string]interface{}{
		"server": map[string]interface{}{
			"config": map[string]interface{}{
				"authenticationType": "PASSWORD",
			},
		},
		"auth": map[string]interface{}{
			"passwordAuth":  passwordAuthFixture,
			"groups":        groupsAuthFixture,
			"refreshPeriod": "10s",
		},
	})
	preset := sizing.Presets["s"]

	passwordSecret := BuildPasswordAuthSecret(xtrinode)
	require.NotNil(t, passwordSecret)
	assert.Equal(t, "trino-golden-auth-password-file", passwordSecret.Name)
	assert.Equal(t, []byte(passwordAuthFixture), passwordSecret.Data["password.db"])

	groupsSecret := BuildGroupsAuthSecret(xtrinode)
	require.NotNil(t, groupsSecret)
	assert.Equal(t, "trino-golden-auth-groups-file", groupsSecret.Name)
	assert.Equal(t, []byte(groupsAuthFixture), groupsSecret.Data["group.db"])

	coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)
	chartGoldenAssertProperties(t, coordinatorConfigMap.Data["config.properties"], map[string]string{
		"http-server.authentication.type": "PASSWORD",
	})
	passwordAuthenticatorConfig := "password-authenticator.properties"                                        // #nosec G101 -- Trino property filename, not a credential.
	chartGoldenAssertProperties(t, coordinatorConfigMap.Data[passwordAuthenticatorConfig], map[string]string{ // #nosec G101 -- Trino property names, not credentials.
		"password-authenticator.name": "file",
		"file.password-file":          "/etc/trino/auth/password/password.db",
	})
	chartGoldenAssertProperties(t, coordinatorConfigMap.Data["group-provider.properties"], map[string]string{
		"group-provider.name": "file",
		"file.group-file":     "/etc/trino/auth/group/group.db",
		"file.refresh-period": "10s",
	})

	coordinatorDeployment, err := BuildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)
	coordinatorContainer := chartGoldenContainer(t, coordinatorDeployment, "trino-coordinator")

	assert.Equal(t, "secret:trino-golden-auth-password-file", chartGoldenVolumeSource(coordinatorDeployment.Spec.Template.Spec.Volumes, "file-password-authentication-volume"))
	assert.Equal(t, "secret:trino-golden-auth-groups-file", chartGoldenVolumeSource(coordinatorDeployment.Spec.Template.Spec.Volumes, "file-groups-authentication-volume"))
	assert.Equal(t, "/etc/trino/auth/password", chartGoldenVolumeMounts(coordinatorContainer)["file-password-authentication-volume"])
	assert.Equal(t, "/etc/trino/auth/group", chartGoldenVolumeMounts(coordinatorContainer)["file-groups-authentication-volume"])
}

func TestChartGoldenResourceGroups(t *testing.T) {
	preset := sizing.Presets["s"]

	t.Run("inline config map is coordinator only", func(t *testing.T) {
		xtrinode := chartGoldenXTrinode("golden-rg", map[string]interface{}{
			"resourceGroups": map[string]interface{}{
				"type":                 "configmap",
				"resourceGroupsConfig": `{"rootGroups":[]}`,
			},
		})

		coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
		require.NoError(t, err)
		workerConfigMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
		require.NoError(t, err)

		chartGoldenAssertProperties(t, coordinatorConfigMap.Data["resource-groups.properties"], map[string]string{
			"resource-groups.configuration-manager": "file",
			"resource-groups.config-file":           "/etc/trino/resource-groups/resource-groups.json",
		})
		assert.NotContains(t, workerConfigMap.Data, "resource-groups.properties")

		resourceGroupsConfigMap := BuildResourceGroupsConfigMapCoordinator(xtrinode)
		require.NotNil(t, resourceGroupsConfigMap)
		assert.Equal(t, "trino-golden-rg-resource-groups-volume-coordinator", resourceGroupsConfigMap.Name)
		assert.Equal(t, `{"rootGroups":[]}`, resourceGroupsConfigMap.Data["resource-groups.json"])
		assert.Nil(t, BuildResourceGroupsConfigMapWorker(xtrinode))

		coordinatorDeployment, err := BuildCoordinatorDeployment(
			xtrinode,
			&preset,
			coordinatorConfigMap.Name,
			nil,
			chartGoldenRevision,
			chartGoldenRolloutHash,
			nil,
		)
		require.NoError(t, err)
		workerDeployment, err := BuildWorkerDeployment(
			xtrinode,
			&preset,
			workerConfigMap.Name,
			nil,
			chartGoldenRevision,
			chartGoldenRolloutHash,
			nil,
		)
		require.NoError(t, err)

		assert.Equal(t, "configmap:trino-golden-rg-resource-groups-volume-coordinator", chartGoldenVolumeSource(coordinatorDeployment.Spec.Template.Spec.Volumes, "resource-groups-volume"))
		assert.Equal(t, "/etc/trino/resource-groups", chartGoldenVolumeMounts(chartGoldenContainer(t, coordinatorDeployment, "trino-coordinator"))["resource-groups-volume"])
		assert.Empty(t, chartGoldenVolumeSource(workerDeployment.Spec.Template.Spec.Volumes, "resource-groups-volume"))
		assert.Empty(t, chartGoldenVolumeMounts(chartGoldenContainer(t, workerDeployment, "trino-worker"))["resource-groups-volume"])
	})

	t.Run("external profile config map is mounted without generated config map", func(t *testing.T) {
		xtrinode := chartGoldenXTrinode("golden-rg-profile", nil)
		xtrinode.Spec.ResourceGroupsProfile = "existing-resource-groups"

		coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
		require.NoError(t, err)
		chartGoldenAssertProperties(t, coordinatorConfigMap.Data["resource-groups.properties"], map[string]string{
			"resource-groups.configuration-manager": "file",
			"resource-groups.config-file":           "/etc/trino/resource-groups/resource-groups.json",
		})
		assert.Nil(t, BuildResourceGroupsConfigMapCoordinator(xtrinode))

		coordinatorDeployment, err := BuildCoordinatorDeployment(
			xtrinode,
			&preset,
			coordinatorConfigMap.Name,
			nil,
			chartGoldenRevision,
			chartGoldenRolloutHash,
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, "configmap:existing-resource-groups", chartGoldenVolumeSource(coordinatorDeployment.Spec.Template.Spec.Volumes, "resource-groups-volume"))
	})
}

func TestChartGoldenJMX(t *testing.T) {
	xtrinode := chartGoldenXTrinode("golden-jmx", map[string]interface{}{
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
	})
	preset := sizing.Presets["s"]

	coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)
	workerConfigMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)

	for _, configMap := range []*corev1.ConfigMap{coordinatorConfigMap, workerConfigMap} {
		chartGoldenAssertProperties(t, configMap.Data["config.properties"], map[string]string{
			"jmx.rmiregistry.port": "19080",
			"jmx.rmiserver.port":   "19081",
		})
		assert.Contains(t, configMap.Data["jvm.config"], "-Dcom.sun.management.jmxremote.rmi.port=19081")
	}

	coordinatorJMXConfigMap := BuildJMXExporterConfigMap(xtrinode, "coordinator")
	require.NotNil(t, coordinatorJMXConfigMap)
	assert.Equal(t, "trino-golden-jmx-jmx-exporter-config-coordinator", coordinatorJMXConfigMap.Name)
	assert.Contains(t, coordinatorJMXConfigMap.Data["jmx-exporter-config.yaml"], "hostPort: localhost:19080")
	workerJMXConfigMap := BuildJMXExporterConfigMap(xtrinode, "worker")
	require.NotNil(t, workerJMXConfigMap)
	assert.Equal(t, "trino-golden-jmx-jmx-exporter-config-worker", workerJMXConfigMap.Name)
	assert.Contains(t, workerJMXConfigMap.Data["jmx-exporter-config.yaml"], "hostPort: localhost:19080")

	coordinatorDeployment, err := BuildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)
	workerDeployment, err := BuildWorkerDeployment(
		xtrinode,
		&preset,
		workerConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)

	for _, deployment := range []*appsv1.Deployment{coordinatorDeployment, workerDeployment} {
		role := strings.TrimPrefix(deployment.Name, config.BuildCoordinatorServiceName(xtrinode.Name)+"-")
		if role == "" {
			role = "coordinator"
		}
		if strings.HasSuffix(deployment.Name, "-worker") {
			role = "worker"
		}

		trinoContainer := chartGoldenContainer(t, deployment, "trino-"+role)
		assert.Equal(t, map[string]int32{
			"http":         config.TrinoPortHTTP,
			"jmx-registry": 19080,
			"jmx-server":   19081,
		}, chartGoldenContainerPorts(trinoContainer))

		jmxContainer := chartGoldenContainer(t, deployment, "jmx-exporter")
		assert.Equal(t, "example.com/jmx-exporter:test", jmxContainer.Image)
		assert.Equal(t, []string{"15556", "/etc/jmx-exporter/jmx-exporter-config.yaml"}, jmxContainer.Args)
		assert.Equal(t, map[string]int32{"jmx-exporter": 15556}, chartGoldenContainerPorts(jmxContainer))
		assert.Equal(t, "/etc/jmx-exporter", chartGoldenVolumeMounts(jmxContainer)["jmx-exporter-config-volume"])
		assert.Equal(t, fmt.Sprintf("configmap:trino-golden-jmx-jmx-exporter-config-%s", role), chartGoldenVolumeSource(deployment.Spec.Template.Spec.Volumes, "jmx-exporter-config-volume"))
	}

	assert.Equal(t, map[string]chartGoldenServicePort{
		"http":         {Port: config.TrinoPortHTTP, TargetPort: "http"},
		"jmx-exporter": {Port: 15556, TargetPort: "jmx-exporter"},
	}, chartGoldenServicePorts(BuildCoordinatorService(xtrinode)))
	assert.Equal(t, map[string]chartGoldenServicePort{
		"http":         {Port: config.TrinoPortHTTP, TargetPort: "http"},
		"jmx-exporter": {Port: 15556, TargetPort: "jmx-exporter"},
	}, chartGoldenServicePorts(BuildWorkerService(xtrinode)))

	coordinatorMetricsService := BuildCoordinatorMetricsService(xtrinode)
	require.NotNil(t, coordinatorMetricsService)
	assert.Equal(t, map[string]chartGoldenServicePort{
		"jmx-exporter": {Port: 15556, TargetPort: "15556"},
	}, chartGoldenServicePorts(coordinatorMetricsService))
	workerMetricsService := BuildWorkerMetricsService(xtrinode)
	require.NotNil(t, workerMetricsService)
	assert.Equal(t, map[string]chartGoldenServicePort{
		"jmx-exporter": {Port: 15556, TargetPort: "15556"},
	}, chartGoldenServicePorts(workerMetricsService))

	chartGoldenAssertServiceMonitorEndpointPort(t, BuildCoordinatorServiceMonitor(xtrinode), "jmx-exporter")
	chartGoldenAssertServiceMonitorEndpointPort(t, BuildWorkerServiceMonitor(xtrinode), "jmx-exporter")
}

func TestChartGoldenNetworkPolicy(t *testing.T) {
	xtrinode := chartGoldenXTrinode("golden-network-policy", nil)
	xtrinode.Spec.HelmChartConfig = &analyticsv1.HelmChartConfigSpec{
		NetworkPolicy: &analyticsv1.NetworkPolicySpec{
			Enabled: true,
		},
	}
	preset := sizing.Presets["s"]

	coordinatorConfigMap, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)
	workerConfigMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, chartGoldenRevision)
	require.NoError(t, err)
	coordinatorDeployment, err := BuildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)
	workerDeployment, err := BuildWorkerDeployment(
		xtrinode,
		&preset,
		workerConfigMap.Name,
		nil,
		chartGoldenRevision,
		chartGoldenRolloutHash,
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, "enabled", coordinatorDeployment.Spec.Template.Labels["trino.io/network-policy-protection"])
	assert.Equal(t, "enabled", workerDeployment.Spec.Template.Labels["trino.io/network-policy-protection"])

	networkPolicy := BuildNetworkPolicy(xtrinode)
	require.NotNil(t, networkPolicy)
	assert.Equal(t, "enabled", networkPolicy.Spec.PodSelector.MatchLabels["trino.io/network-policy-protection"])
	require.Len(t, networkPolicy.Spec.PodSelector.MatchExpressions, 1)
	assert.Equal(t, []string{ComponentCoordinator, ComponentWorker}, networkPolicy.Spec.PodSelector.MatchExpressions[0].Values)

	require.Len(t, networkPolicy.Spec.Ingress, 1)
	require.Len(t, networkPolicy.Spec.Ingress[0].From, 1)
	peer := networkPolicy.Spec.Ingress[0].From[0]
	require.NotNil(t, peer.PodSelector)
	assert.Equal(t, "enabled", peer.PodSelector.MatchLabels["trino.io/network-policy-protection"])
	require.NotNil(t, peer.NamespaceSelector)
	assert.Equal(t, chartGoldenNamespace, peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
}

func chartGoldenXTrinode(name string, valuesOverlay map[string]interface{}) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: chartGoldenNamespace,
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:          "s",
			ValuesOverlay: mustValuesOverlay(valuesOverlay),
		},
	}
}

func chartGoldenDataKeys(data map[string]string) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func chartGoldenAssertProperties(t *testing.T, raw string, want map[string]string) {
	t.Helper()
	got := chartGoldenProperties(t, raw)
	for key, wantValue := range want {
		assert.Equal(t, wantValue, got[key], "property %s", key)
	}
}

func chartGoldenAssertMissingProperties(t *testing.T, raw string, keys ...string) {
	t.Helper()
	got := chartGoldenProperties(t, raw)
	for _, key := range keys {
		assert.NotContains(t, got, key)
	}
}

func chartGoldenProperties(t *testing.T, raw string) map[string]string {
	t.Helper()
	props := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		require.True(t, ok, "property line %q should be key=value", line)
		props[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return props
}

func chartGoldenContainer(t *testing.T, deployment *appsv1.Deployment, name string) *corev1.Container {
	t.Helper()
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == name {
			return &deployment.Spec.Template.Spec.Containers[i]
		}
	}
	require.FailNowf(t, "missing container", "container %q not found in deployment %s", name, deployment.Name)
	return nil
}

func chartGoldenContainerPorts(container *corev1.Container) map[string]int32 {
	ports := make(map[string]int32, len(container.Ports))
	for _, port := range container.Ports {
		ports[port.Name] = port.ContainerPort
	}
	return ports
}

func chartGoldenVolumeMounts(container *corev1.Container) map[string]string {
	mounts := make(map[string]string, len(container.VolumeMounts))
	for _, mount := range container.VolumeMounts {
		mounts[mount.Name] = mount.MountPath
	}
	return mounts
}

func chartGoldenVolumeSource(volumes []corev1.Volume, name string) string {
	for i := range volumes {
		volume := &volumes[i]
		if volume.Name != name {
			continue
		}
		switch {
		case volume.ConfigMap != nil:
			return "configmap:" + volume.ConfigMap.Name
		case volume.Secret != nil:
			return "secret:" + volume.Secret.SecretName
		case volume.Projected != nil:
			return "projected"
		default:
			return "other"
		}
	}
	return ""
}

type chartGoldenServicePort struct {
	Port       int32
	TargetPort string
}

func chartGoldenServicePorts(service *corev1.Service) map[string]chartGoldenServicePort {
	ports := make(map[string]chartGoldenServicePort, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports[port.Name] = chartGoldenServicePort{
			Port:       port.Port,
			TargetPort: port.TargetPort.String(),
		}
	}
	return ports
}

func chartGoldenAssertServiceMonitorEndpointPort(t *testing.T, monitor interface{}, wantPort string) {
	t.Helper()
	require.NotNil(t, monitor)
	obj, ok := monitor.(*unstructured.Unstructured)
	require.True(t, ok, "expected unstructured ServiceMonitor")
	endpoints, found, err := unstructured.NestedSlice(obj.Object, "spec", "endpoints")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, endpoints, 1)
	endpoint, ok := endpoints[0].(map[string]interface{})
	require.True(t, ok, "expected endpoint map")
	assert.Equal(t, wantPort, endpoint["port"])
}
