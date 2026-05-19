package gateway

import (
	"context"
	"time"

	"github.com/xtrinode/xtrinode/internal/config"
)

// startHealthChecker starts the active health checker background goroutine
func (gs *GatewayService) startHealthChecker(ctx context.Context) {
	ticker := time.NewTicker(config.GatewayHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			urls := gs.collectHealthCheckBackendURLs()
			if len(urls) > 0 && gs.healthChecker != nil {
				gs.healthChecker.checkAllBackends(ctx, urls)
			}
		}
	}
}

func (gs *GatewayService) collectHealthCheckBackendURLs() []string {
	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	backendURLs := make(map[string]bool)
	collect := func(route *RouteEntry) {
		if route == nil {
			return
		}
		for _, backend := range route.Backends {
			if backend.State != "" && backend.State != StateRunning {
				continue
			}
			if backend.Active {
				backendURLs[backend.CoordinatorURL] = true
			}
		}
	}

	for _, route := range gs.routes {
		collect(route)
	}
	collect(gs.defaultRoute)

	urls := make([]string, 0, len(backendURLs))
	for url := range backendURLs {
		urls = append(urls, url)
	}
	return urls
}
