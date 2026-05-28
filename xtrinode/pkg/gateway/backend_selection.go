package gateway

import (
	"context"

	"github.com/xtrinode/xtrinode/internal/config"
)

// selectBackend selects a backend from the route using capacity-aware load balancing.
//
// Load Balancing Policy: "Small-first, spill to large"
//   - Calculates effectiveLoad = (running + queued) / capacityUnits
//   - Selects backend with minimum effectiveLoad
//   - Tie-breaker: prefer smaller capacity (cost optimization)
//   - Fully deterministic (no randomness)
//
// Backend Filtering:
//   - State-based routing (operator-owned, gateway-enforced):
//   - Only State == RUNNING backends are eligible for NEW queries
//   - State != RUNNING → reject (PAUSED, RESUMING, DRAINING)
//   - Filters out backends with Active=false
//   - Filters out backends marked unhealthy by health checker
//   - Filters out backends with open circuit breakers
//   - Falls back to all backends if none are healthy (fail-open behavior)
//
// Returns nil if no backends are available in the route.
func (gs *GatewayService) selectBackend(route *RouteEntry) *Backend {
	if len(route.Backends) == 0 {
		return nil
	}

	// Filter backends: state + active flag + health checker + circuit breaker.
	// Use Selectable() for filtering to avoid mutating circuit breaker state.
	activeBackends := []Backend{}
	for i := range route.Backends {
		if gs.isBackendSelectable(&route.Backends[i]) {
			activeBackends = append(activeBackends, route.Backends[i])
		}
	}

	if len(activeBackends) == 0 {
		// Fail-open: retry with health checks and circuit breaker disabled, but never
		// bypass a fresh "sleeping" signal. Sleeping means the service is scaled to
		// zero or has no connectable endpoint; routing to it creates slow proxy
		// timeouts instead of the controlled resume response.
		gs.log.V(0).Info("No healthy backends found, retrying with non-sleeping health/breaker checks disabled (fail-open)", "route", route.RoutingGroup)
		for i := range route.Backends {
			backend := &route.Backends[i]
			// Still enforce state machine (critical: never route to non-RUNNING backends)
			if backend.State != "" && backend.State != StateRunning {
				continue
			}

			// Still check explicit Active flag
			if !backend.Active {
				continue
			}

			if gs.healthChecker != nil && gs.healthChecker.IsSleeping(backend.CoordinatorURL) {
				if !gs.healthChecker.probeBackend(context.Background(), backend.CoordinatorURL) {
					gs.log.V(1).Info("Not fail-opening sleeping backend", "backend", backend.CoordinatorURL)
					continue
				}
			}

			// Ignore ordinary unhealthy state and circuit breaker in fail-open mode.
			// This allows routing to backends that are temporarily marked unavailable,
			// while preserving sleeping/scale-to-zero as a resume signal.
			activeBackends = append(activeBackends, *backend)
		}
	}

	if len(activeBackends) == 0 {
		// Returning nil lets handleRequest trigger resume through the API server.
		// errorHandler only runs on proxy transport errors such as connection refused.
		gs.log.V(1).Info("No active backends available", "route", route.RoutingGroup)
		return nil
	}

	// Use capacity-aware load balancing: "small-first, spill to large"
	// This is the single deterministic policy - no random selection
	var selected *Backend
	backendLoads := gs.queryActivity.BackendLoads()
	if hasLoadData(activeBackends, backendLoads) {
		selected = gs.selectByCapacity(activeBackends, backendLoads)
	} else {
		// No load data: select backend with smallest capacity (deterministic)
		selected = gs.selectSmallestCapacity(activeBackends)
	}

	// Return selected backend without calling Allow()
	// RetryProxy will call Allow() right before proxying to avoid half-open probe wedge
	return selected
}

// isBackendSelectable checks whether a backend may receive a new query.
// It is the centralized filter for state, active flag, health checks, and circuit breakers.
func (gs *GatewayService) isBackendSelectable(backend *Backend) bool {
	if backend == nil {
		return false
	}

	// STATE MACHINE ENFORCEMENT (gateway-enforced, operator-owned)
	// Rule: If State != RUNNING, backend not eligible for NEW queries regardless of Active.
	// Rule: If State == RUNNING and Active == false, also not eligible.
	if backend.State != "" && backend.State != StateRunning {
		gs.log.V(1).Info("Skipping backend (state not RUNNING)", "backend", backend.CoordinatorURL, "state", backend.State)
		return false
	}

	if !backend.Active {
		return false
	}

	return gs.backendPassesRuntimeGuards(backend.CoordinatorURL)
}

// isStickyBackendRoutable checks whether an existing query may continue to a cached backend.
// DRAINING backends reject new query selection, but they still may receive follow-up requests for
// query IDs already assigned to them as long as runtime health and circuit breaker guards still pass.
func (gs *GatewayService) isStickyBackendRoutable(backend *Backend) bool {
	if backend == nil {
		return false
	}

	switch backend.State {
	case "", StateRunning:
		if !backend.Active {
			return false
		}
	case StateDraining:
		// Active may be false during draining; sticky continuations are allowed.
	default:
		return false
	}

	return gs.backendPassesRuntimeGuards(backend.CoordinatorURL)
}

func (gs *GatewayService) backendPassesRuntimeGuards(backendURL string) bool {
	if gs.healthChecker != nil && !gs.healthChecker.IsHealthy(backendURL) {
		gs.log.V(1).Info("Skipping unhealthy backend (health checker)", "backend", backendURL)
		return false
	}

	if gs.circuitBreaker != nil {
		breaker := gs.circuitBreaker.GetOrCreateBreaker(
			backendURL,
			config.GatewayCircuitBreakerFailureThreshold,
			config.GatewayCircuitBreakerSuccessThreshold,
			config.GatewayCircuitBreakerTimeout,
		)
		if !breaker.Selectable() {
			gs.log.V(1).Info("Skipping backend (circuit breaker not selectable)", "backend", backendURL)
			return false
		}
	}

	return true
}

// selectByCapacity selects backend using capacity-aware load balancing.
// Policy: "small-first, spill to large as load grows"
//
// Algorithm:
// 1. Compare load ratios using integer cross-multiplication (avoids float equality issues)
// 2. loadA/capA < loadB/capB ⟺ loadA*capB < loadB*capA
// 3. Tie-breaker: prefer smaller capacityUnits (small-first)
// 4. Second tie-breaker: first in list (deterministic)
//
// This gives deterministic, capacity-aware spillover without randomness or float comparison.
func (gs *GatewayService) selectByCapacity(backends []Backend, loads map[string]BackendLoad) *Backend {
	var selected *Backend
	var minLoad int
	var minCapacity int

	for i := range backends {
		load := getBackendLoad(loads, backends[i].CoordinatorURL)
		totalLoad := load.RunningQueries + load.QueuedQueries

		// Get capacity units (default to 1 if not set)
		capacity := backends[i].CapacityUnits
		if capacity <= 0 {
			capacity = 1
		}

		if selected == nil {
			// First backend - initialize
			selected = &backends[i]
			minLoad = totalLoad
			minCapacity = capacity
		} else {
			// Compare ratios using cross-multiplication: loadA/capA vs loadB/capB
			// loadA/capA < loadB/capB ⟺ loadA*capB < loadB*capA
			crossA := totalLoad * minCapacity
			crossB := minLoad * capacity

			if crossA < crossB {
				// New backend has lower effective load
				selected = &backends[i]
				minLoad = totalLoad
				minCapacity = capacity
			} else if crossA == crossB && capacity < minCapacity {
				// Equal effective load - prefer smaller capacity (small-first)
				selected = &backends[i]
				minLoad = totalLoad
				minCapacity = capacity
			}
		}
	}

	if selected == nil {
		// Fallback to smallest capacity if no backends
		return gs.selectSmallestCapacity(backends)
	}

	return selected
}

// selectSmallestCapacity selects the backend with smallest capacity units.
// Used when no load data is available. Deterministic (no randomness).
func (gs *GatewayService) selectSmallestCapacity(backends []Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}

	selected := &backends[0]
	minCapacity := backends[0].CapacityUnits
	if minCapacity <= 0 {
		minCapacity = 1
	}

	for i := 1; i < len(backends); i++ {
		capacity := backends[i].CapacityUnits
		if capacity <= 0 {
			capacity = 1
		}

		if capacity < minCapacity {
			minCapacity = capacity
			selected = &backends[i]
		}
	}

	return selected
}

// hasLoadData checks if we have load data for any of the backends.
func hasLoadData(backends []Backend, loads map[string]BackendLoad) bool {
	for i := range backends {
		load, ok := loads[backends[i].CoordinatorURL]
		if ok && load.RunningQueries+load.QueuedQueries > 0 {
			return true
		}
	}
	return false
}

func getBackendLoad(loads map[string]BackendLoad, backendURL string) BackendLoad {
	load, exists := loads[backendURL]
	if !exists {
		return BackendLoad{}
	}
	return load
}
