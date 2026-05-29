package gateway

import "context"

type stickyBackendSelection struct {
	namespace  string
	name       string
	backendURL string
}

func (gs *GatewayService) stickyBackendForQuery(ctx context.Context, queryID string) (stickyBackendSelection, bool) {
	stickyNs, stickyName, stickyBackendURL, stickyRoutingGroup, found := gs.stickyClient.Get(ctx, queryID)
	if !found || stickyBackendURL == "" {
		return stickyBackendSelection{}, false
	}

	cachedBackend, found := gs.findBackendByCoordinatorURL(stickyBackendURL)
	if found && gs.isStickyBackendRoutable(&cachedBackend) {
		gs.log.V(1).Info("Using sticky backend for query ID (cross-route)",
			"queryId", queryID,
			"backend", stickyBackendURL,
			"routingGroup", stickyRoutingGroup)
		return stickyBackendSelection{
			namespace:  stickyNs,
			name:       stickyName,
			backendURL: stickyBackendURL,
		}, true
	}

	gs.log.V(1).Info("Sticky backend no longer routable, invalidating",
		"queryId", queryID,
		"backend", stickyBackendURL)
	if err := gs.stickyClient.Delete(ctx, queryID); err != nil {
		gs.log.V(1).Info("Failed to delete sticky route", "queryId", queryID, "error", err)
	}
	return stickyBackendSelection{}, false
}

func (gs *GatewayService) findBackendByCoordinatorURL(coordinatorURL string) (Backend, bool) {
	gs.routesLock.RLock()
	defer gs.routesLock.RUnlock()

	for _, routeEntry := range gs.routes {
		for i := range routeEntry.Backends {
			backend := routeEntry.Backends[i]
			if backend.CoordinatorURL == coordinatorURL {
				return backend, true
			}
		}
	}

	// defaultRoute may not be in the routes map if it has no routingGroup,
	// hostname, or header.
	if gs.defaultRoute != nil {
		for i := range gs.defaultRoute.Backends {
			backend := gs.defaultRoute.Backends[i]
			if backend.CoordinatorURL == coordinatorURL {
				return backend, true
			}
		}
	}

	return Backend{}, false
}
