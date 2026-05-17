package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, 5, config.Steps)
	assert.Equal(t, 10*time.Millisecond, config.Duration)
	assert.Equal(t, 2.0, config.Factor)
	assert.Equal(t, 0.1, config.Jitter)
	assert.Equal(t, 1*time.Second, config.Cap)
}

func TestFastConfig(t *testing.T) {
	config := FastConfig()
	assert.Equal(t, 3, config.Steps)
	assert.Equal(t, 5*time.Millisecond, config.Duration)
	assert.Equal(t, 100*time.Millisecond, config.Cap)
}

func TestSlowConfig(t *testing.T) {
	config := SlowConfig()
	assert.Equal(t, 10, config.Steps)
	assert.Equal(t, 50*time.Millisecond, config.Duration)
	assert.Equal(t, 5*time.Second, config.Cap)
}

func TestOnConflict_Success(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := FastConfig()

	called := false
	err := OnConflict(ctx, config, log, func() error {
		called = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestOnConflict_NonConflictError(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := FastConfig()

	expectedErr := errors.New("not a conflict")
	callCount := 0
	err := OnConflict(ctx, config, log, func() error {
		callCount++
		return expectedErr
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, 1, callCount) // Should not retry non-conflict errors
}

func TestOnConflict_ConflictError_Retries(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := Config{
		Steps:    3,
		Duration: 1 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.0, // No jitter for predictable tests
		Cap:      10 * time.Millisecond,
	}

	callCount := 0
	err := OnConflict(ctx, config, log, func() error {
		callCount++
		if callCount < 3 {
			// Return conflict error for first 2 attempts
			return k8serrors.NewConflict(
				schema.GroupResource{Resource: "test"},
				"test",
				errors.New("conflict"),
			)
		}
		return nil // Success on 3rd attempt
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount)
}

func TestOnConflict_ConflictError_ExhaustsRetries(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := Config{
		Steps:    3,
		Duration: 1 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.0,
		Cap:      10 * time.Millisecond,
	}

	callCount := 0
	err := OnConflict(ctx, config, log, func() error {
		callCount++
		return k8serrors.NewConflict(
			schema.GroupResource{Resource: "test"},
			"test",
			errors.New("conflict"),
		)
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exhausted retries")
	assert.Equal(t, 3, callCount) // Should retry 3 times
}

func TestOnConflict_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	log := logr.Discard()
	config := FastConfig()

	cancel() // Cancel context immediately

	callCount := 0
	err := OnConflict(ctx, config, log, func() error {
		callCount++
		return k8serrors.NewConflict(
			schema.GroupResource{Resource: "test"},
			"test",
			errors.New("conflict"),
		)
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
	assert.Equal(t, 1, callCount) // Should check context before retrying
}

func TestOnConflictWithRefresh_Success(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := FastConfig()

	refreshCount := 0
	modifyCount := 0

	err := OnConflictWithRefresh(ctx, config, log,
		func() error {
			refreshCount++
			return nil
		},
		func() error {
			modifyCount++
			return nil
		},
	)

	assert.NoError(t, err)
	assert.Equal(t, 1, refreshCount)
	assert.Equal(t, 1, modifyCount)
}

func TestOnConflictWithRefresh_RetriesOnConflict(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := Config{
		Steps:    3,
		Duration: 1 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.0,
		Cap:      10 * time.Millisecond,
	}

	refreshCount := 0
	modifyCount := 0

	err := OnConflictWithRefresh(ctx, config, log,
		func() error {
			refreshCount++
			return nil
		},
		func() error {
			modifyCount++
			if modifyCount < 3 {
				return k8serrors.NewConflict(
					schema.GroupResource{Resource: "test"},
					"test",
					errors.New("conflict"),
				)
			}
			return nil
		},
	)

	assert.NoError(t, err)
	assert.Equal(t, 3, refreshCount) // Should refresh before each retry
	assert.Equal(t, 3, modifyCount)
}

func TestOnConflictWithRefresh_RefreshError(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := FastConfig()

	expectedErr := errors.New("refresh failed")
	err := OnConflictWithRefresh(ctx, config, log,
		func() error {
			return expectedErr
		},
		func() error {
			return nil
		},
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh")
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "conflict error",
			err: k8serrors.NewConflict(
				schema.GroupResource{Resource: "test"},
				"test",
				errors.New("conflict"),
			),
			expected: true,
		},
		{
			name: "server error",
			err: k8serrors.NewInternalError(
				errors.New("internal error"),
			),
			expected: true,
		},
		{
			name:     "too many requests",
			err:      k8serrors.NewTooManyRequestsError("too many requests"),
			expected: true,
		},
		{
			name:     "not found error",
			err:      k8serrors.NewNotFound(schema.GroupResource{Resource: "test"}, "test"),
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("generic error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOnConflict_BackoffTiming(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	config := Config{
		Steps:    3,
		Duration: 10 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.0, // No jitter for predictable timing
		Cap:      100 * time.Millisecond,
	}

	attempts := []time.Time{}
	callCount := 0

	err := OnConflict(ctx, config, log, func() error {
		attempts = append(attempts, time.Now())
		callCount++
		if callCount < 3 {
			return k8serrors.NewConflict(
				schema.GroupResource{Resource: "test"},
				"test",
				errors.New("conflict"),
			)
		}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 3, len(attempts))

	// Check that backoff increases between attempts
	delay1 := attempts[1].Sub(attempts[0])
	delay2 := attempts[2].Sub(attempts[1])

	// Allow some tolerance for timing
	assert.Greater(t, delay2, delay1, "backoff should increase")
	assert.Greater(t, delay1, 5*time.Millisecond, "first delay should be ~10ms")
	assert.Less(t, delay1, 20*time.Millisecond, "first delay should be ~10ms")
}
