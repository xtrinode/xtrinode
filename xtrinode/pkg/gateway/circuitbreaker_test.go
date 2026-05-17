package gateway

import (
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func newTestCircuitBreakerManager() *CircuitBreakerManager {
	return NewCircuitBreakerManager(5, 2, 30*time.Second, logr.Discard())
}

func TestCircuitBreakerManager_NewCircuitBreakerManager(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	if cbm == nil {
		t.Fatal("Expected circuit breaker manager, got nil")
	}
	if cbm.breakers == nil {
		t.Error("Expected breakers map, got nil")
	}
}

func TestCircuitBreakerManager_GetOrCreateBreaker(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	backendURL := "http://backend:8080"

	breaker1 := cbm.GetOrCreateBreaker(backendURL, 5, 2, 30*time.Second)
	if breaker1 == nil {
		t.Fatal("Expected circuit breaker, got nil")
	}
	if breaker1.BackendURL != backendURL {
		t.Errorf("Expected backend URL %s, got %s", backendURL, breaker1.BackendURL)
	}
	if breaker1.State != CircuitClosed {
		t.Errorf("Expected initial state Closed, got %s", breaker1.State)
	}

	// Get same breaker again
	breaker2 := cbm.GetOrCreateBreaker(backendURL, 5, 2, 30*time.Second)
	if breaker1 != breaker2 {
		t.Error("Expected same breaker instance")
	}
}

func TestCircuitBreaker_Allow_ClosedState(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Closed state should allow requests
	if !breaker.Allow() {
		t.Error("Expected circuit breaker to allow requests in Closed state")
	}
}

func TestCircuitBreaker_RecordFailure_BelowThreshold(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Record failures below threshold
	for i := 0; i < 4; i++ {
		breaker.RecordFailure()
	}

	// Should still be closed
	if breaker.GetState() != CircuitClosed {
		t.Errorf("Expected state Closed, got %s", breaker.GetState())
	}
	if !breaker.Allow() {
		t.Error("Expected circuit breaker to allow requests (below threshold)")
	}
}

func TestCircuitBreaker_RecordFailure_AtThreshold(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Record failures at threshold
	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}

	// Should be open
	if breaker.GetState() != CircuitOpen {
		t.Errorf("Expected state Open, got %s", breaker.GetState())
	}
	if breaker.Allow() {
		t.Error("Expected circuit breaker to block requests (Open state)")
	}
}

func TestCircuitBreaker_RecordFailure_HalfOpenToOpen(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Open circuit
	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}

	// Wait for timeout to transition to half-open
	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-31 * time.Second)
	breaker.mu.Unlock()

	// Should transition to half-open
	if !breaker.Allow() {
		t.Error("Expected circuit breaker to allow request (HalfOpen state)")
	}
	if breaker.GetState() != CircuitHalfOpen {
		t.Errorf("Expected state HalfOpen, got %s", breaker.GetState())
	}

	// Record failure in half-open - should go back to open
	breaker.RecordFailure()
	if breaker.GetState() != CircuitOpen {
		t.Errorf("Expected state Open after failure in HalfOpen, got %s", breaker.GetState())
	}
}

func TestCircuitBreaker_RecordProxyFailure_HalfOpenExpectedErrorReopens(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}
	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-31 * time.Second)
	breaker.mu.Unlock()

	if !breaker.Allow() {
		t.Fatal("expected half-open probe to be allowed")
	}
	if breaker.GetState() != CircuitHalfOpen {
		t.Fatalf("expected half-open state, got %s", breaker.GetState())
	}

	breaker.RecordProxyFailure(false)
	if breaker.GetState() != CircuitOpen {
		t.Fatalf("expected expected half-open proxy error to reopen breaker, got %s", breaker.GetState())
	}

	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-31 * time.Second)
	breaker.mu.Unlock()
	if !breaker.Allow() {
		t.Fatal("expected probe slot to be cleared after half-open proxy error")
	}
}

func TestCircuitBreaker_RecordSuccess_ClosedState(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Record some failures
	breaker.RecordFailure()
	breaker.RecordFailure()

	// Record success - should reset failure count
	breaker.RecordSuccess()

	stats := breaker.GetStats()
	if stats["consecutiveFailures"].(int) != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", stats["consecutiveFailures"])
	}
	if breaker.GetState() != CircuitClosed {
		t.Errorf("Expected state Closed, got %s", breaker.GetState())
	}
}

func TestCircuitBreaker_RecordSuccess_HalfOpenToClosed(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Open circuit
	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}

	// Wait for timeout
	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-31 * time.Second)
	breaker.mu.Unlock()

	// Transition to half-open
	breaker.Allow()
	if breaker.GetState() != CircuitHalfOpen {
		t.Fatalf("Expected state HalfOpen, got %s", breaker.GetState())
	}

	// Record successes - should close after threshold
	breaker.RecordSuccess()
	if breaker.GetState() != CircuitHalfOpen {
		t.Errorf("Expected state HalfOpen (1 success), got %s", breaker.GetState())
	}

	breaker.RecordSuccess()
	if breaker.GetState() != CircuitClosed {
		t.Errorf("Expected state Closed (2 successes), got %s", breaker.GetState())
	}
}

func TestCircuitBreaker_Timeout(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	// Open circuit
	for i := 0; i < 5; i++ {
		breaker.RecordFailure()
	}

	// Should block immediately
	if breaker.Allow() {
		t.Error("Expected circuit breaker to block requests immediately after opening")
	}

	// Wait for timeout
	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-31 * time.Second)
	breaker.mu.Unlock()

	// Should allow one request (half-open)
	if !breaker.Allow() {
		t.Error("Expected circuit breaker to allow request after timeout")
	}
	if breaker.GetState() != CircuitHalfOpen {
		t.Errorf("Expected state HalfOpen, got %s", breaker.GetState())
	}
}

func TestCircuitBreaker_GetStats(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	breaker.RecordFailure()
	breaker.RecordFailure()
	breaker.RecordSuccess()

	stats := breaker.GetStats()
	if stats == nil {
		t.Fatal("Expected stats, got nil")
	}

	if stats["state"].(string) != string(CircuitClosed) {
		t.Errorf("Expected state Closed, got %s", stats["state"])
	}
	if stats["consecutiveFailures"].(int) != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", stats["consecutiveFailures"])
	}
	if stats["consecutiveSuccesses"].(int) != 1 {
		t.Errorf("Expected 1 consecutive success, got %d", stats["consecutiveSuccesses"])
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 5, 2, 30*time.Second)

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent failures
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			breaker.RecordFailure()
		}
	}()

	// Concurrent successes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			breaker.RecordSuccess()
		}
	}()

	// Concurrent Allow() calls
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			breaker.Allow()
		}
	}()

	wg.Wait()

	// Should have valid state
	state := breaker.GetState()
	if state != CircuitClosed && state != CircuitOpen && state != CircuitHalfOpen {
		t.Errorf("Expected valid state, got %s", state)
	}
}

func TestCircuitBreaker_MultipleBackends(t *testing.T) {
	cbm := newTestCircuitBreakerManager()

	breaker1 := cbm.GetOrCreateBreaker("http://backend1:8080", 5, 2, 30*time.Second)
	breaker2 := cbm.GetOrCreateBreaker("http://backend2:8080", 5, 2, 30*time.Second)

	// Open breaker1
	for i := 0; i < 5; i++ {
		breaker1.RecordFailure()
	}

	// breaker2 should still be closed
	if breaker2.GetState() != CircuitClosed {
		t.Errorf("Expected breaker2 state Closed, got %s", breaker2.GetState())
	}
	if !breaker2.Allow() {
		t.Error("Expected breaker2 to allow requests")
	}

	// breaker1 should be open
	if breaker1.GetState() != CircuitOpen {
		t.Errorf("Expected breaker1 state Open, got %s", breaker1.GetState())
	}
	if breaker1.Allow() {
		t.Error("Expected breaker1 to block requests")
	}
}

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	cbm := newTestCircuitBreakerManager()
	breaker := cbm.GetOrCreateBreaker("http://backend:8080", 3, 2, 1*time.Second)

	// Initial state: Closed
	if breaker.GetState() != CircuitClosed {
		t.Fatalf("Expected initial state Closed, got %s", breaker.GetState())
	}

	// Transition: Closed -> Open
	for i := 0; i < 3; i++ {
		breaker.RecordFailure()
	}
	if breaker.GetState() != CircuitOpen {
		t.Fatalf("Expected state Open, got %s", breaker.GetState())
	}

	// Wait for timeout
	breaker.mu.Lock()
	breaker.LastFailure = time.Now().Add(-2 * time.Second)
	breaker.mu.Unlock()

	// Transition: Open -> HalfOpen
	if !breaker.Allow() {
		t.Fatal("Expected Allow() to transition to HalfOpen")
	}
	if breaker.GetState() != CircuitHalfOpen {
		t.Fatalf("Expected state HalfOpen, got %s", breaker.GetState())
	}

	// Transition: HalfOpen -> Closed (after 2 successes)
	breaker.RecordSuccess()
	if breaker.GetState() != CircuitHalfOpen {
		t.Fatalf("Expected state HalfOpen (1 success), got %s", breaker.GetState())
	}

	breaker.RecordSuccess()
	if breaker.GetState() != CircuitClosed {
		t.Fatalf("Expected state Closed (2 successes), got %s", breaker.GetState())
	}
}
