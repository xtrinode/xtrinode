package gateway

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/xtrinode/xtrinode/pkg/gateway/auth"
)

const (
	TrinoUIPath          = "/ui"
	GatewayUIPath        = "/ui/admin"
	GatewayUIRedirectURL = GatewayUIPath + "/"
)

//go:embed ui/static/*
var gatewayUIAssets embed.FS

func (gs *GatewayService) gatewayUIHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gs.ui.Enabled {
			http.NotFound(w, r)
			return
		}

		if gs.ui.RequireAuth {
			if gs.authenticator == nil {
				http.Error(w, "Gateway UI requires gateway authentication", http.StatusServiceUnavailable)
				return
			}
			auth.Middleware(gs.authenticator)(next).ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (gs *GatewayService) redirectGatewayUI(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, GatewayUIRedirectURL, http.StatusPermanentRedirect)
}

func (gs *GatewayService) gatewayUIFileServer() http.Handler {
	static, err := fs.Sub(gatewayUIAssets, "ui/static")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Gateway UI assets unavailable", http.StatusInternalServerError)
		})
	}

	fileServer := http.FileServer(http.FS(static))
	return http.StripPrefix(GatewayUIPath+"/", gatewayUISecurityHeaders(fileServer))
}

func gatewayUISecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func isGatewayAdminUIRequest(path string) bool {
	return path == GatewayUIPath || strings.HasPrefix(path, GatewayUIPath+"/")
}
