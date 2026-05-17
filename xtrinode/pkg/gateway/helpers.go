package gateway

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// getConfigMap gets an existing ConfigMap without creating it.
// Returns the ConfigMap and nil error on success.
// Returns nil and the original error (including NotFound) on failure.
// Use this for read-only operations (DrainRoute, DeregisterRoute) that should
// not create a ConfigMap as a side effect.
func getConfigMap(ctx context.Context, cli client.Client, name, namespace string) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := cli.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)
	if err != nil {
		return nil, err
	}

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	return configMap, nil
}

// getOrCreateConfigMap gets an existing ConfigMap or creates it if it doesn't exist.
//
// Parameters:
//   - ctx: Context for the operation
//   - cli: Kubernetes client
//   - name: ConfigMap name
//   - namespace: ConfigMap namespace
//
// Returns:
//   - *corev1.ConfigMap: The ConfigMap (existing or newly created)
//   - error: Error if operation fails
func getOrCreateConfigMap(ctx context.Context, cli client.Client, name, namespace string) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := cli.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get gateway ConfigMap: %w", err)
		}
		// ConfigMap doesn't exist, create it
		configMap.Data = make(map[string]string)
		configMap.Data[GatewayConfigMapKey] = ""
		if err := cli.Create(ctx, configMap); err != nil {
			// Handle race condition: another process may have created it
			if k8serrors.IsAlreadyExists(err) {
				// Retry Get to fetch the existing ConfigMap
				if getErr := cli.Get(ctx, client.ObjectKeyFromObject(configMap), configMap); getErr != nil {
					return nil, fmt.Errorf("failed to get ConfigMap after AlreadyExists: %w", getErr)
				}
				// Successfully retrieved existing ConfigMap, continue
			} else {
				return nil, fmt.Errorf("failed to create gateway ConfigMap: %w", err)
			}
		}
	}

	// Ensure Data map is initialized (defensive check)
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	return configMap, nil
}

// ensureConfigMapData ensures the ConfigMap's Data map is initialized and key exists.
// This prevents "missing key → parse empty → clear routes" surprises.
func ensureConfigMapData(configMap *corev1.ConfigMap) {
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}
	if _, ok := configMap.Data[GatewayConfigMapKey]; !ok {
		configMap.Data[GatewayConfigMapKey] = ""
	}
}

// findRouteByRoutingGroup finds a route by its routing group name.
//
// Parameters:
//   - routes: Slice of routes to search
//   - routingGroup: Routing group name to find
//
// Returns:
//   - *RouteEntry: Pointer to the found route, or nil if not found
//   - int: Index of the route in the slice, or -1 if not found
func findRouteByRoutingGroup(routes []RouteEntry, routingGroup string) (route *RouteEntry, index int) {
	for i := range routes {
		if routes[i].RoutingGroup == routingGroup {
			return &routes[i], i
		}
	}
	return nil, -1
}

// updateBackendInRoute updates or adds a backend to a route.
//
// This function handles:
//   - Updating existing backend if found (matches by Name + Namespace)
//   - Adding new backend if not found
//   - Updating route metadata after uniqueness validation
//   - Enforcing dedicated runtime exclusivity
//
// Dedicated vs Shared semantics:
//   - Shared pool: Allows multiple backends from different XTrinodes
//   - Dedicated: Allows exactly one backend, must be from the same XTrinode that owns the route
func updateBackendInRouteWithMode(route *RouteEntry, backend *Backend, header, hostname string, defaultRoute, dedicatedRoute bool) error {
	// Check if we're updating an existing backend
	found := false
	for i, b := range route.Backends {
		if b.Name == backend.Name && b.Namespace == backend.Namespace {
			// Updating existing backend - always allowed
			route.Backends[i] = *backend
			found = true
			break
		}
	}

	if !found {
		// Adding new backend - enforce exclusivity for dedicated runtimes
		if dedicatedRoute {
			if len(route.Backends) > 0 {
				existingBackend := route.Backends[0]
				return fmt.Errorf("dedicated runtime '%s' exclusivity violated: route already contains backend '%s/%s'",
					route.RoutingGroup, existingBackend.Namespace, existingBackend.Name)
			}
		}

		route.Backends = append(route.Backends, *backend)
	}

	backendOwnsOnlyRouteEntry := len(route.Backends) == 1 &&
		route.Backends[0].Name == backend.Name &&
		route.Backends[0].Namespace == backend.Namespace

	if backendOwnsOnlyRouteEntry {
		// Single-backend routes can replace or clear selector metadata during
		// break-glass routing identity changes.
		route.Header = header
		route.Hostname = hostname
		route.Default = defaultRoute
		return nil
	}

	// Multi-backend route metadata is shared by the pool. Non-empty selectors
	// may be moved to the new value after uniqueness validation; empty values
	// do not clear shared metadata because another backend may still depend on it.
	if header != "" {
		route.Header = header
	}
	if hostname != "" {
		route.Hostname = hostname
	}
	if defaultRoute {
		route.Default = true
	}

	return nil
}

// createRouteEntry creates a new route entry with a single backend.
func createRouteEntry(name, routingGroup, header, hostname string, backend *Backend, defaultRoute bool) RouteEntry {
	return RouteEntry{
		Name:         name,
		RoutingGroup: routingGroup,
		Header:       header,
		Hostname:     hostname,
		Backends:     []Backend{*backend},
		Default:      defaultRoute,
	}
}

// validateRouteUniqueness checks that hostname, routingGroup, header, and default route are unique across routes.
// Returns error if any conflicts are found.
// Normalizes hostnames the same way as loadRoutes() to catch conflicts like "Host:443" vs "host".
func validateRouteUniqueness(routes []RouteEntry, newRoutingGroup, newHostname, newHeader string, newDefault bool) error {
	// Normalize new hostname for comparison
	normalizedNewHostname := ""
	if newHostname != "" {
		normalizedNewHostname = normalizeHostname(newHostname)
	}

	for _, route := range routes {
		// Skip the route we're updating (same routing group)
		if route.RoutingGroup == newRoutingGroup {
			continue
		}

		// Check hostname uniqueness with normalization
		if normalizedNewHostname != "" && route.Hostname != "" {
			normalizedRouteHostname := normalizeHostname(route.Hostname)
			if normalizedRouteHostname == normalizedNewHostname {
				return fmt.Errorf("hostname conflict: '%s' (normalized: '%s') already used by route '%s'",
					newHostname, normalizedNewHostname, route.RoutingGroup)
			}
		}

		// Check header uniqueness (if both are set)
		if newHeader != "" && route.Header != "" && route.Header == newHeader {
			return fmt.Errorf("header conflict: '%s' already used by route '%s'", newHeader, route.RoutingGroup)
		}

		if newDefault && route.Default {
			return fmt.Errorf("default route conflict: route '%s' is already marked default", route.RoutingGroup)
		}
	}

	return nil
}

// extractIPFromAddr extracts the IP address from a network address string.
//
// Handles addresses in format "ip:port" or "ip" and removes the port portion.
//
// Parameters:
//   - addr: Network address (e.g., "192.168.1.1:8080" or "192.168.1.1")
//
// Returns:
//   - string: IP address without port
func extractIPFromAddr(addr string) string {
	// Try using net.SplitHostPort for reliable parsing
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	// If SplitHostPort fails, return as-is (likely no port)
	return addr
}

// buildHealthCheckURL constructs a health check URL from a backend URL.
//
// Parameters:
//   - backendURL: Full backend URL (e.g., "http://coordinator:8080")
//   - healthPath: Health check path (e.g., "/v1/info")
//
// Returns:
//   - string: Health check URL
//   - error: Error if URL parsing fails
func buildHealthCheckURL(backendURL, healthPath string) (healthURL string, err error) {
	u, err := url.Parse(backendURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse backend URL: %w", err)
	}

	// Use URL path joining to safely combine paths
	u.Path = singleJoiningSlash(u.Path, healthPath)
	return u.String(), nil
}

// parseHeaderValue extracts the value from a header specification.
//
// Handles formats like "X-Trino-XTrinode=dummy" or "X-Trino-XTrinode: dummy" and extracts "dummy".
// Trims spaces around the value.
//
// Parameters:
//   - headerSpec: Header specification string
//
// Returns:
//   - string: Extracted header value, or original string if no "=" or ":" found
func parseHeaderValue(headerSpec string) string {
	// Try "=" first
	if idx := strings.Index(headerSpec, "="); idx >= 0 && idx < len(headerSpec)-1 {
		return strings.TrimSpace(headerSpec[idx+1:])
	}
	// Try ":" format
	if idx := strings.Index(headerSpec, ":"); idx >= 0 && idx < len(headerSpec)-1 {
		return strings.TrimSpace(headerSpec[idx+1:])
	}
	return strings.TrimSpace(headerSpec) // Return trimmed as-is if no delimiter found
}

// extractRoutingGroup extracts the routing group from a XTrinode spec.
//
// Routing semantics:
// - If routingGroup is explicitly set, use it (e.g., "shared" for shared pools)
// - Otherwise, default to namespace-qualified runtime identity for dedicated runtimes
func extractRoutingGroup(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.Routing != nil && xtrinode.Spec.Routing.RoutingGroup != "" {
		return xtrinode.Spec.Routing.RoutingGroup
	}
	return defaultDedicatedRoutingGroup(xtrinode)
}

func defaultDedicatedRoutingGroup(xtrinode *analyticsv1.XTrinode) string {
	return config.DefaultDedicatedRoutingGroup(xtrinode.Namespace, xtrinode.Name)
}

func isDedicatedRoutingGroup(xtrinode *analyticsv1.XTrinode, routingGroup string) bool {
	if xtrinode.Spec.Routing == nil || xtrinode.Spec.Routing.RoutingGroup == "" {
		return true
	}
	return routingGroup == defaultDedicatedRoutingGroup(xtrinode)
}

// mapSizeToCapacityUnits maps XTrinode size to normalized capacity units for load balancing.
// Capacity units are used to calculate normalized load: effectiveLoad = (running + queued) / capacityUnits
// This enables "small-first, spill to large" load balancing behavior.
func mapSizeToCapacityUnits(size string) int {
	switch size {
	case "xs":
		return 1
	case "s":
		return 2
	case "m":
		return 4
	case "l":
		return 8
	case "xl":
		return 16
	case "xxl":
		return 32
	default:
		// Default to medium capacity if unknown
		return 4
	}
}

// buildHostname builds the hostname for a XTrinode based on routing configuration.
//
// Hostname generation logic:
// 1. If explicit Hostname is set, use it
// 2. If HostnameDomain is set, auto-generate: {routingGroup}.{hostnameDomain}
//   - For shared pools: shared.trino-gw.company.com
//   - For dedicated: runtimeA.trino-gw.company.com
//
// 3. Otherwise, return empty string (no hostname routing)
func buildHostname(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.Routing == nil {
		return ""
	}

	// Explicit hostname takes precedence
	if xtrinode.Spec.Routing.Hostname != "" {
		return xtrinode.Spec.Routing.Hostname
	}

	// Auto-generate hostname from domain using routing group (not runtime name)
	// This ensures shared pools get stable hostnames like "shared.trino-gw.company.com"
	if xtrinode.Spec.Routing.HostnameDomain != "" {
		routingGroup := extractRoutingGroup(xtrinode)
		return fmt.Sprintf("%s.%s", routingGroup, xtrinode.Spec.Routing.HostnameDomain)
	}

	return ""
}

// normalizeHostname normalizes a hostname for comparison (lowercase, strip port).
// This must match the normalization in loadRoutes() indexing.
func normalizeHostname(hostname string) string {
	hostname = strings.ToLower(hostname)
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = h
	}
	return hostname
}
