package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/httpclient"
)

// HealthState represents the health check state of a backend
type HealthState string

const (
	// HealthStateUnknown means we haven't checked yet
	HealthStateUnknown HealthState = "unknown"
	// HealthStateHealthy means backend is responding correctly
	HealthStateHealthy HealthState = "healthy"
	// HealthStateUnhealthy means backend is returning errors (5xx, timeouts, etc.)
	HealthStateUnhealthy HealthState = "unhealthy"
	// HealthStateSleeping means backend is suspended/not running (connection refused)
	HealthStateSleeping HealthState = "sleeping"
)

// BackendHealth represents the health status of a backend
type BackendHealth struct {
	URL                 string
	State               HealthState
	LastCheck           time.Time
	LastSuccess         time.Time
	LastError           string
	LastStatus          int
	ConsecutiveFailures int
}

// BackendHealthChecker performs active health checks on backends
type BackendHealthChecker struct {
	healthStatus     map[string]*BackendHealth
	mu               sync.RWMutex
	interval         time.Duration
	timeout          time.Duration
	failureThreshold int
	maxConcurrent    int
	staleThreshold   time.Duration
	httpClient       *http.Client
	log              logr.Logger
}

// NewBackendHealthChecker creates a new health checker
func NewBackendHealthChecker(interval, timeout time.Duration, failureThreshold int, log logr.Logger) *BackendHealthChecker {
	// Create HTTP client with retry transport for resilient health checks
	retryConfig := httpclient.RetryConfig{
		MaxRetries: config.HTTPRetryHealthCheckMaxRetries,
		BaseDelay:  config.HTTPRetryHealthCheckBaseDelay,
		MaxDelay:   config.HTTPRetryHealthCheckMaxDelay,
		Timeout:    timeout,
	}
	httpClient := httpclient.NewRetryClientWithConfig(retryConfig, log)

	return &BackendHealthChecker{
		healthStatus:     make(map[string]*BackendHealth),
		interval:         interval,
		timeout:          timeout,
		failureThreshold: failureThreshold,
		maxConcurrent:    20,
		staleThreshold:   interval * 3,
		httpClient:       httpClient,
		log:              log,
	}
}

// checkAllBackends checks health for all backends with bounded concurrency
func (hc *BackendHealthChecker) checkAllBackends(ctx context.Context, backendURLs []string) {
	if len(backendURLs) == 0 {
		return
	}

	sem := make(chan struct{}, hc.maxConcurrent)
	var wg sync.WaitGroup

	for _, u := range backendURLs {
		wg.Add(1)
		sem <- struct{}{}
		go func(url string) {
			defer wg.Done()
			defer func() { <-sem }()
			hc.checkBackend(ctx, url)
		}(u)
	}

	wg.Wait()
}

// checkBackend performs a health check on a single backend
func (hc *BackendHealthChecker) checkBackend(ctx context.Context, backendURL string) {
	hc.probeBackend(ctx, backendURL)
}

func (hc *BackendHealthChecker) probeBackend(ctx context.Context, backendURL string) bool {
	healthURL, err := buildHealthCheckURL(backendURL, "/v1/info")
	if err != nil {
		hc.log.V(1).Info("Failed to parse backend URL for health check", "backend", backendURL, "error", err)
		hc.recordFailure(backendURL, 0, err.Error())
		return false
	}

	reqCtx, cancel := context.WithTimeout(ctx, hc.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", healthURL, http.NoBody)
	if err != nil {
		hc.log.V(1).Info("Failed to create health check request", "backend", backendURL, "error", err)
		hc.recordFailure(backendURL, 0, err.Error())
		return false
	}

	resp, err := hc.httpClient.Do(req)
	if err != nil {
		// Distinguish scaled-to-zero connectivity errors from ordinary backend failures.
		if isConnectionError(err) {
			hc.log.V(1).Info("Backend is sleeping (connection error)", "backend", backendURL)
			hc.recordSleeping(backendURL, err.Error())
		} else {
			hc.log.V(1).Info("Health check failed", "backend", backendURL, "error", err)
			hc.recordFailure(backendURL, 0, err.Error())
		}
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		hc.recordSuccess(backendURL)
		return true
	}

	hc.log.V(1).Info("Health check returned non-2xx status", "backend", backendURL, "status", resp.StatusCode)
	hc.recordFailure(backendURL, resp.StatusCode, "non-2xx status")
	return false
}

// recordSuccess records a successful health check
func (hc *BackendHealthChecker) recordSuccess(backendURL string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		health = &BackendHealth{
			URL:   backendURL,
			State: HealthStateHealthy,
		}
		hc.healthStatus[backendURL] = health
	}

	now := time.Now()
	health.LastCheck = now
	health.LastSuccess = now
	health.ConsecutiveFailures = 0
	health.LastError = ""
	health.LastStatus = 200

	if health.State != HealthStateHealthy {
		health.State = HealthStateHealthy
		hc.log.Info("Backend marked as healthy", "backend", backendURL)
	}
}

// recordFailure records a failed health check (5xx, timeout, etc.)
func (hc *BackendHealthChecker) recordFailure(backendURL string, statusCode int, errMsg string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		health = &BackendHealth{
			URL:   backendURL,
			State: HealthStateUnknown,
		}
		hc.healthStatus[backendURL] = health
	}

	health.LastCheck = time.Now()
	health.ConsecutiveFailures++
	health.LastError = errMsg
	health.LastStatus = statusCode

	if health.ConsecutiveFailures >= hc.failureThreshold {
		if health.State != HealthStateUnhealthy {
			health.State = HealthStateUnhealthy
			hc.log.Info("Backend marked as unhealthy", "backend", backendURL, "consecutiveFailures", health.ConsecutiveFailures, "error", errMsg)
		}
	}
}

// recordSleeping records a sleeping backend (connection refused)
func (hc *BackendHealthChecker) recordSleeping(backendURL, errMsg string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		health = &BackendHealth{
			URL:   backendURL,
			State: HealthStateSleeping,
		}
		hc.healthStatus[backendURL] = health
	}

	health.LastCheck = time.Now()
	health.ConsecutiveFailures++
	health.LastError = errMsg
	health.LastStatus = 0

	if health.State != HealthStateSleeping {
		health.State = HealthStateSleeping
		hc.log.Info("Backend marked as sleeping", "backend", backendURL)
	}
}

// resetSleeping clears a fresh sleeping mark when route configuration says the
// backend is active and running again. The next request or active health check
// will verify the backend immediately.
func (hc *BackendHealthChecker) resetSleeping(backendURLs []string) {
	if len(backendURLs) == 0 {
		return
	}

	hc.mu.Lock()
	defer hc.mu.Unlock()

	for _, backendURL := range backendURLs {
		health, exists := hc.healthStatus[backendURL]
		if !exists || health.State != HealthStateSleeping {
			continue
		}

		health.State = HealthStateUnknown
		health.ConsecutiveFailures = 0
		health.LastError = ""
		health.LastStatus = 0
		hc.log.Info("Cleared stale sleeping backend health after route reload", "backend", backendURL)
	}
}

// IsHealthy checks if a backend is healthy
// Returns true for HealthStateHealthy and HealthStateUnknown (fail-open)
// Returns false for HealthStateUnhealthy and HealthStateSleeping
// Returns true if data is stale (fail-open)
func (hc *BackendHealthChecker) IsHealthy(backendURL string) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		return true
	}

	if time.Since(health.LastCheck) > hc.staleThreshold {
		return true
	}

	return health.State == HealthStateHealthy || health.State == HealthStateUnknown
}

// IsSleeping checks if a backend is sleeping (connection refused)
func (hc *BackendHealthChecker) IsSleeping(backendURL string) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		return false
	}

	if time.Since(health.LastCheck) > hc.staleThreshold {
		return false
	}

	return health.State == HealthStateSleeping
}

// GetState returns the current health state of a backend
func (hc *BackendHealthChecker) GetState(backendURL string) HealthState {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		return HealthStateUnknown
	}

	if time.Since(health.LastCheck) > hc.staleThreshold {
		return HealthStateUnknown
	}

	return health.State
}

// GetHealthStatus returns the health status for a backend
func (hc *BackendHealthChecker) GetHealthStatus(backendURL string) *BackendHealth {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	health, exists := hc.healthStatus[backendURL]
	if !exists {
		return &BackendHealth{
			URL:   backendURL,
			State: HealthStateUnknown,
		}
	}

	copied := *health
	return &copied
}
