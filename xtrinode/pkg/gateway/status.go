package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

const GatewayStatusAPIPath = GatewayUIPath + "/api/gateway/status"

type routeReloadState struct {
	LastAttempt              time.Time
	LastSuccess              time.Time
	LastFailure              time.Time
	LastError                string
	ConfigMapResourceVersion string
	RoutesLoaded             int
	InvalidRoutes            int
}

type GatewayStatusResponse struct {
	GeneratedAt time.Time                `json:"generatedAt"`
	UI          GatewayUIStatus          `json:"ui"`
	Summary     GatewayStatusSummary     `json:"summary"`
	Reload      GatewayRouteReloadStatus `json:"reload"`
	Routes      []GatewayRouteStatus     `json:"routes"`
}

type GatewayUIStatus struct {
	Enabled     bool `json:"enabled"`
	RequireAuth bool `json:"requireAuth"`
}

type GatewayStatusSummary struct {
	Routes              int `json:"routes"`
	Backends            int `json:"backends"`
	ActiveBackends      int `json:"activeBackends"`
	RunningBackends     int `json:"runningBackends"`
	PausedBackends      int `json:"pausedBackends"`
	ResumingBackends    int `json:"resumingBackends"`
	DrainingBackends    int `json:"drainingBackends"`
	HealthyBackends     int `json:"healthyBackends"`
	UnhealthyBackends   int `json:"unhealthyBackends"`
	SleepingBackends    int `json:"sleepingBackends"`
	OpenCircuitBackends int `json:"openCircuitBackends"`
}

type GatewayRouteReloadStatus struct {
	LastAttempt              *time.Time `json:"lastAttempt,omitempty"`
	LastSuccess              *time.Time `json:"lastSuccess,omitempty"`
	LastFailure              *time.Time `json:"lastFailure,omitempty"`
	LastError                string     `json:"lastError,omitempty"`
	ConfigMapResourceVersion string     `json:"configMapResourceVersion,omitempty"`
	RoutesLoaded             int        `json:"routesLoaded"`
	InvalidRoutes            int        `json:"invalidRoutes"`
}

type GatewayRouteStatus struct {
	Name         string                 `json:"name"`
	RoutingGroup string                 `json:"routingGroup,omitempty"`
	Hostname     string                 `json:"hostname,omitempty"`
	Header       string                 `json:"header,omitempty"`
	Default      bool                   `json:"default"`
	Backends     []GatewayBackendStatus `json:"backends"`
}

type GatewayBackendStatus struct {
	Name           string                     `json:"name"`
	Namespace      string                     `json:"namespace"`
	CoordinatorURL string                     `json:"coordinatorUrl"`
	State          BackendState               `json:"state"`
	Active         bool                       `json:"active"`
	Tier           string                     `json:"tier,omitempty"`
	CapacityUnits  int                        `json:"capacityUnits,omitempty"`
	DrainUntil     string                     `json:"drainUntil,omitempty"`
	TrinoUIPath    string                     `json:"trinoUiPath,omitempty"`
	Health         GatewayBackendHealthStatus `json:"health"`
	CircuitBreaker CircuitBreakerStatus       `json:"circuitBreaker"`
	Lifecycle      *GatewayBackendLifecycle   `json:"lifecycle,omitempty"`
}

type GatewayBackendLifecycle struct {
	AutoSuspendAfter string     `json:"autoSuspendAfter,omitempty"`
	LastActivity     *time.Time `json:"lastActivity,omitempty"`
	SuspendAt        *time.Time `json:"suspendAt,omitempty"`
}

type GatewayBackendHealthStatus struct {
	State               HealthState `json:"state"`
	LastCheck           *time.Time  `json:"lastCheck,omitempty"`
	LastSuccess         *time.Time  `json:"lastSuccess,omitempty"`
	LastStatus          int         `json:"lastStatus,omitempty"`
	ConsecutiveFailures int         `json:"consecutiveFailures"`
	LastError           string      `json:"lastError,omitempty"`
}

type CircuitBreakerStatus struct {
	Known                bool         `json:"known"`
	State                CircuitState `json:"state"`
	ConsecutiveFailures  int          `json:"consecutiveFailures"`
	ConsecutiveSuccesses int          `json:"consecutiveSuccesses"`
	ConsecutiveOverloads int          `json:"consecutiveOverloads"`
	LastFailure          *time.Time   `json:"lastFailure,omitempty"`
	LastSuccess          *time.Time   `json:"lastSuccess,omitempty"`
}

func (gs *GatewayService) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := json.NewEncoder(w).Encode(gs.gatewayStatusSnapshot(r.Context())); err != nil {
		gs.log.V(1).Info("Failed to encode gateway status response", "error", err)
	}
}

func (gs *GatewayService) gatewayStatusSnapshot(ctx context.Context) GatewayStatusResponse {
	routes := gs.uniqueRouteSnapshot()
	reload := gs.routeReloadSnapshot()
	now := time.Now().UTC()

	response := GatewayStatusResponse{
		GeneratedAt: now,
		UI: GatewayUIStatus{
			Enabled:     gs.ui.Enabled,
			RequireAuth: gs.ui.RequireAuth,
		},
		Reload: reload,
		Routes: make([]GatewayRouteStatus, 0, len(routes)),
	}

	for _, route := range routes {
		routeStatus := GatewayRouteStatus{
			Name:         route.Name,
			RoutingGroup: route.RoutingGroup,
			Hostname:     route.Hostname,
			Header:       route.Header,
			Default:      route.Default,
			Backends:     make([]GatewayBackendStatus, 0, len(route.Backends)),
		}

		for _, backend := range route.Backends {
			backendStatus := gs.gatewayBackendStatus(ctx, &backend, now)
			routeStatus.Backends = append(routeStatus.Backends, backendStatus)
			accumulateGatewaySummary(&response.Summary, &backendStatus)
		}

		response.Routes = append(response.Routes, routeStatus)
	}
	response.Summary.Routes = len(response.Routes)

	return response
}

func (gs *GatewayService) uniqueRouteSnapshot() []RouteEntry {
	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	routes := make([]RouteEntry, 0, len(gs.routes)+1)
	seen := make(map[*RouteEntry]struct{}, len(gs.routes)+1)
	add := func(route *RouteEntry) {
		if route == nil {
			return
		}
		if _, exists := seen[route]; exists {
			return
		}
		seen[route] = struct{}{}
		copied := *route
		copied.Backends = append([]Backend(nil), route.Backends...)
		routes = append(routes, copied)
	}

	for _, route := range gs.routes {
		add(route)
	}
	add(gs.defaultRoute)

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Default != routes[j].Default {
			return routes[i].Default
		}
		if routes[i].RoutingGroup != routes[j].RoutingGroup {
			return routes[i].RoutingGroup < routes[j].RoutingGroup
		}
		return routes[i].Name < routes[j].Name
	})

	return routes
}

func (gs *GatewayService) gatewayBackendStatus(ctx context.Context, backend *Backend, now time.Time) GatewayBackendStatus {
	coordinatorURL := redactURLUserInfo(backend.CoordinatorURL)
	status := GatewayBackendStatus{
		Name:           backend.Name,
		Namespace:      backend.Namespace,
		CoordinatorURL: coordinatorURL,
		State:          backend.State,
		Active:         backend.Active,
		Tier:           backend.Tier,
		CapacityUnits:  backend.CapacityUnits,
		DrainUntil:     backend.DrainUntil,
		TrinoUIPath:    gatewayBackendTrinoUIPath(backend),
		CircuitBreaker: gs.circuitBreakerStatus(backend.CoordinatorURL),
		Lifecycle:      gs.gatewayBackendLifecycle(ctx, backend, now),
	}
	if status.State == "" {
		status.State = StateRunning
	}

	if gs.healthChecker != nil {
		health := gs.healthChecker.GetHealthStatus(backend.CoordinatorURL)
		status.Health = GatewayBackendHealthStatus{
			State:               health.State,
			LastCheck:           timePtr(health.LastCheck),
			LastSuccess:         timePtr(health.LastSuccess),
			LastStatus:          health.LastStatus,
			ConsecutiveFailures: health.ConsecutiveFailures,
			LastError:           redactBackendError(health.LastError, backend.CoordinatorURL),
		}
	} else {
		status.Health.State = HealthStateUnknown
	}

	return status
}

func gatewayBackendTrinoUIPath(backend *Backend) string {
	if backend.Name == "" {
		return ""
	}
	if backend.Namespace == "" {
		return TrinoUIPath + "/" + url.PathEscape(backend.Name) + "/"
	}
	return TrinoUIPath + "/" + url.PathEscape(backend.Namespace) + "/" + url.PathEscape(backend.Name) + "/"
}

func (gs *GatewayService) gatewayBackendLifecycle(ctx context.Context, backend *Backend, now time.Time) *GatewayBackendLifecycle {
	if gs.client == nil || backend.Name == "" || backend.Namespace == "" {
		return nil
	}

	xtrinode := &analyticsv1.XTrinode{}
	if err := gs.client.Get(ctx, types.NamespacedName{Namespace: backend.Namespace, Name: backend.Name}, xtrinode); err != nil {
		if !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
			gs.log.V(1).Info("Failed to read XTrinode lifecycle for gateway status",
				"namespace", backend.Namespace,
				"name", backend.Name,
				"error", err)
		}
		return nil
	}

	status := &GatewayBackendLifecycle{}
	if xtrinode.Status.LastActivity != nil {
		lastActivity := xtrinode.Status.LastActivity.UTC()
		status.LastActivity = &lastActivity
	}

	if xtrinode.Spec.AutoSuspendAfter != nil {
		autoSuspendAfter := xtrinode.Spec.AutoSuspendAfter.Duration
		status.AutoSuspendAfter = autoSuspendAfter.String()

		if !xtrinode.Spec.Suspended {
			idleSince := xtrinode.CreationTimestamp.Time
			if status.LastActivity != nil {
				idleSince = *status.LastActivity
			}

			if !idleSince.IsZero() {
				suspendAt := idleSince.Add(autoSuspendAfter).UTC()
				if xtrinode.Status.Wake != nil && xtrinode.Status.Wake.ExpiresAt.After(now) && xtrinode.Status.Wake.ExpiresAt.After(suspendAt) {
					suspendAt = xtrinode.Status.Wake.ExpiresAt.UTC()
				}
				status.SuspendAt = timePtr(suspendAt)
			}
		}
	}
	if status.AutoSuspendAfter == "" && status.LastActivity == nil && status.SuspendAt == nil {
		return nil
	}
	return status
}

func (gs *GatewayService) circuitBreakerStatus(backendURL string) CircuitBreakerStatus {
	status := CircuitBreakerStatus{
		State: CircuitClosed,
	}
	if gs.circuitBreaker == nil {
		return status
	}

	gs.circuitBreaker.mu.RLock()
	breaker := gs.circuitBreaker.breakers[backendURL]
	gs.circuitBreaker.mu.RUnlock()
	if breaker == nil {
		return status
	}

	breaker.mu.RLock()
	defer breaker.mu.RUnlock()

	status.Known = true
	status.State = breaker.State
	status.ConsecutiveFailures = breaker.ConsecutiveFailures
	status.ConsecutiveSuccesses = breaker.ConsecutiveSuccesses
	status.ConsecutiveOverloads = breaker.ConsecutiveOverloads
	status.LastFailure = timePtr(breaker.LastFailure)
	status.LastSuccess = timePtr(breaker.LastSuccess)
	return status
}

func accumulateGatewaySummary(summary *GatewayStatusSummary, backend *GatewayBackendStatus) {
	summary.Backends++
	if backend.Active {
		summary.ActiveBackends++
	}
	switch backend.State {
	case StatePaused:
		summary.PausedBackends++
	case StateResuming:
		summary.ResumingBackends++
	case StateDraining:
		summary.DrainingBackends++
	default:
		summary.RunningBackends++
	}

	switch backend.Health.State {
	case HealthStateHealthy:
		summary.HealthyBackends++
	case HealthStateUnhealthy:
		summary.UnhealthyBackends++
	case HealthStateSleeping:
		summary.SleepingBackends++
	}

	if backend.CircuitBreaker.State == CircuitOpen {
		summary.OpenCircuitBackends++
	}
}

func (gs *GatewayService) recordRouteReloadAttempt() {
	gs.reloadStateLock.Lock()
	defer gs.reloadStateLock.Unlock()
	gs.reloadState.LastAttempt = time.Now().UTC()
}

func (gs *GatewayService) recordRouteReloadSuccess(resourceVersion string, routesLoaded, invalidRoutes int) {
	gs.reloadStateLock.Lock()
	defer gs.reloadStateLock.Unlock()
	gs.reloadState.LastSuccess = time.Now().UTC()
	gs.reloadState.LastError = ""
	gs.reloadState.ConfigMapResourceVersion = resourceVersion
	gs.reloadState.RoutesLoaded = routesLoaded
	gs.reloadState.InvalidRoutes = invalidRoutes
}

func (gs *GatewayService) recordRouteReloadFailure(resourceVersion string, routesLoaded, invalidRoutes int, message string) {
	gs.reloadStateLock.Lock()
	defer gs.reloadStateLock.Unlock()
	gs.reloadState.LastFailure = time.Now().UTC()
	gs.reloadState.LastError = message
	gs.reloadState.ConfigMapResourceVersion = resourceVersion
	gs.reloadState.RoutesLoaded = routesLoaded
	gs.reloadState.InvalidRoutes = invalidRoutes
}

func (gs *GatewayService) routeReloadSnapshot() GatewayRouteReloadStatus {
	gs.reloadStateLock.RLock()
	defer gs.reloadStateLock.RUnlock()

	return GatewayRouteReloadStatus{
		LastAttempt:              timePtr(gs.reloadState.LastAttempt),
		LastSuccess:              timePtr(gs.reloadState.LastSuccess),
		LastFailure:              timePtr(gs.reloadState.LastFailure),
		LastError:                gs.reloadState.LastError,
		ConfigMapResourceVersion: gs.reloadState.ConfigMapResourceVersion,
		RoutesLoaded:             gs.reloadState.RoutesLoaded,
		InvalidRoutes:            gs.reloadState.InvalidRoutes,
	}
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func redactURLUserInfo(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "") {
		return rawURL
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func redactBackendError(message, backendURL string) string {
	if message == "" || backendURL == "" {
		return message
	}
	return strings.ReplaceAll(message, backendURL, redactURLUserInfo(backendURL))
}
