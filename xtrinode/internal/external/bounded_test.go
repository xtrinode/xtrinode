package external

import (
	"context"
	"errors"
	"testing"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCallWithTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     time.Duration
		fn          func(context.Context) error
		wantErr     bool
		wantTimeout bool
	}{
		{
			name:    "successful call",
			timeout: time.Second,
			fn: func(ctx context.Context) error {
				return nil
			},
		},
		{
			name:    "call times out",
			timeout: 100 * time.Millisecond,
			fn: func(ctx context.Context) error {
				select {
				case <-time.After(200 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
			wantErr:     true,
			wantTimeout: true,
		},
		{
			name:    "call returns error",
			timeout: time.Second,
			fn: func(ctx context.Context) error {
				return errors.New("test error")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CallWithTimeout(context.Background(), tt.timeout, tt.fn)
			if (err != nil) != tt.wantErr {
				t.Errorf("CallWithTimeout() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantTimeout {
				var transientErr *TransientError
				if !errors.As(err, &transientErr) {
					t.Errorf("Expected TransientError for timeout, got %T", err)
				}
			}
		})
	}
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"context canceled", context.Canceled, true},
		{"server timeout", k8serrors.NewServerTimeout(schema.GroupResource{}, "test", 1), true},
		{"timeout", k8serrors.NewTimeoutError("timeout", 1), true},
		{"service unavailable", k8serrors.NewServiceUnavailable("unavailable"), true},
		{"internal error", k8serrors.NewInternalError(errors.New("internal")), true},
		{"too many requests", k8serrors.NewTooManyRequests("rate limited", 1), true},
		{"conflict", k8serrors.NewConflict(schema.GroupResource{}, "test", errors.New("conflict")), true},
		{"not found", k8serrors.NewNotFound(schema.GroupResource{}, "test"), false},
		{"already exists", k8serrors.NewAlreadyExists(schema.GroupResource{}, "test"), false},
		{"invalid", k8serrors.NewInvalid(schema.GroupKind{}, "test", nil), false},
		{"bad request", k8serrors.NewBadRequest("bad"), false},
		{"unauthorized", k8serrors.NewUnauthorized("unauth"), false},
		{"forbidden", k8serrors.NewForbidden(schema.GroupResource{}, "test", errors.New("forbidden")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.want {
				t.Errorf("IsTransient() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPermanent(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"not found", k8serrors.NewNotFound(schema.GroupResource{}, "test"), true},
		{"already exists", k8serrors.NewAlreadyExists(schema.GroupResource{}, "test"), true},
		{"invalid", k8serrors.NewInvalid(schema.GroupKind{}, "test", nil), true},
		{"bad request", k8serrors.NewBadRequest("bad"), true},
		{"unauthorized", k8serrors.NewUnauthorized("unauth"), true},
		{"forbidden", k8serrors.NewForbidden(schema.GroupResource{}, "test", errors.New("forbidden")), true},
		{"timeout", k8serrors.NewTimeoutError("timeout", 1), false},
		{"service unavailable", k8serrors.NewServiceUnavailable("unavailable"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPermanent(tt.err); got != tt.want {
				t.Errorf("IsPermanent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransientError(t *testing.T) {
	baseErr := errors.New("base error")
	transientErr := &TransientError{Err: baseErr}

	if transientErr.Error() != "transient: base error" {
		t.Errorf("TransientError.Error() = %v, want %v", transientErr.Error(), "transient: base error")
	}
	if !errors.Is(transientErr, baseErr) {
		t.Error("TransientError should unwrap to base error")
	}
}

func TestPermanentError(t *testing.T) {
	baseErr := errors.New("base error")
	permanentErr := &PermanentError{Err: baseErr}

	if permanentErr.Error() != "permanent: base error" {
		t.Errorf("PermanentError.Error() = %v, want %v", permanentErr.Error(), "permanent: base error")
	}
	if !errors.Is(permanentErr, baseErr) {
		t.Error("PermanentError should unwrap to base error")
	}
}
