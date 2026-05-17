package gateway

import (
	"fmt"
	"net/http"
	"net/http/httputil"

	"github.com/go-logr/logr"

	"github.com/xtrinode/xtrinode/internal/config"
)

// RetryProxy is a circuit breaker guard, not a retry engine.
// It prevents sending traffic to backends whose circuit breaker is not selectable.
// This primarily protects cached/sticky routing paths where backend selection
// already happened earlier (e.g., query ID cache).
// Actual retries are intentionally NOT implemented to avoid duplicate query submission.
type RetryProxy struct {
	proxy          *httputil.ReverseProxy
	log            logr.Logger
	circuitBreaker *CircuitBreakerManager
}

// NewRetryProxy creates a new retry proxy guard
func NewRetryProxy(proxy *httputil.ReverseProxy, log logr.Logger, circuitBreaker *CircuitBreakerManager) *RetryProxy {
	return &RetryProxy{
		proxy:          proxy,
		log:            log,
		circuitBreaker: circuitBreaker,
	}
}

// ServeHTTP serves HTTP requests with circuit breaker protection.
// IMPORTANT: The backend URL header is set inside ReverseProxy.Director (later),
// so we read the selected backend from context (set by handleRequest).
func (rp *RetryProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Read backend URL from context (set by handleRequest before this is called)
	backendURL, ok := r.Context().Value(ctxBackendURL).(string)
	if !ok || backendURL == "" {
		rp.log.Error(nil, "Missing backendURL in request context (expected handleRequest to set ctxBackendURL)")
		http.Error(w, "Gateway misconfiguration", http.StatusInternalServerError)
		return
	}

	// Call Allow() to reserve the circuit breaker probe slot (for half-open state)
	// This is the single point where we transition open→half-open and reserve the probe
	// selectBackend() filters with Selectable() but doesn't call Allow() to avoid wedge
	if rp.circuitBreaker != nil {
		breaker := rp.circuitBreaker.GetOrCreateBreaker(
			backendURL,
			config.GatewayCircuitBreakerFailureThreshold,
			config.GatewayCircuitBreakerSuccessThreshold,
			config.GatewayCircuitBreakerTimeout,
		)

		if !breaker.Allow() {
			rp.log.V(1).Info("Circuit breaker rejected request (not allowed)", "backend", backendURL)
			// Compute Retry-After from circuit breaker timeout (time until half-open)
			// This tells clients when the breaker will allow probe attempts again
			retryAfter := int(config.GatewayCircuitBreakerTimeout.Seconds())
			if retryAfter < 1 {
				retryAfter = 30 // Safe default
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	// Proxy the request. Success/failure accounting is done in modifyResponse/errorHandler.
	rp.proxy.ServeHTTP(w, r)
}
