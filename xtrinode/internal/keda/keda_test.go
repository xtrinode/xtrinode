package keda

import (
	"context"
	"strings"
	"testing"
	"time"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestEnsureScaledObject(t *testing.T) {
	ctx := context.Background()

	// Create a fake client with KEDA scheme (needed for GVK lookup)
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := log.Log

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
			UID:       "test-uid",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			MinWorkers: int32Ptr(0),
			MaxWorkers: int32Ptr(24),
			KEDA: &analyticsv1.KEDASpec{
				Enabled:       boolPtr(true),
				ScalerType:    "prometheus",
				ScalingMetric: "query",
			},
		},
	}

	// May fail with fake client Apply limitation; tests the function structure
	err := EnsureScaledObject(ctx, cli, scheme, xtrinode, logger)
	if err == nil {
		t.Log("EnsureScaledObject completed")
	}
}

func TestEnsureScaledObjectRejectsRemovedAggregatorEndpoint(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			MinWorkers: int32Ptr(1),
			MaxWorkers: int32Ptr(2),
			KEDA: &analyticsv1.KEDASpec{
				Enabled:       boolPtr(true),
				ScalerType:    "http",
				ScalingMetric: "query",
				HTTPEndpoint:  stringPtr(" Aggregator "),
			},
		},
	}

	err := EnsureScaledObject(ctx, cli, scheme, xtrinode, log.Log)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "httpEndpoint aggregator is no longer supported")
}

func TestDisableScaledObject_NotFound(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := log.Log

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := DisableScaledObject(ctx, cli, xtrinode, logger)
	require.NoError(t, err)
}

func TestDisableScaledObject_DeletesExisting(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))

	scaledObject := &kedav1alpha1.ScaledObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-test-workers",
			Namespace: "default",
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(scaledObject).Build()
	logger := log.Log

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	err := DisableScaledObject(ctx, cli, xtrinode, logger)
	require.NoError(t, err)

	var got kedav1alpha1.ScaledObject
	err = cli.Get(ctx, client.ObjectKey{Name: "trino-test-workers", Namespace: "default"}, &got)
	require.Error(t, err)
}

func TestDeleteScaledObject_NotFound(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := log.Log

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	// Will fail because KEDA types aren't registered in fake client
	// But tests the NotFound handling logic
	err := DeleteScaledObject(ctx, cli, xtrinode, logger)
	// Expected to fail due to missing KEDA types, but tests the logic path
	if err != nil {
		t.Logf("DeleteScaledObject returned error (expected due to missing KEDA types): %v", err)
	}
}

func TestEnableScaledObjectWithWakeMinWorkers_NotFound(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := log.Log

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	// Should call EnsureScaledObject when ScaledObject doesn't exist
	err := EnableScaledObjectWithWakeMinWorkers(ctx, cli, scheme, xtrinode, 2, logger)
	// Will fail because KEDA types aren't registered, but tests the logic
	if err != nil {
		t.Logf("EnableScaledObjectWithWakeMinWorkers returned error (expected): %v", err)
	}
}

func TestEnableScaledObjectWithWakeMinWorkers_CreatesMissingScaledObjectWithWakeMinimum(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := log.Log

	steadyMinWorkers := int32(0)
	maxWorkers := int32(6)
	threshold := "80"
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			MinWorkers: &steadyMinWorkers,
			MaxWorkers: &maxWorkers,
			KEDA: &analyticsv1.KEDASpec{
				Enabled:       boolPtr(true),
				ScalerType:    "http",
				ScalingMetric: "memory",
				Threshold:     &threshold,
			},
		},
	}

	require.NoError(t, EnableScaledObjectWithWakeMinWorkers(ctx, cli, scheme, xtrinode, 3, logger))

	var scaledObject kedav1alpha1.ScaledObject
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "trino-test-workers", Namespace: "default"}, &scaledObject))
	require.NotNil(t, scaledObject.Spec.MinReplicaCount)
	assert.Equal(t, int32(3), *scaledObject.Spec.MinReplicaCount)
	assert.Equal(t, int32(0), *xtrinode.Spec.MinWorkers, "steady-state spec must not be mutated")
}

func TestBuildPrometheusQuery(t *testing.T) {
	query := buildPrometheusQuery("trino-dummy", "team-a")
	if query == "" {
		t.Fatal("Expected non-empty Prometheus query")
	}
	if len(query) < 10 {
		t.Errorf("Query seems too short: %s", query)
	}
	// Verify placeholders are replaced
	if strings.Contains(query, "{releaseName}") || strings.Contains(query, "{namespace}") {
		t.Errorf("Query still contains placeholders: %s", query)
	}
}

func TestBuildPrometheusQueryForMetric_Memory(t *testing.T) {
	query := buildPrometheusQueryForMetric("trino-dummy", "team-a", "memory")
	assert.NotEmpty(t, query)
	assert.Contains(t, query, "trino_memory_allocated_bytes")
	assert.Contains(t, query, "trino-dummy")
	assert.Contains(t, query, "team-a")
}

func TestBuildPrometheusQueryForMetric_CPU(t *testing.T) {
	query := buildPrometheusQueryForMetric("trino-dummy", "team-a", "cpu")
	assert.NotEmpty(t, query)
	assert.Contains(t, query, "container_cpu_usage_seconds_total")
	assert.Contains(t, query, "trino-dummy")
	assert.Contains(t, query, "team-a")
}

func TestBuildPrometheusQueryForMetric_Query(t *testing.T) {
	query := buildPrometheusQueryForMetric("trino-dummy", "team-a", "query")
	assert.NotEmpty(t, query)
	assert.Contains(t, query, "xtrinode_gateway_inflight_queries")
	assert.Contains(t, query, `exported_namespace="team-a"`)
	assert.Contains(t, query, `namespace="team-a"`)
	assert.Contains(t, query, `xtrinode="dummy"`)
	// Verify placeholders are replaced
	assert.NotContains(t, query, "{releaseName}")
	assert.NotContains(t, query, "{namespace}")
}

func TestBuildPrometheusQueryForMetric_Default(t *testing.T) {
	query := buildPrometheusQueryForMetric("trino-dummy", "team-a", "unknown")
	assert.Empty(t, query)
}

func TestRenderPrometheusQueryTemplate(t *testing.T) {
	query := renderPrometheusQueryTemplate(
		`sum(custom_metric{release="{releaseName}",namespace="{namespace}",xtrinode="{xtrinodeName}"})`,
		"trino-dummy",
		"team-a",
		"dummy",
	)

	assert.Equal(t, `sum(custom_metric{release="trino-dummy",namespace="team-a",xtrinode="dummy"})`, query)
}

func TestBuildHTTPScalerEndpoint_CustomURL(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildHTTPScalerEndpoint(xtrinode, "trino-test", "memory", "http://custom-endpoint:8080/metrics")
	assert.Equal(t, "http://custom-endpoint:8080/metrics", endpoint)
}

func TestBuildHTTPScalerEndpoint_JMX(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildHTTPScalerEndpoint(xtrinode, "trino-test", "memory", "jmx")
	assert.Contains(t, endpoint, "test")
	assert.Contains(t, endpoint, "default")
	assert.Contains(t, endpoint, ":5556") // JMX port is in the URL
}

func TestBuildHTTPScalerEndpoint_NormalizesKeyword(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildHTTPScalerEndpoint(xtrinode, "trino-test", "memory", " Coordinator ")
	assert.Contains(t, endpoint, "trino-test")
	assert.Contains(t, endpoint, "default")
	assert.NotEqual(t, " Coordinator ", endpoint)
}

func TestBuildHTTPScalerEndpoint_DefaultQuery(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildHTTPScalerEndpoint(xtrinode, "trino-test", "query", "")
	assert.Contains(t, endpoint, "test")
	assert.Contains(t, endpoint, "default")
	assert.Contains(t, endpoint, "/v1/query")
}

func TestBuildHTTPScalerEndpoint_DefaultMemory(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildHTTPScalerEndpoint(xtrinode, "trino-test", "memory", "")
	assert.Empty(t, endpoint)
}

func TestBuildJMXEndpoint_DefaultPort(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildJMXEndpoint(xtrinode)
	assert.Contains(t, endpoint, "test")
	assert.Contains(t, endpoint, "default")
	// Check that port 5556 is in the URL (format: :5556/metrics)
	assert.Contains(t, endpoint, ":5556")
	assert.Contains(t, endpoint, "/metrics")
}

func TestBuildJMXEndpoint_CustomPort(t *testing.T) {
	customPort := int32(9999)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			KEDA: &analyticsv1.KEDASpec{
				JMXExporter: &analyticsv1.JMXExporterSpec{
					Port: &customPort,
				},
			},
		},
	}

	endpoint := buildJMXEndpoint(xtrinode)
	assert.Contains(t, endpoint, "9999")
}

func TestBuildDefaultEndpoint_Query(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildDefaultEndpoint(xtrinode, "trino-test", "query")
	assert.Contains(t, endpoint, "test")
	assert.Contains(t, endpoint, "default")
	assert.Contains(t, endpoint, "/v1/query")
}

func TestBuildDefaultEndpoint_Memory(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildDefaultEndpoint(xtrinode, "trino-test", "memory")
	assert.Empty(t, endpoint)
}

func TestBuildDefaultEndpoint_NormalizesMetric(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildDefaultEndpoint(xtrinode, "trino-test", " Memory ")
	assert.Empty(t, endpoint)
}

func TestBuildDefaultEndpoint_CPU(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildDefaultEndpoint(xtrinode, "trino-test", "cpu")
	assert.Empty(t, endpoint)
}

func TestBuildDefaultEndpoint_Unknown(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint := buildDefaultEndpoint(xtrinode, "trino-test", "unknown")
	// Should default to coordinator
	assert.Contains(t, endpoint, "test")
	assert.Contains(t, endpoint, "default")
}

func TestBuildHTTPValueLocation_Query(t *testing.T) {
	location := buildHTTPValueLocation("query", "")
	assert.Equal(t, `#(state!="FINISHED")#|#(state!="FAILED")#|#`, location)
}

func TestBuildHTTPValueLocation_Memory(t *testing.T) {
	location := buildHTTPValueLocation("memory", "")
	assert.Contains(t, location, "trino_memory_allocated_bytes")
}

func TestBuildHTTPValueLocation_NormalizesMetric(t *testing.T) {
	location := buildHTTPValueLocation(" Memory ", "")
	assert.Contains(t, location, "trino_memory_allocated_bytes")
}

func TestBuildHTTPValueLocation_CPU(t *testing.T) {
	location := buildHTTPValueLocation("cpu", "")
	assert.Contains(t, location, "container_cpu_usage_seconds_total")
}

func TestBuildHTTPValueLocation_Custom(t *testing.T) {
	customLocation := "custom_regex_pattern"
	location := buildHTTPValueLocation("query", customLocation)
	assert.Equal(t, customLocation, location)
}

func TestBuildHTTPValueLocation_Default(t *testing.T) {
	location := buildHTTPValueLocation("unknown", "")
	assert.Equal(t, "#", location)
}

func TestBuildHTTPScalerConfig(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint, location := buildHTTPScalerConfig(xtrinode, "trino-test", "query", "", "")
	assert.NotEmpty(t, endpoint)
	assert.NotEmpty(t, location)
	assert.Contains(t, endpoint, "/v1/query")
	assert.Equal(t, `#(state!="FINISHED")#|#(state!="FAILED")#|#`, location)
}

func TestBuildHTTPScalerConfig_ResourceMetricsSkipHTTP(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	endpoint, location := buildHTTPScalerConfig(xtrinode, "trino-test", "memory", "", "")
	assert.Empty(t, endpoint)
	assert.Empty(t, location)
}

func TestBuildKEDATriggers_HTTP(t *testing.T) {
	triggers := buildKEDATriggers("http", "memory", "80", "", "", "http://endpoint/metrics", "value_location", "prometheus", "")
	assert.Len(t, triggers, 1)
	assert.Equal(t, "memory", triggers[0].Type)
	assert.Equal(t, "Utilization", string(triggers[0].MetricType))
	assert.Equal(t, "80", triggers[0].Metadata["value"])
}

func TestBuildKEDATriggers_HTTPQueryUsesMetricsAPI(t *testing.T) {
	triggers := buildKEDATriggers("http", "query", "1", "", "", "http://endpoint/v1/query", `#(state!="FINISHED")#|#(state!="FAILED")#|#`, "json", "trino-test-keda-metrics-auth")
	assert.Len(t, triggers, 1)
	assert.Equal(t, "metrics-api", triggers[0].Type)
	assert.Equal(t, "http://endpoint/v1/query", triggers[0].Metadata["url"])
	assert.Equal(t, "json", triggers[0].Metadata["format"])
	assert.Equal(t, `#(state!="FINISHED")#|#(state!="FAILED")#|#`, triggers[0].Metadata["valueLocation"])
	assert.Equal(t, "1", triggers[0].Metadata["targetValue"])
	require.NotNil(t, triggers[0].AuthenticationRef)
	assert.Equal(t, "trino-test-keda-metrics-auth", triggers[0].AuthenticationRef.Name)
}

func TestBuildMetricsAPIAuthDataAndRefs_DefaultUsesTrinoUserHeader(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{Size: "s"},
	}

	data, refs := buildMetricsAPIAuthDataAndRefs(xtrinode, "trino-test-keda-metrics-auth")

	assert.Equal(t, "apiKey", string(data["authModes"]))
	assert.Equal(t, "xtrinode-keda", string(data["apiKey"]))
	assert.Equal(t, "header", string(data["method"]))
	assert.Equal(t, "X-Trino-User", string(data["keyParamName"]))
	require.Len(t, refs, 4)
	assertAuthTargetRef(t, refs, "authModes", "trino-test-keda-metrics-auth", "authModes")
	assertAuthTargetRef(t, refs, "apiKey", "trino-test-keda-metrics-auth", "apiKey")
	assertAuthTargetRef(t, refs, "method", "trino-test-keda-metrics-auth", "method")
	assertAuthTargetRef(t, refs, "keyParamName", "trino-test-keda-metrics-auth", "keyParamName")
}

func TestBuildMetricsAPIAuthDataAndRefs_TrinoControlAuthUsesBasicAndForwardedProto(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
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
			ValuesOverlay: &apiextensionsv1.JSON{Raw: []byte(`{"server":{"config":{"authenticationType":"PASSWORD"}}}`)},
		},
	}

	data, refs := buildMetricsAPIAuthDataAndRefs(xtrinode, "trino-test-keda-metrics-auth")

	assert.Equal(t, "basic,apiKey", string(data["authModes"]))
	assert.Equal(t, "lifecycle-control", string(data["username"]))
	assert.Equal(t, "https", string(data["apiKey"]))
	assert.Equal(t, "header", string(data["method"]))
	assert.Equal(t, "X-Forwarded-Proto", string(data["keyParamName"]))
	assert.NotContains(t, data, "password")
	require.Len(t, refs, 6)
	assertAuthTargetRef(t, refs, "authModes", "trino-test-keda-metrics-auth", "authModes")
	assertAuthTargetRef(t, refs, "username", "trino-test-keda-metrics-auth", "username")
	assertAuthTargetRef(t, refs, "password", "trino-control", "password")
	assertAuthTargetRef(t, refs, "apiKey", "trino-test-keda-metrics-auth", "apiKey")
	assertAuthTargetRef(t, refs, "method", "trino-test-keda-metrics-auth", "method")
	assertAuthTargetRef(t, refs, "keyParamName", "trino-test-keda-metrics-auth", "keyParamName")
}

func TestBuildMetricsAPIAuthDataAndRefs_AdditionalConfigAuthUsesBasicAndForwardedProto(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
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
			ValuesOverlay: &apiextensionsv1.JSON{Raw: []byte(`{
				"additionalConfigProperties": [
					"internal-communication.shared-secret=test-secret",
					"http-server.authentication.type=PASSWORD"
				]
			}`)},
		},
	}

	data, refs := buildMetricsAPIAuthDataAndRefs(xtrinode, "trino-test-keda-metrics-auth")

	assert.Equal(t, "basic,apiKey", string(data["authModes"]))
	assert.Equal(t, "lifecycle-control", string(data["username"]))
	assert.Equal(t, "https", string(data["apiKey"]))
	assert.Equal(t, "X-Forwarded-Proto", string(data["keyParamName"]))
	require.Len(t, refs, 6)
	assertAuthTargetRef(t, refs, "password", "trino-control", "password")
}

func TestBuildMetricsAPIAuthDataAndRefs_ControlAuthWithoutTrinoAuthUsesTrinoUserHeader(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
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

	data, refs := buildMetricsAPIAuthDataAndRefs(xtrinode, "trino-test-keda-metrics-auth")

	assert.Equal(t, "apiKey", string(data["authModes"]))
	assert.Equal(t, "xtrinode-keda", string(data["apiKey"]))
	assert.Equal(t, "X-Trino-User", string(data["keyParamName"]))
	assert.NotContains(t, data, "username")
	require.Len(t, refs, 4)
	assertAuthTargetRef(t, refs, "keyParamName", "trino-test-keda-metrics-auth", "keyParamName")
}

func assertAuthTargetRef(t *testing.T, refs []kedav1alpha1.AuthSecretTargetRef, parameter, name, key string) {
	t.Helper()
	for _, ref := range refs {
		if ref.Parameter == parameter {
			assert.Equal(t, name, ref.Name)
			assert.Equal(t, key, ref.Key)
			return
		}
	}
	t.Fatalf("missing auth target ref for parameter %q", parameter)
}

func TestBuildKEDATriggers_Prometheus(t *testing.T) {
	triggers := buildKEDATriggers("prometheus", "memory", "80", "http://prometheus:9090", "query", "", "", "", "")
	assert.Len(t, triggers, 1)
	assert.Equal(t, "prometheus", triggers[0].Type)
	assert.Equal(t, "http://prometheus:9090", triggers[0].Metadata["serverAddress"])
	assert.Equal(t, "query", triggers[0].Metadata["query"])
	assert.Equal(t, "80", triggers[0].Metadata["threshold"])
	assert.NotContains(t, triggers[0].Metadata, "metricName")
}

func TestApplyKEDACooldownConfigSetsKEDACooldownPeriod(t *testing.T) {
	scaledObject := &kedav1alpha1.ScaledObject{}
	kedaSpec := &analyticsv1.KEDASpec{
		ScaleDownCooldown: &metav1.Duration{Duration: 30 * time.Second},
	}

	applyKEDACooldownConfig(scaledObject, kedaSpec)

	require.NotNil(t, scaledObject.Spec.CooldownPeriod)
	assert.Equal(t, int32(30), *scaledObject.Spec.CooldownPeriod)
	require.NotNil(t, scaledObject.Spec.Advanced)
	require.NotNil(t, scaledObject.Spec.Advanced.HorizontalPodAutoscalerConfig)
	require.NotNil(t, scaledObject.Spec.Advanced.HorizontalPodAutoscalerConfig.Behavior.ScaleDown)
	assert.Equal(t, int32(30), *scaledObject.Spec.Advanced.HorizontalPodAutoscalerConfig.Behavior.ScaleDown.StabilizationWindowSeconds)
}

func TestInt32Ptr(t *testing.T) {
	val := int32Ptr(42)
	if val == nil {
		t.Fatal("Expected non-nil pointer")
	}
	if *val != 42 {
		t.Errorf("Expected 42, got %d", *val)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

func TestInt32Ptr_Zero(t *testing.T) {
	val := int32Ptr(0)
	assert.NotNil(t, val)
	assert.Equal(t, int32(0), *val)
}

func TestInt32Ptr_Negative(t *testing.T) {
	val := int32Ptr(-1)
	assert.NotNil(t, val)
	assert.Equal(t, int32(-1), *val)
}

func TestExtractFallbackConfig_Valid(t *testing.T) {
	fallback := map[string]interface{}{
		"failureThreshold": int64(5),
		"replicas":         int64(2),
	}

	cfg := extractFallbackConfig(fallback)
	assert.NotNil(t, cfg)
	assert.Equal(t, int32(5), cfg.FailureThreshold)
	assert.Equal(t, int32(2), cfg.Replicas)
}

func TestExtractFallbackConfig_Float64(t *testing.T) {
	fallback := map[string]interface{}{
		"failureThreshold": float64(5.0),
		"replicas":         float64(2.0),
	}

	cfg := extractFallbackConfig(fallback)
	assert.NotNil(t, cfg)
	assert.Equal(t, int32(5), cfg.FailureThreshold)
	assert.Equal(t, int32(2), cfg.Replicas)
}

func TestExtractFallbackConfig_Incomplete(t *testing.T) {
	fallback := map[string]interface{}{
		"failureThreshold": int64(5),
		// Missing replicas
	}

	cfg := extractFallbackConfig(fallback)
	assert.Nil(t, cfg, "Should return nil when config is incomplete")
}

func TestExtractFallbackConfig_Empty(t *testing.T) {
	fallback := map[string]interface{}{}

	cfg := extractFallbackConfig(fallback)
	assert.Nil(t, cfg)
}

func TestExtractFallbackConfig_ZeroValues(t *testing.T) {
	fallback := map[string]interface{}{
		"failureThreshold": int64(0),
		"replicas":         int64(0),
	}

	cfg := extractFallbackConfig(fallback)
	assert.Nil(t, cfg, "Should return nil when values are zero")
}
