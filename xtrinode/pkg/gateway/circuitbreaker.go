package gateway

import (
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// CircuitState represents the state of a circuit breaker
type CircuitState string

const (
	// CircuitClosed represents normal operation - requests are allowed
	CircuitClosed CircuitState = "closed"
	// CircuitOpen represents failure state - requests are blocked
	CircuitOpen CircuitState = "open"
	// CircuitHalfOpen represents testing state - one request is allowed to test recovery
	CircuitHalfOpen CircuitState = "half-open"
)

// CircuitBreaker implements the circuit breaker pattern for backend protection
type CircuitBreaker struct {
	BackendURL           string
	State                CircuitState
	FailureThreshold     int           // Open circuit after N consecutive failures
	SuccessThreshold     int           // Close circuit after N consecutive successes (half-open)
	Timeout              time.Duration // Wait before trying half-open
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	ConsecutiveOverloads int // Tracks consecutive 503 overload responses
	LastFailure          time.Time
	LastSuccess          time.Time
	halfOpenProbeActive  bool // Tracks if a probe is in-flight during half-open
	mu                   sync.RWMutex
	log                  logr.Logger
}

// OverloadThresholdMultiplier is the multiplier applied to FailureThreshold
// to determine the overload threshold. 503s are softer signals than connection
// errors, so we use a higher threshold (3x) to avoid false-tripping.
const OverloadThresholdMultiplier = 3

// CircuitBreakerManager manages circuit breakers for all backends
type CircuitBreakerManager struct {
	breakers map[string]*CircuitBreaker // backendURL -> circuit breaker
	mu       sync.RWMutex
	log      logr.Logger
}

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager(failureThreshold, successThreshold int, timeout time.Duration, log logr.Logger) *CircuitBreakerManager {
	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
		log:      log,
	}
}

// GetOrCreateBreaker gets or creates a circuit breaker for a backend
func (cbm *CircuitBreakerManager) GetOrCreateBreaker(backendURL string, failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	cbm.mu.Lock()
	defer cbm.mu.Unlock()

	breaker, exists := cbm.breakers[backendURL]
	if !exists {
		breaker = &CircuitBreaker{
			BackendURL:       backendURL,
			State:            CircuitClosed,
			FailureThreshold: failureThreshold,
			SuccessThreshold: successThreshold,
			Timeout:          timeout,
			log:              cbm.log,
		}
		cbm.breakers[backendURL] = breaker
	}
	return breaker
}

// Allow checks if a request is allowed through the circuit breaker
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.State {
	case CircuitClosed:
		// Normal operation - allow requests
		return true

	case CircuitOpen:
		// Circuit is open - check if timeout has passed to try half-open
		if time.Since(cb.LastFailure) >= cb.Timeout {
			cb.State = CircuitHalfOpen
			cb.ConsecutiveSuccesses = 0
			cb.halfOpenProbeActive = true // Set probe flag to prevent double probes
			cb.log.Info("Circuit breaker transitioning to half-open", "backend", cb.BackendURL)
			return true // Allow one request to test
		}
		return false // Still in timeout period

	case CircuitHalfOpen:
		// Testing recovery - allow only one probe in-flight to avoid stampeding
		if cb.halfOpenProbeActive {
			return false // Probe already in-flight, block other requests
		}
		cb.halfOpenProbeActive = true
		return true

	default:
		return false
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.LastSuccess = time.Now()
	cb.ConsecutiveFailures = 0
	cb.ConsecutiveOverloads = 0
	cb.ConsecutiveSuccesses++

	if cb.State == CircuitHalfOpen {
		cb.halfOpenProbeActive = false // Clear probe flag
		if cb.ConsecutiveSuccesses >= cb.SuccessThreshold {
			cb.State = CircuitClosed
			cb.log.Info("Circuit breaker closed after successful recovery", "backend", cb.BackendURL)
		}
	}
}

// RecordOverload records a 503 overload response (softer than a hard failure).
// Uses a separate counter with a higher threshold (FailureThreshold * OverloadThresholdMultiplier)
// to detect sustained overload without false-tripping on transient Trino queue-full responses.
// Resets the success counter but does NOT reset the hard failure counter.
func (cb *CircuitBreaker) RecordOverload() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.ConsecutiveOverloads++
	cb.ConsecutiveSuccesses = 0

	overloadThreshold := cb.FailureThreshold * OverloadThresholdMultiplier

	switch cb.State {
	case CircuitClosed:
		if cb.ConsecutiveOverloads >= overloadThreshold {
			cb.State = CircuitOpen
			cb.LastFailure = time.Now()
			cb.log.Info("Circuit breaker opened due to sustained overload (503s)",
				"backend", cb.BackendURL, "overloads", cb.ConsecutiveOverloads,
				"threshold", overloadThreshold)
		}
	case CircuitHalfOpen:
		// Overload during half-open probe — reopen circuit
		cb.halfOpenProbeActive = false
		cb.State = CircuitOpen
		cb.LastFailure = time.Now()
		cb.log.Info("Circuit breaker reopened after overload during half-open test", "backend", cb.BackendURL)
	}
}

// RecordFailure records a failed request
// Note: This should NOT be called for sleeping/paused backends (connection refused)
// as those are expected states, not failures. The errorHandler classifies errors
// and only calls RecordFailure for real failures (ErrorTypePaused is excluded).
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.recordFailureLocked()
}

// RecordProxyFailure records a proxy transport failure.
// Expected paused/overload errors are ignored in closed/open states, but any
// error from an in-flight half-open probe must reopen the breaker and clear the
// probe slot.
func (cb *CircuitBreaker) RecordProxyFailure(countFailure bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.State == CircuitHalfOpen || countFailure {
		cb.recordFailureLocked()
	}
}

func (cb *CircuitBreaker) recordFailureLocked() {
	cb.LastFailure = time.Now()
	cb.ConsecutiveSuccesses = 0
	cb.ConsecutiveOverloads = 0
	cb.ConsecutiveFailures++

	switch cb.State {
	case CircuitClosed:
		if cb.ConsecutiveFailures >= cb.FailureThreshold {
			cb.State = CircuitOpen
			cb.log.Info("Circuit breaker opened due to failures", "backend", cb.BackendURL, "failures", cb.ConsecutiveFailures)
		}
	case CircuitHalfOpen:
		// Failed during testing - reopen circuit
		cb.halfOpenProbeActive = false // Clear probe flag
		cb.State = CircuitOpen
		cb.log.Info("Circuit breaker reopened after failed test", "backend", cb.BackendURL)
	}
}

// GetState returns the current circuit breaker state
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.State
}

// Selectable checks if a backend is selectable without mutating state.
// This is used during candidate filtering to avoid consuming the probe slot.
// Only Allow() should mutate state.
func (cb *CircuitBreaker) Selectable() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.State {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Eligible to attempt if timeout has passed
		return time.Since(cb.LastFailure) >= cb.Timeout
	case CircuitHalfOpen:
		// Eligible if probe slot is free
		return !cb.halfOpenProbeActive
	default:
		return false
	}
}

// GetStats returns circuit breaker statistics
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return map[string]interface{}{
		"state":                string(cb.State),
		"consecutiveFailures":  cb.ConsecutiveFailures,
		"consecutiveSuccesses": cb.ConsecutiveSuccesses,
		"consecutiveOverloads": cb.ConsecutiveOverloads,
		"lastFailure":          cb.LastFailure,
		"lastSuccess":          cb.LastSuccess,
	}
}
