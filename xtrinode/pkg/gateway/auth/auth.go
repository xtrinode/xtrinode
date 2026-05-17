package auth

import (
	"context"
	"net/http"
	"strings"
)

// Authenticator is the interface for authentication implementations.
//
// This interface is designed to be extensible for multiple authentication methods:
//   - API key authentication
//   - OAuth/OIDC JWT bearer token authentication
//   - Basic authentication (future)
//
// Implementations should be thread-safe and handle concurrent authentication requests.
type Authenticator interface {
	// Authenticate validates the request and returns authentication result.
	//
	// Parameters:
	//   - r: HTTP request to authenticate
	//
	// Returns:
	//   - *Result: Authentication result with Authenticated flag set to true if successful.
	//     Returns nil if authentication is disabled or not applicable for this request.
	//   - error: Error if authentication check fails (e.g., secret lookup failure).
	//     Should not return error for invalid credentials (return Result with Authenticated=false).
	Authenticate(r *http.Request) (*Result, error)
}

// Startable is an optional interface for authenticators that need initialization
// Some authenticators (like API key) need to start background goroutines
type Startable interface {
	// Start initializes the authenticator and starts any background processes
	Start(ctx context.Context) error
}

// Result represents the result of authentication
type Result struct {
	// Authenticated indicates if authentication was successful
	Authenticated bool

	// KeyID identifies which API key was used, or the JWT subject for bearer auth.
	KeyID string

	// User is the authenticated user identity.
	User string

	// Metadata contains additional authentication metadata
	// For API key: empty or service name.
	// For JWT: selected claims like issuer and auth type.
	Metadata map[string]string
}

// Middleware creates an HTTP middleware that authenticates requests.
//
// The middleware:
//   - Skips authentication if authenticator is nil (authentication disabled)
//   - Calls authenticator.Authenticate() for each request
//   - Returns 401 Unauthorized if authentication fails
//   - Returns 500 Internal Server Error if authentication check encounters an error
//   - Stores authentication result in request context for downstream handlers
//
// Usage:
//
//	handler := auth.Middleware(authenticator)(nextHandler)
//
// Parameters:
//   - authenticator: Authentication implementation (nil disables authentication)
//
// Returns:
//   - Middleware function that wraps HTTP handlers with authentication
func Middleware(authenticator Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip authentication if disabled
			if authenticator == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Authenticate request
			result, err := authenticator.Authenticate(r)
			if err != nil {
				http.Error(w, "Authentication error", http.StatusInternalServerError)
				return
			}

			if result == nil || !result.Authenticated {
				challenge := "X-API-Key"
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Authorization"))), "bearer ") {
					challenge = "Bearer"
				}
				w.Header().Set("WWW-Authenticate", challenge)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Add authentication context to request (for future use)
			// Store result in request context for downstream handlers
			ctx := r.Context()
			ctx = withAuthResult(ctx, result)
			r = r.WithContext(ctx)

			// Continue to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// contextKey is a type for context keys to avoid collisions
type contextKey string

const authResultKey contextKey = "authResult"

// withAuthResult stores the auth result in the context
func withAuthResult(ctx context.Context, result *Result) context.Context {
	return context.WithValue(ctx, authResultKey, result)
}

// GetAuthResult retrieves the authentication result from the request context.
//
// This function should be called by downstream handlers to access authentication information
// that was set by the authentication middleware.
//
// Parameters:
//   - r: HTTP request with authentication context
//
// Returns:
//   - *Result: Authentication result if present, nil otherwise
//
// Example:
//
//	result := auth.GetAuthResult(r)
//	if result != nil {
//	    log.Info("Authenticated user", "keyID", result.KeyID)
//	}
func GetAuthResult(r *http.Request) *Result {
	if result, ok := r.Context().Value(authResultKey).(*Result); ok {
		return result
	}
	return nil
}
