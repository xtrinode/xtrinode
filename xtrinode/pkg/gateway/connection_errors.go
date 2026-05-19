package gateway

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ErrorType classifies errors for proper handling
type ErrorType int

const (
	ErrorTypePaused   ErrorType = iota // Connection refused, backend sleeping
	ErrorTypeOverload                  // Too many queries, backend overloaded
	ErrorTypeOther                     // Other errors
)

// classifyError classifies errors to determine appropriate response
func classifyError(err error, statusCode int) ErrorType {
	// Check connection errors first
	if err != nil && isConnectionError(err) {
		return ErrorTypePaused
	}

	// 503 can be either paused or overload - check error message
	if statusCode != 503 {
		return ErrorTypeOther
	}

	// No error context - default to overload (safer to not auto-resume)
	if err == nil {
		return ErrorTypeOverload
	}

	// Classify 503 by error message
	errMsg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errMsg, "too many queries"),
		strings.Contains(errMsg, "server overloaded"),
		strings.Contains(errMsg, "queue full"):
		return ErrorTypeOverload

	case strings.Contains(errMsg, "connection refused"):
		return ErrorTypePaused

	default:
		// Default 503 to overload (safer to not auto-resume)
		return ErrorTypeOverload
	}
}

// isConnectionError checks if an error is a connection-related error that indicates
// the service is down/scaled-to-zero (not just slow/overloaded).
// Returns true for DNS errors, connection refused, connection reset during dial,
// and dial timeouts. A Kubernetes Service with no ready endpoints can surface as
// a dial timeout, so that path must still trigger resume instead of a raw 502.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for url.Error (wraps network errors)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isConnectionError(urlErr.Err)
	}

	// Check for DNS errors (no such host / NXDOMAIN)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Check for net.OpError
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return opErr.Op == "dial"
		}

		// Check for ECONNREFUSED (connection refused)
		if opErr.Err != nil {
			errStr := opErr.Err.Error()
			if strings.Contains(errStr, "connection refused") {
				return true
			}
			// Connection reset during dial (not after established)
			if strings.Contains(errStr, "connection reset") && opErr.Op == "dial" {
				return true
			}
		}
		return false
	}

	// Fallback: check error string for common connection error patterns
	// This helps with test mocks and wrapped errors
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "connection refused") ||
		(strings.Contains(errStr, "dial tcp") && strings.Contains(errStr, "i/o timeout")) {
		return true
	}
	return false
}
