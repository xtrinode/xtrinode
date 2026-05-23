package config

import (
	"testing"
	"time"
)

func TestBuildCoordinatorServiceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "dummy", "trino-dummy"},
		{"with dash", "test-xtrinode", "trino-test-xtrinode"},
		{"with numbers", "xtrinode123", "trino-xtrinode123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCoordinatorServiceName(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestBuildWorkerServiceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "dummy", "trino-dummy-worker"},
		{"with dash", "test-xtrinode", "trino-test-xtrinode-worker"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildWorkerServiceName(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestBuildCoordinatorURL(t *testing.T) {
	tests := []struct {
		name      string
		xtrinode  string
		namespace string
		expected  string
	}{
		{
			name:      "simple case",
			xtrinode:  "dummy",
			namespace: "team-a",
			expected:  "http://trino-dummy.team-a.svc.cluster.local:8080",
		},
		{
			name:      "with dash",
			xtrinode:  "test-xtrinode",
			namespace: "default",
			expected:  "http://trino-test-xtrinode.default.svc.cluster.local:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCoordinatorURL(tt.xtrinode, tt.namespace)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestBuildCoordinatorURLWithPort(t *testing.T) {
	tests := []struct {
		name      string
		xtrinode  string
		namespace string
		port      int
		expected  string
	}{
		{
			name:      "custom port",
			xtrinode:  "dummy",
			namespace: "team-a",
			port:      9000,
			expected:  "http://trino-dummy.team-a.svc.cluster.local:9000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCoordinatorURLWithPort(tt.xtrinode, tt.namespace, tt.port)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestBuildReleaseName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "dummy", "trino-dummy"},
		{"with dash", "test-xtrinode", "trino-test-xtrinode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildReleaseName(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestBuildCoordinatorMetricsURL(t *testing.T) {
	tests := []struct {
		name      string
		xtrinode  string
		namespace string
		expected  string
	}{
		{
			name:      "standard case",
			xtrinode:  "dummy",
			namespace: "team-a",
			expected:  "http://trino-dummy.team-a.svc.cluster.local:8080/metrics",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCoordinatorMetricsURL(tt.xtrinode, tt.namespace)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestBuildCoordinatorQueryAPIURL(t *testing.T) {
	result := BuildCoordinatorQueryAPIURL("dummy", "team-a")
	expected := "http://trino-dummy.team-a.svc.cluster.local:8080/v1/query"
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestBuildJMXMetricsURL(t *testing.T) {
	tests := []struct {
		name      string
		xtrinode  string
		namespace string
		jmxPort   int32
		expected  string
	}{
		{
			name:      "default JMX port",
			xtrinode:  "dummy",
			namespace: "team-a",
			jmxPort:   5556,
			expected:  "http://trino-dummy.team-a.svc.cluster.local:5556/metrics",
		},
		{
			name:      "custom JMX port",
			xtrinode:  "dummy",
			namespace: "team-a",
			jmxPort:   9999,
			expected:  "http://trino-dummy.team-a.svc.cluster.local:9999/metrics",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildJMXMetricsURL(tt.xtrinode, tt.namespace, tt.jmxPort)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	// Test port constants
	if TrinoPortHTTP != 8080 {
		t.Errorf("Expected TrinoPortHTTP to be 8080, got %d", TrinoPortHTTP)
	}
	if TrinoPortHTTPS != 8443 {
		t.Errorf("Expected TrinoPortHTTPS to be 8443, got %d", TrinoPortHTTPS)
	}
	if JMXExporterPort != 5556 {
		t.Errorf("Expected JMXExporterPort to be 5556, got %d", JMXExporterPort)
	}
	if TrinoJMXPort != 9080 {
		t.Errorf("Expected TrinoJMXPort to be 9080, got %d", TrinoJMXPort)
	}
	if TrinoJMXServerPort != 9081 {
		t.Errorf("Expected TrinoJMXServerPort to be 9081, got %d", TrinoJMXServerPort)
	}
	expectedJMXExporterImage := "bitnami/jmx-exporter@sha256:7c0014b7e1d736faec9760a89727389ba1ba7ad920c764417167abecfb7fd032"
	if DefaultJMXExporterImage != expectedJMXExporterImage {
		t.Errorf("Expected DefaultJMXExporterImage to be %s, got %s", expectedJMXExporterImage, DefaultJMXExporterImage)
	}
	if APIServerPort != 8081 {
		t.Errorf("Expected APIServerPort to be 8081, got %d", APIServerPort)
	}
	if GatewayPort != 8080 {
		t.Errorf("Expected GatewayPort to be 8080, got %d", GatewayPort)
	}

	// Test service name prefix
	if ServiceNamePrefix != "trino-" {
		t.Errorf("Expected ServiceNamePrefix to be 'trino-', got %s", ServiceNamePrefix)
	}

	// Test gateway config
	if GatewayConfigMapName != "trino-gateway-routes" {
		t.Errorf("Expected GatewayConfigMapName to be 'trino-gateway-routes', got %s", GatewayConfigMapName)
	}
	if GatewayConfigMapNamespace != "xtrinode-gateway" {
		t.Errorf("Expected GatewayConfigMapNamespace to be 'xtrinode-gateway', got %s", GatewayConfigMapNamespace)
	}
	if GatewayConfigMapKey != "routes.yaml" {
		t.Errorf("Expected GatewayConfigMapKey to be 'routes.yaml', got %s", GatewayConfigMapKey)
	}

	// Test namespace config
	if OperatorDefaultNamespace != "xtrinode-system" {
		t.Errorf("Expected OperatorDefaultNamespace to be 'xtrinode-system', got %s", OperatorDefaultNamespace)
	}
	if GatewayDefaultNamespace != "xtrinode-gateway" {
		t.Errorf("Expected GatewayDefaultNamespace to be 'xtrinode-gateway', got %s", GatewayDefaultNamespace)
	}
	if OperatorServiceName != "xtrinode-operator" {
		t.Errorf("Expected OperatorServiceName to be 'xtrinode-operator', got %s", OperatorServiceName)
	}

	// Test timeouts
	if HTTPClientTimeout != 5*time.Second {
		t.Errorf("Expected HTTPClientTimeout to be 5s, got %v", HTTPClientTimeout)
	}
	if GatewayShutdownTimeout != 5*time.Second {
		t.Errorf("Expected GatewayShutdownTimeout to be 5s, got %v", GatewayShutdownTimeout)
	}
	if GatewayRouteReloadInterval != 5*time.Second {
		t.Errorf("Expected GatewayRouteReloadInterval to be 5s, got %v", GatewayRouteReloadInterval)
	}

	// Test Redis configuration
	if GatewayRedisEnabled != false {
		t.Errorf("Expected GatewayRedisEnabled to be false by default, got %v", GatewayRedisEnabled)
	}
	if GatewayRedisStickyTTL != 1*time.Hour {
		t.Errorf("Expected GatewayRedisStickyTTL to be 1h, got %v", GatewayRedisStickyTTL)
	}
	if GatewayRedisTimeout != 1*time.Second {
		t.Errorf("Expected GatewayRedisTimeout to be 1s, got %v", GatewayRedisTimeout)
	}

	// Test API server configuration
	if GatewayAPIServerURL != "http://xtrinode-api-server:8081/api/v1" {
		t.Errorf("Expected GatewayAPIServerURL to be http://xtrinode-api-server:8081/api/v1, got %v", GatewayAPIServerURL)
	}
	if got := BuildAPIServerServiceURL("xtrinode-system"); got != "http://xtrinode-api-server.xtrinode-system.svc.cluster.local:8081/api/v1" {
		t.Errorf("Expected BuildAPIServerServiceURL to include API path, got %v", got)
	}
	if GatewayAPIServerTimeout != 5*time.Second {
		t.Errorf("Expected GatewayAPIServerTimeout to be 5s, got %v", GatewayAPIServerTimeout)
	}
	if GatewayDrainDuration != 5*time.Minute {
		t.Errorf("Expected GatewayDrainDuration to be 5m, got %v", GatewayDrainDuration)
	}
	if GatewayDrainRequeueInterval != 30*time.Second {
		t.Errorf("Expected GatewayDrainRequeueInterval to be 30s, got %v", GatewayDrainRequeueInterval)
	}
	if DrainStartedAtAnnotation != "xtrinode.analytics.xtrinode.io/drain-started-at" {
		t.Errorf("Expected DrainStartedAtAnnotation to be xtrinode analytics key, got %s", DrainStartedAtAnnotation)
	}
	if DrainCompletedAtAnnotation != "xtrinode.analytics.xtrinode.io/drain-completed-at" {
		t.Errorf("Expected DrainCompletedAtAnnotation to be xtrinode analytics key, got %s", DrainCompletedAtAnnotation)
	}
	if DrainResultAnnotation != "xtrinode.analytics.xtrinode.io/drain-result" {
		t.Errorf("Expected DrainResultAnnotation to be xtrinode analytics key, got %s", DrainResultAnnotation)
	}

	// Test HTTP paths
	if HealthPath != "/health" {
		t.Errorf("Expected HealthPath to be '/health', got %s", HealthPath)
	}
	if MetricsPath != "/metrics" {
		t.Errorf("Expected MetricsPath to be '/metrics', got %s", MetricsPath)
	}
	if QueryAPIPath != "/v1/query" {
		t.Errorf("Expected QueryAPIPath to be '/v1/query', got %s", QueryAPIPath)
	}
	if StatementAPIPath != "/v1/statement" {
		t.Errorf("Expected StatementAPIPath to be '/v1/statement', got %s", StatementAPIPath)
	}

	// Test service defaults
	if DefaultServiceType != "ClusterIP" {
		t.Errorf("Expected DefaultServiceType to be 'ClusterIP', got %s", DefaultServiceType)
	}
	if HeadlessServiceClusterIP != "None" {
		t.Errorf("Expected HeadlessServiceClusterIP to be 'None', got %s", HeadlessServiceClusterIP)
	}
	if DefaultBackendWeight != 100 {
		t.Errorf("Expected DefaultBackendWeight to be 100, got %d", DefaultBackendWeight)
	}
}
