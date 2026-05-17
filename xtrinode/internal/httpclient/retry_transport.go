package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/xtrinode/xtrinode/internal/config"
)

// RetryConfig holds configuration for HTTP retry logic
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Timeout    time.Duration
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: config.HTTPRetryMaxRetries,
		BaseDelay:  config.HTTPRetryBaseDelay,
		MaxDelay:   config.HTTPRetryMaxDelay,
		Timeout:    config.HTTPRetryTimeout,
	}
}

// RetryTransport wraps an http.RoundTripper with retry logic and exponential backoff
type RetryTransport struct {
	Transport http.RoundTripper
	Config    RetryConfig
	Log       logr.Logger
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// NewRetryTransport creates a new RetryTransport with default configuration
func NewRetryTransport(log logr.Logger) *RetryTransport {
	return &RetryTransport{
		Transport: &http.Transport{
			MaxIdleConns:        config.HTTPTransportMaxIdleConns,
			MaxIdleConnsPerHost: config.HTTPTransportMaxIdleConnsPerHost,
			IdleConnTimeout:     config.HTTPTransportIdleConnTimeout,
		},
		Config: DefaultRetryConfig(),
		Log:    log,
	}
}

// NewRetryTransportWithConfig creates a new RetryTransport with custom configuration
func NewRetryTransportWithConfig(cfg RetryConfig, log logr.Logger) *RetryTransport {
	return &RetryTransport{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
		Config: cfg,
		Log:    log,
	}
}

// RoundTrip implements http.RoundTripper with retry logic
func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	var lastResp *http.Response

	// Check if request is idempotent (safe to retry)
	if !isIdempotent(req.Method) {
		// Non-idempotent requests (POST, PATCH) - no retry
		return rt.Transport.RoundTrip(req)
	}

	// Buffer request body if present (needed for retries)
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		defer func() {
			_ = req.Body.Close()
		}()
	}

	var lastCancel context.CancelFunc
	for attempt := 0; attempt <= rt.Config.MaxRetries; attempt++ {
		// Apply timeout to request
		ctx, cancel := context.WithTimeout(req.Context(), rt.Config.Timeout)
		lastCancel = cancel
		reqWithTimeout := req.Clone(ctx)
		if bodyBytes != nil {
			reqWithTimeout.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			reqWithTimeout.ContentLength = int64(len(bodyBytes))
		}

		// Execute request
		resp, err := rt.Transport.RoundTrip(reqWithTimeout)

		// Check if error is retryable
		if err != nil {
			if !isRetryableError(err) {
				// Non-retryable error - return immediately
				cancel()
				return nil, err
			}
			lastErr = err
			rt.Log.V(1).Info("HTTP request failed, will retry",
				"attempt", attempt+1,
				"maxRetries", rt.Config.MaxRetries,
				"error", err,
				"url", req.URL.String())
			// Always cancel on error path when retrying
			cancel()
			lastCancel = nil
			// Continue to retry logic below
		}

		// Check if status code is retryable
		if resp != nil {
			if !isRetryableStatusCode(resp.StatusCode) {
				// Non-retryable status code - return immediately
				resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
				return resp, nil
			}
			rt.Log.V(1).Info("HTTP request returned retryable status code, will retry",
				"attempt", attempt+1,
				"maxRetries", rt.Config.MaxRetries,
				"statusCode", resp.StatusCode,
				"url", req.URL.String())

			// Avoid leaking response bodies when retrying.
			if attempt < rt.Config.MaxRetries {
				_ = resp.Body.Close()
				cancel()
				lastCancel = nil
			} else {
				// Final attempt: return last response to caller; cancel when body is closed.
				lastResp = resp
				resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
				lastCancel = nil
			}
		}

		// Don't sleep after last attempt
		if attempt < rt.Config.MaxRetries {
			delay := calculateDelay(attempt, rt.Config.BaseDelay, rt.Config.MaxDelay)
			rt.Log.V(1).Info("Waiting before retry",
				"delay", delay,
				"attempt", attempt+1,
				"url", req.URL.String())
			if err := sleepWithContext(req.Context(), delay); err != nil {
				return nil, err
			}
		}
	}

	// Clean up any remaining cancel function
	if lastCancel != nil {
		lastCancel()
	}

	// Exhausted retries
	if lastResp != nil {
		rt.Log.Info("Exhausted retries, returning last response",
			"statusCode", lastResp.StatusCode,
			"url", req.URL.String())
		return lastResp, nil
	}

	rt.Log.Info("Exhausted retries, returning last error",
		"error", lastErr,
		"url", req.URL.String())
	return nil, fmt.Errorf("exhausted retries after %d attempts: %w", rt.Config.MaxRetries+1, lastErr)
}

// isIdempotent checks if HTTP method is idempotent (safe to retry)
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// isRetryableError checks if error is transient and retryable
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Common wrapper used by net/http client.
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}

	// Network errors - check both Timeout()
	// Don't early-return here, as connection refused is a net.Error
	// but not a timeout, so we need to fall through to string checks below
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		// Fall through to string checks for connection refused, etc.
	}

	// Check error message for common transient errors
	// This catches "connection refused" which is critical for sleeping backends
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "TLS handshake timeout") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "EOF")
}

// isRetryableStatusCode checks if HTTP status code is retryable
func isRetryableStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// calculateDelay calculates exponential backoff delay with jitter
func calculateDelay(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	// Exponential backoff: baseDelay * 2^attempt
	delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt)))

	// Cap at maxDelay
	if delay > maxDelay {
		delay = maxDelay
	}

	// Add jitter: randomize ±config.HTTPRetryJitterPercent to prevent thundering herd
	jitter := time.Duration(float64(delay) * config.HTTPRetryJitterPercent * (2*float64(time.Now().UnixNano()%100)/100 - 1))
	delay += jitter

	// Ensure delay is positive
	if delay < 0 {
		delay = baseDelay
	}

	return delay
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// NewRetryClient creates an HTTP client with retry transport
func NewRetryClient(log logr.Logger) *http.Client {
	return &http.Client{
		Transport: NewRetryTransport(log),
		Timeout:   30 * time.Second, // Overall client timeout
	}
}

// NewRetryClientWithConfig creates an HTTP client with custom retry configuration
func NewRetryClientWithConfig(cfg RetryConfig, log logr.Logger) *http.Client {
	return &http.Client{
		Transport: NewRetryTransportWithConfig(cfg, log),
		Timeout:   30 * time.Second, // Overall client timeout
	}
}
