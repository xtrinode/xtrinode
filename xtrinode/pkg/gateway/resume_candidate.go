package gateway

// pickResumeCandidate deterministically selects a backend to resume when no RUNNING backend is available.
// Preference order:
//  1. StatePaused (best) then StateResuming
//  2. Smaller capacity units
//  3. Lexicographically smallest (name, namespace)
//
// As a stale-route fallback, a RUNNING backend that the health checker has freshly
// marked sleeping is also resumable. This covers the short window where the
// operator has scaled the service to zero but the gateway has not loaded the
// PAUSED route revision yet.
//
// Returns nil if there is no resumable candidate.
func (gs *GatewayService) pickResumeCandidate(route *RouteEntry) *Backend {
	if route == nil || len(route.Backends) == 0 {
		return nil
	}

	bestIdx := -1
	bestStatePri := int(^uint(0) >> 1) // max int
	bestCap := int(^uint(0) >> 1)
	bestName := ""
	bestNS := ""

	for i := range route.Backends {
		b := &route.Backends[i]

		// Only consider configured/active backends
		if !b.Active {
			continue
		}
		if b.Name == "" || b.Namespace == "" {
			continue
		}

		// Only resume runtimes that are actually resumable
		pri, ok := resumeStatePriority(b.State)
		if !ok {
			continue
		}

		capacity := b.CapacityUnits
		if capacity <= 0 {
			capacity = 1
		}

		if bestIdx == -1 ||
			pri < bestStatePri ||
			(pri == bestStatePri && capacity < bestCap) ||
			(pri == bestStatePri && capacity == bestCap && (b.Name < bestName ||
				(b.Name == bestName && b.Namespace < bestNS))) {

			bestIdx = i
			bestStatePri = pri
			bestCap = capacity
			bestName = b.Name
			bestNS = b.Namespace
		}
	}

	if bestIdx == -1 {
		return gs.pickSleepingResumeCandidate(route)
	}
	return &route.Backends[bestIdx]
}

func (gs *GatewayService) pickSleepingResumeCandidate(route *RouteEntry) *Backend {
	if gs.healthChecker == nil {
		return nil
	}

	bestIdx := -1
	bestCap := int(^uint(0) >> 1)
	bestName := ""
	bestNS := ""

	for i := range route.Backends {
		b := &route.Backends[i]
		if !b.Active {
			continue
		}
		if b.Name == "" || b.Namespace == "" {
			continue
		}
		if b.State != "" && b.State != StateRunning {
			continue
		}
		if !gs.healthChecker.IsSleeping(b.CoordinatorURL) {
			continue
		}

		capacity := b.CapacityUnits
		if capacity <= 0 {
			capacity = 1
		}

		if bestIdx == -1 ||
			capacity < bestCap ||
			(capacity == bestCap && (b.Name < bestName ||
				(b.Name == bestName && b.Namespace < bestNS))) {
			bestIdx = i
			bestCap = capacity
			bestName = b.Name
			bestNS = b.Namespace
		}
	}

	if bestIdx == -1 {
		return nil
	}
	return &route.Backends[bestIdx]
}

// resumeStatePriority returns (priority, true) for resumable states.
// Lower priority value = better candidate.
// Return ok=false for states we should never try to resume.
func resumeStatePriority(state BackendState) (pri int, ok bool) {
	switch state {
	case StatePaused:
		return 0, true
	case StateResuming:
		// Might already be in progress, but still a valid "poke" target under gate.
		return 1, true
	default:
		// Do NOT try to resume RUNNING, DRAINING, REMOVED, unknown.
		return 0, false
	}
}
