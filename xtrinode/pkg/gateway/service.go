package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/pkg/gateway/auth"
)

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
	ui         GatewayUIConfig

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

	// Route reload status for operator-facing diagnostics.
	reloadState     routeReloadState
	reloadStateLock sync.RWMutex
}

// GatewayOptions contains runtime options that are deployment-specific.
type GatewayOptions struct {
	Redis              RedisConfig
	RateLimit          RateLimitConfig
	HTTPServer         HTTPServerConfig
	UI                 GatewayUIConfig
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

type GatewayUIConfig struct {
	Enabled     bool
	RequireAuth bool
}

// NewGatewayService creates a new gateway service
// authenticator is the authentication implementation (optional, nil disables authentication)
func NewGatewayService(cli client.Client, log logr.Logger, apiServerURL string, authenticator auth.Authenticator) (*GatewayService, error) {
	return NewGatewayServiceWithOptions(cli, log, apiServerURL, authenticator, &GatewayOptions{
		Redis:      defaultRedisConfig(),
		RateLimit:  defaultRateLimitConfig(),
		HTTPServer: defaultHTTPServerConfig(),
		UI:         defaultGatewayUIConfig(),
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

func defaultGatewayUIConfig() GatewayUIConfig {
	return GatewayUIConfig{
		Enabled:     false,
		RequireAuth: true,
	}
}

// NewGatewayServiceWithOptions creates a gateway service with explicit runtime options.
func NewGatewayServiceWithOptions(cli client.Client, log logr.Logger, apiServerURL string, authenticator auth.Authenticator, opts *GatewayOptions) (*GatewayService, error) {
	if opts == nil {
		opts = &GatewayOptions{
			Redis:      defaultRedisConfig(),
			RateLimit:  defaultRateLimitConfig(),
			HTTPServer: defaultHTTPServerConfig(),
			UI:         defaultGatewayUIConfig(),
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
	uiConfig := opts.UI
	if !uiConfig.Enabled {
		uiConfig.RequireAuth = true
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
		ui:              uiConfig,
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

	mux.Handle(GatewayStatusAPIPath, gs.gatewayUIHandler(http.HandlerFunc(gs.handleGatewayStatus)))
	mux.Handle(GatewayUIPath, gs.gatewayUIHandler(http.HandlerFunc(gs.redirectGatewayUI)))
	mux.Handle(GatewayUIPath+"/", gs.gatewayUIHandler(gs.gatewayUIFileServer()))

	// Apply middleware chain: rate limiting -> authentication -> request handler.
	// Rate limiting intentionally runs before auth and keys on network identity.
	var handler http.Handler = http.HandlerFunc(gs.handleRequest)
	var trinoUIHandler http.Handler = http.HandlerFunc(gs.handleTrinoUIRequest)
	if gs.authenticator != nil {
		handler = auth.Middleware(gs.authenticator)(handler)
		trinoUIHandler = auth.Middleware(gs.authenticator)(trinoUIHandler)
	}
	if gs.rateLimiter != nil {
		handler = RateLimitMiddleware(gs.rateLimiter)(handler)
		trinoUIHandler = RateLimitMiddleware(gs.rateLimiter)(trinoUIHandler)
	}
	mux.Handle(TrinoUIPath, trinoUIHandler)
	mux.Handle(TrinoUIPath+"/", trinoUIHandler)
	mux.Handle("/", handler)

	// Health and metrics endpoints bypass auth and rate limiting.
	mux.HandleFunc(config.HealthPath, gs.handleHealth)
	mux.Handle("/metrics", promhttp.HandlerFor(crmetrics.Registry, promhttp.HandlerOpts{}))

	return mux
}
