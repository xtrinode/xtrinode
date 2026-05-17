package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func setupTestServer() (*Server, client.Client) {
	return setupTestServerWithConfig(nil)
}

func setupTestServerWithConfig(mutator func(*ServerConfig)) (*Server, client.Client) {
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme) // Add coordination API for Lease objects
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New(zap.UseDevMode(true))
	cfg := ServerConfig{
		Port:                 8081,
		APIPath:              "/api/v1",
		HealthPath:           "/health",
		MetricsPath:          "/metrics",
		ReadTimeout:          10 * time.Second,
		WriteTimeout:         30 * time.Second,
		ShutdownTimeout:      5 * time.Second,
		RequestTimeout:       30 * time.Second,
		ResumeLeaseDuration:  2 * time.Minute,
		SuspendLeaseDuration: 2 * time.Minute,
		RetryAfterSeconds:    30,
	}
	if mutator != nil {
		mutator(&cfg)
	}
	server := NewServer(cli, log, &cfg)
	// Create LeaseManager for K8s Lease gating
	server.leaseManager = NewLeaseManager(cli, log, "default", 2*time.Minute, "test-api-server")
	return server, cli
}

func TestServer_HandleHealth(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/health", http.NoBody)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestServer_APIAuthRequired(t *testing.T) {
	server, _ := setupTestServerWithConfig(func(cfg *ServerConfig) {
		cfg.AuthEnabled = true
		cfg.AuthToken = "test-token"
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/runtimes", http.NoBody)
	w := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, `Bearer realm="xtrinode-api-server"`, w.Header().Get("WWW-Authenticate"))
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
	var response ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "UNAUTHORIZED", response.Code)
}

func TestServer_APIAuthAllowsBearerToken(t *testing.T) {
	server, _ := setupTestServerWithConfig(func(cfg *ServerConfig) {
		cfg.AuthEnabled = true
		cfg.AuthToken = "test-token"
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/runtimes", http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServer_APIAuthorizationResumeTokenIsLeastPrivilege(t *testing.T) {
	server, _ := setupTestServerWithConfig(func(cfg *ServerConfig) {
		cfg.AuthEnabled = true
		cfg.AuthToken = "admin-token"
		cfg.ResumeAuthToken = "resume-token"
	})

	readReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/runtimes", http.NoBody)
	readReq.Header.Set("Authorization", "Bearer resume-token")
	readResp := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(readResp, readReq)

	assert.Equal(t, http.StatusForbidden, readResp.Code)
	var forbidden ErrorResponse
	require.NoError(t, json.Unmarshal(readResp.Body.Bytes(), &forbidden))
	assert.Equal(t, "FORBIDDEN", forbidden.Code)
	assert.Equal(t, string(apiActionRuntimeRead), forbidden.Details)

	resumeReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resume", bytes.NewReader([]byte(`{}`)))
	resumeReq.Header.Set("Authorization", "Bearer resume-token")
	resumeResp := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(resumeResp, resumeReq)

	assert.Equal(t, http.StatusBadRequest, resumeResp.Code)
	var invalid ErrorResponse
	require.NoError(t, json.Unmarshal(resumeResp.Body.Bytes(), &invalid))
	assert.Equal(t, "INVALID_REQUEST", invalid.Code)
}

func TestServer_APIAuthRejectsAmbiguousAdminAndResumeToken(t *testing.T) {
	server, _ := setupTestServerWithConfig(func(cfg *ServerConfig) {
		cfg.AuthEnabled = true
		cfg.AuthToken = "same-token"
		cfg.ResumeAuthToken = "same-token"
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/runtimes", http.NoBody)
	req.Header.Set("Authorization", "Bearer same-token")
	resp := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestServer_CORSUsesExplicitAllowedOrigins(t *testing.T) {
	server, _ := setupTestServerWithConfig(func(cfg *ServerConfig) {
		cfg.CORSAllowedOrigins = []string{"https://console.example.com"}
	})

	allowedReq := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/api/v1/runtimes", http.NoBody)
	allowedReq.Header.Set("Origin", "https://console.example.com")
	allowed := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(allowed, allowedReq)

	assert.Equal(t, http.StatusNoContent, allowed.Code)
	assert.Equal(t, "https://console.example.com", allowed.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", allowed.Header().Get("Vary"))

	blockedReq := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/api/v1/runtimes", http.NoBody)
	blockedReq.Header.Set("Origin", "https://evil.example.com")
	blocked := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(blocked, blockedReq)

	assert.Equal(t, http.StatusNoContent, blocked.Code)
	assert.Empty(t, blocked.Header().Get("Access-Control-Allow-Origin"))
}

func TestServer_ListRuntimes(t *testing.T) {
	server, cli := setupTestServer()

	// Create test XTrinodes
	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etl",
			Namespace: "team-b",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "m",
		},
	}

	require.NoError(t, cli.Create(context.Background(), xtrinode1))
	require.NoError(t, cli.Create(context.Background(), xtrinode2))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntimes(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var runtimes []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &runtimes)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(runtimes), 2)
	assert.Equal(t, "analytics.xtrinode.io/v1", runtimes[0]["apiVersion"])
	assert.Equal(t, "XTrinode", runtimes[0]["kind"])
}

func TestServer_GetRuntimeIncludesTypeMeta(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	require.NoError(t, cli.Create(context.Background(), xtrinode))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/dummy", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var runtimePayload map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &runtimePayload)
	require.NoError(t, err)
	assert.Equal(t, "analytics.xtrinode.io/v1", runtimePayload["apiVersion"])
	assert.Equal(t, "XTrinode", runtimePayload["kind"])
}

func TestServer_GetRuntimeStatus(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase:   "Ready",
			Workers: 5,
		},
	}

	require.NoError(t, cli.Create(context.Background(), xtrinode))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/dummy/status", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var status map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &status)
	require.NoError(t, err)
	assert.Equal(t, "Ready", status["phase"])
	assert.Equal(t, float64(5), status["workers"])
}

func TestServer_ResumeRuntime(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: true,
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Suspended",
		},
	}

	require.NoError(t, cli.Create(context.Background(), xtrinode))

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/resume", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	// Should return 202 Accepted for async operation
	assert.Equal(t, http.StatusAccepted, w.Code)

	// Verify response is AsyncOperationResponse
	var response AsyncOperationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "resumed", response.Desired)
	assert.Contains(t, response.PollURL, "/status")

	// Verify XTrinode was updated with resume annotation
	var updatedXTrinode analyticsv1.XTrinode
	err = cli.Get(context.Background(), client.ObjectKey{
		Name:      "dummy",
		Namespace: "team-a",
	}, &updatedXTrinode)
	require.NoError(t, err)
	// Server uses annotation-based coordination - controller will read annotation and update Spec.Suspended
	assert.Equal(t, "true", updatedXTrinode.Annotations[config.ResumeRequestedAnnotation])
}

func TestServer_SuspendRuntime(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: false,
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
		},
	}

	require.NoError(t, cli.Create(context.Background(), xtrinode))

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/suspend", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	// Should return 202 Accepted for async operation
	assert.Equal(t, http.StatusAccepted, w.Code)

	// Verify response is AsyncOperationResponse
	var response AsyncOperationResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "suspended", response.Desired)
	assert.Contains(t, response.PollURL, "/status")

	// Verify XTrinode was updated with suspend annotation (using config constants)
	var updatedXTrinode analyticsv1.XTrinode
	err = cli.Get(context.Background(), client.ObjectKey{
		Name:      "dummy",
		Namespace: "team-a",
	}, &updatedXTrinode)
	require.NoError(t, err)
	// Server uses annotation-based coordination - controller will read annotation and update Spec.Suspended
	assert.Equal(t, "true", updatedXTrinode.Annotations[config.SuspendRequestedAnnotation])
}

func TestServer_CreateRuntime(t *testing.T) {
	server, cli := setupTestServer()

	// Use safe DTO instead of full CR
	runtimeReq := CreateRuntimeRequest{
		Name:      "new-runtime",
		Namespace: "team-a",
		Size:      "s",
	}

	body, _ := json.Marshal(runtimeReq)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleRuntimes(w, req)

	if w.Code != http.StatusCreated {
		t.Logf("Response body: %s", w.Body.String())
	}
	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify XTrinode was created
	var xtrinode analyticsv1.XTrinode
	err := cli.Get(context.Background(), client.ObjectKey{
		Name:      "new-runtime",
		Namespace: "team-a",
	}, &xtrinode)
	require.NoError(t, err)
	assert.Equal(t, "s", xtrinode.Spec.Size)
}

func TestServer_DeleteRuntime(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	require.NoError(t, cli.Create(context.Background(), xtrinode))

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/runtimes/team-a/dummy", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify XTrinode was deleted
	var deletedXTrinode analyticsv1.XTrinode
	err := cli.Get(context.Background(), client.ObjectKey{
		Name:      "dummy",
		Namespace: "team-a",
	}, &deletedXTrinode)
	require.Error(t, err)
	assert.True(t, client.IgnoreNotFound(err) == nil)
}

func TestServer_GetRuntime_NotFound(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/nonexistent", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServer_ResumeRuntime_NotFound(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/nonexistent/resume", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServer_InvalidMethod(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "PATCH", "/api/v1/runtimes/team-a/dummy", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestServer_ResumeRuntime_RequiresPOST(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// Try GET on resume endpoint - should fail
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/dummy/resume", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestServer_SuspendRuntime_RequiresPOST(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// Try GET on suspend endpoint - should fail
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/dummy/suspend", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestServer_CreateRuntime_Validation(t *testing.T) {
	server, _ := setupTestServer()

	tests := []struct {
		name    string
		req     CreateRuntimeRequest
		wantErr string
	}{
		{
			name: "missing name",
			req: CreateRuntimeRequest{
				Namespace: "team-a",
				Size:      "s",
			},
			wantErr: "MISSING_NAME",
		},
		{
			name: "missing namespace",
			req: CreateRuntimeRequest{
				Name: "test",
				Size: "s",
			},
			wantErr: "MISSING_NAMESPACE",
		},
		{
			name: "missing size",
			req: CreateRuntimeRequest{
				Name:      "test",
				Namespace: "team-a",
			},
			wantErr: "MISSING_SIZE",
		},
		{
			name: "invalid name",
			req: CreateRuntimeRequest{
				Name:      "Invalid_Name",
				Namespace: "team-a",
				Size:      "s",
			},
			wantErr: "INVALID_NAME",
		},
		{
			name: "invalid size",
			req: CreateRuntimeRequest{
				Name:      "test",
				Namespace: "team-a",
				Size:      "invalid",
			},
			wantErr: "INVALID_SIZE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.req)
			req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleRuntimes(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var errResp ErrorResponse
			err := json.Unmarshal(w.Body.Bytes(), &errResp)
			require.NoError(t, err)
			assert.Equal(t, tt.wantErr, errResp.Code)
		})
	}
}

func TestServer_ResumeRuntime_Idempotency(t *testing.T) {
	server, cli := setupTestServer()

	// Set up xtrinode with active lease
	now := time.Now().UTC()
	leaseUntil := now.Add(2 * time.Minute)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
			Annotations: map[string]string{
				config.ResumeRequestedAnnotation:   "true",
				config.ResumeRequestedAtAnnotation: now.Format(time.RFC3339),
				config.ResumeLeaseUntilAnnotation:  leaseUntil.Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Suspended",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// First request acquires lease
	req1 := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/resume", http.NoBody)
	w1 := httptest.NewRecorder()
	server.handleRuntime(w1, req1)
	assert.Equal(t, http.StatusAccepted, w1.Code)
	assert.Equal(t, "true", w1.Header().Get("X-Lease-Acquired"))

	// Second request should be gated by active K8s Lease (503)
	req2 := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/resume", http.NoBody)
	w2 := httptest.NewRecorder()
	server.handleRuntime(w2, req2)

	// Should return 503 Service Unavailable (gated)
	assert.Equal(t, http.StatusServiceUnavailable, w2.Code)
	assert.Equal(t, "true", w2.Header().Get("X-Lease-Gated"))
	assert.NotEmpty(t, w2.Header().Get("X-Lease-Until"))
	assert.NotEmpty(t, w2.Header().Get("Retry-After"))
}

func TestServer_SuspendRuntime_Idempotency(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// First request acquires lease
	req1 := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/suspend", http.NoBody)
	w1 := httptest.NewRecorder()
	server.handleRuntime(w1, req1)
	assert.Equal(t, http.StatusAccepted, w1.Code)
	assert.Equal(t, "true", w1.Header().Get("X-Lease-Acquired"))

	// Second request should be gated by active K8s Lease (503)
	req2 := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/suspend", http.NoBody)
	w2 := httptest.NewRecorder()
	server.handleRuntime(w2, req2)

	// Should return 503 Service Unavailable (gated)
	assert.Equal(t, http.StatusServiceUnavailable, w2.Code)
	assert.Equal(t, "true", w2.Header().Get("X-Lease-Gated"))
	assert.NotEmpty(t, w2.Header().Get("X-Lease-Until"))
	assert.NotEmpty(t, w2.Header().Get("Retry-After"))
}

func TestServer_ResumeRuntime_LeaseExpiration(t *testing.T) {
	// Use shorter lease duration for testing
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New(zap.UseDevMode(true))
	cfg := ServerConfig{
		Port:                 8081,
		APIPath:              "/api/v1",
		HealthPath:           "/health",
		MetricsPath:          "/metrics",
		ReadTimeout:          10 * time.Second,
		WriteTimeout:         30 * time.Second,
		ShutdownTimeout:      5 * time.Second,
		RequestTimeout:       30 * time.Second,
		ResumeLeaseDuration:  100 * time.Millisecond, // Short lease for testing
		SuspendLeaseDuration: 100 * time.Millisecond,
		RetryAfterSeconds:    30,
	}
	server := NewServer(cli, log, &cfg)
	server.leaseManager = NewLeaseManager(cli, log, "default", 100*time.Millisecond, "test-api-server")

	// Set up xtrinode with expired lease
	now := time.Now().UTC()
	expiredLeaseUntil := now.Add(-1 * time.Second) // Expired 1 second ago

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
			Annotations: map[string]string{
				config.ResumeRequestedAnnotation:   "true",
				config.ResumeRequestedAtAnnotation: expiredLeaseUntil.Format(time.RFC3339),
				config.ResumeLeaseUntilAnnotation:  expiredLeaseUntil.Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Suspended",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// Request should succeed since lease is expired
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/resume", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	// Should succeed with lease acquired
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "true", w.Header().Get("X-Lease-Acquired"))

	// Verify resume annotation was set
	var updatedXTrinode analyticsv1.XTrinode
	err := cli.Get(context.Background(), client.ObjectKey{
		Name:      "dummy",
		Namespace: "team-a",
	}, &updatedXTrinode)
	require.NoError(t, err)
	assert.Equal(t, "true", updatedXTrinode.Annotations[config.ResumeRequestedAnnotation])
}

func TestServer_SuspendRuntime_LeaseExpiration(t *testing.T) {
	// Use shorter lease duration for testing
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New(zap.UseDevMode(true))
	cfg := ServerConfig{
		Port:                 8081,
		APIPath:              "/api/v1",
		HealthPath:           "/health",
		MetricsPath:          "/metrics",
		ReadTimeout:          10 * time.Second,
		WriteTimeout:         30 * time.Second,
		ShutdownTimeout:      5 * time.Second,
		RequestTimeout:       30 * time.Second,
		ResumeLeaseDuration:  100 * time.Millisecond, // Short lease for testing
		SuspendLeaseDuration: 100 * time.Millisecond,
		RetryAfterSeconds:    30,
	}
	server := NewServer(cli, log, &cfg)
	server.leaseManager = NewLeaseManager(cli, log, "default", 100*time.Millisecond, "test-api-server")

	// Set up xtrinode with expired lease
	now := time.Now().UTC()
	expiredLeaseUntil := now.Add(-1 * time.Second) // Expired 1 second ago

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "team-a",
			Annotations: map[string]string{
				config.SuspendRequestedAnnotation:   "true",
				config.SuspendRequestedAtAnnotation: expiredLeaseUntil.Format(time.RFC3339),
				config.SuspendLeaseUntilAnnotation:  expiredLeaseUntil.Format(time.RFC3339),
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// Request should succeed since lease is expired
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/dummy/suspend", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	// Should succeed with lease acquired
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "true", w.Header().Get("X-Lease-Acquired"))

	// Verify suspend annotation was set
	var updatedXTrinode analyticsv1.XTrinode
	err := cli.Get(context.Background(), client.ObjectKey{
		Name:      "dummy",
		Namespace: "team-a",
	}, &updatedXTrinode)
	require.NoError(t, err)
	assert.Equal(t, "true", updatedXTrinode.Annotations[config.SuspendRequestedAnnotation])
}
