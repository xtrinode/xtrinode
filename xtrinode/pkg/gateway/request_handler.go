package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

// Context keys for passing backend selection results
type ctxKey int

const (
	ctxRoute ctxKey = iota
	ctxBackendURL
	ctxXTrinodeName
	ctxNamespace
	ctxTargetPath
	ctxTrinoUIPrefix
)

// responseWriterWrapper wraps http.ResponseWriter to capture status code
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterWrapper) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func (rw *responseWriterWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// handleRequest handles incoming HTTP requests
func (gs *GatewayService) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Track request duration and status code
	startTime := time.Now()
	wrapper := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

	// Find route
	route := gs.findRoute(r)
	if route == nil {
		http.Error(wrapper, "No route found", http.StatusNotFound)
		metrics.GatewayRequestsTotal.WithLabelValues("unknown", strconv.Itoa(http.StatusNotFound)).Inc()
		return
	}

	// Defer metrics recording
	defer func() {
		duration := time.Since(startTime).Seconds()
		metrics.GatewayRequestDuration.WithLabelValues(route.RoutingGroup).Observe(duration)
		metrics.GatewayRequestsTotal.WithLabelValues(route.RoutingGroup, strconv.Itoa(wrapper.statusCode)).Inc()

		// Track 503s separately
		if wrapper.statusCode == http.StatusServiceUnavailable {
			metrics.Gateway503Total.WithLabelValues(route.RoutingGroup).Inc()
		}
	}()

	// Check for sticky routing: extract query ID from request URI
	var backendURL string
	var xtrinodeName, namespace string
	queryId := gs.extractQueryIdFromRequest(r)
	if queryId != "" {
		// Try to get sticky route from Redis (with LRU fallback)
		stickyNs, stickyName, stickyBackendURL, stickyRoutingGroup, found := gs.stickyClient.Get(r.Context(), queryId)
		if found {
			// Query continuation must work across all routes, even if selectors
			// change between requests.
			if stickyBackendURL != "" {
				// Find backend in ALL loaded routes (including defaultRoute), not just current route
				var cachedBackend *Backend
				gs.routesLock.RLock()
				for _, routeEntry := range gs.routes {
					for i := range routeEntry.Backends {
						backend := &routeEntry.Backends[i]
						if backend.CoordinatorURL == stickyBackendURL {
							cachedBackend = backend
							break
						}
					}
					if cachedBackend != nil {
						break
					}
				}
				// Also check defaultRoute which may not be in the routes map
				// (e.g. if it has no routingGroup/hostname/header)
				if cachedBackend == nil && gs.defaultRoute != nil {
					for i := range gs.defaultRoute.Backends {
						backend := &gs.defaultRoute.Backends[i]
						if backend.CoordinatorURL == stickyBackendURL {
							cachedBackend = backend
							break
						}
					}
				}
				gs.routesLock.RUnlock()

				if gs.isStickyBackendRoutable(cachedBackend) {
					// Backend still exists and can receive this query continuation.
					backendURL = stickyBackendURL
					xtrinodeName = stickyName
					namespace = stickyNs
					gs.log.V(1).Info("Using sticky backend for query ID (cross-route)",
						"queryId", queryId,
						"backend", backendURL,
						"routingGroup", stickyRoutingGroup)
				} else {
					// Backend no longer exists or cannot receive query continuations.
					gs.log.V(1).Info("Sticky backend no longer routable, invalidating",
						"queryId", queryId,
						"backend", stickyBackendURL)
					if err := gs.stickyClient.Delete(r.Context(), queryId); err != nil {
						gs.log.V(1).Info("Failed to delete sticky route", "queryId", queryId, "error", err)
					}
					backendURL = "" // Trigger reselection
				}
			}
		}
	}

	// If no cached backend, select one using load balancing
	if backendURL == "" {
		backend := gs.selectBackend(route)
		if backend == nil {
			// No backend available - attempt resume based on route state
			if cand := gs.pickResumeCandidate(route); cand != nil {
				// Track auto-resume metric
				metrics.GatewayAutoResumeTotal.WithLabelValues(route.RoutingGroup).Inc()
				gs.handleResumeViaAPI(wrapper, r, cand.Namespace, cand.Name, route.RoutingGroup)
				return
			}
			http.Error(wrapper, "No backend available", http.StatusServiceUnavailable)
			return
		}
		backendURL = backend.CoordinatorURL
		xtrinodeName = backend.Name
		namespace = backend.Namespace

		// Namespace is required in route backend entries.
		if namespace == "" {
			gs.log.Error(nil, "Backend missing namespace field - this is a configuration error", "backend", xtrinodeName)
			http.Error(wrapper, "Backend configuration error: missing namespace", http.StatusInternalServerError)
			return
		}

		// Store sticky route in Redis if we have a query ID
		if queryId != "" && xtrinodeName != "" && namespace != "" && backendURL != "" {
			if err := gs.stickyClient.Set(r.Context(), queryId, namespace, xtrinodeName, backendURL, route.RoutingGroup); err != nil {
				gs.log.V(1).Info("Failed to store sticky route", "queryId", queryId, "error", err)
			}
		}
	}

	// Store backend selection results in context for director to use
	ctx := r.Context()
	ctx = context.WithValue(ctx, ctxRoute, route)
	ctx = context.WithValue(ctx, ctxBackendURL, backendURL)
	ctx = context.WithValue(ctx, ctxXTrinodeName, xtrinodeName)
	ctx = context.WithValue(ctx, ctxNamespace, namespace)
	ctx = context.WithValue(ctx, ctxTrinoUIPrefix, gatewayBackendTrinoUIPath(Backend{Name: xtrinodeName, Namespace: namespace}))
	r = r.WithContext(ctx)

	// Forward request to coordinator using retry proxy (includes circuit breaker)
	gs.retryProxy.ServeHTTP(wrapper, r)
}

func (gs *GatewayService) handleTrinoUIRequest(w http.ResponseWriter, r *http.Request) {
	if isGatewayAdminUIRequest(r.URL.Path) {
		http.NotFound(w, r)
		return
	}

	if r.URL.Path == TrinoUIPath || r.URL.Path == TrinoUIPath+"/" {
		if gs.ui.Enabled {
			http.Redirect(w, r, GatewayUIRedirectURL, http.StatusTemporaryRedirect)
			return
		}
		_, backend, ok := gs.defaultTrinoUIBackend()
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, gatewayBackendTrinoUIPath(backend), http.StatusTemporaryRedirect)
		return
	}

	route, backend, targetPath, ok, ambiguous := gs.resolveTrinoUIBackend(r.URL.Path)
	if ambiguous {
		http.Error(w, "Backend name is ambiguous; use /ui/<namespace>/<backend>/", http.StatusBadRequest)
		return
	}
	if !ok {
		if isDefaultTrinoUIPath(r.URL.Path) {
			var defaultOK bool
			route, backend, defaultOK = gs.defaultTrinoUIBackend()
			if !defaultOK {
				http.NotFound(w, r)
				return
			}
			targetPath = r.URL.Path
		} else {
			http.NotFound(w, r)
			return
		}
	}

	wrapper := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Seconds()
		metrics.GatewayRequestDuration.WithLabelValues(route.RoutingGroup).Observe(duration)
		metrics.GatewayRequestsTotal.WithLabelValues(route.RoutingGroup, strconv.Itoa(wrapper.statusCode)).Inc()
		if wrapper.statusCode == http.StatusServiceUnavailable {
			metrics.Gateway503Total.WithLabelValues(route.RoutingGroup).Inc()
		}
	}()

	if !gs.isBackendSelectable(&backend) {
		if backend.State == StatePaused || backend.State == StateResuming {
			metrics.GatewayAutoResumeTotal.WithLabelValues(route.RoutingGroup).Inc()
			gs.handleResumeViaAPI(wrapper, r, backend.Namespace, backend.Name, route.RoutingGroup)
			return
		}
		http.Error(wrapper, "Backend is not available for Trino UI", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, ctxRoute, &route)
	ctx = context.WithValue(ctx, ctxBackendURL, backend.CoordinatorURL)
	ctx = context.WithValue(ctx, ctxXTrinodeName, backend.Name)
	ctx = context.WithValue(ctx, ctxNamespace, backend.Namespace)
	ctx = context.WithValue(ctx, ctxTargetPath, targetPath)
	ctx = context.WithValue(ctx, ctxTrinoUIPrefix, gatewayBackendTrinoUIPath(backend))
	r = r.WithContext(ctx)

	gs.retryProxy.ServeHTTP(wrapper, r)
}

func (gs *GatewayService) defaultTrinoUIBackend() (RouteEntry, Backend, bool) {
	routes := gs.uniqueRouteSnapshot()
	for i := range routes {
		if !routes[i].Default {
			continue
		}
		if backend := gs.selectBackend(&routes[i]); backend != nil {
			return routes[i], *backend, true
		}
		if backend := gs.pickResumeCandidate(&routes[i]); backend != nil {
			return routes[i], *backend, true
		}
	}

	type candidate struct {
		route   RouteEntry
		backend Backend
	}
	selectable := make(map[string]candidate)
	for i := range routes {
		for j := range routes[i].Backends {
			backend := routes[i].Backends[j]
			if !gs.isBackendSelectable(&backend) {
				continue
			}
			selectable[backendIdentityKey(backend)] = candidate{route: routes[i], backend: backend}
		}
	}
	if len(selectable) == 1 {
		for _, candidate := range selectable {
			return candidate.route, candidate.backend, true
		}
	}
	if len(selectable) > 1 {
		return RouteEntry{}, Backend{}, false
	}

	resumable := make(map[string]candidate)
	for i := range routes {
		for j := range routes[i].Backends {
			backend := routes[i].Backends[j]
			if !trinoUIResumeCandidate(backend) {
				continue
			}
			resumable[backendIdentityKey(backend)] = candidate{route: routes[i], backend: backend}
		}
	}
	if len(resumable) != 1 {
		return RouteEntry{}, Backend{}, false
	}
	for _, candidate := range resumable {
		return candidate.route, candidate.backend, true
	}
	return RouteEntry{}, Backend{}, false
}

func trinoUIResumeCandidate(backend Backend) bool {
	if !backend.Active || backend.Name == "" || backend.Namespace == "" {
		return false
	}
	_, ok := resumeStatePriority(backend.State)
	return ok
}

func isDefaultTrinoUIPath(path string) bool {
	trimmed := strings.TrimPrefix(path, TrinoUIPath+"/")
	if trimmed == "" || trimmed == path {
		return false
	}
	first, _, _ := strings.Cut(trimmed, "/")
	first, err := url.PathUnescape(first)
	if err != nil || first == "" {
		return false
	}
	switch first {
	case "assets", "vendor", "api", "login", "logout":
		return true
	}
	return strings.HasSuffix(first, ".html") ||
		strings.HasSuffix(first, ".css") ||
		strings.HasSuffix(first, ".js") ||
		strings.HasSuffix(first, ".ico")
}

func (gs *GatewayService) resolveTrinoUIBackend(path string) (RouteEntry, Backend, string, bool, bool) {
	trimmed := strings.TrimPrefix(path, TrinoUIPath+"/")
	if trimmed == "" || trimmed == path {
		return RouteEntry{}, Backend{}, "", false, false
	}

	segments := strings.Split(trimmed, "/")
	if len(segments) >= 2 {
		namespace, namespaceOK := url.PathUnescape(segments[0])
		name, nameOK := url.PathUnescape(segments[1])
		if namespaceOK == nil && nameOK == nil && namespace != "" && name != "" {
			route, backend, ok, _ := gs.findTrinoUIBackend(namespace, name)
			if ok {
				return route, backend, trinoUITargetPath(segments[2:]), true, false
			}
		}
	}

	name, err := url.PathUnescape(segments[0])
	if err != nil || name == "" {
		return RouteEntry{}, Backend{}, "", false, false
	}
	route, backend, ok, ambiguous := gs.findTrinoUIBackend("", name)
	if !ok || ambiguous {
		return RouteEntry{}, Backend{}, "", ok, ambiguous
	}
	return route, backend, trinoUITargetPath(segments[1:]), true, false
}

func (gs *GatewayService) findTrinoUIBackend(namespace, name string) (RouteEntry, Backend, bool, bool) {
	routes := gs.uniqueRouteSnapshot()
	type match struct {
		route   RouteEntry
		backend Backend
	}
	matches := make([]match, 0, 1)
	seen := make(map[string]struct{})
	for i := range routes {
		for j := range routes[i].Backends {
			backend := routes[i].Backends[j]
			if backend.Name != name {
				continue
			}
			if namespace != "" && backend.Namespace != namespace {
				continue
			}
			key := backendIdentityKey(backend)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, match{route: routes[i], backend: backend})
		}
	}
	if len(matches) == 0 {
		return RouteEntry{}, Backend{}, false, false
	}
	if namespace == "" && len(matches) > 1 {
		return RouteEntry{}, Backend{}, true, true
	}
	return matches[0].route, matches[0].backend, true, false
}

func trinoUITargetPath(segments []string) string {
	if len(segments) == 0 || strings.Join(segments, "/") == "" {
		return "/ui/"
	}
	return "/ui/" + strings.Join(segments, "/")
}

func backendIdentityKey(backend Backend) string {
	return backend.Namespace + "\x00" + backend.Name + "\x00" + backend.CoordinatorURL
}

// director sets up the reverse proxy director function.
//
// This function is called for each incoming request to determine the target backend.
// Backend selection is done once in handleRequest and passed via context.
// This function only applies URL mutation and header injection.
//
// It sets:
//   - X-Trino-Route-Name (observability)
//   - X-Trino-XTrinode-Name/Namespace (identity for sticky routing)
//   - X-Trino-Backend-URL (for error handler)
//   - X-Forwarded-Host/Proto (if not already set)
func (gs *GatewayService) director(req *http.Request) {
	// Get backend selection results from context (set by handleRequest)
	//nolint:errcheck // best-effort context value extraction; nil check below handles failure
	route, _ := req.Context().Value(ctxRoute).(*RouteEntry)
	//nolint:errcheck // best-effort context value extraction; empty string check below handles failure
	backendURL, _ := req.Context().Value(ctxBackendURL).(string)
	//nolint:errcheck // best-effort context value extraction; used for logging only
	xtrinodeName, _ := req.Context().Value(ctxXTrinodeName).(string)
	//nolint:errcheck // best-effort context value extraction; used for logging only
	namespace, _ := req.Context().Value(ctxNamespace).(string)
	//nolint:errcheck // best-effort context value extraction; empty string check below handles default path
	targetPath, _ := req.Context().Value(ctxTargetPath).(string)

	if route == nil || backendURL == "" {
		return
	}

	// Capture original request info before mutation for X-Forwarded headers
	origHost := req.Host
	origProto := "http"
	if req.TLS != nil {
		origProto = "https"
	}

	// Parse coordinator URL
	targetURL, err := url.Parse(backendURL)
	if err != nil {
		gs.log.Error(err, "Failed to parse coordinator URL", "url", backendURL)
		return
	}

	// Set target URL
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	requestPath := req.URL.Path
	if targetPath != "" {
		requestPath = targetPath
		req.URL.RawPath = ""
	}
	req.URL.Path = singleJoiningSlash(targetURL.Path, requestPath)
	req.Host = targetURL.Host // Important: set Host header for backend

	// Set X-Forwarded headers with original client info (only if not already set)
	// If behind ingress/proxy, these may already exist
	if req.Header.Get("X-Forwarded-Host") == "" {
		req.Header.Set("X-Forwarded-Host", origHost)
	}
	if req.Header.Get("X-Forwarded-Proto") == "" {
		req.Header.Set("X-Forwarded-Proto", origProto)
	}

	// Store route and backend info in headers for error handler and response modifier
	req.Header.Set("X-Trino-Route-Name", route.Name)            // For observability
	req.Header.Set("X-Trino-Routing-Group", route.RoutingGroup) // For pool-level resume gate
	req.Header.Set("X-Trino-XTrinode-Name", xtrinodeName)       // For sticky routing cache
	req.Header.Set("X-Trino-XTrinode-Namespace", namespace)     // For sticky routing cache
	req.Header.Set("X-Trino-Backend-URL", backendURL)           // For circuit breaker
}

// modifyResponse modifies the response to implement gateway features.
//
// This function is called for each response from the backend coordinator.
// It implements:
//   - Sticky routing: Extracts query ID from JSON response and caches it for subsequent requests
//   - Trino UI link rewriting: Converts JSON infoUri and UI redirects to backend-specific paths
//   - Circuit breaker: Records success/failure based on HTTP status code
//   - 503 handling: Adds Retry-After headers without triggering resume
//
// The function reads and restores the response body to support multiple operations
// (query ID extraction, circuit breaker status recording).
//
// Returns an error if response modification fails (should not happen in normal operation).
func (gs *GatewayService) modifyResponse(resp *http.Response) error {
	rewriteTrinoUILocation(resp)
	rewriteTrinoUIInfoURI(resp)
	gs.handleStickyRoutingFromResponse(resp)
	gs.handle503Response(resp)
	gs.recordCircuitBreakerState(resp)
	return nil
}

func rewriteTrinoUILocation(resp *http.Response) {
	prefix, _ := resp.Request.Context().Value(ctxTrinoUIPrefix).(string)
	if prefix == "" {
		return
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return
	}
	parsed, err := url.Parse(location)
	if err != nil || (parsed.Path != TrinoUIPath && !strings.HasPrefix(parsed.Path, TrinoUIPath+"/")) {
		return
	}

	suffix := strings.TrimPrefix(parsed.Path, TrinoUIPath)
	parsed.Path = strings.TrimRight(prefix, "/") + suffix
	parsed.RawPath = ""
	parsed.Scheme = ""
	parsed.Host = ""
	parsed.User = nil
	parsed.Opaque = ""
	resp.Header.Set("Location", parsed.String())
}

// handleStickyRoutingFromResponse extracts query ID from response and caches it
func (gs *GatewayService) handleStickyRoutingFromResponse(resp *http.Response) {
	if resp.StatusCode != http.StatusOK {
		return
	}

	if !isJSONContentType(resp.Header.Get("Content-Type")) {
		return
	}

	queryID, state := gs.extractQueryInfoFromResponse(resp)
	if queryID == "" {
		return
	}

	xtrinodeName := resp.Request.Header.Get("X-Trino-XTrinode-Name")
	namespace := resp.Request.Header.Get("X-Trino-XTrinode-Namespace")
	backendURL := resp.Request.Header.Get("X-Trino-Backend-URL")
	routingGroup := resp.Request.Header.Get("X-Trino-Routing-Group")
	if xtrinodeName == "" || namespace == "" || backendURL == "" {
		return
	}

	gs.queryActivity.Observe(queryID, namespace, xtrinodeName, routingGroup, backendURL, state)

	if err := gs.stickyClient.Set(resp.Request.Context(), queryID, namespace, xtrinodeName, backendURL, routingGroup); err != nil {
		gs.log.V(1).Info("Failed to store sticky route from response", "queryId", queryID, "error", err)
		return
	}

	gs.log.V(1).Info("Stored sticky route for query ID", "queryId", queryID, "namespace", namespace, "name", xtrinodeName)
}

// handle503Response handles 503 responses
// NOTE: We cannot reliably distinguish "paused" vs "overload" 503s without parsing
// Trino error JSON from response body (expensive). Auto-resume is triggered by
// connection errors (connection refused) in errorHandler(), not HTTP 503 responses.
// This function only sets appropriate Retry-After headers.
func (gs *GatewayService) handle503Response(resp *http.Response) {
	if resp.StatusCode != http.StatusServiceUnavailable {
		return
	}

	// Default to longer Retry-After for 503s
	// Clients should back off and retry, giving time for auto-resume to complete
	// if this is a sleeping backend (triggered via connection error path)
	resp.Header.Set("Retry-After", "30")
}

// recordCircuitBreakerState records circuit breaker state based on response status.
// 503s are recorded as overload signals (RecordOverload) rather than hard failures,
// using a separate higher threshold to detect sustained overload without false-tripping
// on transient 503s from Trino queue-full responses.
func (gs *GatewayService) recordCircuitBreakerState(resp *http.Response) {
	backendURL := resp.Request.Header.Get("X-Trino-Backend-URL")
	if backendURL == "" || gs.circuitBreaker == nil {
		return
	}

	breaker := gs.circuitBreaker.GetOrCreateBreaker(
		backendURL,
		config.GatewayCircuitBreakerFailureThreshold,
		config.GatewayCircuitBreakerSuccessThreshold,
		config.GatewayCircuitBreakerTimeout,
	)

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		// 503 = overload signal (not a hard failure like connection refused)
		// Use separate overload counter with higher threshold to detect sustained overload
		breaker.RecordOverload()
	case resp.StatusCode >= 500:
		breaker.RecordFailure()
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		breaker.RecordSuccess()
	}
}

// errorHandler handles proxy errors (connection refused, etc.)
func (gs *GatewayService) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	gs.log.Error(err, "Proxy error", "path", r.URL.Path)

	errType := classifyError(err, 0)

	// Record circuit breaker failures for real errors only
	gs.recordCircuitBreakerFailure(r, errType)

	// Handle based on error type
	switch errType {
	case ErrorTypePaused:
		gs.handlePausedBackendError(w, r)
	default:
		http.Error(w, "Gateway error: backend request failed", http.StatusBadGateway)
	}
}

// recordCircuitBreakerFailure records circuit breaker failure for real errors
func (gs *GatewayService) recordCircuitBreakerFailure(r *http.Request, errType ErrorType) {
	backendURL := r.Header.Get("X-Trino-Backend-URL")
	if backendURL == "" || gs.circuitBreaker == nil {
		return
	}

	breaker := gs.circuitBreaker.GetOrCreateBreaker(
		backendURL,
		config.GatewayCircuitBreakerFailureThreshold,
		config.GatewayCircuitBreakerSuccessThreshold,
		config.GatewayCircuitBreakerTimeout,
	)

	// For CLOSED/OPEN states, don't record overload or paused as failures
	// (expected states). Half-open probe failures are still recorded inside the
	// breaker so the probe slot is cleared under the breaker lock.
	breaker.RecordProxyFailure(errType != ErrorTypeOverload && errType != ErrorTypePaused)
}

// handlePausedBackendError handles errors from paused backends
func (gs *GatewayService) handlePausedBackendError(w http.ResponseWriter, r *http.Request) {
	xtrinodeName := r.Header.Get("X-Trino-XTrinode-Name")
	namespace := r.Header.Get("X-Trino-XTrinode-Namespace")
	routingGroup := r.Header.Get("X-Trino-Routing-Group")

	if xtrinodeName == "" || namespace == "" {
		http.Error(w, "Gateway error: backend unavailable", http.StatusBadGateway)
		return
	}

	// Trigger resume via API server
	gs.handleResumeViaAPI(w, r, namespace, xtrinodeName, routingGroup)
}

// handleResumeViaAPI calls the API server to trigger resume
func (gs *GatewayService) handleResumeViaAPI(w http.ResponseWriter, r *http.Request, namespace, name, routingGroup string) {
	// Build resume request for API server
	req := ResumeRequest{
		RoutingGroup: routingGroup,
		Candidate: &ResumeCandidate{
			Namespace: namespace,
			Name:      name,
		},
		Reason: "no_running_backend",
	}

	// CRITICAL: Use detached context for resume call
	// If client disconnects quickly, we still want resume to complete
	// Resume is a fire-and-forget operation from the gateway's perspective
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Call API server unified resume endpoint
	resp, err := gs.apiServerClient.Resume(ctx, req)
	if err != nil {
		gs.log.Error(err, "Failed to call API server resume",
			"namespace", namespace,
			"name", name,
			"routingGroup", routingGroup)

		// Conservative fallback: return 503 with default retry-after
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "Failed to trigger resume",
			"retryAfter": 30,
		}); err != nil {
			gs.log.V(1).Info("Failed to encode error response", "error", err)
		}
		return
	}

	// Return response from API server
	w.Header().Set("Content-Type", "application/json")
	// Enforce minimum Retry-After to prevent thundering herd on immediate retry
	retryAfter := resp.RetryAfter
	if retryAfter < 5 {
		retryAfter = 5
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	if resp.Gated {
		w.Header().Set("X-Gateway-Resume-Gated", "true")
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"error":      "XTrinode runtime is resuming, please retry",
		"retryAfter": retryAfter,
		"gated":      resp.Gated,
		"triggered":  resp.Triggered,
	}); err != nil {
		gs.log.V(1).Info("Failed to encode response", "error", err)
	}
}

// handleHealth handles health check requests
func (gs *GatewayService) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		gs.log.V(1).Info("Failed to write health response", "error", err)
	}
}
