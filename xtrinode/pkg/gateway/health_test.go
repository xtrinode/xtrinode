package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func newTestHealthChecker(interval, timeout time.Duration, failureThreshold int) *BackendHealthChecker {
	return NewBackendHealthChecker(interval, timeout, failureThreshold, logr.Discard())
}

func TestBackendHealthChecker_NewBackendHealthChecker(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	if hc == nil {
		t.Fatal("Expected health checker, got nil")
	}
	if hc.interval != 5*time.Second {
		t.Errorf("Expected interval 5s, got %v", hc.interval)
	}
	if hc.timeout != 2*time.Second {
		t.Errorf("Expected timeout 2s, got %v", hc.timeout)
	}
	if hc.failureThreshold != 3 {
		t.Errorf("Expected failure threshold 3, got %d", hc.failureThreshold)
	}
}

func TestBackendHealthChecker_IsHealthy_UnknownBackend(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)

	// Unknown backend should default to healthy (fail open)
	if !hc.IsHealthy("http://unknown:8080") {
		t.Error("Expected unknown backend to be healthy (fail open)")
	}
}

func TestBackendHealthChecker_RecordSuccess(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Record success
	hc.recordSuccess(backendURL)

	if !hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be healthy after success")
	}

	health := hc.GetHealthStatus(backendURL)
	if health == nil {
		t.Fatal("Expected health status, got nil")
	}
	if health.State != HealthStateHealthy {
		t.Error("Expected health status to be healthy")
	}
	if health.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", health.ConsecutiveFailures)
	}
}

func TestBackendHealthChecker_RecordFailure_BelowThreshold(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Record failures below threshold
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")

	// Should still be considered healthy (below threshold, fail-open for HealthStateUnknown)
	if !hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be healthy (below failure threshold)")
	}

	// But state should be Unknown (not yet reached threshold)
	health := hc.GetHealthStatus(backendURL)
	if health.ConsecutiveFailures != 2 {
		t.Errorf("Expected 2 consecutive failures, got %d", health.ConsecutiveFailures)
	}
	if health.State != HealthStateUnknown {
		t.Errorf("Expected HealthStateUnknown, got %v", health.State)
	}
}

func TestBackendHealthChecker_RecordFailure_AboveThreshold(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Record failures above threshold
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")

	// Should be unhealthy (at threshold)
	if hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be unhealthy (at failure threshold)")
	}

	health := hc.GetHealthStatus(backendURL)
	if health.ConsecutiveFailures != 3 {
		t.Errorf("Expected 3 consecutive failures, got %d", health.ConsecutiveFailures)
	}
	if health.State != HealthStateUnhealthy {
		t.Error("Expected health status to be unhealthy")
	}
}

func TestBackendHealthChecker_Recovery(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Mark as unhealthy
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")

	if hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be unhealthy")
	}

	// Record success - should reset failure count
	hc.recordSuccess(backendURL)

	if !hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be healthy after recovery")
	}

	health := hc.GetHealthStatus(backendURL)
	if health.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures after recovery, got %d", health.ConsecutiveFailures)
	}
}

func TestBackendHealthChecker_CheckBackend_Success(t *testing.T) {
	// Create test server that returns 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"nodeVersion":{"version":"123"}}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	ctx := context.Background()

	// Check backend
	hc.checkBackend(ctx, server.URL)

	// Should be healthy
	if !hc.IsHealthy(server.URL) {
		t.Error("Expected backend to be healthy after successful check")
	}
}

func TestBackendHealthChecker_CheckBackend_Failure(t *testing.T) {
	// Create test server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	ctx := context.Background()

	// Check backend
	hc.checkBackend(ctx, server.URL)

	// Should record failure
	health := hc.GetHealthStatus(server.URL)
	if health.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 consecutive failure, got %d", health.ConsecutiveFailures)
	}
}

func TestBackendHealthChecker_CheckBackend_ConnectionError(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 100*time.Millisecond, 3)
	ctx := context.Background()

	// Check non-existent backend
	hc.checkBackend(ctx, "http://nonexistent:8080")

	// Should record failure
	health := hc.GetHealthStatus("http://nonexistent:8080")
	if health == nil {
		t.Fatal("Expected health status, got nil")
	}
	if health.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 consecutive failure, got %d", health.ConsecutiveFailures)
	}
}

func TestBackendHealthChecker_CheckAllBackends(t *testing.T) {
	// Create test servers
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server2.Close()

	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	ctx := context.Background()

	backendURLs := []string{server1.URL, server2.URL}
	hc.checkAllBackends(ctx, backendURLs)

	// Both should be healthy
	if !hc.IsHealthy(server1.URL) {
		t.Error("Expected server1 to be healthy")
	}
	if !hc.IsHealthy(server2.URL) {
		t.Error("Expected server2 to be healthy")
	}
}

func TestBackendHealthChecker_StateTransitions(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Initially unknown
	if state := hc.GetState(backendURL); state != HealthStateUnknown {
		t.Errorf("Expected HealthStateUnknown, got %v", state)
	}

	// After success -> healthy
	hc.recordSuccess(backendURL)
	if state := hc.GetState(backendURL); state != HealthStateHealthy {
		t.Errorf("Expected HealthStateHealthy, got %v", state)
	}

	// After failures -> unhealthy
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")
	if state := hc.GetState(backendURL); state != HealthStateUnhealthy {
		t.Errorf("Expected HealthStateUnhealthy, got %v", state)
	}

	// After sleeping -> sleeping
	hc.recordSleeping(backendURL, "connection refused")
	if state := hc.GetState(backendURL); state != HealthStateSleeping {
		t.Errorf("Expected HealthStateSleeping, got %v", state)
	}

	// Back to healthy
	hc.recordSuccess(backendURL)
	if state := hc.GetState(backendURL); state != HealthStateHealthy {
		t.Errorf("Expected HealthStateHealthy, got %v", state)
	}
}

func TestBackendHealthChecker_ConcurrentAccess(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Concurrent access
	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			hc.recordSuccess(backendURL)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			hc.IsHealthy(backendURL)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			hc.GetHealthStatus(backendURL)
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Should still be healthy
	if !hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be healthy after concurrent access")
	}
}

func TestBackendHealthChecker_GetHealthStatus_Copy(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	hc.recordSuccess(backendURL)

	health1 := hc.GetHealthStatus(backendURL)
	health2 := hc.GetHealthStatus(backendURL)

	// Should be different instances (copies)
	if health1 == health2 {
		t.Error("Expected different instances (copies)")
	}

	// But same values
	if health1.State != health2.State {
		t.Error("Expected same health status")
	}
}

func TestBackendHealthChecker_IsSleeping(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Initially not sleeping
	if hc.IsSleeping(backendURL) {
		t.Error("Expected backend to not be sleeping initially")
	}

	// Mark as sleeping
	hc.recordSleeping(backendURL, "connection refused")

	if !hc.IsSleeping(backendURL) {
		t.Error("Expected backend to be sleeping")
	}

	// Should not be healthy
	if hc.IsHealthy(backendURL) {
		t.Error("Expected sleeping backend to not be healthy")
	}
}

func TestBackendHealthChecker_ResetSleeping(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	sleepingURL := "http://sleeping:8080"
	unhealthyURL := "http://unhealthy:8080"

	hc.recordSleeping(sleepingURL, "connection refused")
	hc.recordFailure(unhealthyURL, 500, "server error")
	hc.recordFailure(unhealthyURL, 500, "server error")
	hc.recordFailure(unhealthyURL, 500, "server error")

	hc.resetSleeping([]string{sleepingURL, unhealthyURL})

	sleeping := hc.GetHealthStatus(sleepingURL)
	if sleeping == nil {
		t.Fatal("Expected sleeping backend health status")
	}
	if sleeping.State != HealthStateUnknown {
		t.Fatalf("Expected sleeping backend to reset to unknown, got %s", sleeping.State)
	}
	if sleeping.ConsecutiveFailures != 0 {
		t.Fatalf("Expected reset failure count, got %d", sleeping.ConsecutiveFailures)
	}
	if !hc.IsHealthy(sleepingURL) {
		t.Fatal("Expected reset backend to fail open as healthy")
	}

	unhealthy := hc.GetHealthStatus(unhealthyURL)
	if unhealthy == nil {
		t.Fatal("Expected unhealthy backend health status")
	}
	if unhealthy.State != HealthStateUnhealthy {
		t.Fatalf("Expected non-sleeping backend state to remain unhealthy, got %s", unhealthy.State)
	}
}

func TestBackendHealthChecker_StaleThreshold(t *testing.T) {
	// Use very short interval for testing
	hc := newTestHealthChecker(100*time.Millisecond, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Mark as unhealthy
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")
	hc.recordFailure(backendURL, 500, "test error")

	// Should be unhealthy
	if hc.IsHealthy(backendURL) {
		t.Error("Expected backend to be unhealthy")
	}

	// Wait for data to become stale (3 * interval)
	time.Sleep(400 * time.Millisecond)

	// Should now be considered healthy (stale data, fail open)
	if !hc.IsHealthy(backendURL) {
		t.Error("Expected stale backend to be healthy (fail open)")
	}

	// State should be unknown when stale
	if state := hc.GetState(backendURL); state != HealthStateUnknown {
		t.Errorf("Expected HealthStateUnknown for stale data, got %v", state)
	}
}

func TestBackendHealthChecker_LastErrorAndStatus(t *testing.T) {
	hc := newTestHealthChecker(5*time.Second, 2*time.Second, 3)
	backendURL := "http://backend:8080"

	// Record failure with error
	hc.recordFailure(backendURL, 503, "service unavailable")

	health := hc.GetHealthStatus(backendURL)
	if health.LastError != "service unavailable" {
		t.Errorf("Expected LastError 'service unavailable', got '%s'", health.LastError)
	}
	if health.LastStatus != 503 {
		t.Errorf("Expected LastStatus 503, got %d", health.LastStatus)
	}

	// Record success - should clear error
	hc.recordSuccess(backendURL)

	health = hc.GetHealthStatus(backendURL)
	if health.LastError != "" {
		t.Errorf("Expected LastError to be cleared, got '%s'", health.LastError)
	}
	if health.LastStatus != 200 {
		t.Errorf("Expected LastStatus 200, got %d", health.LastStatus)
	}
}
