package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/internal/trino/controlendpoint"
	"gopkg.in/yaml.v3"
)

const (
	// GatewayConfigMapName is the name of the ConfigMap storing gateway routes
	GatewayConfigMapName = config.GatewayConfigMapName
	// GatewayConfigMapNamespace is the namespace where the gateway ConfigMap lives
	GatewayConfigMapNamespace = config.GatewayConfigMapNamespace
	// GatewayConfigMapKey is the key in the ConfigMap storing route data
	GatewayConfigMapKey = config.GatewayConfigMapKey
)

// BackendState represents the lifecycle state of a backend
type BackendState string

const (
	// StateRunning means backend is ready and routable for NEW queries
	StateRunning BackendState = "RUNNING"
	// StatePaused means backend is scaled to 0, not routable for NEW queries
	StatePaused BackendState = "PAUSED"
	// StateResuming means resume requested, not ready yet (transitional)
	StateResuming BackendState = "RESUMING"
	// StateDraining means no NEW queries allowed, sticky queries still routed
	StateDraining BackendState = "DRAINING"
	// StateRemoved means backend not present in ConfigMap (terminal state)
	StateRemoved BackendState = "REMOVED"
)

// Backend represents a single backend coordinator in a routing group
// State machine: operator-owned (writes state), gateway-enforced (reads state)
type Backend struct {
	Name           string `yaml:"name"`
	Namespace      string `yaml:"namespace"` // Namespace for unique identity across cluster
	CoordinatorURL string `yaml:"coordinatorURL"`

	// State is the source of truth for backend lifecycle (operator-owned)
	State BackendState `yaml:"state,omitempty"`

	// Active allows manual route-level disablement.
	// Rule: If State != RUNNING, backend not eligible for NEW queries regardless of Active
	// Rule: If State == RUNNING and Active == false, also not eligible
	Active bool `yaml:"active"`

	Tier          string `yaml:"tier,omitempty"`          // Size tier: xs, s, m, l, xl, xxl
	CapacityUnits int    `yaml:"capacityUnits,omitempty"` // Normalized capacity for load balancing

	// DrainUntil is optional RFC3339 timestamp for drain completion (operational clarity)
	DrainUntil string `yaml:"drainUntil,omitempty"`
}

// RouteEntry represents a single route entry in the gateway
// Supports multiple backends (Backends[]) for target groups and load balancing
type RouteEntry struct {
	Name         string    `yaml:"name"`
	RoutingGroup string    `yaml:"routingGroup"`
	Backends     []Backend `yaml:"backends"` // Multiple backends for load balancing
	Header       string    `yaml:"header,omitempty"`
	Hostname     string    `yaml:"hostname,omitempty"`
	Default      bool      `yaml:"default,omitempty"`
}

func (b *Backend) UnmarshalYAML(value *yaml.Node) error {
	type backendAlias Backend
	backend := backendAlias{
		Active: true,
	}
	if err := value.Decode(&backend); err != nil {
		return err
	}
	*b = Backend(backend)
	return nil
}

// RegisterRoute registers a XTrinode with the Trino Gateway
// Implementation: Updates a ConfigMap that the gateway watches
// The gateway reads routes from the ConfigMap and routes queries accordingly
//
// State Machine (operator-owned, gateway-enforced):
// - Computes backend state from XTrinode CR status/phase
// - State determines routing eligibility (gateway enforces)
// - RUNNING: ready and routable for NEW queries
// - PAUSED: scaled to 0, not routable for NEW queries
// - RESUMING: resume requested, not ready yet
// - DRAINING: no NEW queries, sticky queries still routed
//
// Routing semantics:
// - routingGroup = namespace-qualified runtime identity (dedicated) or explicit pool name
// - hostname auto-generated from routing group + domain, or explicitly set
// - routing group organizes backend membership behind the scenes
func RegisterRoute(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) error {
	// Build coordinator URL from the generated HTTP service settings.
	coordinatorURL := controlendpoint.CoordinatorURL(xtrinode)

	// Extract routing configuration with dedicated-runtime and shared-pool semantics.
	routingGroup := extractRoutingGroup(xtrinode) // Defaults to namespace-qualified runtime identity
	hostname := buildHostname(xtrinode)           // Auto-generate or use explicit
	header := ""
	defaultRoute := false
	dedicatedRoute := isDedicatedRoutingGroup(xtrinode, routingGroup)

	if xtrinode.Spec.Routing != nil {
		if xtrinode.Spec.Routing.Header != "" {
			header = parseHeaderValue(xtrinode.Spec.Routing.Header)
		}
		defaultRoute = xtrinode.Spec.Routing.Default
	}

	// Get or create the gateway ConfigMap
	configMap, err := getOrCreateConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	if err != nil {
		return err
	}

	// Create backend entry for this XTrinode
	tier := xtrinode.Spec.Size
	capacityUnits := mapSizeToCapacityUnits(tier)

	// Compute backend state from XTrinode CR status (operator-owned)
	backendState := computeBackendState(xtrinode)

	backend := Backend{
		Name:           xtrinode.Name,
		Namespace:      xtrinode.Namespace,
		CoordinatorURL: coordinatorURL,
		State:          backendState,
		Active:         true,
		Tier:           tier,
		CapacityUnits:  capacityUnits,
	}

	// Update ConfigMap with retry logic for conflict handling
	return retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), logr.Discard(),
		func() error {
			// Refresh ConfigMap before updating
			key := client.ObjectKeyFromObject(configMap)
			return cli.Get(ctx, key, configMap)
		},
		func() error {
			ensureConfigMapData(configMap)
			routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
			if err != nil {
				// Abort update on parse error - don't overwrite with empty routes
				return fmt.Errorf("cannot update routes due to parse error: %w", err)
			}
			routes = removeBackendFromOtherRoutes(routes, routingGroup, xtrinode.Name, xtrinode.Namespace)
			// Validate route uniqueness (hostname, header must be unique)
			if err := validateRouteUniqueness(routes, routingGroup, hostname, header, defaultRoute); err != nil {
				return fmt.Errorf("route uniqueness violation: %w", err)
			}

			// Apply route update logic
			existingRoute, _ := findRouteByRoutingGroup(routes, routingGroup)
			if existingRoute != nil {
				if err := updateBackendInRouteWithMode(existingRoute, &backend, header, hostname, defaultRoute, dedicatedRoute); err != nil {
					return fmt.Errorf("route metadata conflict: %w", err)
				}
			} else {
				// Create new route entry
				// Use routing group as the route name for clarity (e.g., "runtimeA" or "shared")
				newRoute := createRouteEntry(routingGroup, routingGroup, header, hostname, &backend, defaultRoute)
				routes = append(routes, newRoute)
			}

			serializedRoutes := serializeRoutes(routes)
			if configMap.Data[GatewayConfigMapKey] == serializedRoutes {
				return nil
			}
			configMap.Data[GatewayConfigMapKey] = serializedRoutes
			return cli.Update(ctx, configMap)
		},
	)
}

// DrainRoute sets a backend to DRAINING state to prepare for removal.
// State machine: RUNNING/PAUSED → DRAINING (no NEW queries, sticky queries still routed)
// This is the first step in graceful backend removal.
// After drain condition met (time-based or query-based), call DeregisterRoute to fully remove.
//
// Drain policy:
// - Sets State = DRAINING
// - Sets DrainUntil = now + configured drain duration
// - Gateway will reject NEW queries but allow sticky queries
// - Operator should wait for drain condition before calling DeregisterRoute
func DrainRoute(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) error {
	return DrainRouteWithDuration(ctx, cli, xtrinode, config.GatewayDrainDuration)
}

func DrainRouteWithDuration(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, drainDuration time.Duration) error {
	// Get the gateway ConfigMap (read-only, don't create if missing)
	configMap, err := getConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	if err != nil {
		// If ConfigMap doesn't exist, nothing to drain
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Update ConfigMap with retry logic for conflict handling
	return retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), logr.Discard(),
		func() error {
			// Refresh ConfigMap before updating
			key := client.ObjectKeyFromObject(configMap)
			return cli.Get(ctx, key, configMap)
		},
		func() error {
			ensureConfigMapData(configMap)
			routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
			if err != nil {
				// Abort update on parse error
				return fmt.Errorf("cannot update routes due to parse error: %w", err)
			}
			// Set backend to DRAINING state with drain timestamp anywhere it exists.
			routes, changed := setBackendDrainingAllRoutes(routes, xtrinode.Name, xtrinode.Namespace, drainDuration)
			if !changed {
				return nil
			}
			configMap.Data[GatewayConfigMapKey] = serializeRoutes(routes)
			return cli.Update(ctx, configMap)
		},
	)
}

// DeregisterRoute removes a XTrinode from the Trino Gateway.
// For graceful removal, call DrainRoute first to set Active=false, wait for queries to complete,
// then call this function to fully remove the backend.
func DeregisterRoute(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) error {
	// Get the gateway ConfigMap (read-only, don't create if missing)
	configMap, err := getConfigMap(ctx, cli, GatewayConfigMapName, GatewayConfigMapNamespace)
	if err != nil {
		// If ConfigMap doesn't exist, nothing to clean up
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Update ConfigMap with retry logic for conflict handling
	return retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), logr.Discard(),
		func() error {
			// Refresh ConfigMap before updating
			key := client.ObjectKeyFromObject(configMap)
			return cli.Get(ctx, key, configMap)
		},
		func() error {
			ensureConfigMapData(configMap)
			routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
			if err != nil {
				// Abort update on parse error - don't overwrite with empty routes
				return fmt.Errorf("cannot update routes due to parse error: %w", err)
			}
			routes, changed := removeBackendFromAllRoutes(routes, xtrinode.Name, xtrinode.Namespace)
			if !changed {
				return nil
			}
			configMap.Data[GatewayConfigMapKey] = serializeRoutes(routes)
			return cli.Update(ctx, configMap)
		},
	)
}

// RoutesYAML represents the YAML structure for routes
type RoutesYAML struct {
	Routes []RouteEntry `yaml:"routes"`
}

// parseRoutes parses routes from YAML string
// Format: proper YAML with routes array containing backends
// Returns error if YAML is invalid to prevent route wipe
func parseRoutes(yamlData string) ([]RouteEntry, error) {
	if yamlData == "" {
		return []RouteEntry{}, nil
	}

	var routesYAML RoutesYAML
	if err := yaml.Unmarshal([]byte(yamlData), &routesYAML); err != nil {
		return nil, fmt.Errorf("failed to parse routes YAML: %w", err)
	}

	return routesYAML.Routes, nil
}

// serializeRoutes serializes routes to YAML string
// Format: proper YAML with routes array
func serializeRoutes(routes []RouteEntry) string {
	if len(routes) == 0 {
		return "routes: []\n"
	}

	routesYAML := RoutesYAML{
		Routes: routes,
	}

	yamlBytes, err := yaml.Marshal(&routesYAML)
	if err != nil {
		// Fallback to simple format if YAML marshal fails
		return fmt.Sprintf("# Error marshaling YAML: %v\nroutes: []\n", err)
	}

	return string(yamlBytes)
}

// computeBackendState determines backend state from XTrinode CR status/phase
// State machine rules:
// - Suspended → PAUSED (scaled to 0, not routable for NEW queries)
// - Reconciling → RESUMING (resources are changing, not ready yet)
// - Ready → RUNNING (ready and routable for NEW queries)
// - Unknown/default phases → RESUMING (fail closed until operator marks Ready)
// - Deleting (DeletionTimestamp set) → handled by DrainRoute, not here
func computeBackendState(xtrinode *analyticsv1.XTrinode) BackendState {
	// Check if suspended
	if xtrinode.Spec.Suspended {
		return StatePaused
	}

	// Check phase
	switch xtrinode.Status.Phase {
	case "Suspended":
		return StatePaused
	case "Reconciling":
		return StateResuming
	case "Ready":
		return StateRunning
	case "Error":
		// Error state - not routable
		return StatePaused
	default:
		// Unknown phase - fail closed until the operator explicitly marks Ready.
		return StateResuming
	}
}

func setBackendDrainingAllRoutes(routes []RouteEntry, backendName, backendNamespace string, drainDuration time.Duration) ([]RouteEntry, bool) {
	if drainDuration <= 0 {
		drainDuration = config.GatewayDrainDuration
	}
	drainUntil := time.Now().Add(drainDuration).Format(time.RFC3339)
	changed := false
	for i := range routes {
		for j := range routes[i].Backends {
			if routes[i].Backends[j].Name != backendName || routes[i].Backends[j].Namespace != backendNamespace {
				continue
			}
			if routes[i].Backends[j].State == StateDraining &&
				!routes[i].Backends[j].Active &&
				routes[i].Backends[j].DrainUntil != "" {
				continue
			}
			routes[i].Backends[j].State = StateDraining
			routes[i].Backends[j].DrainUntil = drainUntil
			routes[i].Backends[j].Active = false
			changed = true
		}
	}
	return routes, changed
}

func removeBackendFromOtherRoutes(routes []RouteEntry, targetRoutingGroup, backendName, backendNamespace string) []RouteEntry {
	filteredRoutes, _ := filterBackendFromRoutes(routes, backendName, backendNamespace, func(route RouteEntry) bool {
		return route.RoutingGroup != targetRoutingGroup
	})
	return filteredRoutes
}

func removeBackendFromAllRoutes(routes []RouteEntry, backendName, backendNamespace string) ([]RouteEntry, bool) {
	return filterBackendFromRoutes(routes, backendName, backendNamespace, func(RouteEntry) bool {
		return true
	})
}

func filterBackendFromRoutes(routes []RouteEntry, backendName, backendNamespace string, shouldFilter func(RouteEntry) bool) ([]RouteEntry, bool) {
	filteredRoutes := make([]RouteEntry, 0, len(routes))
	changed := false
	for _, route := range routes {
		if !shouldFilter(route) {
			filteredRoutes = append(filteredRoutes, route)
			continue
		}

		backends := make([]Backend, 0, len(route.Backends))
		for _, backend := range route.Backends {
			if backend.Name == backendName && backend.Namespace == backendNamespace {
				changed = true
				continue
			}
			backends = append(backends, backend)
		}
		if len(backends) == 0 {
			continue
		}
		route.Backends = backends
		filteredRoutes = append(filteredRoutes, route)
	}
	return filteredRoutes, changed
}
