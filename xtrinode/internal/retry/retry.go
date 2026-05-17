package retry

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Config holds retry configuration
type Config struct {
	// Steps is the maximum number of retry attempts
	Steps int
	// Duration is the base delay between retries
	Duration time.Duration
	// Factor is the exponential backoff multiplier
	Factor float64
	// Jitter adds randomness to backoff (0.0 to 1.0)
	Jitter float64
	// Cap is the maximum delay between retries
	Cap time.Duration
}

// DefaultConfig returns a default retry configuration suitable for Kubernetes API conflicts
func DefaultConfig() Config {
	return Config{
		Steps:    5,
		Duration: 10 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Cap:      1 * time.Second,
	}
}

// FastConfig returns a faster retry configuration for low-latency operations
func FastConfig() Config {
	return Config{
		Steps:    3,
		Duration: 5 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Cap:      100 * time.Millisecond,
	}
}

// SlowConfig returns a slower retry configuration for high-contention scenarios
func SlowConfig() Config {
	return Config{
		Steps:    10,
		Duration: 50 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Cap:      5 * time.Second,
	}
}

// OnConflict retries a function on Kubernetes API conflicts.
// It uses exponential backoff and only retries on conflict errors.
// Non-conflict errors are returned immediately.
// If log is nil, logging is skipped.
//
// Example:
//
//	err := retry.OnConflict(ctx, retry.DefaultConfig(), log, func() error {
//	    if err := client.Get(ctx, key, obj); err != nil {
//	        return err
//	    }
//	    obj.Spec.Field = newValue
//	    return client.Update(ctx, obj)
//	})
func OnConflict(ctx context.Context, config Config, log logr.Logger, fn func() error) error {
	backoff := wait.Backoff{
		Steps:    config.Steps,
		Duration: config.Duration,
		Factor:   config.Factor,
		Jitter:   config.Jitter,
		Cap:      config.Cap,
	}

	var lastErr error
	var nonConflictErr error
	var attempt int

	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		attempt++

		err := fn()
		if err == nil {
			return true, nil // Success
		}

		// Only retry on conflict errors
		if !k8serrors.IsConflict(err) {
			nonConflictErr = err
			return false, err // Don't retry non-conflict errors - return immediately
		}

		lastErr = err
		if log.Enabled() {
			log.V(1).Info("Retrying on conflict",
				"attempt", attempt,
				"maxAttempts", config.Steps,
				"error", err)
		}

		// Check context cancellation before retrying
		if ctx.Err() != nil {
			return false, fmt.Errorf("context canceled: %w", ctx.Err())
		}

		return false, nil // Retry
	})

	if err != nil {
		// Return non-conflict errors directly (not wrapped)
		if nonConflictErr != nil {
			return nonConflictErr
		}
		// Check if error is context cancellation (may be wrapped by wait.ExponentialBackoff)
		if ctx.Err() != nil {
			return fmt.Errorf("context canceled: %w", ctx.Err())
		}
		// If wait.ExponentialBackoff returns an error, it means we exhausted retries
		if lastErr != nil {
			return fmt.Errorf("exhausted retries after %d attempts: %w", attempt, lastErr)
		}
		// Other error
		return err
	}

	return nil
}

// OnConflictWithRefresh retries a function on Kubernetes API conflicts,
// refreshing the object before each retry attempt. This is useful for
// read-modify-write operations where you need to refresh the object
// state before modifying it.
//
// The refreshFn should fetch the latest version of the object.
// The modifyFn should modify the object and attempt to update it.
//
// Example:
//
//	err := retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
//	    func() error {
//	        return client.Get(ctx, key, obj) // Refresh
//	    },
//	    func() error {
//	        obj.Spec.Field = newValue
//	        return client.Update(ctx, obj) // Modify and update
//	    },
//	)
func OnConflictWithRefresh(ctx context.Context, config Config, log logr.Logger, refreshFn, modifyFn func() error) error {
	backoff := wait.Backoff{
		Steps:    config.Steps,
		Duration: config.Duration,
		Factor:   config.Factor,
		Jitter:   config.Jitter,
		Cap:      config.Cap,
	}

	var lastErr error
	var nonConflictErr error
	var attempt int

	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		attempt++

		// Check context cancellation before attempting (but after first attempt)
		if attempt > 1 && ctx.Err() != nil {
			return false, fmt.Errorf("context canceled: %w", ctx.Err())
		}

		// Refresh object state before modifying
		if err := refreshFn(); err != nil {
			return false, fmt.Errorf("failed to refresh object: %w", err)
		}

		// Attempt to modify and update
		err := modifyFn()
		if err == nil {
			return true, nil // Success
		}

		// Only retry on conflict errors
		if !k8serrors.IsConflict(err) {
			nonConflictErr = err
			return false, err // Don't retry non-conflict errors - return immediately
		}

		lastErr = err
		if log.Enabled() {
			log.V(1).Info("Retrying on conflict with refresh",
				"attempt", attempt,
				"maxAttempts", config.Steps,
				"error", err)
		}

		// Check context cancellation before retrying
		if ctx.Err() != nil {
			return false, fmt.Errorf("context canceled: %w", ctx.Err())
		}

		return false, nil // Retry
	})

	if err != nil {
		// Return non-conflict errors directly (not wrapped)
		if nonConflictErr != nil {
			return nonConflictErr
		}
		// Check if error is context cancellation (may be wrapped by wait.ExponentialBackoff)
		if ctx.Err() != nil {
			return fmt.Errorf("context canceled: %w", ctx.Err())
		}
		// If wait.ExponentialBackoff returns an error, it means we exhausted retries
		if lastErr != nil {
			return fmt.Errorf("exhausted retries after %d attempts: %w", attempt, lastErr)
		}
		// Other error
		return err
	}

	return nil
}

// IsRetryableError checks if an error is retryable (conflict or server errors)
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Retry on conflicts
	if k8serrors.IsConflict(err) {
		return true
	}

	// Retry on server errors (5xx) - check status code
	if statusErr, ok := err.(*k8serrors.StatusError); ok {
		if statusErr.Status().Code >= 500 && statusErr.Status().Code < 600 {
			return true
		}
	}

	// Retry on rate limiting
	if k8serrors.IsTooManyRequests(err) {
		return true
	}

	return false
}
