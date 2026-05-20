package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newMockLogger() logr.Logger {
	return logr.Discard()
}

// createTestGatewayService creates a GatewayService for testing
func createTestGatewayService(t *testing.T, routes []RouteEntry) (*GatewayService, client.Client) {
	cli := fake.NewClientBuilder().Build()
	log := newMockLogger()
	gs, err := NewGatewayService(cli, log, "http://api-server:8081/api/v1", nil)
	if err != nil {
		t.Fatalf("Failed to create gateway service: %v", err)
	}

	// Manually set routes for testing (use prefixed keys to match production code)
	gs.routesLock.Lock()
	gs.routes = make(map[string]*RouteEntry)
	gs.defaultRoute = nil
	for i := range routes {
		route := &routes[i]
		if route.Default {
			gs.defaultRoute = route
		}
		if route.RoutingGroup != "" {
			gs.routes["rg:"+route.RoutingGroup] = route
		}
		if route.Hostname != "" {
			gs.routes["host:"+route.Hostname] = route
		}
		if route.Header != "" {
			gs.routes["hdr:"+route.Header] = route
		}
	}
	gs.routesLock.Unlock()

	return gs, cli
}

func TestNewGatewayServiceWithOptions_UsesConfiguredPort(t *testing.T) {
	cli := fake.NewClientBuilder().Build()
	gs, err := NewGatewayServiceWithOptions(cli, newMockLogger(), "http://api-server:8081/api/v1", nil, &GatewayOptions{
		Redis:     defaultRedisConfig(),
		RateLimit: defaultRateLimitConfig(),
		Port:      18080,
	})
	if err != nil {
		t.Fatalf("NewGatewayServiceWithOptions failed: %v", err)
	}
	if gs.port != 18080 {
		t.Fatalf("expected configured port 18080, got %d", gs.port)
	}
}

func requireStickySet(t *testing.T, gs *GatewayService, queryID, namespace, name, backendURL, routingGroup string) {
	t.Helper()
	if err := gs.stickyClient.Set(context.Background(), queryID, namespace, name, backendURL, routingGroup); err != nil {
		t.Fatalf("failed to seed sticky route: %v", err)
	}
}

type trackingReadCloser struct {
	reader *strings.Reader
	closed *atomic.Bool
}

func (r trackingReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r trackingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func TestExtractQueryIdFromRequest(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "query ID in statement executing path",
			path:     "/v1/statement/executing/20250115_123456_00001_abc12/0",
			expected: "20250115_123456_00001_abc12",
		},
		{
			name:     "query ID in statement path with trailing slash",
			path:     "/v1/statement/20250115_123456_00001_abc12/",
			expected: "20250115_123456_00001_abc12",
		},
		{
			name:     "query ID in query path",
			path:     "/v1/query/20250115_123456_00001_abc12",
			expected: "20250115_123456_00001_abc12",
		},
		{
			name:     "query ID at end of path without trailing slash",
			path:     "/v1/query/20250115_123456_00001_abc12",
			expected: "20250115_123456_00001_abc12",
		},
		{
			name:     "no query ID",
			path:     "/v1/statement",
			expected: "",
		},
		{
			name:     "invalid query ID format",
			path:     "/v1/statement/20250115_123456",
			expected: "",
		},
		{
			name:     "multiple query IDs (should match first)",
			path:     "/v1/statement/20250115_123456_00001_abc12/20250115_123457_00002_def34",
			expected: "20250115_123456_00001_abc12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				URL: &url.URL{Path: tt.path},
			}
			result := gs.extractQueryIdFromRequest(req)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestExtractQueryIdFromResponse(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	tests := []struct {
		name        string
		contentType string
		body        string
		expected    string
	}{
		{
			name:        "valid JSON with query ID",
			contentType: "application/json",
			body:        `{"id": "20250115_123456_00001_abc12", "state": "RUNNING"}`,
			expected:    "20250115_123456_00001_abc12",
		},
		{
			name:        "JSON with charset",
			contentType: "application/json; charset=utf-8",
			body:        `{"id": "20250115_123456_00001_abc12"}`,
			expected:    "20250115_123456_00001_abc12",
		},
		{
			name:        "JSON without query ID",
			contentType: "application/json",
			body:        `{"state": "RUNNING"}`,
			expected:    "",
		},
		{
			name:        "non-JSON content type still parses (caller filters)",
			contentType: "text/plain",
			body:        `{"id": "20250115_123456_00001_abc12"}`,
			expected:    "20250115_123456_00001_abc12", // Content-Type filtering is done by caller
		},
		{
			name:        "invalid JSON",
			contentType: "application/json",
			body:        `{invalid json}`,
			expected:    "",
		},
		{
			name:        "query ID is not a string",
			contentType: "application/json",
			body:        `{"id": 12345}`,
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				Header: http.Header{
					"Content-Type": []string{tt.contentType},
				},
				Body: io.NopCloser(strings.NewReader(tt.body)),
			}
			result := gs.extractQueryIdFromResponse(resp)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
			// Verify body is still readable (was restored)
			body, _ := io.ReadAll(resp.Body)
			if string(body) != tt.body {
				t.Errorf("Response body was not properly restored")
			}
		})
	}
}

func TestExtractQueryInfoFromResponse(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	resp := &http.Response{
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"20250115_123456_00001_abc12","stats":{"state":"QUEUED"}}`)),
	}

	queryID, state := gs.extractQueryInfoFromResponse(resp)
	if queryID != "20250115_123456_00001_abc12" {
		t.Fatalf("expected query ID, got %q", queryID)
	}
	if state != "QUEUED" {
		t.Fatalf("expected QUEUED state, got %q", state)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"id":"20250115_123456_00001_abc12","stats":{"state":"QUEUED"}}` {
		t.Fatalf("response body was not restored: %s", string(body))
	}
}

func TestExtractQueryInfoFromResponseRestoredBodyClosesOriginal(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	body := `{"id":"20250115_123456_00001_abc12","stats":{"state":"QUEUED"}}`
	var closed atomic.Bool
	resp := &http.Response{
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: trackingReadCloser{
			reader: strings.NewReader(body),
			closed: &closed,
		},
	}

	queryID, state := gs.extractQueryInfoFromResponse(resp)
	if queryID != "20250115_123456_00001_abc12" || state != "QUEUED" {
		t.Fatalf("unexpected query info: id=%q state=%q", queryID, state)
	}
	restored, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read restored body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("response body was not restored: %s", string(restored))
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("failed to close restored body: %v", err)
	}
	if !closed.Load() {
		t.Fatalf("expected restored body close to close original body")
	}
}

func TestExtractQueryInfoFromResponse_LargeResponsePrefix(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})

	body := `{"id":"20250115_123456_00001_abc12","stats":{"state":"RUNNING"},"data":"` +
		strings.Repeat("x", 70*1024) + `"}`
	resp := &http.Response{
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}

	queryID, state := gs.extractQueryInfoFromResponse(resp)
	if queryID != "20250115_123456_00001_abc12" {
		t.Fatalf("expected query ID from large response prefix, got %q", queryID)
	}
	if state != "RUNNING" {
		t.Fatalf("expected RUNNING state, got %q", state)
	}

	restored, _ := io.ReadAll(resp.Body)
	if string(restored) != body {
		t.Fatalf("response body was not restored")
	}
}

func TestFindRoute(t *testing.T) {
	routes := []RouteEntry{
		{
			Name:         "dummy",
			RoutingGroup: "dummy",
			Backends: []Backend{
				{CoordinatorURL: "http://dummy-coord:8080", Active: true},
			},
			Header:   "dummy",
			Hostname: "dummy.trino.local",
		},
		{
			Name:         "default",
			RoutingGroup: "default",
			Backends: []Backend{
				{CoordinatorURL: "http://default-coord:8080", Active: true},
			},
			Default: true,
		},
	}

	gs, _ := createTestGatewayService(t, routes)

	tests := []struct {
		name     string
		hostname string
		header   string
		expected string
	}{
		{
			name:     "route by hostname",
			hostname: "dummy.trino.local",
			expected: "dummy",
		},
		{
			name:     "route by hostname with port",
			hostname: "dummy.trino.local:443",
			expected: "dummy",
		},
		{
			name:     "route by hostname case insensitive",
			hostname: "DUMMY.TRINO.LOCAL",
			expected: "dummy",
		},
		{
			name:     "route by header",
			header:   "dummy",
			expected: "dummy",
		},
		{
			name:     "route by default",
			expected: "default",
		},
		{
			name:     "unknown hostname falls through to default route",
			hostname: "unknown.trino.local",
			expected: "default", // Host header always present - fall through to default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				Host:   tt.hostname,
				Header: make(http.Header),
			}
			if tt.header != "" {
				req.Header.Set("X-Trino-XTrinode", tt.header)
			}

			route := gs.findRoute(req)
			if tt.expected == "" {
				if route != nil {
					t.Errorf("Expected nil route, got '%s'", route.Name)
				}
			} else {
				if route == nil {
					t.Fatal("Expected route to be found")
				}
				if route.Name != tt.expected {
					t.Errorf("Expected route '%s', got '%s'", tt.expected, route.Name)
				}
			}
		})
	}
}

func TestLoadBalancing(t *testing.T) {
	tests := []struct {
		name     string
		backends []Backend
		strategy string
	}{
		{
			name: "default smallest-capacity selection",
			backends: []Backend{
				{CoordinatorURL: "http://coord1:8080", Active: true},
				{CoordinatorURL: "http://coord2:8080", Active: true},
				{CoordinatorURL: "http://coord3:8080", Active: true},
			},
			strategy: "default",
		},
		{
			name: "capacity-aware selection",
			backends: []Backend{
				{CoordinatorURL: "http://coord1:8080", Active: true, Tier: "s", CapacityUnits: 2},
				{CoordinatorURL: "http://coord2:8080", Active: true, Tier: "m", CapacityUnits: 4},
				{CoordinatorURL: "http://coord3:8080", Active: true, Tier: "l", CapacityUnits: 8},
			},
			strategy: "capacity",
		},
		{
			name: "observed query load selection",
			backends: []Backend{
				{CoordinatorURL: "http://coord1:8080", Active: true},
				{CoordinatorURL: "http://coord2:8080", Active: true},
			},
			strategy: "observed-load",
		},
		{
			name: "no healthy backends but active ones exist (fail-open)",
			backends: []Backend{
				{Name: "coord1", CoordinatorURL: "http://coord1:8080", Active: true},
				{Name: "coord2", CoordinatorURL: "http://coord2:8080", Active: true},
			},
			strategy: "default",
		},
		{
			name:     "empty backends",
			backends: []Backend{},
			strategy: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &RouteEntry{
				RoutingGroup: "test",
				Backends:     tt.backends,
			}

			gs, _ := createTestGatewayService(t, []RouteEntry{*route})

			// For observed-load strategy, seed the activity tracker as if proxied Trino responses were seen.
			if tt.strategy == "observed-load" && len(tt.backends) > 0 {
				for i := 0; i < 10; i++ {
					gs.queryActivity.Observe("backend-1-running-"+strconv.Itoa(i), "team-a", "coord1", "test", tt.backends[0].CoordinatorURL, "RUNNING")
				}
				for i := 0; i < 5; i++ {
					gs.queryActivity.Observe("backend-1-queued-"+strconv.Itoa(i), "team-a", "coord1", "test", tt.backends[0].CoordinatorURL, "QUEUED")
				}
				for i := 0; i < 2; i++ {
					gs.queryActivity.Observe("backend-2-running-"+strconv.Itoa(i), "team-a", "coord2", "test", tt.backends[1].CoordinatorURL, "RUNNING")
				}
				gs.queryActivity.Observe("backend-2-queued", "team-a", "coord2", "test", tt.backends[1].CoordinatorURL, "QUEUED")
			}

			// Test multiple selections to ensure it works
			selected := make(map[string]int)
			for i := 0; i < 100; i++ {
				backend := gs.selectBackend(route)
				if backend == nil {
					if len(tt.backends) == 0 {
						continue // Expected for empty backends
					}
					t.Fatal("Expected backend to be selected")
				}
				selected[backend.CoordinatorURL]++
			}

			// With observed load data, the lower-load backend should be selected deterministically.
			if tt.strategy == "observed-load" && len(tt.backends) >= 2 {
				if selected[tt.backends[1].CoordinatorURL] < selected[tt.backends[0].CoordinatorURL] {
					t.Fatalf("expected lower-load backend to be selected at least as often: coord2=%d coord1=%d",
						selected[tt.backends[1].CoordinatorURL], selected[tt.backends[0].CoordinatorURL])
				}
			}
		})
	}
}

func TestStickyQueryContinuesToDrainingBackend(t *testing.T) {
	queryID := "20250115_123456_00001_abc12"
	var backendHits int32
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&backendHits, 1)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("sticky-ok")); err != nil {
			t.Errorf("failed to write sticky response: %v", err)
		}
	}))
	defer backendServer.Close()

	routes := []RouteEntry{
		{
			Name:         "runtime-a",
			RoutingGroup: "runtime-a",
			Header:       "runtime-a",
			Backends: []Backend{
				{
					Name:           "runtime-a",
					Namespace:      "team-a",
					CoordinatorURL: backendServer.URL,
					State:          StateDraining,
					Active:         false,
				},
			},
		},
	}
	gs, _ := createTestGatewayService(t, routes)
	requireStickySet(t, gs, queryID, "team-a", "runtime-a", backendServer.URL, "runtime-a")

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/statement/executing/"+queryID+"/0",
		http.NoBody,
	)
	req.Header.Set("X-Trino-XTrinode", "runtime-a")
	rec := httptest.NewRecorder()

	gs.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected sticky request to reach draining backend, got status %d body %q", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&backendHits) != 1 {
		t.Fatalf("expected draining backend to be hit once, got %d", backendHits)
	}
}

func TestStickyQueryInvalidatesUnhealthyBackendAndReselects(t *testing.T) {
	queryID := "20250115_123456_00001_abc12"
	var unhealthyHits int32
	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&unhealthyHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer unhealthyServer.Close()

	var healthyHits int32
	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&healthyHits, 1)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("healthy-ok")); err != nil {
			t.Errorf("failed to write healthy response: %v", err)
		}
	}))
	defer healthyServer.Close()

	routes := []RouteEntry{{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Header:       "runtime-a",
		Backends: []Backend{
			{Name: "bad", Namespace: "team-a", CoordinatorURL: unhealthyServer.URL, State: StateRunning, Active: true},
			{Name: "good", Namespace: "team-a", CoordinatorURL: healthyServer.URL, State: StateRunning, Active: true},
		},
	}}
	gs, _ := createTestGatewayService(t, routes)
	for i := 0; i < 3; i++ {
		gs.healthChecker.recordFailure(unhealthyServer.URL, http.StatusInternalServerError, "test failure")
	}
	requireStickySet(t, gs, queryID, "team-a", "bad", unhealthyServer.URL, "runtime-a")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/query/"+queryID, http.NoBody)
	req.Header.Set("X-Trino-XTrinode", "runtime-a")
	rec := httptest.NewRecorder()

	gs.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected reselection to healthy backend, got status %d body %q", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&unhealthyHits) != 0 {
		t.Fatalf("expected unhealthy sticky backend to be skipped, got %d hits", unhealthyHits)
	}
	if atomic.LoadInt32(&healthyHits) != 1 {
		t.Fatalf("expected healthy backend to be hit once, got %d", healthyHits)
	}

	_, name, backendURL, _, found := gs.stickyClient.Get(context.Background(), queryID)
	if !found || name != "good" || backendURL != healthyServer.URL {
		t.Fatalf("expected sticky route to be rewritten to healthy backend, found=%v name=%q backend=%q", found, name, backendURL)
	}
}

func TestStickyQueryInvalidatesOpenCircuitBackendAndReselects(t *testing.T) {
	queryID := "20250115_123456_00002_abc12"
	var openCircuitHits int32
	openCircuitServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&openCircuitHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer openCircuitServer.Close()

	var selectableHits int32
	selectableServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&selectableHits, 1)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("selectable-ok")); err != nil {
			t.Errorf("failed to write selectable response: %v", err)
		}
	}))
	defer selectableServer.Close()

	routes := []RouteEntry{{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Header:       "runtime-a",
		Backends: []Backend{
			{Name: "open-circuit", Namespace: "team-a", CoordinatorURL: openCircuitServer.URL, State: StateRunning, Active: true},
			{Name: "selectable", Namespace: "team-a", CoordinatorURL: selectableServer.URL, State: StateRunning, Active: true},
		},
	}}
	gs, _ := createTestGatewayService(t, routes)
	breaker := gs.circuitBreaker.GetOrCreateBreaker(openCircuitServer.URL, 5, 2, 30*time.Second)
	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}
	requireStickySet(t, gs, queryID, "team-a", "open-circuit", openCircuitServer.URL, "runtime-a")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/query/"+queryID, http.NoBody)
	req.Header.Set("X-Trino-XTrinode", "runtime-a")
	rec := httptest.NewRecorder()

	gs.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected reselection around open circuit, got status %d body %q", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&openCircuitHits) != 0 {
		t.Fatalf("expected open-circuit sticky backend to be skipped, got %d hits", openCircuitHits)
	}
	if atomic.LoadInt32(&selectableHits) != 1 {
		t.Fatalf("expected selectable backend to be hit once, got %d", selectableHits)
	}
}

func TestSelectBackendDoesNotFailOpenSleepingBackend(t *testing.T) {
	sleepingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	sleepingServer.Close()

	route := &RouteEntry{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Header:       "runtime-a",
		Backends: []Backend{
			{
				Name:           "runtime-a",
				Namespace:      "team-a",
				CoordinatorURL: sleepingServer.URL,
				State:          StateRunning,
				Active:         true,
			},
		},
	}
	gs, _ := createTestGatewayService(t, []RouteEntry{*route})
	gs.healthChecker.recordSleeping(route.Backends[0].CoordinatorURL, "dial tcp: connection refused")

	if backend := gs.selectBackend(route); backend != nil {
		t.Fatalf("expected sleeping backend not to be selected via fail-open, got %s", backend.CoordinatorURL)
	}

	candidate := gs.pickResumeCandidate(route)
	if candidate == nil {
		t.Fatal("expected sleeping running backend to be a stale-route resume candidate")
	}
	if candidate.Name != "runtime-a" || candidate.Namespace != "team-a" {
		t.Fatalf("expected team-a/runtime-a candidate, got %s/%s", candidate.Namespace, candidate.Name)
	}
}

func TestSelectBackendProbesSleepingRunningBackendBeforeResume(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/info" {
			t.Fatalf("unexpected probe path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backendServer.Close()

	route := &RouteEntry{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Header:       "runtime-a",
		Backends: []Backend{
			{
				Name:           "runtime-a",
				Namespace:      "team-a",
				CoordinatorURL: backendServer.URL,
				State:          StateRunning,
				Active:         true,
			},
		},
	}
	gs, _ := createTestGatewayService(t, []RouteEntry{*route})
	gs.healthChecker.recordSleeping(backendServer.URL, "dial tcp: connection refused")

	backend := gs.selectBackend(route)
	if backend == nil {
		t.Fatal("expected sleeping RUNNING backend to be reprobed and selected")
	}
	if backend.CoordinatorURL != backendServer.URL {
		t.Fatalf("expected backend %s, got %s", backendServer.URL, backend.CoordinatorURL)
	}
	if got := gs.healthChecker.GetState(backendServer.URL); got != HealthStateHealthy {
		t.Fatalf("expected probe to mark backend healthy, got %s", got)
	}
}

func TestSleepingRunningBackendTriggersResumeWithoutProxying(t *testing.T) {
	var resumeCalls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/resume") {
			t.Errorf("unexpected API server request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&resumeCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"triggered":  true,
			"gated":      false,
			"retryAfter": 5,
		}); err != nil {
			t.Errorf("failed to write API response: %v", err)
		}
	}))
	defer apiServer.Close()

	route := RouteEntry{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Header:       "runtime-a",
		Backends: []Backend{
			{
				Name:           "runtime-a",
				Namespace:      "team-a",
				CoordinatorURL: "http://trino-runtime-a.team-a.svc.cluster.local:8080",
				State:          StateRunning,
				Active:         true,
			},
		},
	}
	gs, _ := createTestGatewayService(t, []RouteEntry{route})
	gs.apiServerClient = NewAPIServerClient(apiServer.URL, 5*time.Second, logr.Discard())
	gs.healthChecker.recordSleeping(route.Backends[0].CoordinatorURL, "dial tcp: i/o timeout")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/statement", strings.NewReader("SELECT 1"))
	req.Header.Set("X-Trino-XTrinode", "runtime-a")
	req.Header.Set("X-Trino-User", "local-e2e")
	rec := httptest.NewRecorder()

	gs.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected controlled resume 503, got status %d body %q", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&resumeCalls) != 1 {
		t.Fatalf("expected one resume call, got %d", resumeCalls)
	}
	if !strings.Contains(rec.Body.String(), "XTrinode runtime is resuming") {
		t.Fatalf("expected resume response body, got %q", rec.Body.String())
	}
}

func TestSelectBackendRejectsDrainingForNewQueries(t *testing.T) {
	route := &RouteEntry{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Backends: []Backend{
			{
				Name:           "runtime-a",
				Namespace:      "team-a",
				CoordinatorURL: "http://coordinator:8080",
				State:          StateDraining,
				Active:         false,
			},
		},
	}
	gs, _ := createTestGatewayService(t, []RouteEntry{*route})

	if backend := gs.selectBackend(route); backend != nil {
		t.Fatalf("expected no backend for new query selection, got %s", backend.CoordinatorURL)
	}
}

func TestCollectHealthCheckBackendURLsIncludesDefaultRoute(t *testing.T) {
	backendURL := "http://default-backend:8080"
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:    "default",
			Default: true,
			Backends: []Backend{
				{
					CoordinatorURL: backendURL,
					Active:         true,
					State:          StateRunning,
				},
			},
		},
	})

	urls := gs.collectHealthCheckBackendURLs()
	if len(urls) != 1 {
		t.Fatalf("expected one backend URL, got %d: %v", len(urls), urls)
	}
	if urls[0] != backendURL {
		t.Fatalf("expected %q, got %q", backendURL, urls[0])
	}
}

func TestHandleHealth(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", http.NoBody)
	rec := httptest.NewRecorder()

	gs.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "OK" {
		t.Fatalf("expected health body OK, got %q", rec.Body.String())
	}
}

func TestGatewayServiceStartReturnsInitialLoadError(t *testing.T) {
	cli := fake.NewClientBuilder().Build()
	gs, err := NewGatewayService(cli, newMockLogger(), "http://api-server:8081/api/v1", nil)
	if err != nil {
		t.Fatalf("failed to create gateway service: %v", err)
	}

	err = gs.Start(context.Background())
	if err == nil {
		t.Fatal("expected missing initial routes ConfigMap to fail startup")
	}
	if !strings.Contains(err.Error(), "failed to load initial routes") {
		t.Fatalf("unexpected startup error: %v", err)
	}
}

func TestGatewayServiceWatchRoutesReturnsWhenContextCanceled(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		gs.watchRoutes(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchRoutes did not return after context cancellation")
	}
}

func TestGatewayServiceStartHealthCheckerReturnsWhenContextCanceled(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		gs.startHealthChecker(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startHealthChecker did not return after context cancellation")
	}
}

func Test503DetectionAndAutoResume(t *testing.T) {
	var apiServerCalled int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/resume") {
			atomic.StoreInt32(&apiServerCalled, 1)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]bool{"triggered": true})
		}
	}))
	defer apiServer.Close()

	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.apiServerClient = NewAPIServerClient(apiServer.URL, 5*time.Second, logr.Discard())

	// Create a response with 503 status
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/statement", http.NoBody)
	req.Header.Set("X-Trino-XTrinode-Name", "test-dummy")
	req.Header.Set("X-Trino-XTrinode-Namespace", "team-a")

	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     make(http.Header),
		Request:    req,
		Body:       io.NopCloser(strings.NewReader("Service Unavailable")),
	}

	// Call modifyResponse
	err := gs.modifyResponse(resp)
	if err != nil {
		t.Fatalf("modifyResponse failed: %v", err)
	}

	// Check Retry-After header
	// All 503s get 30s retry-after (we cannot reliably distinguish overload vs paused)
	// Auto-resume is triggered by connection errors, not HTTP 503 responses
	if resp.Header.Get("Retry-After") != "30" {
		t.Errorf("Expected Retry-After header to be '30', got '%s'", resp.Header.Get("Retry-After"))
	}

	// Wait a bit to ensure no async resume call
	time.Sleep(100 * time.Millisecond)

	// API server should not be called for overload 503
	if atomic.LoadInt32(&apiServerCalled) != 0 {
		t.Error("Expected API server NOT to be called for overload 503")
	}
}

func TestBackendLoadTrackingFromQueryActivity(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	backendURL := "http://coordinator:8080"

	gs.queryActivity.Observe("running-1", "team-a", "dummy", "dummy", backendURL, "RUNNING")
	gs.queryActivity.Observe("running-2", "team-a", "dummy", "dummy", backendURL, "RUNNING")
	gs.queryActivity.Observe("queued-1", "team-a", "dummy", "dummy", backendURL, "QUEUED")

	load := gs.queryActivity.BackendLoads()[backendURL]

	if load.RunningQueries != 2 {
		t.Errorf("Expected 2 running queries, got %d", load.RunningQueries)
	}
	if load.QueuedQueries != 1 {
		t.Errorf("Expected 1 queued query, got %d", load.QueuedQueries)
	}
}

func TestPickResumeCandidate(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	route := &RouteEntry{
		Name:         "pool",
		RoutingGroup: "pool",
		Backends: []Backend{
			{Name: "large-resuming", Namespace: "team-a", State: StateResuming, Active: true, CapacityUnits: 8},
			{Name: "small-paused-b", Namespace: "team-b", State: StatePaused, Active: true, CapacityUnits: 2},
			{Name: "small-paused-a", Namespace: "team-a", State: StatePaused, Active: true, CapacityUnits: 2},
			{Name: "running", Namespace: "team-a", State: StateRunning, Active: true, CapacityUnits: 1},
			{Name: "inactive-paused", Namespace: "team-a", State: StatePaused, Active: false, CapacityUnits: 1},
		},
	}

	candidate := gs.pickResumeCandidate(route)
	if candidate == nil {
		t.Fatal("expected resume candidate")
	}
	if candidate.Name != "small-paused-a" || candidate.Namespace != "team-a" {
		t.Fatalf("expected deterministic smallest paused candidate, got %s/%s", candidate.Namespace, candidate.Name)
	}
}

func TestPickResumeCandidate_NoResumableBackends(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	route := &RouteEntry{
		Name:         "running-only",
		RoutingGroup: "running-only",
		Backends: []Backend{
			{Name: "running", Namespace: "team-a", State: StateRunning, Active: true},
			{Name: "draining", Namespace: "team-a", State: StateDraining, Active: true},
		},
	}

	if candidate := gs.pickResumeCandidate(route); candidate != nil {
		t.Fatalf("expected no resume candidate, got %s/%s", candidate.Namespace, candidate.Name)
	}
}

func TestDirector(t *testing.T) {
	routes := []RouteEntry{
		{
			Name:         "dummy",
			RoutingGroup: "dummy",
			Backends: []Backend{
				{Name: "dummy", Namespace: "team-a", CoordinatorURL: "http://trino-dummy.team-a.svc.cluster.local:8080", Active: true},
			},
			Header: "dummy",
		},
	}

	gs, _ := createTestGatewayService(t, routes)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/v1/statement", strings.NewReader("SELECT 1"))
	req.Header.Set("X-Trino-XTrinode", "dummy")
	req.Host = "gateway:8080" // Set req.Host directly, not via header

	// Set backend selection results in context (as handleRequest would do)
	ctx := req.Context()
	ctx = context.WithValue(ctx, ctxRoute, &routes[0])
	ctx = context.WithValue(ctx, ctxBackendURL, "http://trino-dummy.team-a.svc.cluster.local:8080")
	ctx = context.WithValue(ctx, ctxXTrinodeName, "dummy")
	ctx = context.WithValue(ctx, ctxNamespace, "team-a")
	req = req.WithContext(ctx)

	gs.director(req)

	// Check URL was rewritten
	if req.URL.Host != "trino-dummy.team-a.svc.cluster.local:8080" {
		t.Errorf("Expected host 'trino-dummy.team-a.svc.cluster.local:8080', got '%s'", req.URL.Host)
	}
	if req.URL.Scheme != "http" {
		t.Errorf("Expected scheme 'http', got '%s'", req.URL.Scheme)
	}

	// Check headers
	if req.Header.Get("X-Trino-Route-Name") != "dummy" {
		t.Error("Expected X-Trino-Route-Name header")
	}
	if req.Header.Get("X-Trino-XTrinode-Name") != "dummy" {
		t.Errorf("Expected X-Trino-XTrinode-Name header to be 'dummy', got '%s'", req.Header.Get("X-Trino-XTrinode-Name"))
	}
	if req.Header.Get("X-Trino-XTrinode-Namespace") != "team-a" {
		t.Errorf("Expected X-Trino-XTrinode-Namespace header to be 'team-a', got '%s'", req.Header.Get("X-Trino-XTrinode-Namespace"))
	}
	if req.Header.Get("X-Forwarded-Host") != "gateway:8080" {
		t.Error("Expected X-Forwarded-Host header")
	}
}

func TestHandleRequest(t *testing.T) {
	routes := []RouteEntry{
		{
			Name:         "dummy",
			RoutingGroup: "dummy",
			Backends: []Backend{
				{
					Name:           "dummy",
					Namespace:      "default",
					CoordinatorURL: "http://coordinator:8080",
					Active:         true,
					Tier:           "m",
					CapacityUnits:  4,
				},
			},
			Header: "dummy",
		},
	}

	gs, _ := createTestGatewayService(t, routes)

	// Create a mock coordinator server
	coordinatorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			t.Errorf("failed to write coordinator response: %v", err)
		}
	}))
	defer coordinatorServer.Close()

	// Update backend URL to point to test server
	gs.routesLock.Lock()
	routes[0].Backends[0].CoordinatorURL = coordinatorServer.URL
	gs.routes["hdr:dummy"] = &routes[0] // Use prefixed key for header routing
	gs.routesLock.Unlock()

	// Test request with valid route
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/info", http.NoBody)
	req.Host = "" // Clear default host to avoid hostname-based routing
	req.Header.Set("X-Trino-XTrinode", "dummy")
	w := httptest.NewRecorder()

	gs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Test request without route
	req2 := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/info", http.NoBody)
	w2 := httptest.NewRecorder()

	gs.handleRequest(w2, req2)

	if w2.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w2.Code)
	}
}

func TestQueryIdCachingFromResponse(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "dummy",
			RoutingGroup: "dummy",
			Backends: []Backend{
				{CoordinatorURL: "http://coordinator:8080", Active: true},
			},
		},
	})

	// Create response with query ID
	req := &http.Request{
		Header: make(http.Header),
	}
	req.Header.Set("X-Trino-Backend-URL", "http://coordinator:8080")
	req.Header.Set("X-Trino-XTrinode-Name", "dummy")
	req.Header.Set("X-Trino-XTrinode-Namespace", "team-a")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Request: req,
		Body:    io.NopCloser(bytes.NewReader([]byte(`{"id": "20250115_123456_00001_abc12", "state": "RUNNING"}`))),
	}

	// Call modifyResponse
	err := gs.modifyResponse(resp)
	if err != nil {
		t.Fatalf("modifyResponse failed: %v", err)
	}

	ns, name, backendURL, _, found := gs.stickyClient.Get(context.Background(), "20250115_123456_00001_abc12")
	if !found {
		t.Fatal("expected query ID to be stored")
	}
	if ns != "team-a" || name != "dummy" || backendURL != "http://coordinator:8080" {
		t.Fatalf("unexpected sticky route: namespace=%q name=%q backendURL=%q", ns, name, backendURL)
	}
}

func TestLoadRoutes(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	log := newMockLogger()

	gs, err := NewGatewayService(cli, log, "http://api-server:8081/api/v1", nil)
	if err != nil {
		t.Fatalf("Failed to create gateway service: %v", err)
	}

	// Create ConfigMap with routes
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GatewayConfigMapName,
			Namespace: GatewayConfigMapNamespace,
		},
		Data: map[string]string{
			GatewayConfigMapKey: `routes:
  - name: dummy
    routingGroup: dummy
    backends:
      - name: dummy
        namespace: default
        coordinatorURL: http://coordinator:8080
        active: true
    header: dummy
`,
		},
	}

	if err := cli.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Load routes
	if err := gs.loadRoutes(ctx); err != nil {
		t.Fatalf("loadRoutes failed: %v", err)
	}

	// Verify route was loaded (use prefixed key)
	gs.routesLock.RLock()
	route, exists := gs.routes["rg:dummy"]
	gs.routesLock.RUnlock()

	if !exists {
		t.Fatal("Expected route 'dummy' to be loaded")
	}
	if route.Name != "dummy" {
		t.Errorf("Expected route name 'dummy', got '%s'", route.Name)
	}
	if len(route.Backends) != 1 {
		t.Errorf("Expected 1 backend, got %d", len(route.Backends))
	}
}

func TestLoadRoutesClearsStaleSleepingHealthForRunningBackends(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	log := newMockLogger()

	gs, err := NewGatewayService(cli, log, "http://api-server:8081/api/v1", nil)
	if err != nil {
		t.Fatalf("Failed to create gateway service: %v", err)
	}

	const backendURL = "http://coordinator:8080"
	gs.healthChecker.recordSleeping(backendURL, "connection refused")
	if gs.healthChecker.IsHealthy(backendURL) {
		t.Fatal("expected sleeping backend to be blocked before route reload")
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GatewayConfigMapName,
			Namespace: GatewayConfigMapNamespace,
		},
		Data: map[string]string{
			GatewayConfigMapKey: `routes:
  - name: dummy
    routingGroup: dummy
    backends:
      - name: dummy
        namespace: default
        coordinatorURL: http://coordinator:8080
        active: true
        state: RUNNING
    header: dummy
`,
		},
	}
	if err := cli.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	if err := gs.loadRoutes(ctx); err != nil {
		t.Fatalf("loadRoutes failed: %v", err)
	}

	if !gs.healthChecker.IsHealthy(backendURL) {
		t.Fatal("expected route reload to clear stale sleeping backend health")
	}
	if got := gs.healthChecker.GetState(backendURL); got != HealthStateUnknown {
		t.Fatalf("expected reset backend state to be unknown, got %s", got)
	}
}

func TestFindRoute_NoFallbackOnExplicitSelector(t *testing.T) {
	// Test Snowflake semantics: explicit selector should not fall back to default
	routes := []RouteEntry{
		{
			Name:         "default-route",
			RoutingGroup: "default",
			Backends: []Backend{
				{
					Name:           "default-backend",
					Namespace:      "default",
					CoordinatorURL: "http://default:8080",
					Active:         true,
					Tier:           "m",
					CapacityUnits:  4,
				},
			},
			Default: true,
		},
		{
			Name:         "runtime-a",
			RoutingGroup: "runtime-a",
			Backends: []Backend{
				{
					Name:           "runtime-a",
					Namespace:      "default",
					CoordinatorURL: "http://runtime-a:8080",
					Active:         true,
					Tier:           "m",
					CapacityUnits:  4,
				},
			},
			Hostname: "runtime-a.example.com",
		},
	}

	gs, _ := createTestGatewayService(t, routes)

	// Test 1: Unknown hostname should fall through to default route
	// (Host header is always present in HTTP/1.1, so it's not an explicit selector)
	req1 := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/info", http.NoBody)
	req1.Host = "unknown-runtime.example.com"
	route1 := gs.findRoute(req1)
	if route1 == nil {
		t.Error("Expected default route for unknown hostname")
	} else if route1.Name != "default-route" {
		t.Errorf("Expected default-route for unknown hostname, got '%s'", route1.Name)
	}

	// Test 2: Unknown header should return nil (not default)
	req2 := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/info", http.NoBody)
	req2.Header.Set("X-Trino-XTrinode", "unknown-runtime")
	route2 := gs.findRoute(req2)
	if route2 != nil {
		t.Errorf("Expected nil for unknown header, got route '%s'", route2.Name)
	}

	// Test 3: No selector should return default
	// Note: httptest.NewRequest sets Host to "example.com" by default, so we need to clear it
	req3 := httptest.NewRequestWithContext(context.Background(), "GET", "http://example.com/v1/info", http.NoBody)
	req3.Host = "" // Clear host to ensure no hostname selector
	route3 := gs.findRoute(req3)
	if route3 == nil {
		t.Error("Expected default route when no selector provided")
	} else if route3.Name != "default-route" {
		t.Errorf("Expected default-route, got '%s'", route3.Name)
	}

	// Test 4: Known hostname should work
	req4 := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/info", http.NoBody)
	req4.Host = "runtime-a.example.com"
	route4 := gs.findRoute(req4)
	if route4 == nil {
		t.Error("Expected route for known hostname")
	} else if route4.Name != "runtime-a" {
		t.Errorf("Expected runtime-a, got '%s'", route4.Name)
	}
}

func TestDrainRoute(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()

	// Create a XTrinode
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "m",
			Routing: &analyticsv1.RoutingSpec{
				RoutingGroup: "test-runtime",
			},
		},
	}

	// Register the route first
	err := RegisterRoute(ctx, cli, xtrinode)
	if err != nil {
		t.Fatalf("RegisterRoute failed: %v", err)
	}

	// Verify backend is active
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: GatewayConfigMapName, Namespace: GatewayConfigMapNamespace}
	if getErr := cli.Get(ctx, key, configMap); getErr != nil {
		t.Fatalf("Failed to get ConfigMap: %v", getErr)
	}
	routes, _ := parseRoutes(configMap.Data[GatewayConfigMapKey])
	if len(routes) != 1 || len(routes[0].Backends) != 1 {
		t.Fatal("Expected 1 route with 1 backend")
	}
	if !routes[0].Backends[0].Active {
		t.Error("Expected backend to be active initially")
	}

	// Drain the route
	err = DrainRoute(ctx, cli, xtrinode)
	if err != nil {
		t.Fatalf("DrainRoute failed: %v", err)
	}

	// Verify backend is now inactive
	if getErr2 := cli.Get(ctx, key, configMap); getErr2 != nil {
		t.Fatalf("Failed to get ConfigMap after drain: %v", getErr2)
	}
	routes, _ = parseRoutes(configMap.Data[GatewayConfigMapKey])
	if len(routes) != 1 || len(routes[0].Backends) != 1 {
		t.Fatal("Expected route to still exist after drain")
	}
	if routes[0].Backends[0].Active {
		t.Error("Expected backend to be inactive after drain")
	}

	// Now deregister completely
	err = DeregisterRoute(ctx, cli, xtrinode)
	if err != nil {
		t.Fatalf("DeregisterRoute failed: %v", err)
	}

	// Verify route is removed
	if err := cli.Get(ctx, key, configMap); err != nil {
		t.Fatalf("Failed to get ConfigMap after deregister: %v", err)
	}
	routes, _ = parseRoutes(configMap.Data[GatewayConfigMapKey])
	if len(routes) != 0 {
		t.Errorf("Expected no routes after deregister, got %d", len(routes))
	}
}

func TestLoadRoutes_SkipsInvalidRoutes(t *testing.T) {
	ctx := context.Background()

	// Create ConfigMap with mix of valid and invalid routes
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GatewayConfigMapName,
			Namespace: GatewayConfigMapNamespace,
		},
		Data: map[string]string{
			GatewayConfigMapKey: `routes:
  - name: valid-route-1
    routingGroup: runtime-a
    backends:
      - name: runtime-a
        namespace: default
        coordinatorURL: http://runtime-a:8080
        active: true
        tier: m
        capacityUnits: 4
    hostname: runtime-a.example.com
  - name: invalid-duplicate-hostname
    routingGroup: runtime-b
    backends:
      - name: runtime-b
        namespace: default
        coordinatorURL: http://runtime-b:8080
        active: true
        tier: m
        capacityUnits: 4
    hostname: runtime-a.example.com
  - name: valid-route-2
    routingGroup: runtime-c
    backends:
      - name: runtime-c
        namespace: default
        coordinatorURL: http://runtime-c:8080
        active: true
        tier: m
        capacityUnits: 4
    header: runtime-c
  - name: invalid-duplicate-routing-group
    routingGroup: runtime-a
    backends:
      - name: runtime-a-2
        namespace: default
        coordinatorURL: http://runtime-a-2:8080
        active: true
        tier: m
        capacityUnits: 4
`,
		},
	}

	// Create fake client with ConfigMap already present
	cli := fake.NewClientBuilder().WithObjects(configMap).Build()

	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.client = cli // Set the client so loadRoutes can access ConfigMap

	// Load routes - should skip invalid ones
	if err := gs.loadRoutes(ctx); err != nil {
		t.Fatalf("loadRoutes failed: %v", err)
	}

	// Verify only valid routes were loaded
	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	// Debug: print all loaded routes
	t.Logf("Loaded routes: %d", len(gs.routes))
	for key := range gs.routes {
		t.Logf("  Route key: %s", key)
	}

	// Should have 2 valid routes (runtime-a and runtime-c)
	// runtime-a has both rg: and host: keys, runtime-c has both rg: and hdr: keys
	// So we expect 4 map entries total (2 routes × 2 keys each)
	if len(gs.routes) != 4 {
		t.Errorf("Expected 4 route map entries (2 routes with 2 keys each), got %d", len(gs.routes))
	}

	// Verify runtime-a is loaded
	if _, exists := gs.routes["rg:runtime-a"]; !exists {
		t.Error("Expected runtime-a to be loaded")
	}

	// Verify runtime-c is loaded
	if _, exists := gs.routes["rg:runtime-c"]; !exists {
		t.Error("Expected runtime-c to be loaded")
	}

	// Verify runtime-b (duplicate hostname) was skipped
	if _, exists := gs.routes["rg:runtime-b"]; exists {
		t.Error("Expected runtime-b to be skipped (duplicate hostname)")
	}
}

func TestLoadRoutes_KeepsLastGoodWhenAllParsedRoutesInvalid(t *testing.T) {
	ctx := context.Background()

	existingRoute := RouteEntry{
		Name:         "last-good",
		RoutingGroup: "last-good",
		Backends: []Backend{
			{Name: "last-good", Namespace: "team-a", CoordinatorURL: "http://last-good:8080", State: StateRunning, Active: true},
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GatewayConfigMapName,
			Namespace: GatewayConfigMapNamespace,
		},
		Data: map[string]string{
			GatewayConfigMapKey: `routes:
  - name: duplicate-a
    routingGroup: duplicate
    backends:
      - name: duplicate-a
        coordinatorURL: http://duplicate-a:8080
        state: RUNNING
        active: true
  - name: duplicate-b
    routingGroup: duplicate
    backends:
      - name: duplicate-b
        coordinatorURL: http://duplicate-b:8080
        state: RUNNING
        active: true
`,
		},
	}

	cli := fake.NewClientBuilder().WithObjects(configMap).Build()
	gs, _ := createTestGatewayService(t, []RouteEntry{existingRoute})
	gs.client = cli

	if err := gs.loadRoutes(ctx); err != nil {
		t.Fatalf("loadRoutes failed: %v", err)
	}

	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	if _, exists := gs.routes["rg:last-good"]; !exists {
		t.Error("Expected last-good route to be kept when all parsed routes are invalid")
	}
	if _, exists := gs.routes["rg:duplicate"]; exists {
		t.Error("Expected invalid duplicate route set not to replace last-good routes")
	}
}

func TestDedicatedRuntimeExclusivity_Unbypassable(t *testing.T) {
	// Test that dedicated runtime exclusivity cannot be bypassed
	route := &RouteEntry{
		Name:         "runtime-a",
		RoutingGroup: "runtime-a",
		Backends:     []Backend{},
	}

	// Test 1: Adding backend with matching name should succeed
	backend1 := Backend{
		Name:           "runtime-a",
		Namespace:      "default",
		CoordinatorURL: "http://runtime-a:8080",
		Active:         true,
		Tier:           "m",
		CapacityUnits:  4,
	}
	err := updateBackendInRouteWithMode(route, &backend1, "", "", false, true)
	if err != nil {
		t.Errorf("Expected success for matching backend name, got error: %v", err)
	}

	// Test 2: Trying to add a second backend with different name should fail
	// This is the bypass attempt - even if caller tries to add different backend
	backend2 := Backend{
		Name:           "runtime-b",
		Namespace:      "default",
		CoordinatorURL: "http://runtime-b:8080",
		Active:         true,
		Tier:           "m",
		CapacityUnits:  4,
	}
	err = updateBackendInRouteWithMode(route, &backend2, "", "", false, true)
	if err == nil {
		t.Error("Expected error when adding backend with non-matching name to dedicated runtime")
	}
	if !strings.Contains(err.Error(), "exclusivity violated") {
		t.Errorf("Expected specific error message, got: %v", err)
	}

	// Test 3: Verify route still has only one backend
	if len(route.Backends) != 1 {
		t.Errorf("Expected 1 backend, got %d", len(route.Backends))
	}
	if route.Backends[0].Name != "runtime-a" {
		t.Errorf("Expected backend name 'runtime-a', got '%s'", route.Backends[0].Name)
	}

	// Test 4: Trying to add second backend with same name but different namespace should fail
	backend3 := Backend{
		Name:           "runtime-a",
		Namespace:      "other-namespace",
		CoordinatorURL: "http://runtime-a-other:8080",
		Active:         true,
		Tier:           "m",
		CapacityUnits:  4,
	}
	err = updateBackendInRouteWithMode(route, &backend3, "", "", false, true)
	if err == nil {
		t.Error("Expected error when adding second backend to dedicated runtime")
	}
	if !strings.Contains(err.Error(), "exclusivity violated") {
		t.Errorf("Expected exclusivity violation error, got: %v", err)
	}
}
