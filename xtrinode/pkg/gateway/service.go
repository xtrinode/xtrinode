package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/pkg/gateway/auth"
	"github.com/xtrinode/xtrinode/pkg/metrics"
)

// Package-level compiled regex for Trino query ID extraction
// Format: YYYYMMDD_HHMMSS_seq_random (e.g., 20250115_123456_00001_abc12)
var trinoQueryIDRe = regexp.MustCompile(`/(\d{8}_\d{6}_\d{5}_[a-zA-Z0-9]+)(?:/|$)`)

// singleJoiningSlash joins two URL paths, ensuring exactly one slash between them
// This prevents double slashes when concatenating paths
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if b != "" {
			return a + "/" + b
		}
		return a
	default:
		return a + b
	}
}

// GatewayService is an HTTP reverse proxy that routes Trino queries to the correct coordinator.
//
// Query routing model:
//   - Routing groups organize backend membership (e.g., "runtime-a" for dedicated, "shared" for pools)
//   - Hostname-based routing provides stable user entrypoints (e.g., runtime-a.trino-gw.company.com)
//   - Multiple backends in the same routing group are load-balanced automatically
//   - Routing priority: hostname -> header -> default route
//
// Key Features:
//   - Route-based query routing: Routes queries based on hostname, header (X-Trino-XTrinode), or default route
//   - Load balancing: Uses deterministic capacity-aware selection across multiple backends
//   - Sticky routing: Caches query ID -> backend mapping to ensure subsequent requests for the same query go to the same backend
//   - Auto-resume: Automatically resumes suspended XTrinodes when detecting connection errors
//   - Circuit breaking: Prevents routing to failing backends
//   - Health checking: Actively monitors backend health
//   - Rate limiting: Protects backends from overload
//   - Authentication: Optional gateway authentication
//
// The service watches a ConfigMap for route configuration and automatically reloads routes on changes.
// Routes are indexed by routingGroup, hostname, and header values for fast lookup.
type GatewayService struct {
	client client.Client
	log    logr.Logger
	port   int

	httpServer HTTPServerConfig

	// Route cache
	// NOTE: Multi-index map - same *RouteEntry may be indexed by routingGroup, hostname, and header.
	// When iterating for metrics/stats, build a unique set by pointer address to avoid double-counting.
	routes       map[string]*RouteEntry // key: routingGroup or hostname or header
	defaultRoute *RouteEntry            // Separate storage for default route (deterministic lookup)
	routesLock   sync.RWMutex

	// Sticky routing client (Redis with LRU fallback)
	stickyClient *RedisStickyClient

	// API server client for resume operations
	apiServerClient *APIServerClient

	// HTTP proxy
	proxy *httputil.ReverseProxy

	// Authentication
	authenticator auth.Authenticator

	// Active health checking
	healthChecker *BackendHealthChecker

	// Circuit breaker
	circuitBreaker *CircuitBreakerManager

	// Rate limiting
	rateLimiter *RateLimiter

	// Query activity metrics for Prometheus-driven worker scale-from-zero
	queryActivity *QueryActivityTracker

	// Retry proxy wrapper
	retryProxy *RetryProxy
}

// GatewayOptions contains runtime options that are deployment-specific.
type GatewayOptions struct {
	Redis              RedisConfig
	RateLimit          RateLimitConfig
	HTTPServer         HTTPServerConfig
	APIServerAuthToken string
	Port               int
}

type HTTPServerConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

type RateLimitConfig struct {
	Enabled    bool
	Capacity   int
	RefillRate time.Duration
}

// Context keys for passing backend selection results
type ctxKey int

const (
	ctxRoute ctxKey = iota
	ctxBackendURL
	ctxXTrinodeName
	ctxNamespace
)

// BackendLoad tracks recently observed query activity for a backend.
type BackendLoad struct {
	RunningQueries int       `json:"runningQueries"`
	QueuedQueries  int       `json:"queuedQueries"`
	LastUpdate     time.Time `json:"lastUpdate"`
}

// NewGatewayService creates a new gateway service
// authenticator is the authentication implementation (optional, nil disables authentication)
func NewGatewayService(cli client.Client, log logr.Logger, apiServerURL string, authenticator auth.Authenticator) (*GatewayService, error) {
	return NewGatewayServiceWithOptions(cli, log, apiServerURL, authenticator, &GatewayOptions{
		Redis:      defaultRedisConfig(),
		RateLimit:  defaultRateLimitConfig(),
		HTTPServer: defaultHTTPServerConfig(),
	})
}

func defaultRedisConfig() RedisConfig {
	return RedisConfig{
		Enabled:  config.GatewayRedisEnabled,
		URL:      config.GatewayRedisURL,
		Password: config.GatewayRedisPassword,
		DB:       config.GatewayRedisDB,
		TTL:      config.GatewayRedisStickyTTL,
		Timeout:  config.GatewayRedisTimeout,
	}
}

func defaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:    true,
		Capacity:   config.GatewayRateLimitCapacity,
		RefillRate: config.GatewayRateLimitRefillRate,
	}
}

func defaultHTTPServerConfig() HTTPServerConfig {
	return HTTPServerConfig{
		ReadHeaderTimeout: config.GatewayReadHeaderTimeout,
		ReadTimeout:       config.GatewayReadTimeout,
		WriteTimeout:      config.GatewayWriteTimeout,
		IdleTimeout:       config.GatewayIdleTimeout,
	}
}

// NewGatewayServiceWithOptions creates a gateway service with explicit runtime options.
func NewGatewayServiceWithOptions(cli client.Client, log logr.Logger, apiServerURL string, authenticator auth.Authenticator, opts *GatewayOptions) (*GatewayService, error) {
	if opts == nil {
		opts = &GatewayOptions{
			Redis:      defaultRedisConfig(),
			RateLimit:  defaultRateLimitConfig(),
			HTTPServer: defaultHTTPServerConfig(),
		}
	}
	redisConfig := opts.Redis
	if redisConfig.TTL <= 0 {
		redisConfig.TTL = config.GatewayRedisStickyTTL
	}
	if redisConfig.Timeout <= 0 {
		redisConfig.Timeout = config.GatewayRedisTimeout
	}
	port := opts.Port
	if port <= 0 {
		port = config.GatewayPort
	}
	httpServerConfig := opts.HTTPServer
	if httpServerConfig.ReadHeaderTimeout <= 0 {
		httpServerConfig.ReadHeaderTimeout = config.GatewayReadHeaderTimeout
	}
	if httpServerConfig.IdleTimeout <= 0 {
		httpServerConfig.IdleTimeout = config.GatewayIdleTimeout
	}
	stickyClient, err := NewRedisStickyClient(redisConfig, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create sticky client: %w", err)
	}

	// Initialize API server client for resume operations.
	if apiServerURL == "" {
		apiServerURL = config.GatewayAPIServerURL // Fallback to default
	}
	apiServerClient := NewAPIServerClientWithToken(
		apiServerURL,
		config.GatewayAPIServerTimeout,
		opts.APIServerAuthToken,
		log,
	)

	gs := &GatewayService{
		client:          cli,
		log:             log,
		port:            port,
		httpServer:      httpServerConfig,
		routes:          make(map[string]*RouteEntry),
		authenticator:   authenticator,
		stickyClient:    stickyClient,
		apiServerClient: apiServerClient,
		queryActivity:   NewQueryActivityTracker(0),
	}

	// Initialize active health checker
	gs.healthChecker = NewBackendHealthChecker(
		config.GatewayHealthCheckInterval,
		config.GatewayHealthCheckTimeout,
		config.GatewayHealthCheckFailureThreshold,
		log,
	)

	// Initialize circuit breaker manager
	gs.circuitBreaker = NewCircuitBreakerManager(
		config.GatewayCircuitBreakerFailureThreshold,
		config.GatewayCircuitBreakerSuccessThreshold,
		config.GatewayCircuitBreakerTimeout,
		log,
	)

	gs.rateLimiter = newGatewayRateLimiter(stickyClient, opts.RateLimit, log)

	// Create reverse proxy with response modifier to detect 503
	gs.proxy = &httputil.ReverseProxy{
		Director:       gs.director,
		Transport:      newGatewayProxyTransport(),
		ModifyResponse: gs.modifyResponse,
		ErrorHandler:   gs.errorHandler,
	}

	// Wrap proxy with circuit breaker guard
	gs.retryProxy = NewRetryProxy(gs.proxy, log, gs.circuitBreaker)

	return gs, nil
}

func newGatewayProxyTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   config.GatewayHTTPClientTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          config.GatewayHTTPClientMaxIdleConns,
		MaxIdleConnsPerHost:   config.GatewayHTTPClientMaxIdleConnsPerHost,
		IdleConnTimeout:       config.GatewayHTTPClientIdleConnTimeout,
		TLSHandshakeTimeout:   config.GatewayHTTPClientTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func newGatewayRateLimiter(stickyClient *RedisStickyClient, rateLimitConfig RateLimitConfig, log logr.Logger) *RateLimiter {
	if !rateLimitConfig.Enabled {
		log.Info("Gateway rate limiting disabled")
		return nil
	}
	if rateLimitConfig.Capacity <= 0 {
		rateLimitConfig.Capacity = config.GatewayRateLimitCapacity
	}
	if rateLimitConfig.RefillRate <= 0 {
		rateLimitConfig.RefillRate = config.GatewayRateLimitRefillRate
	}

	if stickyClient != nil && stickyClient.enabled && stickyClient.redis != nil {
		return NewDistributedRateLimiter(
			rateLimitConfig.Capacity,
			rateLimitConfig.RefillRate,
			stickyClient.redis,
			config.GatewayRedisTimeout,
			log,
		)
	}

	return NewRateLimiter(
		rateLimitConfig.Capacity,
		rateLimitConfig.RefillRate,
		log,
	)
}

// Start starts the gateway service
func (gs *GatewayService) Start(ctx context.Context) error {
	// Create child context that we can cancel on any exit
	// This ensures all background goroutines stop if server fails to start
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Load initial routes
	if err := gs.loadRoutes(ctx); err != nil {
		return fmt.Errorf("failed to load initial routes: %w", err)
	}

	// Watch ConfigMap for route changes
	go gs.watchRoutes(ctx)

	// Start active health checker
	go gs.startHealthChecker(ctx)

	// Start rate limiter cleanup to prevent memory leaks
	if gs.rateLimiter != nil {
		go gs.rateLimiter.StartCleanup(ctx)
	}

	// Start query activity cleanup to expire abandoned Trino query IDs
	go gs.queryActivity.StartCleanup(ctx)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", gs.port),
		Handler:           gs.buildMux(),
		ReadHeaderTimeout: gs.httpServer.ReadHeaderTimeout,
		ReadTimeout:       gs.httpServer.ReadTimeout,
		WriteTimeout:      gs.httpServer.WriteTimeout,
		IdleTimeout:       gs.httpServer.IdleTimeout,
		// Set BaseContext so all requests inherit our cancellable context
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	gs.log.Info("Starting gateway service", "port", gs.port)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		gs.log.Info("Shutting down gateway service")
		// Close Redis sticky client to release connection
		if gs.stickyClient != nil {
			if err := gs.stickyClient.Close(); err != nil {
				gs.log.Error(err, "Error closing Redis sticky client")
			}
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.GatewayShutdownTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			gs.log.Error(err, "Error during graceful shutdown")
			return err
		}
		return nil
	case err := <-errChan:
		// Server failed to start - cancel context to stop background goroutines
		gs.log.Error(err, "Server failed to start")
		cancel()
		return err
	}
}

func (gs *GatewayService) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Apply middleware chain: rate limiting -> authentication -> request handler.
	// Rate limiting intentionally runs before auth and keys on network identity.
	var handler http.Handler = http.HandlerFunc(gs.handleRequest)
	if gs.authenticator != nil {
		handler = auth.Middleware(gs.authenticator)(handler)
	}
	if gs.rateLimiter != nil {
		handler = RateLimitMiddleware(gs.rateLimiter)(handler)
	}
	mux.Handle("/", handler)

	// Health and metrics endpoints bypass auth and rate limiting.
	mux.HandleFunc(config.HealthPath, gs.handleHealth)
	mux.Handle("/metrics", promhttp.HandlerFor(crmetrics.Registry, promhttp.HandlerOpts{}))

	return mux
}

// loadRoutes loads routes from ConfigMap
func (gs *GatewayService) loadRoutes(ctx context.Context) error {
	configMap := &corev1.ConfigMap{}
	err := gs.client.Get(ctx, types.NamespacedName{
		Name:      GatewayConfigMapName,
		Namespace: GatewayConfigMapNamespace,
	}, configMap)

	if err != nil {
		return fmt.Errorf("failed to get gateway ConfigMap: %w", err)
	}

	// Check if key exists in ConfigMap
	yamlData, keyExists := configMap.Data[GatewayConfigMapKey]
	if !keyExists {
		gs.log.Error(nil, "ConfigMap key missing, keeping existing routes", "key", GatewayConfigMapKey)
		return nil
	}

	routes, err := parseRoutes(yamlData)
	if err != nil {
		// Don't replace in-memory routes with empty on parse error
		// Just log and keep last-good routes
		gs.log.Error(err, "Failed to parse routes from ConfigMap, keeping existing routes")
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
		return nil
	}

	loadedRunningBackends := make([]string, 0)
	for i := range validRoutes {
		for _, backend := range validRoutes[i].Backends {
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
	return nil
}

func isLoadableRoute(route *RouteEntry) bool {
	if route == nil || route.Name == "" || len(route.Backends) == 0 {
		return false
	}
	if route.RoutingGroup == "" && route.Hostname == "" && route.Header == "" && !route.Default {
		return false
	}
	for _, backend := range route.Backends {
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
	r = r.WithContext(ctx)

	// Forward request to coordinator using retry proxy (includes circuit breaker)
	gs.retryProxy.ServeHTTP(wrapper, r)
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
	req.URL.Path = singleJoiningSlash(targetURL.Path, req.URL.Path)
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
//   - Circuit breaker: Records success/failure based on HTTP status code
//   - 503 handling: Adds Retry-After headers without triggering resume
//
// The function reads and restores the response body to support multiple operations
// (query ID extraction, circuit breaker status recording).
//
// Returns an error if response modification fails (should not happen in normal operation).
func (gs *GatewayService) modifyResponse(resp *http.Response) error {
	gs.handleStickyRoutingFromResponse(resp)
	gs.handle503Response(resp)
	gs.recordCircuitBreakerState(resp)
	return nil
}

// handleStickyRoutingFromResponse extracts query ID from response and caches it
func (gs *GatewayService) handleStickyRoutingFromResponse(resp *http.Response) {
	if resp.StatusCode != http.StatusOK {
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/json" && !strings.HasPrefix(contentType, "application/json;") {
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

// ErrorType classifies errors for proper handling
type ErrorType int

const (
	ErrorTypePaused   ErrorType = iota // Connection refused, backend sleeping
	ErrorTypeOverload                  // Too many queries, backend overloaded
	ErrorTypeOther                     // Other errors
)

// classifyError classifies errors to determine appropriate response
func classifyError(err error, statusCode int) ErrorType {
	// Check connection errors first
	if err != nil && isConnectionError(err) {
		return ErrorTypePaused
	}

	// 503 can be either paused or overload - check error message
	if statusCode != 503 {
		return ErrorTypeOther
	}

	// No error context - default to overload (safer to not auto-resume)
	if err == nil {
		return ErrorTypeOverload
	}

	// Classify 503 by error message
	errMsg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errMsg, "too many queries"),
		strings.Contains(errMsg, "server overloaded"),
		strings.Contains(errMsg, "queue full"):
		return ErrorTypeOverload

	case strings.Contains(errMsg, "connection refused"):
		return ErrorTypePaused

	default:
		// Default 503 to overload (safer to not auto-resume)
		return ErrorTypeOverload
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

// selectBackend selects a backend from the route using capacity-aware load balancing.
//
// Load Balancing Policy: "Small-first, spill to large"
//   - Calculates effectiveLoad = (running + queued) / capacityUnits
//   - Selects backend with minimum effectiveLoad
//   - Tie-breaker: prefer smaller capacity (cost optimization)
//   - Fully deterministic (no randomness)
//
// Backend Filtering:
//   - State-based routing (operator-owned, gateway-enforced):
//   - Only State == RUNNING backends are eligible for NEW queries
//   - State != RUNNING → reject (PAUSED, RESUMING, DRAINING)
//   - Filters out backends with Active=false
//   - Filters out backends marked unhealthy by health checker
//   - Filters out backends with open circuit breakers
//   - Falls back to all backends if none are healthy (fail-open behavior)
//
// Returns nil if no backends are available in the route.
func (gs *GatewayService) selectBackend(route *RouteEntry) *Backend {
	if len(route.Backends) == 0 {
		return nil
	}

	// Filter backends: state + active flag + health checker + circuit breaker.
	// Use Selectable() for filtering to avoid mutating circuit breaker state.
	activeBackends := []Backend{}
	for i := range route.Backends {
		if gs.isBackendSelectable(&route.Backends[i]) {
			activeBackends = append(activeBackends, route.Backends[i])
		}
	}

	if len(activeBackends) == 0 {
		// Fail-open: retry with health checks and circuit breaker disabled, but never
		// bypass a fresh "sleeping" signal. Sleeping means the service is scaled to
		// zero or has no connectable endpoint; routing to it creates slow proxy
		// timeouts instead of the controlled resume response.
		gs.log.V(0).Info("No healthy backends found, retrying with non-sleeping health/breaker checks disabled (fail-open)", "route", route.RoutingGroup)
		for _, b := range route.Backends {
			// Still enforce state machine (critical: never route to non-RUNNING backends)
			if b.State != "" && b.State != StateRunning {
				continue
			}

			// Still check explicit Active flag
			if !b.Active {
				continue
			}

			if gs.healthChecker != nil && gs.healthChecker.IsSleeping(b.CoordinatorURL) {
				if !gs.healthChecker.probeBackend(context.Background(), b.CoordinatorURL) {
					gs.log.V(1).Info("Not fail-opening sleeping backend", "backend", b.CoordinatorURL)
					continue
				}
			}

			// Ignore ordinary unhealthy state and circuit breaker in fail-open mode.
			// This allows routing to backends that are temporarily marked unavailable,
			// while preserving sleeping/scale-to-zero as a resume signal.
			activeBackends = append(activeBackends, b)
		}
	}

	if len(activeBackends) == 0 {
		// Returning nil lets handleRequest trigger resume through the API server.
		// errorHandler only runs on proxy transport errors such as connection refused.
		gs.log.V(1).Info("No active backends available", "route", route.RoutingGroup)
		return nil
	}

	// Use capacity-aware load balancing: "small-first, spill to large"
	// This is the single deterministic policy - no random selection
	var selected *Backend
	backendLoads := gs.queryActivity.BackendLoads()
	if hasLoadData(activeBackends, backendLoads) {
		selected = gs.selectByCapacity(activeBackends, backendLoads)
	} else {
		// No load data: select backend with smallest capacity (deterministic)
		selected = gs.selectSmallestCapacity(activeBackends)
	}

	// Return selected backend without calling Allow()
	// RetryProxy will call Allow() right before proxying to avoid half-open probe wedge
	return selected
}

// isBackendSelectable checks whether a backend may receive a new query.
// It is the centralized filter for state, active flag, health checks, and circuit breakers.
func (gs *GatewayService) isBackendSelectable(backend *Backend) bool {
	if backend == nil {
		return false
	}

	// STATE MACHINE ENFORCEMENT (gateway-enforced, operator-owned)
	// Rule: If State != RUNNING, backend not eligible for NEW queries regardless of Active.
	// Rule: If State == RUNNING and Active == false, also not eligible.
	if backend.State != "" && backend.State != StateRunning {
		gs.log.V(1).Info("Skipping backend (state not RUNNING)", "backend", backend.CoordinatorURL, "state", backend.State)
		return false
	}

	if !backend.Active {
		return false
	}

	return gs.backendPassesRuntimeGuards(backend.CoordinatorURL)
}

// isStickyBackendRoutable checks whether an existing query may continue to a cached backend.
// DRAINING backends reject new query selection, but they still may receive follow-up requests for
// query IDs already assigned to them as long as runtime health and circuit breaker guards still pass.
func (gs *GatewayService) isStickyBackendRoutable(backend *Backend) bool {
	if backend == nil {
		return false
	}

	switch backend.State {
	case "", StateRunning:
		if !backend.Active {
			return false
		}
	case StateDraining:
		// Active may be false during draining; sticky continuations are allowed.
	default:
		return false
	}

	return gs.backendPassesRuntimeGuards(backend.CoordinatorURL)
}

func (gs *GatewayService) backendPassesRuntimeGuards(backendURL string) bool {
	if gs.healthChecker != nil && !gs.healthChecker.IsHealthy(backendURL) {
		gs.log.V(1).Info("Skipping unhealthy backend (health checker)", "backend", backendURL)
		return false
	}

	if gs.circuitBreaker != nil {
		breaker := gs.circuitBreaker.GetOrCreateBreaker(
			backendURL,
			config.GatewayCircuitBreakerFailureThreshold,
			config.GatewayCircuitBreakerSuccessThreshold,
			config.GatewayCircuitBreakerTimeout,
		)
		if !breaker.Selectable() {
			gs.log.V(1).Info("Skipping backend (circuit breaker not selectable)", "backend", backendURL)
			return false
		}
	}

	return true
}

// selectByCapacity selects backend using capacity-aware load balancing.
// Policy: "small-first, spill to large as load grows"
//
// Algorithm:
// 1. Compare load ratios using integer cross-multiplication (avoids float equality issues)
// 2. loadA/capA < loadB/capB ⟺ loadA*capB < loadB*capA
// 3. Tie-breaker: prefer smaller capacityUnits (small-first)
// 4. Second tie-breaker: first in list (deterministic)
//
// This gives deterministic, capacity-aware spillover without randomness or float comparison.
func (gs *GatewayService) selectByCapacity(backends []Backend, loads map[string]BackendLoad) *Backend {
	var selected *Backend
	var minLoad int
	var minCapacity int

	for i := range backends {
		load := getBackendLoad(loads, backends[i].CoordinatorURL)
		totalLoad := load.RunningQueries + load.QueuedQueries

		// Get capacity units (default to 1 if not set)
		capacity := backends[i].CapacityUnits
		if capacity <= 0 {
			capacity = 1
		}

		if selected == nil {
			// First backend - initialize
			selected = &backends[i]
			minLoad = totalLoad
			minCapacity = capacity
		} else {
			// Compare ratios using cross-multiplication: loadA/capA vs loadB/capB
			// loadA/capA < loadB/capB ⟺ loadA*capB < loadB*capA
			crossA := totalLoad * minCapacity
			crossB := minLoad * capacity

			if crossA < crossB {
				// New backend has lower effective load
				selected = &backends[i]
				minLoad = totalLoad
				minCapacity = capacity
			} else if crossA == crossB && capacity < minCapacity {
				// Equal effective load - prefer smaller capacity (small-first)
				selected = &backends[i]
				minLoad = totalLoad
				minCapacity = capacity
			}
		}
	}

	if selected == nil {
		// Fallback to smallest capacity if no backends
		return gs.selectSmallestCapacity(backends)
	}

	return selected
}

// selectSmallestCapacity selects the backend with smallest capacity units.
// Used when no load data is available. Deterministic (no randomness).
func (gs *GatewayService) selectSmallestCapacity(backends []Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}

	selected := &backends[0]
	minCapacity := backends[0].CapacityUnits
	if minCapacity <= 0 {
		minCapacity = 1
	}

	for i := 1; i < len(backends); i++ {
		capacity := backends[i].CapacityUnits
		if capacity <= 0 {
			capacity = 1
		}

		if capacity < minCapacity {
			minCapacity = capacity
			selected = &backends[i]
		}
	}

	return selected
}

// hasLoadData checks if we have load data for any of the backends.
func hasLoadData(backends []Backend, loads map[string]BackendLoad) bool {
	for _, b := range backends {
		load, ok := loads[b.CoordinatorURL]
		if ok && load.RunningQueries+load.QueuedQueries > 0 {
			return true
		}
	}
	return false
}

func getBackendLoad(loads map[string]BackendLoad, backendURL string) BackendLoad {
	load, exists := loads[backendURL]
	if !exists {
		return BackendLoad{}
	}
	return load
}

// extractQueryIdFromRequest extracts Trino query ID from request URI
// Trino query IDs are in format: YYYYMMDD_HHMMSS_seq_random
// Examples:
//   - /v1/statement/executing/20250115_123456_00001_abc12/...
//   - /v1/query/20250115_123456_00001_abc12
func (gs *GatewayService) extractQueryIdFromRequest(req *http.Request) string {
	path := req.URL.Path

	// Use package-level compiled regex (no recompilation per request)
	matches := trinoQueryIDRe.FindStringSubmatch(path)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

// extractQueryIdFromResponse extracts Trino query ID from JSON response body
// Trino returns query ID in JSON: {"id": "20250115_123456_00001_abc12", ...}
// NOTE: Caller must ensure Content-Type is application/json before calling.
func (gs *GatewayService) extractQueryIdFromResponse(resp *http.Response) string {
	queryID, _ := gs.extractQueryInfoFromResponse(resp)
	return queryID
}

func (gs *GatewayService) extractQueryInfoFromResponse(resp *http.Response) (queryID, state string) {
	// Read only a small prefix (64 KiB) to find the query ID, then restore the full stream.
	// This avoids buffering large responses while preserving the client response body.
	const sniffSize = 64 * 1024
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, sniffSize))
	if err != nil {
		gs.log.V(1).Info("Failed to read response prefix for query ID extraction", "error", err)
		return "", ""
	}

	// Restore full body by prepending the prefix we read
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), resp.Body))

	queryID, state, err = extractQueryInfoFromJSONPrefix(prefix)
	if err != nil {
		gs.log.V(1).Info("Failed to parse JSON prefix for query ID extraction", "error", err)
	}

	return queryID, state
}

func extractQueryInfoFromJSONPrefix(prefix []byte) (queryID, state string, err error) {
	dec := json.NewDecoder(bytes.NewReader(prefix))
	tok, err := dec.Token()
	if err != nil {
		return "", "", err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return "", "", fmt.Errorf("expected JSON object")
	}

	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			if queryID != "" {
				return queryID, state, nil
			}
			return "", "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			if queryID != "" {
				return queryID, state, nil
			}
			return "", "", fmt.Errorf("expected JSON object key")
		}

		switch key {
		case "id":
			valueToken, valueErr := dec.Token()
			if valueErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", valueErr
			}
			if id, ok := valueToken.(string); ok {
				queryID = id
			}
		case "state":
			valueToken, valueErr := dec.Token()
			if valueErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", valueErr
			}
			if topLevelState, ok := valueToken.(string); ok && state == "" {
				state = topLevelState
			}
		case "stats":
			statsState, statsErr := extractStatsState(dec)
			if statsState != "" {
				state = statsState
			}
			if statsErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", statsErr
			}
		default:
			if skipErr := skipJSONValue(dec); skipErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", skipErr
			}
		}

		if queryID != "" && state != "" {
			return queryID, state, nil
		}
	}

	return queryID, state, nil
}

func extractStatsState(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return "", nil
	}

	state := ""
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			if state != "" {
				return state, nil
			}
			return "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			if state != "" {
				return state, nil
			}
			return "", fmt.Errorf("expected stats object key")
		}
		if key == "state" {
			valueToken, valueErr := dec.Token()
			if valueErr != nil {
				if state != "" {
					return state, nil
				}
				return "", valueErr
			}
			if statsState, ok := valueToken.(string); ok {
				state = statsState
			}
			continue
		}
		if skipErr := skipJSONValue(dec); skipErr != nil {
			if state != "" {
				return state, nil
			}
			return "", skipErr
		}
	}

	if _, err := dec.Token(); err != nil && state == "" {
		return "", err
	}
	return state, nil
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		for dec.More() {
			if _, tokenErr := dec.Token(); tokenErr != nil {
				return tokenErr
			}
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return skipErr
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return skipErr
			}
		}
		_, err = dec.Token()
		return err
	default:
		return nil
	}
}

// isConnectionError checks if an error is a connection-related error that indicates
// the service is down/scaled-to-zero (not just slow/overloaded).
// Returns true for DNS errors, connection refused, connection reset during dial,
// and dial timeouts. A Kubernetes Service with no ready endpoints can surface as
// a dial timeout, so that path must still trigger resume instead of a raw 502.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for url.Error (wraps network errors)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isConnectionError(urlErr.Err)
	}

	// Check for DNS errors (no such host / NXDOMAIN)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Check for net.OpError
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return opErr.Op == "dial"
		}

		// Check for ECONNREFUSED (connection refused)
		if opErr.Err != nil {
			errStr := opErr.Err.Error()
			if strings.Contains(errStr, "connection refused") {
				return true
			}
			// Connection reset during dial (not after established)
			if strings.Contains(errStr, "connection reset") && opErr.Op == "dial" {
				return true
			}
		}
		return false
	}

	// Fallback: check error string for common connection error patterns
	// This helps with test mocks and wrapped errors
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "connection refused") ||
		(strings.Contains(errStr, "dial tcp") && strings.Contains(errStr, "i/o timeout")) {
		return true
	}
	return false
}

// startHealthChecker starts the active health checker background goroutine
func (gs *GatewayService) startHealthChecker(ctx context.Context) {
	ticker := time.NewTicker(config.GatewayHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			urls := gs.collectHealthCheckBackendURLs()
			if len(urls) > 0 && gs.healthChecker != nil {
				gs.healthChecker.checkAllBackends(ctx, urls)
			}
		}
	}
}

func (gs *GatewayService) collectHealthCheckBackendURLs() []string {
	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	backendURLs := make(map[string]bool)
	collect := func(route *RouteEntry) {
		if route == nil {
			return
		}
		for _, backend := range route.Backends {
			if backend.State != "" && backend.State != StateRunning {
				continue
			}
			if backend.Active {
				backendURLs[backend.CoordinatorURL] = true
			}
		}
	}

	for _, route := range gs.routes {
		collect(route)
	}
	collect(gs.defaultRoute)

	urls := make([]string, 0, len(backendURLs))
	for url := range backendURLs {
		urls = append(urls, url)
	}
	return urls
}
