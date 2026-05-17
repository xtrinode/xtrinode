package rollout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestComputeSecretDigestIncludesCatalogSecretValue(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"catalog": "runtime"},
			},
		},
	}
	pgCatalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgres",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"catalog": "runtime"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://postgres:5432/analytics",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "pg-auth"},
						Key:                  "password",
					},
				},
			},
		},
	}

	digestWith := func(secretData map[string][]byte) string {
		t.Helper()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pg-auth",
				Namespace: "team-a",
			},
			Data: secretData,
		}
		cli := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(pgCatalog.DeepCopy(), secret).
			Build()
		d, err := ComputeSecretDigest(context.Background(), cli, xtrinode)
		require.NoError(t, err)
		require.NotEmpty(t, d)
		return d
	}

	first := digestWith(map[string][]byte{
		"password": []byte("first-password"),
		"ignored":  []byte("first-ignored"),
	})
	second := digestWith(map[string][]byte{
		"password": []byte("second-password"),
		"ignored":  []byte("first-ignored"),
	})
	onlyUnreferencedChanged := digestWith(map[string][]byte{
		"password": []byte("first-password"),
		"ignored":  []byte("second-ignored"),
	})

	require.NotEqual(t, first, second, "catalog password Secret value changes must trigger rollout hash changes")
	require.Equal(t, first, onlyUnreferencedChanged, "unreferenced Secret keys should not trigger catalog password rollouts")
}

func TestRolloutHashesUseExpectedInputs(t *testing.T) {
	t.Parallel()

	inputs := RolloutInputs{
		BaseRevision:        "base-a",
		CatalogDigest:       "catalog-a",
		AccessControlDigest: "access-a",
		SessionPropsDigest:  "session-a",
		SecretDigest:        "secret-a",
		CoordConfig:         map[string]string{"role": "coordinator"},
		WorkerConfig:        map[string]string{"role": "worker"},
	}

	coordHash := CoordinatorRolloutHash(inputs)
	workerHash := WorkerRolloutHash(inputs, false)
	workerWithCatalogHash := WorkerRolloutHash(inputs, true)

	require.NotEmpty(t, coordHash)
	require.NotEmpty(t, workerHash)
	require.NotEqual(t, coordHash, workerHash)
	require.NotEqual(t, workerHash, workerWithCatalogHash)

	changedCatalog := inputs
	changedCatalog.CatalogDigest = "catalog-b"
	require.NotEqual(t, coordHash, CoordinatorRolloutHash(changedCatalog))
	require.Equal(t, workerHash, WorkerRolloutHash(changedCatalog, false))
	require.NotEqual(t, workerWithCatalogHash, WorkerRolloutHash(changedCatalog, true))

	changedWorker := inputs
	changedWorker.WorkerConfig = map[string]string{"role": "worker", "changed": "true"}
	require.NotEqual(t, workerHash, WorkerRolloutHash(changedWorker, false))
}

func TestComputeCatalogDigest(t *testing.T) {
	t.Parallel()

	_, cli := rolloutFakeClient(t,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "trino-catalog-hive", Namespace: "team-a"},
			Data:       map[string]string{"hive.properties": "connector.name=hive"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "trino-catalog-tpch", Namespace: "team-a"},
			Data:       map[string]string{"tpch.properties": "connector.name=tpch"},
		},
	)
	xtrinode := rolloutXTrinode()

	first, err := ComputeCatalogDigest(context.Background(), cli, xtrinode, []string{"tpch", "missing", "hive"})
	require.NoError(t, err)
	require.NotEmpty(t, first)

	second, err := ComputeCatalogDigest(context.Background(), cli, xtrinode, []string{"tpch", "hive"})
	require.NoError(t, err)
	require.Equal(t, first, second, "missing catalogs are ignored")
}

func TestComputeAccessControlDigest(t *testing.T) {
	t.Parallel()

	_, cli := rolloutFakeClient(t)
	xtrinode := rolloutXTrinode()

	empty, err := ComputeAccessControlDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.Empty(t, empty)

	xtrinode.Spec.HelmChartConfig = &analyticsv1.HelmChartConfigSpec{
		AccessControl: &analyticsv1.AccessControlSpec{
			Type:          "configmap",
			RefreshPeriod: "30s",
			ConfigFile:    "rules.json",
			Rules: map[string]string{
				"rules.json": `{"catalogs":[]}`,
			},
		},
	}
	first, err := ComputeAccessControlDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.NotEmpty(t, first)

	xtrinode.Spec.HelmChartConfig.AccessControl.Rules["rules.json"] = `{"catalogs":[{"allow":"all"}]}`
	second, err := ComputeAccessControlDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestComputeSessionPropsDigest(t *testing.T) {
	t.Parallel()

	_, cli := rolloutFakeClient(t)
	xtrinode := rolloutXTrinode()

	empty, err := ComputeSessionPropsDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.Empty(t, empty)

	xtrinode.Spec.ValuesOverlay = rolloutValuesOverlay(t, map[string]interface{}{
		"sessionProperties": map[string]interface{}{
			"type":                    "configmap",
			"sessionPropertiesConfig": `{"sessionProperties":[]}`,
		},
	})
	configMapDigest, err := ComputeSessionPropsDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.NotEmpty(t, configMapDigest)

	xtrinode.Spec.ValuesOverlay = rolloutValuesOverlay(t, map[string]interface{}{
		"sessionProperties": map[string]interface{}{
			"type":       "properties",
			"properties": "query_max_run_time=1h",
		},
	})
	propertiesDigest, err := ComputeSessionPropsDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.NotEmpty(t, propertiesDigest)
	require.NotEqual(t, configMapDigest, propertiesDigest)
}

func TestComputeSecretDigestIncludesConfiguredSecretSources(t *testing.T) {
	t.Parallel()

	xtrinode := rolloutXTrinode()
	xtrinode.Spec.TLS = &analyticsv1.TLSSpec{
		ServerSecretClass:   "server-class",
		InternalSecretClass: "internal-class",
	}
	xtrinode.Spec.HelmChartConfig = &analyticsv1.HelmChartConfigSpec{
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "registry-auth"}},
		SecretMounts: []analyticsv1.SecretMountSpec{
			{Name: "global", SecretName: "global-secret", Path: "/etc/global"},
		},
		Coordinator: &analyticsv1.CoordinatorHelmConfigSpec{
			SecretMounts: []analyticsv1.SecretMountSpec{
				{Name: "coord", SecretName: "coord-secret", Path: "/etc/coord"},
			},
		},
		Worker: &analyticsv1.WorkerHelmConfigSpec{
			SecretMounts: []analyticsv1.SecretMountSpec{
				{Name: "worker", SecretName: "worker-secret", Path: "/etc/worker"},
			},
		},
	}
	xtrinode.Spec.ValuesOverlay = rolloutValuesOverlay(t, map[string]interface{}{
		"auth": map[string]interface{}{
			"passwordAuthSecret": "password-secret",
			"groupsAuthSecret":   "groups-secret",
		},
	})

	digestFor := func(globalSecretValue string) string {
		t.Helper()
		_, cli := rolloutFakeClient(t,
			rolloutSecret("global-secret", globalSecretValue),
			rolloutSecret("coord-secret", "coord"),
			rolloutSecret("worker-secret", "worker"),
		)
		result, err := ComputeSecretDigest(context.Background(), cli, xtrinode)
		require.NoError(t, err)
		require.NotEmpty(t, result)
		return result
	}

	first := digestFor("global")
	second := digestFor("changed")
	require.NotEqual(t, first, second)

	_, cli := rolloutFakeClient(t)
	missingOnly, err := ComputeSecretDigest(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	require.NotEmpty(t, missingOnly, "secret names and TLS classes still affect the digest even before Secret objects exist")
}

func TestComputeSecretDigestIncludesTLSSecretBytes(t *testing.T) {
	t.Parallel()

	xtrinode := rolloutXTrinode()
	xtrinode.Spec.TLS = &analyticsv1.TLSSpec{
		ServerSecretClass:   "server-tls",
		InternalSecretClass: "internal-tls",
	}

	digestFor := func(serverCert, internalCert string) string {
		t.Helper()
		_, cli := rolloutFakeClient(t,
			rolloutSecret("server-tls", serverCert),
			rolloutSecret("internal-tls", internalCert),
		)
		result, err := ComputeSecretDigest(context.Background(), cli, xtrinode)
		require.NoError(t, err)
		require.NotEmpty(t, result)
		return result
	}

	first := digestFor("server-a", "internal-a")
	require.NotEqual(t, first, digestFor("server-b", "internal-a"))
	require.NotEqual(t, first, digestFor("server-a", "internal-b"))
}

func TestComputeSecretDigestIncludesMountedExternalResources(t *testing.T) {
	t.Parallel()

	xtrinode := rolloutXTrinode()
	xtrinode.Spec.ResourceGroupsProfile = "resource-groups"
	xtrinode.Spec.CustomConfigMaps = []string{"custom-config"}
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		JMXExporter: &analyticsv1.JMXExporterSpec{
			Enabled:   true,
			ConfigMap: "jmx-config",
		},
	}
	xtrinode.Spec.ValuesOverlay = rolloutValuesOverlay(t, map[string]interface{}{
		"configMounts": []interface{}{
			map[string]interface{}{
				"name":      "global-config",
				"configMap": "overlay-config",
				"path":      "/etc/trino/global-config",
			},
		},
		"secretMounts": []interface{}{
			map[string]interface{}{
				"name":       "global-secret",
				"secretName": "overlay-secret",
				"path":       "/etc/trino/global-secret",
			},
		},
		"envFrom": []interface{}{
			map[string]interface{}{
				"configMapRef": map[string]interface{}{"name": "env-config"},
			},
			map[string]interface{}{
				"secretRef": map[string]interface{}{"name": "env-secret"},
			},
		},
		"coordinator": map[string]interface{}{
			"additionalVolumes": []interface{}{
				map[string]interface{}{
					"name":      "additional-config",
					"configMap": map[string]interface{}{"name": "additional-config"},
				},
				map[string]interface{}{
					"name":   "additional-secret",
					"secret": map[string]interface{}{"secretName": "additional-secret"},
				},
				map[string]interface{}{
					"name": "projected",
					"projected": map[string]interface{}{
						"sources": []interface{}{
							map[string]interface{}{
								"configMap": map[string]interface{}{"name": "projected-config"},
							},
							map[string]interface{}{
								"secret": map[string]interface{}{"name": "projected-secret"},
							},
						},
					},
				},
			},
		},
		"worker": map[string]interface{}{
			"configMounts": []interface{}{
				map[string]interface{}{
					"name":      "worker-config",
					"configMap": "worker-config",
					"path":      "/etc/trino/worker-config",
				},
			},
			"secretMounts": []interface{}{
				map[string]interface{}{
					"name":       "worker-secret",
					"secretName": "worker-secret",
					"path":       "/etc/trino/worker-secret",
				},
			},
		},
	})

	require.ElementsMatch(t, []string{
		"resource-groups",
		"custom-config",
		"jmx-config",
		"overlay-config",
		"env-config",
		"additional-config",
		"projected-config",
		"worker-config",
	}, externalConfigMapReferences(xtrinode))
	require.ElementsMatch(t, []string{
		"overlay-secret",
		"env-secret",
		"additional-secret",
		"projected-secret",
		"worker-secret",
	}, externalSecretReferences(xtrinode))

	digestFor := func(overlayConfigValue, overlaySecretValue, jmxConfigValue string) string {
		t.Helper()
		_, cli := rolloutFakeClient(t,
			rolloutConfigMap("resource-groups", "resource-groups"),
			rolloutConfigMap("custom-config", "custom"),
			rolloutConfigMap("jmx-config", jmxConfigValue),
			rolloutConfigMap("overlay-config", overlayConfigValue),
			rolloutConfigMap("env-config", "env"),
			rolloutConfigMap("additional-config", "additional"),
			rolloutConfigMap("projected-config", "projected"),
			rolloutConfigMap("worker-config", "worker"),
			rolloutSecret("overlay-secret", overlaySecretValue),
			rolloutSecret("env-secret", "env"),
			rolloutSecret("additional-secret", "additional"),
			rolloutSecret("projected-secret", "projected"),
			rolloutSecret("worker-secret", "worker"),
		)
		result, err := ComputeSecretDigest(context.Background(), cli, xtrinode)
		require.NoError(t, err)
		require.NotEmpty(t, result)
		return result
	}

	first := digestFor("overlay-config", "overlay-secret", "jmx")
	require.NotEqual(t, first, digestFor("overlay-config-changed", "overlay-secret", "jmx"))
	require.NotEqual(t, first, digestFor("overlay-config", "overlay-secret-changed", "jmx"))
	require.NotEqual(t, first, digestFor("overlay-config", "overlay-secret", "jmx-changed"))
}

func TestStampRolloutHash(t *testing.T) {
	t.Parallel()

	template := &corev1.PodTemplateSpec{}
	StampRolloutHash(template, CoordinatorRolloutHashKey, "abc123")
	require.Equal(t, "abc123", template.Annotations[CoordinatorRolloutHashKey])

	StampRolloutHash(template, WorkerRolloutHashKey, "def456")
	require.Equal(t, "abc123", template.Annotations[CoordinatorRolloutHashKey])
	require.Equal(t, "def456", template.Annotations[WorkerRolloutHashKey])
}

func rolloutXTrinode() *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{Size: "s"},
	}
}

func rolloutFakeClient(t *testing.T, objects ...client.Object) (*runtime.Scheme, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()
	return scheme, cli
}

func rolloutValuesOverlay(t *testing.T, values map[string]interface{}) *apiextensionsv1.JSON {
	t.Helper()
	data, err := json.Marshal(values)
	require.NoError(t, err)
	return &apiextensionsv1.JSON{Raw: data}
}

func rolloutSecret(name, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "team-a",
		},
		Data: map[string][]byte{
			"value": []byte(value),
		},
	}
}

func rolloutConfigMap(name, value string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "team-a",
		},
		Data: map[string]string{
			"value": value,
		},
	}
}
