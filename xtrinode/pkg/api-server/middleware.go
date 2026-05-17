package apiserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	requestIDKey     contextKey = "requestID"
	authPrincipalKey contextKey = "authPrincipal"
)

type bearerAuthConfig struct {
	Enabled         bool
	AdminToken      string
	ResumeOnlyToken string
}

type authPrincipal struct {
	name       string
	allActions bool
	actions    map[apiAction]struct{}
}

func adminPrincipal() authPrincipal {
	return authPrincipal{name: "admin", allActions: true}
}

func resumeOnlyPrincipal() authPrincipal {
	return authPrincipal{
		name: "resume",
		actions: map[apiAction]struct{}{
			apiActionRuntimeResume: {},
		},
	}
}

// withRequestID adds a unique request ID to the context
func withRequestID(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		w.Header().Set("X-Request-ID", requestID)
		next(w, r.WithContext(ctx))
	}
}

// withCORS adds CORS headers for explicitly allowed browser origins.
func withCORS(allowedOrigins []string, next http.HandlerFunc) http.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func withBearerAuth(cfg bearerAuthConfig, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Enabled {
			next(w, r)
			return
		}

		principal, ok := authenticateBearerToken(r.Header.Get("Authorization"), cfg)
		if !ok {
			writeUnauthorized(w)
			return
		}

		ctx := context.WithValue(r.Context(), authPrincipalKey, principal)
		next(w, r.WithContext(ctx))
	}
}

func authenticateBearerToken(headerValue string, cfg bearerAuthConfig) (authPrincipal, bool) {
	if cfg.AdminToken != "" && cfg.ResumeOnlyToken != "" && cfg.AdminToken == cfg.ResumeOnlyToken {
		return authPrincipal{}, false
	}
	if cfg.AdminToken != "" && bearerTokenMatches(headerValue, cfg.AdminToken) {
		return adminPrincipal(), true
	}
	if cfg.ResumeOnlyToken != "" && bearerTokenMatches(headerValue, cfg.ResumeOnlyToken) {
		return resumeOnlyPrincipal(), true
	}
	return authPrincipal{}, false
}

func bearerTokenMatches(headerValue, expectedToken string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(headerValue, prefix) {
		return false
	}
	actualToken := strings.TrimSpace(strings.TrimPrefix(headerValue, prefix))
	if actualToken == "" || len(actualToken) != len(expectedToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actualToken), []byte(expectedToken)) == 1
}

func writeUnauthorized(w http.ResponseWriter) {
	body, err := json.Marshal(ErrorResponse{
		Error: "Unauthorized",
		Code:  "UNAUTHORIZED",
	})
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	body = append(body, '\n')

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="xtrinode-api-server"`)
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write(body); err != nil {
		return
	}
}

func writeForbidden(w http.ResponseWriter, action apiAction) {
	body, err := json.Marshal(ErrorResponse{
		Error:   "Forbidden",
		Code:    "FORBIDDEN",
		Details: string(action),
	})
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	body = append(body, '\n')

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	if _, err := w.Write(body); err != nil {
		return
	}
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, action apiAction) bool {
	if !s.config.AuthEnabled {
		return true
	}

	principal, ok := r.Context().Value(authPrincipalKey).(authPrincipal)
	if !ok {
		writeUnauthorized(w)
		return false
	}

	if principal.allActions {
		return true
	}
	if _, ok := principal.actions[action]; ok {
		return true
	}

	s.log.V(1).Info("API request denied by authorization policy", "principal", principal.name, "action", action, "path", r.URL.Path)
	writeForbidden(w, action)
	return false
}
