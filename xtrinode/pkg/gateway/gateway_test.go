package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRegisterRoute(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				Header: "X-Trino-XTrinode=dummy",
			},
		},
	}

	err := RegisterRoute(ctx, cli, xtrinode)
	if err != nil {
		t.Fatalf("RegisterRoute failed: %v", err)
	}
}

func TestRegisterRoute_UsesValuesOverlayServicePort(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				Default: true,
			},
			ValuesOverlay: &apiextensionsv1.JSON{
				Raw: []byte(`{"service":{"port":8181}}`),
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	configMap := &corev1.ConfigMap{}
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}, configMap))
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Backends, 1)
	require.Equal(t, "http://trino-runtime.team-a.svc.cluster.local:8181", routes[0].Backends[0].CoordinatorURL)
}

func TestRegisterRoute_UsesResolvedRuntimeShapeCapacity(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	maxWorkers := int32(3)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:       "s",
			MaxWorkers: &maxWorkers,
			Resources: &analyticsv1.RuntimeResourcesSpec{
				Worker: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("32Gi"),
					},
				},
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	configMap := &corev1.ConfigMap{}
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}, configMap))
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Backends, 1)
	require.Equal(t, "s", routes[0].Backends[0].Tier)
	require.Equal(t, 6, routes[0].Backends[0].CapacityUnits)
}

func TestRegisterRoute_UsesExplicitRoutingCapacityOverride(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	capacityUnits := int32(9)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				CapacityUnits: &capacityUnits,
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	configMap := &corev1.ConfigMap{}
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}, configMap))
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Backends, 1)
	require.Equal(t, 9, routes[0].Backends[0].CapacityUnits)
}

func TestRegisterRoute_UpdateExisting(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	// Register first XTrinode
	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				RoutingGroup: "dummy-group",
			},
		},
	}

	err := RegisterRoute(ctx, cli, xtrinode1)
	if err != nil {
		t.Fatalf("First RegisterRoute failed: %v", err)
	}

	// Register second XTrinode with shared routing group (should add to existing route)
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy-2",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				RoutingGroup: "shared", // Use shared pool to allow multiple backends
			},
		},
	}

	err = RegisterRoute(ctx, cli, xtrinode2)
	if err != nil {
		t.Fatalf("Second RegisterRoute failed: %v", err)
	}

	// Update existing XTrinode (should update backend in existing route)
	xtrinode1.Spec.Size = "m" // Change something
	err = RegisterRoute(ctx, cli, xtrinode1)
	if err != nil {
		t.Fatalf("Update RegisterRoute failed: %v", err)
	}
}

func TestRegisterRoute_RejectsSecondDefaultRoute(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	first := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-a",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				Header:  "X-Trino-XTrinode=default-a",
				Default: true,
			},
		},
	}
	second := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-b",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				Header:  "X-Trino-XTrinode=default-b",
				Default: true,
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, first))
	err := RegisterRoute(ctx, cli, second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "default route conflict")
}

func TestRegisterRoute_ClearsSingleBackendDefaultRoute(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-a",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				Header:  "X-Trino-XTrinode=default-a",
				Default: true,
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))
	xtrinode.Spec.Routing.Default = false
	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	configMap, err := getConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	require.NoError(t, err)
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.False(t, routes[0].Default)
}

func TestRegisterRoute_NamespaceQualifiesDefaultRoutingGroup(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	first := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size:    "s",
			Routing: &analyticsv1.RoutingSpec{},
		},
	}
	second := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-b"},
		Spec: analyticsv1.XTrinodeSpec{
			Size:    "s",
			Routing: &analyticsv1.RoutingSpec{},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, first))
	require.NoError(t, RegisterRoute(ctx, cli, second))

	configMap, err := getConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	require.NoError(t, err)
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 2)

	backendsByRoutingGroup := make(map[string]Backend)
	for _, route := range routes {
		require.Len(t, route.Backends, 1)
		backendsByRoutingGroup[route.RoutingGroup] = route.Backends[0]
	}

	require.Equal(t, "team-a", backendsByRoutingGroup["team-a--runtime"].Namespace)
	require.Equal(t, "runtime", backendsByRoutingGroup["team-a--runtime"].Name)
	require.Equal(t, "team-b", backendsByRoutingGroup["team-b--runtime"].Namespace)
	require.Equal(t, "runtime", backendsByRoutingGroup["team-b--runtime"].Name)
}

func TestRegisterRoute_ReplacesSingleBackendRouteSelectors(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				RoutingGroup: "analytics",
				Header:       "X-Trino-XTrinode=old",
				Hostname:     "old.example.com",
				Default:      true,
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))
	xtrinode.Spec.Routing.Header = "X-Trino-XTrinode=new"
	xtrinode.Spec.Routing.Hostname = ""
	xtrinode.Spec.Routing.Default = false
	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	configMap, err := getConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	require.NoError(t, err)
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, "new", routes[0].Header)
	require.Empty(t, routes[0].Hostname)
	require.False(t, routes[0].Default)
}

func TestRegisterRoute_RemovesBackendFromStaleRoutingGroup(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				RoutingGroup: "old-group",
				Hostname:     "old.example.com",
			},
		},
	}

	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))
	xtrinode.Spec.Routing.RoutingGroup = "new-group"
	xtrinode.Spec.Routing.Hostname = "new.example.com"
	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	configMap, err := getConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	require.NoError(t, err)
	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, "new-group", routes[0].RoutingGroup)
	require.Equal(t, "new.example.com", routes[0].Hostname)
	require.Len(t, routes[0].Backends, 1)
	require.Equal(t, "runtime", routes[0].Backends[0].Name)
}

func TestComputeBackendState(t *testing.T) {
	tests := []struct {
		name      string
		xtrinode  *analyticsv1.XTrinode
		wantState BackendState
	}{
		{
			name: "spec suspended wins",
			xtrinode: &analyticsv1.XTrinode{
				Spec:   analyticsv1.XTrinodeSpec{Suspended: true},
				Status: analyticsv1.XTrinodeStatus{Phase: "Ready"},
			},
			wantState: StatePaused,
		},
		{
			name:      "status suspended",
			xtrinode:  &analyticsv1.XTrinode{Status: analyticsv1.XTrinodeStatus{Phase: "Suspended"}},
			wantState: StatePaused,
		},
		{
			name: "reconciling from suspended means resuming",
			xtrinode: &analyticsv1.XTrinode{Status: analyticsv1.XTrinodeStatus{
				Phase: "Reconciling",
				Conditions: []metav1.Condition{
					{Type: "Suspended", Status: metav1.ConditionTrue},
				},
			}},
			wantState: StateResuming,
		},
		{
			name:      "reconciling without suspended condition is not routable",
			xtrinode:  &analyticsv1.XTrinode{Status: analyticsv1.XTrinodeStatus{Phase: "Reconciling"}},
			wantState: StateResuming,
		},
		{
			name:      "ready",
			xtrinode:  &analyticsv1.XTrinode{Status: analyticsv1.XTrinodeStatus{Phase: "Ready"}},
			wantState: StateRunning,
		},
		{
			name:      "error",
			xtrinode:  &analyticsv1.XTrinode{Status: analyticsv1.XTrinodeStatus{Phase: "Error"}},
			wantState: StatePaused,
		},
		{
			name:      "unknown phase fails closed",
			xtrinode:  &analyticsv1.XTrinode{Status: analyticsv1.XTrinodeStatus{Phase: "Provisioning"}},
			wantState: StateResuming,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantState, computeBackendState(tt.xtrinode))
		})
	}
}

func TestDeregisterRoute(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	// Deregister should succeed even if route doesn't exist
	err := DeregisterRoute(ctx, cli, xtrinode)
	if err != nil {
		t.Fatalf("DeregisterRoute failed: %v", err)
	}
}

func TestParseRoutes(t *testing.T) {
	// Test new YAML format with backends
	yamlData := `routes:
  - name: dummy
    routingGroup: dummy
    backends:
      - name: dummy
        coordinatorURL: http://coord:8080
        active: true
        tier: m
        capacityUnits: 4
    header: dummy
    hostname: ""
    default: false`
	routes, err := parseRoutes(yamlData)
	if err != nil {
		t.Fatalf("parseRoutes failed: %v", err)
	}

	if len(routes) != 1 {
		t.Fatalf("Expected 1 route, got %d", len(routes))
	}

	if routes[0].Name != "dummy" {
		t.Errorf("Expected name 'dummy', got '%s'", routes[0].Name)
	}

	if len(routes[0].Backends) != 1 {
		t.Fatalf("Expected 1 backend, got %d", len(routes[0].Backends))
	}

	if routes[0].Backends[0].CoordinatorURL != "http://coord:8080" {
		t.Errorf("Expected URL 'http://coord:8080', got '%s'", routes[0].Backends[0].CoordinatorURL)
	}
}

func TestSerializeRoutes(t *testing.T) {
	routes := []RouteEntry{
		{
			Name:         "dummy",
			RoutingGroup: "dummy",
			Backends: []Backend{
				{
					Name:           "dummy",
					CoordinatorURL: "http://coord:8080",
					Tier:           "m",
					CapacityUnits:  4,
					Active:         true,
				},
			},
			Header:   "dummy",
			Hostname: "",
			Default:  false,
		},
	}

	yaml := serializeRoutes(routes)
	if yaml == "" {
		t.Fatal("Expected non-empty YAML")
	}

	// Verify it's proper YAML format
	if !strings.Contains(yaml, "routes:") {
		t.Errorf("Expected YAML to contain 'routes:', got: %s", yaml)
	}
	if !strings.Contains(yaml, "name: dummy") {
		t.Errorf("Expected YAML to contain 'name: dummy', got: %s", yaml)
	}
	if !strings.Contains(yaml, "backends:") {
		t.Errorf("Expected YAML to contain 'backends:', got: %s", yaml)
	}
}

func TestFindRouteByRoutingGroup(t *testing.T) {
	routes := []RouteEntry{
		{RoutingGroup: "group1", Name: "route1"},
		{RoutingGroup: "group2", Name: "route2"},
		{RoutingGroup: "group3", Name: "route3"},
	}

	// Test finding existing route
	route, idx := findRouteByRoutingGroup(routes, "group2")
	if route == nil {
		t.Fatal("Expected to find route, got nil")
	}
	if route.Name != "route2" {
		t.Errorf("Expected route name 'route2', got '%s'", route.Name)
	}
	if idx != 1 {
		t.Errorf("Expected index 1, got %d", idx)
	}

	// Test finding non-existent route
	route, idx = findRouteByRoutingGroup(routes, "nonexistent")
	if route != nil {
		t.Errorf("Expected nil for non-existent route, got %v", route)
	}
	if idx != -1 {
		t.Errorf("Expected index -1, got %d", idx)
	}
}

func TestUpdateBackendInRoute(t *testing.T) {
	route := &RouteEntry{
		Name:         "test-route",
		RoutingGroup: "shared", // Use shared pool to allow multiple backends
		Backends: []Backend{
			{Name: "backend1", Namespace: "default", CoordinatorURL: "http://backend1:8080"},
		},
	}

	// Test adding new backend to shared pool
	newBackend := Backend{
		Name:           "backend2",
		Namespace:      "default",
		CoordinatorURL: "http://backend2:8080",
		Active:         true,
		Tier:           "m",
		CapacityUnits:  4,
	}
	require.NoError(t, updateBackendInRouteWithMode(route, &newBackend, "header-value", "hostname-value", true, false))

	if len(route.Backends) != 2 {
		t.Errorf("Expected 2 backends, got %d", len(route.Backends))
	}
	if route.Header != "header-value" {
		t.Errorf("Expected header 'header-value', got '%s'", route.Header)
	}
	if route.Hostname != "hostname-value" {
		t.Errorf("Expected hostname 'hostname-value', got '%s'", route.Hostname)
	}
	if !route.Default {
		t.Error("Expected Default to be true")
	}

	// Test updating existing backend
	updatedBackend := Backend{
		Name:           "backend1",
		Namespace:      "default",
		CoordinatorURL: "http://backend1-updated:8080",
		Active:         false,
		Tier:           "l",
		CapacityUnits:  8,
	}
	require.NoError(t, updateBackendInRouteWithMode(route, &updatedBackend, "", "", false, false))

	if len(route.Backends) != 2 {
		t.Errorf("Expected 2 backends, got %d", len(route.Backends))
	}
	if route.Backends[0].CoordinatorURL != "http://backend1-updated:8080" {
		t.Errorf("Expected updated URL, got '%s'", route.Backends[0].CoordinatorURL)
	}
	// Header and hostname should be preserved (not overwritten with empty)
	if route.Header != "header-value" {
		t.Errorf("Expected header to be preserved, got '%s'", route.Header)
	}
}

func TestUpdateBackendInRoute_ReconcilesInactiveBackend(t *testing.T) {
	route := &RouteEntry{
		Name:         "test-route",
		RoutingGroup: "test-route",
		Backends: []Backend{
			{
				Name:           "backend1",
				Namespace:      "default",
				CoordinatorURL: "http://backend1:8080",
				State:          StateRunning,
				Active:         false,
			},
		},
	}
	updatedBackend := Backend{
		Name:           "backend1",
		Namespace:      "default",
		CoordinatorURL: "http://backend1-updated:8080",
		State:          StateRunning,
		Active:         true,
		Tier:           "l",
		CapacityUnits:  8,
	}

	require.NoError(t, updateBackendInRouteWithMode(route, &updatedBackend, "", "", false, true))
	require.True(t, route.Backends[0].Active)
	require.Equal(t, "http://backend1-updated:8080", route.Backends[0].CoordinatorURL)
	require.Equal(t, StateRunning, route.Backends[0].State)
}

func TestUpdateBackendInRoute_ReactivatesAfterLifecycleStateChange(t *testing.T) {
	route := &RouteEntry{
		Name:         "test-route",
		RoutingGroup: "test-route",
		Backends: []Backend{
			{
				Name:           "backend1",
				Namespace:      "default",
				CoordinatorURL: "http://backend1:8080",
				State:          StateDraining,
				Active:         false,
				DrainUntil:     "2026-05-13T19:00:00Z",
			},
		},
	}
	updatedBackend := Backend{
		Name:           "backend1",
		Namespace:      "default",
		CoordinatorURL: "http://backend1-updated:8080",
		State:          StateRunning,
		Active:         true,
		Tier:           "l",
		CapacityUnits:  8,
	}

	require.NoError(t, updateBackendInRouteWithMode(route, &updatedBackend, "", "", false, true))
	require.True(t, route.Backends[0].Active)
	require.Equal(t, "http://backend1-updated:8080", route.Backends[0].CoordinatorURL)
	require.Equal(t, StateRunning, route.Backends[0].State)
	require.Empty(t, route.Backends[0].DrainUntil)
}

func TestUpdateBackendInRoute_ClearsDefaultForSingleBackendRoute(t *testing.T) {
	route := &RouteEntry{
		Name:         "dummy",
		RoutingGroup: "dummy",
		Default:      true,
		Backends: []Backend{
			{Name: "dummy", Namespace: "team-a", CoordinatorURL: "http://dummy:8080", Active: true},
		},
	}
	backend := Backend{Name: "dummy", Namespace: "team-a", CoordinatorURL: "http://dummy:8080", Active: true}

	require.NoError(t, updateBackendInRouteWithMode(route, &backend, "", "", false, true))
	require.False(t, route.Default)
}

func TestUpdateBackendInRouteWithMode_NamespaceQualifiedDedicatedRoute(t *testing.T) {
	route := &RouteEntry{
		Name:         "team-a--runtime",
		RoutingGroup: "team-a--runtime",
		Backends:     []Backend{},
	}
	ownerBackend := Backend{Name: "runtime", Namespace: "team-a", CoordinatorURL: "http://runtime.team-a:8080", Active: true}

	require.NoError(t, updateBackendInRouteWithMode(route, &ownerBackend, "", "", false, true))
	require.Len(t, route.Backends, 1)

	otherNamespaceBackend := Backend{Name: "runtime", Namespace: "team-b", CoordinatorURL: "http://runtime.team-b:8080", Active: true}
	err := updateBackendInRouteWithMode(route, &otherNamespaceBackend, "", "", false, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exclusivity violated")
	require.Len(t, route.Backends, 1)
	require.Equal(t, "team-a", route.Backends[0].Namespace)
}

func TestExtractIPFromAddr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "IPv4 with port",
			input:    "192.168.1.1:8080",
			expected: "192.168.1.1",
		},
		{
			name:     "IPv4 without port",
			input:    "192.168.1.1",
			expected: "192.168.1.1",
		},
		{
			name:     "IPv6 with port",
			input:    "[::1]:8080",
			expected: "::1",
		},
		{
			name:     "IPv6 without brackets",
			input:    "::1",
			expected: "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractIPFromAddr(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestExtractRoutingGroup(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		expected string
	}{
		{
			name: "with explicit routing group",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{
						RoutingGroup: "custom-group",
					},
				},
			},
			expected: "custom-group",
		},
		{
			name: "without routing group - defaults to namespace-qualified runtime identity",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA", Namespace: "default"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{},
				},
			},
			expected: "default--runtimeA",
		},
		{
			name: "nil routing spec - defaults to namespace-qualified runtime identity",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeB", Namespace: "default"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: nil,
				},
			},
			expected: "default--runtimeB",
		},
		{
			name: "empty namespace falls back to runtime name",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeC"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: nil,
				},
			},
			expected: "runtimeC",
		},
		{
			name: "shared pool",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtime-shared-1"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{
						RoutingGroup: "shared",
					},
				},
			},
			expected: "shared",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRoutingGroup(tt.xtrinode)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestParseHeaderValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "with equals sign",
			input:    "X-Trino-XTrinode=dummy",
			expected: "dummy",
		},
		{
			name:     "without equals sign",
			input:    "dummy",
			expected: "dummy",
		},
		{
			name:     "multiple equals",
			input:    "key=value=extra",
			expected: "value=extra", // Return everything after first "="
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseHeaderValue(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestBuildHostname(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		expected string
	}{
		{
			name: "explicit hostname",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{
						Hostname: "custom.company.com",
					},
				},
			},
			expected: "custom.company.com",
		},
		{
			name: "auto-generated hostname from domain with empty namespace",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{
						HostnameDomain: "trino-gw.company.com",
					},
				},
			},
			expected: "runtimeA.trino-gw.company.com",
		},
		{
			name: "auto-generated hostname from domain uses namespace-qualified routing group",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA", Namespace: "production"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{
						HostnameDomain: "trino-gw.company.com",
					},
				},
			},
			expected: "production--runtimeA.trino-gw.company.com",
		},
		{
			name: "explicit hostname takes precedence over domain",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{
						Hostname:       "explicit.company.com",
						HostnameDomain: "trino-gw.company.com",
					},
				},
			},
			expected: "explicit.company.com",
		},
		{
			name: "no hostname configuration",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: &analyticsv1.RoutingSpec{},
				},
			},
			expected: "",
		},
		{
			name: "nil routing spec",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtimeA"},
				Spec: analyticsv1.XTrinodeSpec{
					Routing: nil,
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildHostname(tt.xtrinode)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
