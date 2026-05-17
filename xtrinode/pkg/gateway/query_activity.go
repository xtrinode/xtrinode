package gateway

import (
	"context"
	"sync"
	"time"
)

const defaultQueryActivityTTL = 10 * time.Minute

type queryActivity struct {
	namespace    string
	xtrinode     string
	routingGroup string
	backendURL   string
	state        string
	lastSeen     time.Time
}

type QueryActivityTracker struct {
	mu      sync.Mutex
	queries map[string]queryActivity
	ttl     time.Duration
	now     func() time.Time
}

func NewQueryActivityTracker(ttl time.Duration) *QueryActivityTracker {
	if ttl <= 0 {
		ttl = defaultQueryActivityTTL
	}
	return &QueryActivityTracker{
		queries: make(map[string]queryActivity),
		ttl:     ttl,
		now:     time.Now,
	}
}

func (t *QueryActivityTracker) Observe(queryID, namespace, xtrinode, routingGroup, backendURL, state string) {
	if t == nil || queryID == "" || namespace == "" || xtrinode == "" {
		return
	}

	state = normalizeQueryState(state)
	t.mu.Lock()
	defer t.mu.Unlock()

	if isTerminalQueryState(state) {
		delete(t.queries, queryID)
		t.recomputeGaugesLocked()
		return
	}

	t.queries[queryID] = queryActivity{
		namespace:    namespace,
		xtrinode:     xtrinode,
		routingGroup: routingGroup,
		backendURL:   backendURL,
		state:        state,
		lastSeen:     t.now(),
	}
	t.recomputeGaugesLocked()
}

func (t *QueryActivityTracker) BackendLoads() map[string]BackendLoad {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	changed := false
	loads := make(map[string]BackendLoad)
	for queryID, activity := range t.queries {
		if now.Sub(activity.lastSeen) > t.ttl {
			delete(t.queries, queryID)
			changed = true
			continue
		}
		if activity.backendURL == "" {
			continue
		}

		load := loads[activity.backendURL]
		switch activity.state {
		case "QUEUED":
			load.QueuedQueries++
		default:
			load.RunningQueries++
		}
		load.LastUpdate = now
		loads[activity.backendURL] = load
	}
	if changed {
		t.recomputeGaugesLocked()
	}

	return loads
}

func (t *QueryActivityTracker) CleanupExpired() {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	changed := false
	for queryID, activity := range t.queries {
		if now.Sub(activity.lastSeen) > t.ttl {
			delete(t.queries, queryID)
			changed = true
		}
	}
	if changed {
		t.recomputeGaugesLocked()
	}
}

func (t *QueryActivityTracker) StartCleanup(ctx context.Context) {
	if t == nil {
		return
	}

	interval := t.ttl / 3
	if interval <= 0 || interval > 5*time.Minute {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.CleanupExpired()
		}
	}
}

func (t *QueryActivityTracker) recomputeGaugesLocked() {
	gatewayInflightQueries.Reset()
	counts := make(map[queryActivity]int)
	for _, activity := range t.queries {
		key := queryActivity{
			namespace:    activity.namespace,
			xtrinode:     activity.xtrinode,
			routingGroup: activity.routingGroup,
			state:        activity.state,
		}
		counts[key]++
	}
	for labels, count := range counts {
		gatewayInflightQueries.WithLabelValues(
			labels.namespace,
			labels.xtrinode,
			labels.routingGroup,
			labels.state,
		).Set(float64(count))
	}
}

func normalizeQueryState(state string) string {
	if state == "" {
		return "UNKNOWN"
	}
	return state
}

const alternateCanceledState = "CANCEL" + "LED"

func isTerminalQueryState(state string) bool {
	switch state {
	case "FINISHED", "FAILED", "CANCELED", alternateCanceledState:
		return true
	default:
		return false
	}
}
