package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/pkg/gateway/auth"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type staticAuthenticator struct {
	authenticated bool
	err           error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func (a staticAuthenticator) Authenticate(*http.Request) (*auth.Result, error) {
	if a.err != nil {
		return nil, a.err
	}
	return &auth.Result{Authenticated: a.authenticated, User: "test-user"}, nil
}

func TestGatewayStatusSnapshotDeduplicatesRoutesAndRedactsURLUserInfo(t *testing.T) {
	// #nosec G101 -- Deliberate fake URL userinfo for redaction coverage.
	backendURL := "http://user:secret@trino-runtime.team-a.svc.cluster.local:8080"
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
			Hostname:     "runtime-a.example.com",
			Header:       "runtime-a",
			Backends: []Backend{
				{
					Name:                "runtime-a",
					Namespace:           "team-a",
					CoordinatorURL:      backendURL,
					State:               StateRunning,
					Active:              true,
					Tier:                "m",
					CapacityUnits:       4,
					RuntimeShapeVersion: analyticsv1.ObservedRuntimeShapeStatusVersion,
					RuntimeShapeHash:    "shape-123",
					ObservedGeneration:  9,
				},
			},
		},
	})
	gs.healthChecker.recordSuccess(backendURL)
	breaker := gs.circuitBreaker.GetOrCreateBreaker(backendURL, 1, 1, time.Minute)
	breaker.RecordFailure()

	status := gs.gatewayStatusSnapshot(context.Background())

	if len(status.Routes) != 1 {
		t.Fatalf("expected one unique route, got %d", len(status.Routes))
	}
	if status.Summary.Routes != 1 || status.Summary.Backends != 1 {
		t.Fatalf("unexpected summary: %+v", status.Summary)
	}
	if status.Summary.OpenCircuitBackends != 1 {
		t.Fatalf("expected one open circuit, got %d", status.Summary.OpenCircuitBackends)
	}
	backend := status.Routes[0].Backends[0]
	if strings.Contains(backend.CoordinatorURL, "secret") || strings.Contains(backend.CoordinatorURL, "user@") {
		t.Fatalf("coordinator URL was not redacted: %q", backend.CoordinatorURL)
	}
	if backend.CoordinatorURL != "http://trino-runtime.team-a.svc.cluster.local:8080" {
		t.Fatalf("unexpected redacted coordinator URL: %q", backend.CoordinatorURL)
	}
	if backend.Health.State != HealthStateHealthy {
		t.Fatalf("expected healthy backend, got %q", backend.Health.State)
	}
	if backend.CircuitBreaker.State != CircuitOpen {
		t.Fatalf("expected open circuit, got %q", backend.CircuitBreaker.State)
	}
	if backend.TrinoUIPath != "/ui/team-a/runtime-a/" {
		t.Fatalf("unexpected Trino UI path: %q", backend.TrinoUIPath)
	}
	if backend.RuntimeShapeVersion != analyticsv1.ObservedRuntimeShapeStatusVersion || backend.RuntimeShapeHash != "shape-123" || backend.ObservedGeneration != 9 {
		t.Fatalf("runtime shape diagnostics were not copied: %+v", backend)
	}
}

func TestGatewayStatusSnapshotIncludesLifecycleFromXTrinode(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := analyticsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register XTrinode scheme: %v", err)
	}

	autoSuspendAfter := metav1.Duration{Duration: 30 * time.Minute}
	lastActivity := metav1.NewTime(time.Date(2026, 5, 19, 17, 56, 31, 0, time.UTC))
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "runtime-a",
			Namespace:         "team-a",
			CreationTimestamp: metav1.NewTime(lastActivity.Add(-time.Hour)),
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &lastActivity,
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(xtrinode).Build()
	gs, err := NewGatewayService(cli, newMockLogger(), "http://api-server:8081/api/v1", nil)
	if err != nil {
		t.Fatalf("failed to create gateway service: %v", err)
	}
	setGatewayStatusRoutes(gs, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
	})

	status := gs.gatewayStatusSnapshot(context.Background())

	lifecycle := status.Routes[0].Backends[0].Lifecycle
	if lifecycle == nil {
		t.Fatal("expected backend lifecycle status")
	}
	if lifecycle.AutoSuspendAfter != "30m0s" {
		t.Fatalf("unexpected auto suspend duration: %q", lifecycle.AutoSuspendAfter)
	}
	if lifecycle.LastActivity == nil || !lifecycle.LastActivity.Equal(lastActivity.Time) {
		t.Fatalf("unexpected last activity: %v", lifecycle.LastActivity)
	}
	expectedSuspendAt := lastActivity.Add(30 * time.Minute)
	if lifecycle.SuspendAt == nil || !lifecycle.SuspendAt.Equal(expectedSuspendAt) {
		t.Fatalf("unexpected suspend at: %v", lifecycle.SuspendAt)
	}
}

func setGatewayStatusRoutes(gs *GatewayService, routes []RouteEntry) {
	gs.routesLock.Lock()
	defer gs.routesLock.Unlock()

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
}

func TestGatewayStatusAPIIsHiddenWhenUIDisabled(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayStatusAPIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when UI is disabled, got %d", rec.Code)
	}
}

func TestGatewayUIFlagDisabledStillAllowsQueryRouting(t *testing.T) {
	var seenPath string
	const backendURL = "http://trino-runtime-a.team-a.svc.cluster.local:8080"

	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "runtime-a",
			RoutingGroup: "runtime-a",
			Header:       "runtime-a",
			Backends: []Backend{
				{
					Name:           "runtime-a",
					Namespace:      "team-a",
					CoordinatorURL: backendURL,
					State:          StateRunning,
					Active:         true,
				},
			},
		},
	})
	gs.proxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenPath = r.URL.Path
		if got := r.URL.Scheme + "://" + r.URL.Host; got != backendURL {
			t.Fatalf("expected request to target backend URL %q, got %q", backendURL, got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("query-ok")),
			Request:    r,
		}, nil
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/statement", strings.NewReader("SELECT 1"))
	req.Header.Set("X-Trino-XTrinode", "runtime-a")
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected query routing to work with UI disabled, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "query-ok" {
		t.Fatalf("unexpected backend response: %q", rec.Body.String())
	}
	if seenPath != "/v1/statement" {
		t.Fatalf("expected request path to reach backend, got %q", seenPath)
	}
}

func TestGatewayUIRewritesTrinoQueryInfoURIToBackendUIPath(t *testing.T) {
	const backendURL = "http://trino-runtime-a.team-a.svc.cluster.local:8080"
	const queryID = "20260520_101112_00000_abc12"

	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "runtime-a",
			RoutingGroup: "runtime-a",
			Header:       "runtime-a",
			Backends: []Backend{
				{
					Name:           "runtime-a",
					Namespace:      "team-a",
					CoordinatorURL: backendURL,
					State:          StateRunning,
					Active:         true,
				},
			},
		},
	})
	gs.proxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"id":"` + queryID + `","infoUri":"http://gateway.example/ui/query.html?` + queryID + `","nextUri":"http://gateway.example/v1/statement/queued/` + queryID + `/token/1","stats":{"state":"QUEUED"},"data":[[1]]}`)),
			Request: r,
		}, nil
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://gateway.example/v1/statement", strings.NewReader("SELECT 1"))
	req.Header.Set("X-Trino-XTrinode", "runtime-a")
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected query routing to work, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	wantInfoURI := "http://gateway.example/ui/team-a/runtime-a/query.html?" + queryID
	if body["infoUri"] != wantInfoURI {
		t.Fatalf("expected rewritten infoUri %q, got %q", wantInfoURI, body["infoUri"])
	}
	wantNextURI := "http://gateway.example/v1/statement/queued/" + queryID + "/token/1"
	if body["nextUri"] != wantNextURI {
		t.Fatalf("expected nextUri to remain %q, got %q", wantNextURI, body["nextUri"])
	}
}

func TestGatewayStatusAPIRequiresAuthenticatorWhenConfigured(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: true}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayStatusAPIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when auth is required but unavailable, got %d", rec.Code)
	}
}

func TestGatewayStatusAPIUsesAuthMiddleware(t *testing.T) {
	gs, err := NewGatewayService(nil, newMockLogger(), "http://api-server:8081/api/v1", staticAuthenticator{authenticated: false})
	if err != nil {
		t.Fatalf("failed to create gateway service: %v", err)
	}
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: true}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayStatusAPIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for failed auth, got %d", rec.Code)
	}
}

func TestGatewayStatusAPIAuthError(t *testing.T) {
	gs, err := NewGatewayService(nil, newMockLogger(), "http://api-server:8081/api/v1", staticAuthenticator{err: errors.New("auth backend down")})
	if err != nil {
		t.Fatalf("failed to create gateway service: %v", err)
	}
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: true}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayStatusAPIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for auth error, got %d", rec.Code)
	}
}

func TestGatewayStatusAPIEnabledWithoutRequiredAuth(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "default",
			RoutingGroup: "default",
			Default:      true,
			Backends: []Backend{
				{
					Name:           "runtime",
					Namespace:      "team-a",
					CoordinatorURL: "http://trino-runtime.team-a.svc.cluster.local:8080",
					State:          StatePaused,
					Active:         false,
				},
			},
		},
	})
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: false}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayStatusAPIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response GatewayStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	if response.Summary.PausedBackends != 1 {
		t.Fatalf("expected one paused backend, got %+v", response.Summary)
	}
}

func TestGatewayUIServesStaticShellWhenEnabled(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: false}
	mux := gs.buildMux()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayUIPath+"/", http.NoBody)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("expected CSP without unsafe-inline, got %q", csp)
	}
	if !strings.Contains(rec.Body.String(), "XTrinode Gateway") {
		t.Fatalf("expected gateway UI shell, got %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `href="app.css"`) {
		t.Fatalf("expected gateway UI shell to load app.css")
	}
	if !strings.Contains(rec.Body.String(), `src="app.js"`) {
		t.Fatalf("expected gateway UI shell to load app.js")
	}
	if !strings.Contains(rec.Body.String(), "Backend details") {
		t.Fatalf("expected backend detail drawer in gateway UI shell")
	}
	if !strings.Contains(rec.Body.String(), "Query Examples") {
		t.Fatalf("expected query examples in gateway UI shell")
	}
	if !strings.Contains(rec.Body.String(), "Lifecycle") {
		t.Fatalf("expected lifecycle section in gateway UI shell")
	}

	scriptReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayUIPath+"/app.js", http.NoBody)
	scriptRec := httptest.NewRecorder()
	mux.ServeHTTP(scriptRec, scriptReq)
	if scriptRec.Code != http.StatusOK {
		t.Fatalf("expected app.js 200, got %d", scriptRec.Code)
	}
	if !strings.Contains(scriptRec.Body.String(), "Selectable for new queries") {
		t.Fatalf("expected selectability wording in gateway UI script")
	}
	if !strings.Contains(scriptRec.Body.String(), "Route active") {
		t.Fatalf("expected route active wording in gateway UI script")
	}
	if !strings.Contains(scriptRec.Body.String(), "Gateway authentication is enabled") {
		t.Fatalf("expected auth omission hint in gateway UI script")
	}
	if !strings.Contains(scriptRec.Body.String(), "X-Trino-XTrinode") {
		t.Fatalf("expected header-based query examples in gateway UI shell")
	}
	if !strings.Contains(scriptRec.Body.String(), "Suspend ETA") {
		t.Fatalf("expected suspend ETA wording in gateway UI script")
	}
	if !strings.Contains(scriptRec.Body.String(), "Open Trino UI") {
		t.Fatalf("expected Trino UI link wording in gateway UI script")
	}

	styleReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, GatewayUIPath+"/app.css", http.NoBody)
	styleRec := httptest.NewRecorder()
	mux.ServeHTTP(styleRec, styleReq)
	if styleRec.Code != http.StatusOK {
		t.Fatalf("expected app.css 200, got %d", styleRec.Code)
	}
	if !strings.Contains(styleRec.Body.String(), ".hint") {
		t.Fatalf("expected auth hint styling in gateway UI stylesheet")
	}
}

func TestGatewayUIDoesNotRegisterOldXTrinodePath(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{})
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: false}

	for _, path := range []string{"/_xtrinode/", "/_xtrinode/api/gateway/status"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()

			gs.buildMux().ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404 for unregistered old UI path, got %d", rec.Code)
			}
		})
	}
}

func TestGatewayUIRootRedirectsToAdminUI(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
	})
	gs.ui = GatewayUIConfig{Enabled: true, RequireAuth: false}

	for _, path := range []string{TrinoUIPath, TrinoUIPath + "/"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()

			gs.buildMux().ServeHTTP(rec, req)

			if rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("expected 307 redirect, got %d", rec.Code)
			}
			if location := rec.Header().Get("Location"); location != GatewayUIRedirectURL {
				t.Fatalf("expected admin UI redirect, got %q", location)
			}
		})
	}
}

func TestGatewayUIRootRedirectsToTrinoBackendWhenAdminUIDisabled(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
	})

	for _, path := range []string{TrinoUIPath, TrinoUIPath + "/"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()

			gs.buildMux().ServeHTTP(rec, req)

			if rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("expected 307 redirect, got %d", rec.Code)
			}
			if location := rec.Header().Get("Location"); location != "/ui/team-a/runtime-a/" {
				t.Fatalf("expected Trino UI backend redirect, got %q", location)
			}
		})
	}
}

func TestGatewayUIRootTrinoPathUsesDefaultBackendWhenAdminUIDisabled(t *testing.T) {
	var gotPath, gotQuery string
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("query page"))
	}))
	t.Cleanup(backendServer.Close)

	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ui/query.html?20260519_214150_00000_vyspp", http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected root Trino UI path to proxy to default backend, got %d", rec.Code)
	}
	if gotPath != "/ui/query.html" {
		t.Fatalf("expected Trino UI query path, got %q", gotPath)
	}
	if gotQuery != "20260519_214150_00000_vyspp" {
		t.Fatalf("expected query ID to be preserved, got %q", gotQuery)
	}
	if body := rec.Body.String(); body != "query page" {
		t.Fatalf("unexpected response body: %q", body)
	}
}

func TestGatewayUIRootRedirectsToResumableDefaultBackendWhenAdminUIDisabled(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
			Default:      true,
			Backends: []Backend{
				{
					Name:           "runtime-a",
					Namespace:      "team-a",
					CoordinatorURL: "http://trino-runtime-a.team-a.svc.cluster.local:8080",
					State:          StatePaused,
					Active:         true,
				},
			},
		},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, TrinoUIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect, got %d", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/ui/team-a/runtime-a/" {
		t.Fatalf("expected resumable Trino UI backend redirect, got %q", location)
	}
}

func TestGatewayUIRootDoesNotGuessWhenAdminUIDisabledAndBackendsAmbiguous(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
		{
			Name:         "team-b-runtime-b",
			RoutingGroup: "team-b--runtime-b",
			Header:       "runtime-b",
			Backends: []Backend{
				{
					Name:           "runtime-b",
					Namespace:      "team-b",
					CoordinatorURL: "http://trino-runtime-b.team-b.svc.cluster.local:8080",
					State:          StateRunning,
					Active:         true,
				},
			},
		},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, TrinoUIPath, http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for ambiguous Trino UI root, got %d", rec.Code)
	}
}

func TestGatewayUIBackendPathProxiesToSelectedTrinoBackend(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
	})

	route, backend, targetPath, ok, ambiguous := gs.resolveTrinoUIBackend("/ui/team-a/runtime-a/catalogs")

	if !ok || ambiguous {
		t.Fatalf("expected backend path to resolve, ok=%v ambiguous=%v", ok, ambiguous)
	}
	if route.RoutingGroup != "team-a--runtime-a" {
		t.Fatalf("unexpected route: %+v", route)
	}
	if backend.Name != "runtime-a" || backend.Namespace != "team-a" {
		t.Fatalf("unexpected backend: %+v", backend)
	}
	if targetPath != "/ui/catalogs" {
		t.Fatalf("unexpected rewritten path: %q", targetPath)
	}
}

func TestGatewayUIUnknownBackendPathReturnsNotFound(t *testing.T) {
	gs, _ := createTestGatewayService(t, []RouteEntry{
		{
			Name:         "team-a-runtime-a",
			RoutingGroup: "team-a--runtime-a",
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
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ui/team-a/not-there/", http.NoBody)
	rec := httptest.NewRecorder()

	gs.buildMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected unknown backend path to return 404, got %d", rec.Code)
	}
}

func TestGatewayUIRewritesTrinoRedirectLocationToBackendPath(t *testing.T) {
	tests := []struct {
		name     string
		location string
		want     string
	}{
		{
			name:     "absolute gateway location",
			location: "http://127.0.0.1:18180/ui/login.html",
			want:     "/ui/team-a/runtime-a/login.html",
		},
		{
			name:     "absolute backend location",
			location: "http://trino-runtime-a.team-a.svc.cluster.local:8080/ui/login.html?next=%2Fui%2F",
			want:     "/ui/team-a/runtime-a/login.html?next=%2Fui%2F",
		},
		{
			name:     "relative location",
			location: "/ui/login.html",
			want:     "/ui/team-a/runtime-a/login.html",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ui/team-a/runtime-a/", http.NoBody)
			req = req.WithContext(context.WithValue(req.Context(), ctxTrinoUIPrefix, "/ui/team-a/runtime-a/"))
			resp := &http.Response{
				Header:  make(http.Header),
				Request: req,
			}
			resp.Header.Set("Location", test.location)

			rewriteTrinoUILocation(resp)

			if location := resp.Header.Get("Location"); location != test.want {
				t.Fatalf("unexpected rewritten Location: got %q, want %q", location, test.want)
			}
		})
	}
}
