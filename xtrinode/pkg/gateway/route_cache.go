package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// loadRoutes loads routes from ConfigMap
func (gs *GatewayService) loadRoutes(ctx context.Context) error {
	gs.recordRouteReloadAttempt()

	configMap := &corev1.ConfigMap{}
	err := gs.client.Get(ctx, types.NamespacedName{
		Name:      GatewayConfigMapName,
		Namespace: GatewayConfigMapNamespace,
	}, configMap)

	if err != nil {
		gs.recordRouteReloadFailure("", 0, 0, fmt.Sprintf("failed to get gateway ConfigMap: %v", err))
		return fmt.Errorf("failed to get gateway ConfigMap: %w", err)
	}

	// Check if key exists in ConfigMap
	yamlData, keyExists := configMap.Data[GatewayConfigMapKey]
	if !keyExists {
		gs.log.Error(nil, "ConfigMap key missing, keeping existing routes", "key", GatewayConfigMapKey)
		gs.recordRouteReloadFailure(configMap.ResourceVersion, 0, 0, fmt.Sprintf("ConfigMap key %q missing", GatewayConfigMapKey))
		return nil
	}

	routes, err := parseRoutes(yamlData)
	if err != nil {
		// Don't replace in-memory routes with empty on parse error
		// Just log and keep last-good routes
		gs.log.Error(err, "Failed to parse routes from ConfigMap, keeping existing routes")
		gs.recordRouteReloadFailure(configMap.ResourceVersion, 0, 0, fmt.Sprintf("failed to parse routes: %v", err))
		return nil
	}

	// Filter out invalid routes - validate each route individually
	// This ensures we load only valid routes instead of all-or-nothing
	validRoutes := make([]RouteEntry, 0, len(routes))
	seenHostnames := make(map[string]string)   // normalized hostname -> routingGroup
	seenHeaders := make(map[string]string)     // header -> routingGroup
	seenRoutingGroups := make(map[string]bool) // routingGroup -> exists

	for i := range routes {
		r := &routes[i]
		valid := true
		if !isLoadableRoute(r) {
			gs.log.Error(nil, "Skipping structurally invalid route", "route", r.Name, "routingGroup", r.RoutingGroup)
			continue
		}

		// Check routing group uniqueness
		if r.RoutingGroup != "" {
			if seenRoutingGroups[r.RoutingGroup] {
				gs.log.Error(nil, "Skipping route with duplicate routing group",
					"route", r.Name, "routingGroup", r.RoutingGroup)
				valid = false
			} else {
				seenRoutingGroups[r.RoutingGroup] = true
			}
		}

		// Check hostname uniqueness
		if valid && r.Hostname != "" {
			hostname := normalizeHostname(r.Hostname)
			if existingRG, exists := seenHostnames[hostname]; exists {
				gs.log.Error(nil, "Skipping route with duplicate hostname",
					"route", r.Name, "hostname", r.Hostname, "normalized", hostname,
					"conflictsWith", existingRG)
				valid = false
			} else {
				seenHostnames[hostname] = r.RoutingGroup
			}
		}

		// Check header uniqueness
		if valid && r.Header != "" {
			if existingRG, exists := seenHeaders[r.Header]; exists {
				gs.log.Error(nil, "Skipping route with duplicate header",
					"route", r.Name, "header", r.Header, "conflictsWith", existingRG)
				valid = false
			} else {
				seenHeaders[r.Header] = r.RoutingGroup
			}
		}

		if valid {
			validRoutes = append(validRoutes, *r)
		}
	}

	if len(validRoutes) < len(routes) {
		gs.log.Info("Filtered out invalid routes",
			"total", len(routes), "valid", len(validRoutes), "invalid", len(routes)-len(validRoutes))
	}
	if len(routes) > 0 && len(validRoutes) == 0 {
		gs.log.Error(nil, "All parsed routes were invalid, keeping existing routes")
		gs.recordRouteReloadFailure(configMap.ResourceVersion, 0, len(routes), "all parsed routes were invalid")
		return nil
	}

	loadedRunningBackends := make([]string, 0)
	for i := range validRoutes {
		for j := range validRoutes[i].Backends {
			backend := &validRoutes[i].Backends[j]
			if backend.Active && (backend.State == "" || backend.State == StateRunning) {
				loadedRunningBackends = append(loadedRunningBackends, backend.CoordinatorURL)
			}
		}
	}

	gs.routesLock.Lock()
	defer gs.routesLock.Unlock()

	// Clear existing routes
	gs.routes = make(map[string]*RouteEntry)
	gs.defaultRoute = nil

	// Use the index so each pointer refers to the stored route entry.
	for i := range validRoutes {
		r := &validRoutes[i]

		// Store default route separately for deterministic lookup
		// Detect multiple defaults and log warning
		if r.Default {
			if gs.defaultRoute != nil {
				gs.log.Error(nil, "Multiple default routes detected - keeping first, ignoring others",
					"first", gs.defaultRoute.Name, "ignored", r.Name)
			} else {
				gs.defaultRoute = r
			}
		}

		// Index by routingGroup with prefix to avoid collisions
		if r.RoutingGroup != "" {
			gs.routes["rg:"+r.RoutingGroup] = r
		}

		// Index by hostname if present (normalize to lowercase, strip port)
		// Use prefix to avoid collision with routingGroup or header
		if r.Hostname != "" {
			hostname := normalizeHostname(r.Hostname)
			gs.routes["host:"+hostname] = r
		}

		// Index by header value if present with prefix
		if r.Header != "" {
			gs.routes["hdr:"+r.Header] = r
		}
	}

	if gs.healthChecker != nil {
		gs.healthChecker.resetSleeping(loadedRunningBackends)
	}

	gs.log.Info("Loaded routes", "count", len(validRoutes))
	gs.recordRouteReloadSuccess(configMap.ResourceVersion, len(validRoutes), len(routes)-len(validRoutes))
	return nil
}

func isLoadableRoute(route *RouteEntry) bool {
	if route == nil || route.Name == "" || len(route.Backends) == 0 {
		return false
	}
	if route.RoutingGroup == "" && route.Hostname == "" && route.Header == "" && !route.Default {
		return false
	}
	for i := range route.Backends {
		backend := &route.Backends[i]
		if backend.Name == "" || backend.Namespace == "" || backend.CoordinatorURL == "" {
			return false
		}
	}
	return true
}

// watchRoutes watches ConfigMap for changes using ResourceVersion-based change detection.
// Only triggers a full route reload when the ConfigMap has actually changed, reducing
// API server load compared to unconditional polling. Polls every 2s for near-real-time detection.
func (gs *GatewayService) watchRoutes(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastResourceVersion string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := gs.hasConfigMapChanged(ctx, &lastResourceVersion)
			if err != nil {
				gs.log.V(1).Info("Failed to check ConfigMap version", "error", err)
				continue
			}
			if !changed {
				continue
			}
			if err := gs.loadRoutes(ctx); err != nil {
				gs.log.V(1).Info("Failed to reload routes", "error", err)
			}
		}
	}
}

// hasConfigMapChanged checks if the gateway ConfigMap has changed by comparing ResourceVersion.
// Updates lastVersion in-place when a change is detected. This is a lightweight GET that
// only triggers a full parse+reload when the ConfigMap content has actually been modified.
func (gs *GatewayService) hasConfigMapChanged(ctx context.Context, lastVersion *string) (bool, error) {
	configMap := &corev1.ConfigMap{}
	err := gs.client.Get(ctx, types.NamespacedName{
		Name:      GatewayConfigMapName,
		Namespace: GatewayConfigMapNamespace,
	}, configMap)
	if err != nil {
		return false, err
	}

	currentVersion := configMap.ResourceVersion
	if currentVersion == *lastVersion {
		return false, nil // No change
	}

	*lastVersion = currentVersion
	return true, nil
}

// findRoute finds the route for the request
func (gs *GatewayService) findRoute(r *http.Request) *RouteEntry {
	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	// Try hostname first - but only treat as explicit selector if it matches a configured route
	// Otherwise fall through to header/default (avoid "Host always exists" trap)
	if hostname := normalizeHostname(r.Host); hostname != "" {
		if route, ok := gs.routes["host:"+hostname]; ok {
			return route
		}
		// Hostname exists but doesn't match - NOT an explicit selector failure
		// Continue to header/default route (Host header is always present in HTTP/1.1)
	}

	// Try header - this IS an explicit selector
	if header := r.Header.Get("X-Trino-XTrinode"); header != "" {
		if route, ok := gs.routes["hdr:"+header]; ok {
			return route
		}
		// Snowflake semantics: if header provided but not found, don't fall back
		return nil
	}

	// Use default route when no explicit selector was provided
	return gs.defaultRoute
}
