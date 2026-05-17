package external

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// TransientError represents a temporary error that should be retried.
type TransientError struct {
	Err error
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("transient: %v", e.Err)
}

func (e *TransientError) Unwrap() error {
	return e.Err
}

// PermanentError represents a permanent error that should not be retried.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("permanent: %v", e.Err)
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// CallWithTimeout wraps an external call with timeout and classifies errors.
func CallWithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := fn(ctx)
	if err == nil {
		return nil
	}

	if IsTransient(err) {
		return &TransientError{Err: err}
	}
	return &PermanentError{Err: err}
}

// IsTransient determines if an error is transient and should be retried.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	if k8serrors.IsServerTimeout(err) ||
		k8serrors.IsTimeout(err) ||
		k8serrors.IsServiceUnavailable(err) ||
		k8serrors.IsInternalError(err) ||
		k8serrors.IsTooManyRequests(err) ||
		k8serrors.IsConflict(err) {
		return true
	}

	errMsg := err.Error()
	return containsAny(errMsg, []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"no such host",
		"network is unreachable",
		"i/o timeout",
	})
}

// IsPermanent determines if an error is permanent and should not be retried.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}

	return k8serrors.IsNotFound(err) ||
		k8serrors.IsAlreadyExists(err) ||
		k8serrors.IsInvalid(err) ||
		k8serrors.IsBadRequest(err) ||
		k8serrors.IsUnauthorized(err) ||
		k8serrors.IsForbidden(err) ||
		k8serrors.IsMethodNotSupported(err)
}

func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
