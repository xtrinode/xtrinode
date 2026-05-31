package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestQueryActivityTrackerObserveAndTerminalState(t *testing.T) {
	gatewayInflightQueries.Reset()
	tracker := NewQueryActivityTracker(time.Minute)

	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", "QUEUED")
	require.Equal(t, 1.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "QUEUED")))

	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", "RUNNING")
	require.Equal(t, 0.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "QUEUED")))
	require.Equal(t, 1.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "RUNNING")))

	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", "FINISHED")
	require.Equal(t, 0.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "RUNNING")))
}

func TestQueryActivityTrackerTreatsNonTerminalStatesAsActive(t *testing.T) {
	gatewayInflightQueries.Reset()
	tracker := NewQueryActivityTracker(time.Minute)

	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", " planning ")
	require.Equal(t, 1.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "PLANNING")))

	load := tracker.BackendLoads()["http://trino-1:8080"]
	require.Equal(t, 1, load.RunningQueries)
	require.Equal(t, 0, load.QueuedQueries)

	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", "FAILED")
	require.Equal(t, 0.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "PLANNING")))
}

func TestQueryActivityTrackerCleanupExpired(t *testing.T) {
	gatewayInflightQueries.Reset()
	now := time.Now()
	tracker := NewQueryActivityTracker(time.Second)
	tracker.now = func() time.Time { return now }

	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", "QUEUED")
	require.Equal(t, 1.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "QUEUED")))

	now = now.Add(2 * time.Second)
	tracker.CleanupExpired()
	require.Equal(t, 0.0, testutil.ToFloat64(gatewayInflightQueries.WithLabelValues("team-a", "test", "test", "QUEUED")))
}

func TestQueryActivityTrackerStartCleanup(t *testing.T) {
	gatewayInflightQueries.Reset()
	now := time.Now()
	tracker := NewQueryActivityTracker(3 * time.Millisecond)
	tracker.now = func() time.Time { return now }
	tracker.Observe("q1", "team-a", "test", "test", "http://trino-1:8080", "QUEUED")
	now = now.Add(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tracker.StartCleanup(ctx)
		close(done)
	}()
	defer func() {
		<-done
	}()
	defer cancel()

	require.Eventually(t, func() bool {
		tracker.mu.Lock()
		defer tracker.mu.Unlock()
		return len(tracker.queries) == 0
	}, 200*time.Millisecond, time.Millisecond)
}

func TestQueryActivityTrackerStartCleanupReturnsForNilAndCanceledContext(t *testing.T) {
	var nilTracker *QueryActivityTracker
	nilTracker.StartCleanup(context.Background())

	tracker := NewQueryActivityTracker(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		tracker.StartCleanup(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartCleanup did not return after context cancellation")
	}
}
