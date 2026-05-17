package resources

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/sizing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildWorkerDeployment_DefaultFixedReplicas(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)

	require.NotNil(t, deployment.Spec.Replicas, "KEDA is opt-in, so fixed replicas should be set by default")
	assert.Equal(t, int32(2), *deployment.Spec.Replicas)
}

func TestBuildWorkerDeployment_KEDAEnabledOmitsReplicas(t *testing.T) {
	preset := sizing.Presets["s"]
	enabled := true
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			KEDA: &analyticsv1.KEDASpec{
				Enabled:       &enabled,
				ScalerType:    "prometheus",
				ScalingMetric: "query",
			},
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)

	assert.Nil(t, deployment.Spec.Replicas, "KEDA owns worker replicas when explicitly enabled with metric config")
}

func TestBuildWorkerDeployment_KEDADisabledUsesWorkersOverlay(t *testing.T) {
	preset := sizing.Presets["s"]
	disabled := false
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			KEDA: &analyticsv1.KEDASpec{
				Enabled: &disabled,
			},
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"server": map[string]interface{}{
					"workers": int64(4),
				},
			}),
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotNil(t, deployment.Spec.Replicas)

	assert.Equal(t, int32(4), *deployment.Spec.Replicas)
}

func TestBuildWorkerConfigMap_GracefulShutdownAddsWorkerAccessControl(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"worker": map[string]interface{}{
					"gracefulShutdown": map[string]interface{}{
						"enabled":            true,
						"gracePeriodSeconds": int64(180),
					},
				},
			}),
		},
	}

	configMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	assert.Contains(t, configMap.Data["access-control.properties"], "graceful-shutdown-rules.json")
	assert.Contains(t, configMap.Data["config.properties"], "shutdown.grace-period=180s")
	assert.NotContains(t, configMap.Data["config.properties"], "node-scheduler.include-coordinator")

	accessControlConfigMap := BuildAccessControlConfigMapWorker(xtrinode)
	require.NotNil(t, accessControlConfigMap)
	assert.Contains(t, accessControlConfigMap.Data["graceful-shutdown-rules.json"], `"user": "xtrinode-operator"`)
}

func TestBuildWorkerDeployment_GracefulShutdownUsesControlAuthSecret(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &analyticsv1.TrinoControlAuthSpec{
				Username: "lifecycle-control",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"worker": map[string]interface{}{
					"gracefulShutdown": map[string]interface{}{
						"enabled":            true,
						"gracePeriodSeconds": int64(180),
					},
				},
			}),
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	container := deployment.Spec.Template.Spec.Containers[0]
	envByName := map[string]corev1.EnvVar{}
	for _, env := range container.Env {
		envByName[env.Name] = env
	}
	assert.Equal(t, "lifecycle-control", envByName["XTRINODE_TRINO_CONTROL_USER"].Value)
	require.NotNil(t, envByName["XTRINODE_TRINO_CONTROL_PASSWORD"].ValueFrom)
	require.NotNil(t, envByName["XTRINODE_TRINO_CONTROL_PASSWORD"].ValueFrom.SecretKeyRef)
	assert.Equal(t, "trino-control", envByName["XTRINODE_TRINO_CONTROL_PASSWORD"].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", envByName["XTRINODE_TRINO_CONTROL_PASSWORD"].ValueFrom.SecretKeyRef.Key)

	require.NotNil(t, container.Lifecycle)
	require.NotNil(t, container.Lifecycle.PreStop)
	command := container.Lifecycle.PreStop.Exec.Command[2]
	assert.Contains(t, command, `-u "${XTRINODE_TRINO_CONTROL_USER}:${XTRINODE_TRINO_CONTROL_PASSWORD}"`)
	assert.Contains(t, command, `-H 'X-Forwarded-Proto: https'`)
	assert.Contains(t, command, `-H "X-Trino-User: ${XTRINODE_TRINO_CONTROL_USER}"`)
	assert.Contains(t, command, `exit $status`)
	assert.NotContains(t, command, `|| true`)
}

func TestBuildWorkerConfigMap_GracefulShutdownUsesControlUserInAccessControl(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &analyticsv1.TrinoControlAuthSpec{
				Username: "lifecycle-control",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"worker": map[string]interface{}{
					"gracefulShutdown": map[string]interface{}{
						"enabled": true,
					},
				},
			}),
		},
	}

	accessControlConfigMap := BuildAccessControlConfigMapWorker(xtrinode)
	require.NotNil(t, accessControlConfigMap)
	assert.Contains(t, accessControlConfigMap.Data["graceful-shutdown-rules.json"], `"user": "lifecycle-control"`)
}

func TestBuildWorkerConfigMap_PasswordAuthIncludesAuthenticatorConfig(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"server": map[string]interface{}{
					"config": map[string]interface{}{
						"authenticationType": "PASSWORD",
					},
				},
				"auth": map[string]interface{}{
					"passwordAuth": "lifecycle-control:hashed-password", // #nosec G101 -- test fixture for password-auth file rendering.
				},
			}),
		},
	}

	configMap, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	assert.Contains(t, configMap.Data["config.properties"], "http-server.authentication.type=PASSWORD")
	assert.Contains(t, configMap.Data["password-authenticator.properties"], "password-authenticator.name=file")
	assert.Contains(t, configMap.Data["password-authenticator.properties"], "file.password-file=/etc/trino/auth/password/password.db")
}

func TestBuildConfigMaps_RoutingEnablesForwardedHeaders(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				Header: "X-Trino-XTrinode=test-trino",
			},
		},
	}

	coordinator, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	worker, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	assert.Contains(t, coordinator.Data["config.properties"], "http-server.process-forwarded=true")
	assert.Contains(t, worker.Data["config.properties"], "http-server.process-forwarded=true")
}

func TestBuildConfigMaps_TrinoControlAuthEnablesForwardedHeadersWithoutRouting(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &analyticsv1.TrinoControlAuthSpec{
				Username: "lifecycle-control",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
		},
	}

	coordinator, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	worker, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	assert.Contains(t, coordinator.Data["config.properties"], "http-server.process-forwarded=true")
	assert.Contains(t, worker.Data["config.properties"], "http-server.process-forwarded=true")
}

func TestBuildWorkerDeployment_GracefulShutdownMountsAccessControl(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"worker": map[string]interface{}{
					"gracefulShutdown": map[string]interface{}{
						"enabled": true,
					},
				},
			}),
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotEmpty(t, deployment.Spec.Template.Spec.Containers)

	container := deployment.Spec.Template.Spec.Containers[0]
	require.NotNil(t, container.Lifecycle)
	require.NotNil(t, container.Lifecycle.PreStop)

	assert.True(t, slices.ContainsFunc(container.VolumeMounts, func(mount corev1.VolumeMount) bool {
		return mount.Name == "access-control-volume" && mount.MountPath == "/etc/trino/access-control"
	}))
	assert.True(t, slices.ContainsFunc(deployment.Spec.Template.Spec.Volumes, func(volume corev1.Volume) bool {
		return volume.Name == "access-control-volume" &&
			volume.ConfigMap != nil &&
			volume.ConfigMap.Name == "trino-test-trino-access-control-volume-worker"
	}))
}

func TestBuildWorkerDeployment_UsesWorkerOverlayForContainerSettings(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: mustValuesOverlay(map[string]interface{}{
				"coordinator": map[string]interface{}{
					"livenessProbe": map[string]interface{}{
						"httpGet": map[string]interface{}{
							"path": "/coordinator-only",
							"port": "http",
						},
					},
					"additionalExposedPorts": map[string]interface{}{
						"coordinator-debug": map[string]interface{}{
							"name":     "coordinator-debug",
							"port":     int64(9998),
							"protocol": "TCP",
						},
					},
				},
				"worker": map[string]interface{}{
					"livenessProbe": map[string]interface{}{
						"httpGet": map[string]interface{}{
							"path": "/worker-health",
							"port": "http",
						},
					},
					"startupProbe": map[string]interface{}{
						"exec": map[string]interface{}{
							"command": []interface{}{"/bin/true"},
						},
						"periodSeconds": int64(3),
					},
					"additionalExposedPorts": map[string]interface{}{
						"worker-debug": map[string]interface{}{
							"name":     "worker-debug",
							"port":     int64(9999),
							"protocol": "TCP",
						},
					},
					"additionalVolumeMounts": []interface{}{
						map[string]interface{}{
							"name":      "worker-extra",
							"mountPath": "/worker-extra",
						},
					},
				},
			}),
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotEmpty(t, deployment.Spec.Template.Spec.Containers)

	container := deployment.Spec.Template.Spec.Containers[0]
	require.NotNil(t, container.LivenessProbe)
	require.NotNil(t, container.LivenessProbe.HTTPGet)
	require.NotNil(t, container.StartupProbe)
	require.NotNil(t, container.StartupProbe.Exec)

	assert.Equal(t, "/worker-health", container.LivenessProbe.HTTPGet.Path)
	assert.Equal(t, int32(3), container.StartupProbe.PeriodSeconds)
	assert.Equal(t, []string{"/bin/true"}, container.StartupProbe.Exec.Command)
	assert.True(t, hasContainerPort(&container, "worker-debug", 9999))
	assert.False(t, hasContainerPort(&container, "coordinator-debug", 9998))
	assert.True(t, slices.ContainsFunc(container.VolumeMounts, func(mount corev1.VolumeMount) bool {
		return mount.Name == "worker-extra" && mount.MountPath == "/worker-extra"
	}))
}

func TestBuildWorkerDeployment_DefaultStartupProbeMatchesHelmChart(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	deployment, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "rev", "hash", nil)
	require.NoError(t, err)
	require.NotEmpty(t, deployment.Spec.Template.Spec.Containers)

	probe := deployment.Spec.Template.Spec.Containers[0].StartupProbe
	require.NotNil(t, probe)
	require.NotNil(t, probe.Exec)

	assert.Equal(t, []string{"/usr/lib/trino/bin/health-check"}, probe.Exec.Command)
	assert.Equal(t, int32(10), probe.InitialDelaySeconds)
	assert.Equal(t, int32(2), probe.PeriodSeconds)
	assert.Equal(t, int32(2), probe.TimeoutSeconds)
	assert.Equal(t, int32(60), probe.FailureThreshold)
	assert.Equal(t, int32(1), probe.SuccessThreshold)
}

func hasContainerPort(container *corev1.Container, name string, port int32) bool {
	return slices.ContainsFunc(container.Ports, func(containerPort corev1.ContainerPort) bool {
		return containerPort.Name == name && containerPort.ContainerPort == port
	})
}
