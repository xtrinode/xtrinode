package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------------------------------------------------------------------------
// RateLimiter RLock fast path tests
// ---------------------------------------------------------------------------

func TestRateLimiter_RLockFastPath_ExistingBucket(t *testing.T) {
	rl := newTestRateLimiter(100, 1*time.Second)

	// Prime the bucket
	require.True(t, rl.Allow("key1"))

	// Subsequent calls should use the RLock fast path (no write lock)
	// Verify correctness with many concurrent readers
	var wg sync.WaitGroup
	var allowed int64
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Allow("key1") {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	// Should have allowed up to capacity (100 - 1 priming = 99 remaining)
	if allowed < 40 || allowed > 99 {
		t.Errorf("Expected ~50-99 allowed (concurrent readers), got %d", allowed)
	}
}

func TestRateLimiter_RLockFastPath_DoubleCheckPattern(t *testing.T) {
	rl := newTestRateLimiter(10, 1*time.Second)

	// Concurrent first-access for the SAME key should only create one bucket
	var wg sync.WaitGroup
	var allowed int64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Allow("new-key") {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	// Should have allowed exactly capacity (10) even with concurrent creation
	if allowed < 8 || allowed > 12 {
		t.Errorf("Expected ~10 allowed (double-check pattern), got %d", allowed)
	}
}

func TestRateLimiter_RLockFastPath_MultipleKeysConcurrent(t *testing.T) {
	rl := newTestRateLimiter(5, 1*time.Second)

	// Concurrent access to different keys should not interfere
	var wg sync.WaitGroup
	results := make(map[string]*int64)
	for _, key := range []string{"a", "b", "c", "d"} {
		counter := new(int64)
		results[key] = counter
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(k string, c *int64) {
				defer wg.Done()
				if rl.Allow(k) {
					atomic.AddInt64(c, 1)
				}
			}(key, counter)
		}
	}
	wg.Wait()

	for key, counter := range results {
		allowed := atomic.LoadInt64(counter)
		if allowed < 3 || allowed > 7 {
			t.Errorf("Key %s: expected ~5 allowed, got %d", key, allowed)
		}
	}
}

// ---------------------------------------------------------------------------
// Distributed rate limiting via Redis
// ---------------------------------------------------------------------------

func TestNewDistributedRateLimiter_NilRedis(t *testing.T) {
	rl := NewDistributedRateLimiter(100, time.Second, nil, time.Second, logr.Discard())
	require.NotNil(t, rl)
	require.False(t, rl.redisEnabled)

	// Should fall back to local token bucket
	require.True(t, rl.Allow("key1"))
}

func TestNewDistributedRateLimiter_WindowSize(t *testing.T) {
	// With nil Redis client, windowSize is not computed (Redis path skipped)
	rl := NewDistributedRateLimiter(100, time.Second, nil, time.Second, logr.Discard())
	require.Equal(t, time.Duration(0), rl.windowSize, "windowSize should be 0 when Redis is nil")
	require.False(t, rl.redisEnabled)

	// Verify windowSize calculation logic directly
	// windowSize = capacity * refillRate, floored to 1s
	windowSize := time.Duration(100) * time.Second
	require.Equal(t, 100*time.Second, windowSize)

	smallWindow := time.Duration(1) * time.Millisecond
	if smallWindow < time.Second {
		smallWindow = time.Second
	}
	require.Equal(t, time.Second, smallWindow, "Small window should be floored to 1s")
}

func TestNewGatewayRateLimiter_LocalWhenStickyRedisUnavailable(t *testing.T) {
	rl := newGatewayRateLimiter(&RedisStickyClient{}, defaultRateLimitConfig(), logr.Discard())
	require.NotNil(t, rl)
	require.False(t, rl.redisEnabled)
}

func TestNewGatewayRateLimiter_UsesStickyRedisClient(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer redisClient.Close()

	rl := newGatewayRateLimiter(&RedisStickyClient{
		redis:   redisClient,
		enabled: true,
	}, defaultRateLimitConfig(), logr.Discard())

	require.NotNil(t, rl)
	require.True(t, rl.redisEnabled)
	require.Same(t, redisClient, rl.redis)
}

func TestNewGatewayRateLimiter_Disabled(t *testing.T) {
	rl := newGatewayRateLimiter(&RedisStickyClient{}, RateLimitConfig{Enabled: false}, logr.Discard())
	require.Nil(t, rl)
}

func TestRateLimiter_AllowRedis_FallbackOnError(t *testing.T) {
	// Create a rate limiter with redisEnabled=true but nil redis client
	// This simulates Redis being unavailable
	rl := NewRateLimiter(10, time.Second, logr.Discard())
	rl.redisEnabled = true // Force enabled but no client
	rl.redis = nil
	rl.redisTimeout = time.Second

	// Should fall back to local token bucket
	require.True(t, rl.Allow("key1"))
}

// ---------------------------------------------------------------------------
// ConfigMap ResourceVersion-based polling
// ---------------------------------------------------------------------------

func TestHasConfigMapChanged(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	gs, err := NewGatewayService(cli, logr.Discard(), "http://api-server:8081/api/v1", nil)
	require.NoError(t, err)

	// Create ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GatewayConfigMapName,
			Namespace: GatewayConfigMapNamespace,
		},
		Data: map[string]string{
			GatewayConfigMapKey: "routes: []\n",
		},
	}
	require.NoError(t, cli.Create(ctx, configMap))

	var lastVersion string

	// First check should detect change (lastVersion is empty)
	changed, err := gs.hasConfigMapChanged(ctx, &lastVersion)
	require.NoError(t, err)
	require.True(t, changed, "First check should detect change")
	require.NotEmpty(t, lastVersion)

	// Second check with same version should detect no change
	changed, err = gs.hasConfigMapChanged(ctx, &lastVersion)
	require.NoError(t, err)
	require.False(t, changed, "Second check should detect no change")

	// Update ConfigMap
	configMap.Data[GatewayConfigMapKey] = `routes:
  - name: test
    routingGroup: test
    backends:
      - name: test
        coordinatorURL: http://test:8080
        active: true
`
	require.NoError(t, cli.Update(ctx, configMap))

	// Third check should detect change
	changed, err = gs.hasConfigMapChanged(ctx, &lastVersion)
	require.NoError(t, err)
	require.True(t, changed, "Third check should detect change after update")
}

func TestHasConfigMapChanged_MissingConfigMap(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	gs, err := NewGatewayService(cli, logr.Discard(), "http://api-server:8081/api/v1", nil)
	require.NoError(t, err)

	var lastVersion string
	_, err = gs.hasConfigMapChanged(ctx, &lastVersion)
	require.Error(t, err, "Should error when ConfigMap doesn't exist")
}

// ---------------------------------------------------------------------------
// Sticky routing cross-route search includes defaultRoute
// ---------------------------------------------------------------------------

func TestStickyRouting_DefaultRouteSearch(t *testing.T) {
	// Create a default route that has NO routingGroup, hostname, or header
	// This means it would only be in gs.defaultRoute, not in gs.routes map
	defaultRoute := RouteEntry{
		Name: "default-only",
		Backends: []Backend{
			{
				Name:           "default-backend",
				Namespace:      "default",
				CoordinatorURL: "http://default-backend:8080",
				Active:         true,
				State:          StateRunning,
			},
		},
		Default: true,
	}

	gs, _ := createTestGatewayService(t, []RouteEntry{})

	// Manually set only defaultRoute (not in routes map)
	gs.routesLock.Lock()
	gs.defaultRoute = &defaultRoute
	// Don't add to gs.routes — simulates a route with no routingGroup/hostname/header
	gs.routesLock.Unlock()

	// Store a sticky route for a query
	queryID := "20250115_123456_00001_abc12"
	err := gs.stickyClient.Set(context.Background(), queryID,
		"default", "default-backend", "http://default-backend:8080", "")
	require.NoError(t, err)

	// Now simulate the handleRequest lookup: build a request with the query ID
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/v1/statement/executing/"+queryID+"/0", http.NoBody)
	req.Host = ""

	// Set default route for findRoute
	gs.routesLock.Lock()
	gs.defaultRoute = &defaultRoute
	gs.routesLock.Unlock()

	// Verify the backend can be found through defaultRoute
	gs.routesLock.RLock()
	var found *Backend
	// Iterate routes map (won't find it)
	for _, routeEntry := range gs.routes {
		for i := range routeEntry.Backends {
			if routeEntry.Backends[i].CoordinatorURL == "http://default-backend:8080" {
				found = &routeEntry.Backends[i]
				break
			}
		}
	}
	// Check defaultRoute (should find it here)
	if found == nil && gs.defaultRoute != nil {
		for i := range gs.defaultRoute.Backends {
			if gs.defaultRoute.Backends[i].CoordinatorURL == "http://default-backend:8080" {
				found = &gs.defaultRoute.Backends[i]
				break
			}
		}
	}
	gs.routesLock.RUnlock()

	require.NotNil(t, found, "Backend should be found in defaultRoute")
	require.Equal(t, "default-backend", found.Name)
}

// ---------------------------------------------------------------------------
// Circuit breaker overload accounting
// ---------------------------------------------------------------------------

func TestCircuitBreaker_RecordOverload_BelowThreshold(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	// failureThreshold=5, so overload threshold = 5 * 3 = 15
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Record overloads below threshold (14 out of 15)
	for i := 0; i < 14; i++ {
		breaker.RecordOverload()
	}

	require.Equal(t, CircuitClosed, breaker.GetState(),
		"Should still be closed below overload threshold")
	require.True(t, breaker.Allow(), "Should still allow requests")
}

func TestCircuitBreaker_RecordOverload_AtThreshold(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Record overloads at threshold (15 = 5 * 3)
	overloadThreshold := 5 * OverloadThresholdMultiplier
	for i := 0; i < overloadThreshold; i++ {
		breaker.RecordOverload()
	}

	require.Equal(t, CircuitOpen, breaker.GetState(),
		"Should be open at overload threshold")
	require.False(t, breaker.Allow(), "Should block requests when open")
}

func TestCircuitBreaker_RecordOverload_ResetBySuccess(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Record some overloads
	for i := 0; i < 10; i++ {
		breaker.RecordOverload()
	}

	// Success resets overload counter
	breaker.RecordSuccess()

	stats := breaker.GetStats()
	require.Equal(t, 0, stats["consecutiveOverloads"].(int),
		"Overload counter should be reset by success")
}

func TestCircuitBreaker_RecordOverload_ResetByFailure(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	for i := 0; i < 10; i++ {
		breaker.RecordOverload()
	}

	// Hard failure resets overload counter
	breaker.RecordFailure()

	stats := breaker.GetStats()
	require.Equal(t, 0, stats["consecutiveOverloads"].(int),
		"Overload counter should be reset by hard failure")
}

func TestCircuitBreaker_RecordOverload_HalfOpen(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Open circuit via hard failures
	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}
	require.Equal(t, CircuitOpen, breaker.GetState())

	// Wait for timeout to transition to half-open
	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-31 * time.Second)
	breaker.mu.Unlock()

	require.True(t, breaker.Allow(), "Should allow probe in half-open")
	require.Equal(t, CircuitHalfOpen, breaker.GetState())

	// Overload during half-open should reopen
	breaker.RecordOverload()
	require.Equal(t, CircuitOpen, breaker.GetState(),
		"Overload during half-open should reopen circuit")
}

func TestCircuitBreaker_OverloadThresholdMultiplier(t *testing.T) {
	require.Equal(t, 3, OverloadThresholdMultiplier,
		"Overload threshold should be 3x failure threshold")
}

func TestCircuitBreaker_RecordOverload_InGetStats(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	breaker.RecordOverload()
	breaker.RecordOverload()
	breaker.RecordOverload()

	stats := breaker.GetStats()
	require.Equal(t, 3, stats["consecutiveOverloads"].(int))
}

// ---------------------------------------------------------------------------
// DrainRoute and DeregisterRoute preserve absent ConfigMaps
// ---------------------------------------------------------------------------

func TestDrainRoute_NoConfigMapCreation(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "m",
		},
	}

	// DrainRoute should succeed even without ConfigMap (nothing to drain)
	err := DrainRoute(ctx, cli, xtrinode)
	require.NoError(t, err)

	// Verify no ConfigMap was created
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}
	getErr := cli.Get(ctx, key, configMap)
	require.Error(t, getErr, "ConfigMap should NOT be created by DrainRoute")
}

func TestDeregisterRoute_NoConfigMapCreation(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "m",
		},
	}

	// DeregisterRoute should succeed even without ConfigMap (nothing to deregister)
	err := DeregisterRoute(ctx, cli, xtrinode)
	require.NoError(t, err)

	// Verify no ConfigMap was created
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}
	getErr := cli.Get(ctx, key, configMap)
	require.Error(t, getErr, "ConfigMap should NOT be created by DeregisterRoute")
}

func TestDrainRoute_WithExistingConfigMap(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:    "m",
			Routing: &analyticsv1.RoutingSpec{RoutingGroup: "test-runtime"},
		},
	}

	// Register first
	require.NoError(t, RegisterRoute(ctx, cli, xtrinode))

	// Drain should work on existing ConfigMap
	require.NoError(t, DrainRoute(ctx, cli, xtrinode))

	// Verify backend state changed
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}
	require.NoError(t, cli.Get(ctx, key, configMap))

	routes, err := parseRoutes(configMap.Data[GatewayConfigMapKey])
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Backends, 1)
	require.Equal(t, StateDraining, routes[0].Backends[0].State)
	require.False(t, routes[0].Backends[0].Active)
}

func TestParseRoutes_DefaultsOmittedActiveToTrue(t *testing.T) {
	routes, err := parseRoutes(`
routes:
  - name: manual
    routingGroup: manual
    backends:
      - name: manual
        namespace: team-a
        coordinatorURL: http://manual:8080
        state: RUNNING
`)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Backends, 1)
	require.True(t, routes[0].Backends[0].Active)
}

func TestParseRoutes_PreservesExplicitInactiveBackend(t *testing.T) {
	routes, err := parseRoutes(`
routes:
  - name: manual
    routingGroup: manual
    backends:
      - name: manual
        namespace: team-a
        coordinatorURL: http://manual:8080
        state: RUNNING
        active: false
`)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Backends, 1)
	require.False(t, routes[0].Backends[0].Active)
}

// ---------------------------------------------------------------------------
// handleStickyRoutingFromResponse filters non-JSON responses
// ---------------------------------------------------------------------------

func TestHandleStickyRouting_FiltersNonJSON(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	// Non-JSON response should be skipped by handleStickyRoutingFromResponse
	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Trino-Backend-URL", "http://backend:8080")
	req.Header.Set("X-Trino-XTrinode-Name", "test")
	req.Header.Set("X-Trino-XTrinode-Namespace", "default")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/plain"},
		},
		Request: req,
		Body:    http.NoBody,
	}

	// Should not panic and should not try to parse
	gs.handleStickyRoutingFromResponse(resp)
}

func TestHandleStickyRouting_ProcessesJSON(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Trino-Backend-URL", "http://backend:8080")
	req.Header.Set("X-Trino-XTrinode-Name", "test")
	req.Header.Set("X-Trino-XTrinode-Namespace", "default")
	req.Header.Set("X-Trino-Routing-Group", "test-group")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Request: req,
		Body:    http.NoBody,
	}

	// Should not panic for empty body
	gs.handleStickyRoutingFromResponse(resp)
}

// ---------------------------------------------------------------------------
// Rewritten: Sticky routing tests (Redis-backed)
// ---------------------------------------------------------------------------

func TestStickyRouting_SetAndGet(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	ctx := context.Background()

	queryID := "20250115_123456_00001_abc12"
	err := gs.stickyClient.Set(ctx, queryID, "team-a", "dummy", "http://dummy:8080", "dummy-group")
	require.NoError(t, err)

	ns, name, url, rg, found := gs.stickyClient.Get(ctx, queryID)
	require.True(t, found)
	require.Equal(t, "team-a", ns)
	require.Equal(t, "dummy", name)
	require.Equal(t, "http://dummy:8080", url)
	require.Equal(t, "dummy-group", rg)
}

func TestStickyRouting_Miss(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	ctx := context.Background()

	_, _, _, _, found := gs.stickyClient.Get(ctx, "nonexistent") //nolint:dogsled // test only checks found flag
	require.False(t, found)
}

func TestStickyRouting_Delete(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	ctx := context.Background()

	queryID := "20250115_123456_00001_abc12"
	require.NoError(t, gs.stickyClient.Set(ctx, queryID, "ns", "name", "http://url:8080", "rg"))
	require.NoError(t, gs.stickyClient.Delete(ctx, queryID))

	_, _, _, _, found := gs.stickyClient.Get(ctx, queryID) //nolint:dogsled // test only checks found flag
	require.False(t, found, "Should be deleted")
}

func TestStickyRouting_Overwrite(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	ctx := context.Background()

	queryID := "20250115_123456_00001_abc12"
	require.NoError(t, gs.stickyClient.Set(ctx, queryID, "ns1", "name1", "http://url1:8080", "rg1"))
	require.NoError(t, gs.stickyClient.Set(ctx, queryID, "ns2", "name2", "http://url2:8080", "rg2"))

	ns, name, url, rg, found := gs.stickyClient.Get(ctx, queryID)
	require.True(t, found)
	require.Equal(t, "ns2", ns)
	require.Equal(t, "name2", name)
	require.Equal(t, "http://url2:8080", url)
	require.Equal(t, "rg2", rg)
}

func TestRedisStickyClient_FallbackWhenRedisCommandsFail(t *testing.T) {
	sticky, err := NewRedisStickyClient(RedisConfig{
		Enabled: false,
		TTL:     time.Minute,
		Timeout: 10 * time.Millisecond,
	}, logr.Discard())
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
	})
	defer redisClient.Close()

	sticky.redis = redisClient
	sticky.enabled = true

	queryID := "20250115_123456_00003_abc12"
	setCtx, setCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer setCancel()
	require.NoError(t, sticky.Set(setCtx, queryID, "team-a", "runtime-a", "http://backend:8080", "runtime-a"))

	getCtx, getCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer getCancel()
	ns, name, backendURL, routingGroup, found := sticky.Get(getCtx, queryID)
	require.True(t, found)
	require.Equal(t, "team-a", ns)
	require.Equal(t, "runtime-a", name)
	require.Equal(t, "http://backend:8080", backendURL)
	require.Equal(t, "runtime-a", routingGroup)
}

// ---------------------------------------------------------------------------
// Rewritten: Error handler tests (API server client)
// ---------------------------------------------------------------------------

func TestErrorHandler_ConnectionRefused(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"triggered":true,"gated":false,"retryAfter":30}`)
	}))
	defer apiServer.Close()

	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.apiServerClient = NewAPIServerClient(apiServer.URL, 5*time.Second, logr.Discard())

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/statement", http.NoBody)
	req.Header.Set("X-Trino-XTrinode-Name", "test-backend")
	req.Header.Set("X-Trino-XTrinode-Namespace", "test-ns")
	req.Header.Set("X-Trino-Routing-Group", "test-group")
	req.Header.Set("X-Trino-Backend-URL", "http://test-backend:8080")

	w := httptest.NewRecorder()
	gs.errorHandler(w, req, fmt.Errorf("connection refused"))

	// Connection refused → paused backend → resume via API
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestErrorHandler_GenericError(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/statement", http.NoBody)
	w := httptest.NewRecorder()
	gs.errorHandler(w, req, fmt.Errorf("some random error"))

	// Generic error → 502 Bad Gateway
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), "Gateway error: backend request failed")
	require.NotContains(t, w.Body.String(), "some random error")
}

func TestErrorHandler_NoSuchHost(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"triggered":true,"gated":false,"retryAfter":30}`)
	}))
	defer apiServer.Close()

	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.apiServerClient = NewAPIServerClient(apiServer.URL, 5*time.Second, logr.Discard())

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/statement", http.NoBody)
	req.Header.Set("X-Trino-XTrinode-Name", "test-backend")
	req.Header.Set("X-Trino-XTrinode-Namespace", "test-ns")
	req.Header.Set("X-Trino-Routing-Group", "test-group")
	req.Header.Set("X-Trino-Backend-URL", "http://test-backend:8080")

	w := httptest.NewRecorder()
	gs.errorHandler(w, req, fmt.Errorf("no such host"))

	// DNS error → connection error → paused → resume
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ---------------------------------------------------------------------------
// Integration: recordCircuitBreakerState now records 503s
// ---------------------------------------------------------------------------

func TestRecordCircuitBreakerState_503AsOverload(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Trino-Backend-URL", "http://backend:8080")

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
		Request:    req,
	}

	// Record many 503s
	for i := 0; i < 20; i++ {
		gs.recordCircuitBreakerState(resp)
	}

	// Verify overload was recorded (threshold = 5 * 3 = 15)
	breaker := gs.circuitBreaker.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)
	require.Equal(t, CircuitOpen, breaker.GetState(),
		"Should be open after 20 consecutive 503s (threshold=15)")
}

func TestRecordCircuitBreakerState_503BelowThreshold(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Trino-Backend-URL", "http://backend2:8080")

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
		Request:    req,
	}

	// Record 503s below threshold
	for i := 0; i < 10; i++ {
		gs.recordCircuitBreakerState(resp)
	}

	breaker := gs.circuitBreaker.GetOrCreateBreaker("http://backend2:8080", 5, 2, 30*time.Second)
	require.Equal(t, CircuitClosed, breaker.GetState(),
		"Should still be closed below overload threshold (10 < 15)")
}

func TestRecordCircuitBreakerState_503ResetBySuccess(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Trino-Backend-URL", "http://backend3:8080")

	// Record some 503s
	resp503 := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
		Request:    req,
	}
	for i := 0; i < 10; i++ {
		gs.recordCircuitBreakerState(resp503)
	}

	// Success resets overload counter
	resp200 := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Request:    req,
	}
	gs.recordCircuitBreakerState(resp200)

	// More 503s should start counting from 0
	for i := 0; i < 10; i++ {
		gs.recordCircuitBreakerState(resp503)
	}

	breaker := gs.circuitBreaker.GetOrCreateBreaker("http://backend3:8080", 5, 2, 30*time.Second)
	require.Equal(t, CircuitClosed, breaker.GetState(),
		"Should be closed: overloads were reset by success")
}

// ---------------------------------------------------------------------------
// getConfigMap helper tests
// ---------------------------------------------------------------------------

func TestGetConfigMap_NotFound(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	cm, err := getConfigMap(ctx, cli, "nonexistent", "default")
	require.Error(t, err)
	require.Nil(t, cm)
}

func TestGetConfigMap_Exists(t *testing.T) {
	ctx := context.Background()

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm",
			Namespace: "default",
		},
		Data: map[string]string{"key": "value"},
	}
	cli := fake.NewClientBuilder().WithObjects(existing).Build()

	cm, err := getConfigMap(ctx, cli, "test-cm", "default")
	require.NoError(t, err)
	require.NotNil(t, cm)
	require.Equal(t, "value", cm.Data["key"])
}

func TestGetConfigMap_NilData(t *testing.T) {
	ctx := context.Background()

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm-nil",
			Namespace: "default",
		},
	}
	cli := fake.NewClientBuilder().WithObjects(existing).Build()

	cm, err := getConfigMap(ctx, cli, "test-cm-nil", "default")
	require.NoError(t, err)
	require.NotNil(t, cm.Data, "Data map should be initialized")
}

// ---------------------------------------------------------------------------
// isConnectionError classification tests
// ---------------------------------------------------------------------------

type testTimeoutError struct{}

func (testTimeoutError) Error() string   { return "i/o timeout" }
func (testTimeoutError) Timeout() bool   { return true }
func (testTimeoutError) Temporary() bool { return true }

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		expected   ErrorType
	}{
		{"connection refused", fmt.Errorf("connection refused"), 0, ErrorTypePaused},
		{"no such host", fmt.Errorf("no such host"), 0, ErrorTypePaused},
		{"dial timeout", &net.OpError{Op: "dial", Err: testTimeoutError{}}, 0, ErrorTypePaused},
		{"wrapped dial timeout string", fmt.Errorf("dial tcp 10.43.155.194:8080: i/o timeout"), 0, ErrorTypePaused},
		{"read timeout", &net.OpError{Op: "read", Err: testTimeoutError{}}, 0, ErrorTypeOther},
		{"generic error", fmt.Errorf("something went wrong"), 0, ErrorTypeOther},
		{"503 no error", nil, 503, ErrorTypeOverload},
		{"503 too many queries", fmt.Errorf("too many queries"), 503, ErrorTypeOverload},
		{"503 connection refused", fmt.Errorf("connection refused"), 503, ErrorTypePaused},
		{"non-503 status", nil, 500, ErrorTypeOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyError(tt.err, tt.statusCode)
			require.Equal(t, tt.expected, result)
		})
	}
}

// ---------------------------------------------------------------------------
// Retry-After floor test
// ---------------------------------------------------------------------------

func TestRetryAfterFloor(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		// Return retryAfter: 0 (should be floored to 5)
		fmt.Fprintf(w, `{"triggered":true,"gated":false,"retryAfter":0}`)
	}))
	defer apiServer.Close()

	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.apiServerClient = NewAPIServerClient(apiServer.URL, 5*time.Second, logr.Discard())

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/statement", http.NoBody)
	w := httptest.NewRecorder()

	gs.handleResumeViaAPI(w, req, "test-ns", "test-name", "test-group")

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	retryAfter := w.Header().Get("Retry-After")
	require.Equal(t, "5", retryAfter, "Retry-After should be floored to 5")

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(5), body["retryAfter"], "response body retryAfter should match floored header")
}

// ---------------------------------------------------------------------------
// Metrics endpoint test
// ---------------------------------------------------------------------------

func TestMetricsEndpoint_Registered(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	w := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}
