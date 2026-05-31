package autosuspend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func withTestCoordinatorURL(t *testing.T, url string) {
	t.Helper()
	previous := buildCoordinatorURL
	buildCoordinatorURL = func(*analyticsv1.XTrinode) string {
		return url
	}
	t.Cleanup(func() {
		buildCoordinatorURL = previous
	})
}

func TestCheckAutoSuspend(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	logger := log.Log

	// Test case 1: No autoSuspendAfter configured
	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-1",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	shouldSuspend, err := CheckAutoSuspend(ctx, cli, xtrinode1, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if shouldSuspend {
		t.Error("Should not suspend when autoSuspendAfter is not configured")
	}

	// Test case 2: Idle for longer than threshold
	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-2",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
		},
	}

	shouldSuspend, err = CheckAutoSuspend(ctx, cli, xtrinode2, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if !shouldSuspend {
		t.Error("Should suspend when idle for longer than autoSuspendAfter")
	}

	// Test case 3: Already suspended
	xtrinode3 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-3",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        true,
		},
	}

	shouldSuspend, err = CheckAutoSuspend(ctx, cli, xtrinode3, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if shouldSuspend {
		t.Error("Should not suspend when already suspended")
	}

	// Test case 4: No lastActivity, uses creation time
	xtrinode4 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-4",
			Namespace:         "team-a",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-20 * time.Minute)),
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: nil,
		},
	}

	shouldSuspend, err = CheckAutoSuspend(ctx, cli, xtrinode4, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if !shouldSuspend {
		t.Error("Should suspend when idle for longer than autoSuspendAfter (using creation time)")
	}

	// Test case 5: Idle for less than threshold
	recentTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	xtrinode5 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-5",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &recentTime,
		},
	}

	shouldSuspend, err = CheckAutoSuspend(ctx, cli, xtrinode5, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if shouldSuspend {
		t.Error("Should not suspend when idle for less than autoSuspendAfter")
	}
}

func TestCheckAutoSuspend_ActiveWakeWindow(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	logger := log.Log

	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute)) // idle > threshold

	// Active wake window — should NOT auto-suspend even though idle > threshold
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-wake-active",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
			Wake: &analyticsv1.WakeState{
				MinWorkers: 3,
				ExpiresAt:  metav1.NewTime(time.Now().Add(5 * time.Minute)), // still active
			},
		},
	}

	shouldSuspend, err := CheckAutoSuspend(ctx, cli, xtrinode, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if shouldSuspend {
		t.Error("Should NOT auto-suspend during active wake window")
	}
}

func TestCheckAutoSuspend_ExpiredWakeWindow(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	logger := log.Log

	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute)) // idle > threshold

	// Expired wake window — should proceed with normal auto-suspend check
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-wake-expired",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
			Wake: &analyticsv1.WakeState{
				MinWorkers: 3,
				ExpiresAt:  metav1.NewTime(time.Now().Add(-1 * time.Minute)), // expired
			},
		},
	}

	shouldSuspend, err := CheckAutoSuspend(ctx, cli, xtrinode, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if !shouldSuspend {
		t.Error("Should auto-suspend when wake window is expired and idle > threshold")
	}
}

func TestCheckAutoSuspend_NoWakeWindow(t *testing.T) {
	ctx := context.Background()
	cli := fake.NewClientBuilder().Build()
	logger := log.Log

	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute))

	// No wake window at all
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-no-wake",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
			Wake:         nil, // no wake
		},
	}

	shouldSuspend, err := CheckAutoSuspend(ctx, cli, xtrinode, logger)
	if err != nil {
		t.Fatalf("CheckAutoSuspend failed: %v", err)
	}
	if !shouldSuspend {
		t.Error("Should auto-suspend when no wake window and idle > threshold")
	}
}

func TestUpdateLastActivity(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-1",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(xtrinode).
		Build()

	logger := log.Log
	ctx := context.Background()

	err := UpdateLastActivity(ctx, cli, xtrinode, logger)
	if err != nil {
		t.Fatalf("UpdateLastActivity failed: %v", err)
	}

	// Verify status was updated
	var updated analyticsv1.XTrinode
	err = cli.Get(ctx, client.ObjectKey{Name: "test-1", Namespace: "team-a"}, &updated)
	if err != nil {
		t.Fatalf("Failed to get updated xtrinode: %v", err)
	}

	if updated.Status.LastActivity == nil {
		t.Error("LastActivity should be set")
	}
}

func TestQueryTrinoMetrics(t *testing.T) {
	// Test with Prometheus format metrics
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`trino_query_queued{state="QUEUED"} 3.0
trino_query_running{state="RUNNING"} 2.0
trino_query_queued{state="QUEUED",user="test"} 1.0
trino_query_running 4
`)); err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	logger := logr.Discard()
	count, err := queryTrinoMetrics(ctx, server.URL, logger)
	if err != nil {
		t.Fatalf("queryTrinoMetrics failed: %v", err)
	}
	if count != 10.0 {
		t.Errorf("Expected 10.0 active queries, got %f", count)
	}
}

func TestQueryTrinoMetrics_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx := context.Background()
	logger := logr.Discard()
	_, err := queryTrinoMetrics(ctx, server.URL, logger)
	if err == nil {
		t.Error("Expected error for non-200 status")
	}
}

func TestQueryTrinoQueryActivity(t *testing.T) {
	// Test with JSON format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, config.TrinoOperatorUser, r.Header.Get(config.TrinoUserHeader))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		queries := []map[string]interface{}{
			{"queryId": "1", "state": "RUNNING"},
			{"queryId": "2", "state": "QUEUED"},
			{"queryId": "3", "state": "FINISHED"},
		}
		if err := json.NewEncoder(w).Encode(queries); err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	logger := logr.Discard()
	activity, err := queryTrinoQueryActivity(ctx, server.URL, logger)
	if err != nil {
		t.Fatalf("queryTrinoQueryActivity failed: %v", err)
	}
	if activity.ActiveQueries != 2.0 {
		t.Errorf("Expected 2.0 active queries, got %f", activity.ActiveQueries)
	}
}

func TestQueryTrinoQueryActivity_NonTerminalStatesAreActive(t *testing.T) {
	alternateCanceledState := "CANCEL" + "LED"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		queries := []map[string]interface{}{
			{"queryId": "1", "state": "WAITING_FOR_PREREQUISITES"},
			{"queryId": "2", "state": "WAITING_FOR_RESOURCES"},
			{"queryId": "3", "state": "DISPATCHING"},
			{"queryId": "4", "state": "PLANNING"},
			{"queryId": "5", "state": "STARTING"},
			{"queryId": "6", "state": "FINISHING"},
			{"queryId": "7", "state": "UNKNOWN"},
			{"queryId": "8"},
			{"queryId": "9", "state": "FINISHED"},
			{"queryId": "10", "state": "FAILED"},
			{"queryId": "11", "state": "CANCELED"},
			{"queryId": "12", "state": alternateCanceledState},
		}
		require.NoError(t, json.NewEncoder(w).Encode(queries))
	}))
	defer server.Close()

	activity, err := queryTrinoQueryActivity(context.Background(), server.URL, logr.Discard())
	require.NoError(t, err)
	require.Equal(t, 8.0, activity.ActiveQueries)
}

func TestQueryTrinoQueryActivityWithCredentialSendsBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "lifecycle-control", r.Header.Get(config.TrinoUserHeader))
		username, password, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "lifecycle-control", username)
		require.Equal(t, "secret", password)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]string{}))
	}))
	defer server.Close()

	_, err := queryTrinoQueryActivityWithCredential(
		context.Background(),
		server.URL,
		controlauth.Credential{Username: "lifecycle-control", Password: "secret", HasPassword: true},
		logr.Discard(),
	)
	require.NoError(t, err)
}

func TestQueryTrinoQueryActivityTracksFinishedQueryTimestamp(t *testing.T) {
	latest := time.Now().Add(-10 * time.Second).UTC().Truncate(time.Millisecond)
	older := latest.Add(-5 * time.Minute)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, config.TrinoOperatorUser, r.Header.Get(config.TrinoUserHeader))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		queries := []map[string]interface{}{
			{
				"queryId": "finished-short-query",
				"state":   "FINISHED",
				"queryStats": map[string]interface{}{
					"createTime": older.Format(time.RFC3339Nano),
					"endTime":    latest.Format(time.RFC3339Nano),
				},
			},
			{
				"queryId": "running-query",
				"state":   "RUNNING",
				"queryStats": map[string]interface{}{
					"createTime": older.Format(time.RFC3339Nano),
				},
			},
		}
		if err := json.NewEncoder(w).Encode(queries); err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	activity, err := queryTrinoQueryActivity(context.Background(), server.URL, logr.Discard())
	require.NoError(t, err)
	require.Equal(t, 1.0, activity.ActiveQueries)
	require.NotNil(t, activity.LatestActivity)
	require.True(t, activity.LatestActivity.Equal(latest), "expected latest activity %s, got %s", latest, activity.LatestActivity)
}

func TestUpdateLastActivityFromQueryActivity_CompletedQueryUpdatesStaleLastActivity(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	oldActivity := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	queryActivityTime := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Second)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-history",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldActivity,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(xtrinode).
		Build()
	require.NoError(t, UpdateLastActivityAt(context.Background(), cli, xtrinode, oldActivity.Time, logr.Discard()))
	require.NoError(t, cli.Get(context.Background(), client.ObjectKey{Name: "test-history", Namespace: "team-a"}, xtrinode))

	err := updateLastActivityFromQueryActivity(context.Background(), cli, xtrinode, queryActivity{
		LatestActivity: &queryActivityTime,
	}, logr.Discard())
	require.NoError(t, err)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(context.Background(), client.ObjectKey{Name: "test-history", Namespace: "team-a"}, &updated))
	require.NotNil(t, updated.Status.LastActivity)
	require.True(t, updated.Status.LastActivity.Equal(&metav1.Time{Time: queryActivityTime}),
		"expected LastActivity %s, got %s", queryActivityTime, updated.Status.LastActivity.Time)
}

func TestUpdateLastActivityFromQueryActivity_IgnoresOlderHistory(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	lastActivity := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	olderQueryActivity := lastActivity.Add(-5 * time.Minute)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-old-history",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &lastActivity,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(xtrinode).
		Build()
	xtrinode.Status.LastActivity = &lastActivity

	err := updateLastActivityFromQueryActivity(context.Background(), cli, xtrinode, queryActivity{
		LatestActivity: &olderQueryActivity,
	}, logr.Discard())
	require.NoError(t, err)

	require.NotNil(t, xtrinode.Status.LastActivity)
	require.True(t, xtrinode.Status.LastActivity.Equal(&lastActivity),
		"older query history should not move LastActivity backwards")
}

func TestQueryTrinoQueryActivity_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx := context.Background()
	logger := logr.Discard()
	_, err := queryTrinoQueryActivity(ctx, server.URL, logger)
	if err == nil {
		t.Error("Expected error for non-200 status")
	}
}

func TestQueryTrinoQueryActivity_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("invalid json")); err != nil {
			panic(err)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	logger := logr.Discard()
	_, err := queryTrinoQueryActivity(ctx, server.URL, logger)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestUpdateLastActivityIfQueriesActive(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-1",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(xtrinode).
		Build()

	logger := log.Log
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case config.MetricsPath:
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`trino_query_queued{state="QUEUED"} 0.0
trino_query_running{state="RUNNING"} 0.0
`)); err != nil {
				panic(err)
			}
		case config.QueryAPIPath:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`[]`)); err != nil {
				panic(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	withTestCoordinatorURL(t, server.URL)

	err := UpdateLastActivityIfQueriesActive(ctx, cli, xtrinode, logger)
	require.NoError(t, err)
}

func TestUpdateLastActivityIfQueriesActive_QueryAPIKeepsNonTerminalActive(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nonterminal-active",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(xtrinode).
		Build()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case config.MetricsPath:
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`trino_query_queued{state="QUEUED"} 0.0
trino_query_running{state="RUNNING"} 0.0
`))
			require.NoError(t, err)
		case config.QueryAPIPath:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			require.NoError(t, json.NewEncoder(w).Encode([]map[string]string{
				{"queryId": "planning-query", "state": "PLANNING"},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	withTestCoordinatorURL(t, server.URL)

	err := UpdateLastActivityIfQueriesActive(context.Background(), cli, xtrinode, logr.Discard())
	require.NoError(t, err)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(context.Background(), client.ObjectKey{Name: xtrinode.Name, Namespace: xtrinode.Namespace}, &updated))
	require.NotNil(t, updated.Status.LastActivity)
	require.True(t, updated.Status.LastActivity.After(oldTime.Time))
}

func TestAutoSuspendIfNeeded(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute))

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-1",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()
	server := httptest.NewServer(noQueryActivityHandler())
	defer server.Close()
	withTestCoordinatorURL(t, server.URL)

	logger := log.Log
	ctx := context.Background()

	suspended, err := AutoSuspendIfNeeded(ctx, cli, xtrinode, logger)
	if err != nil {
		t.Fatalf("AutoSuspendIfNeeded failed: %v", err)
	}
	if !suspended {
		t.Error("Should suspend when idle for longer than autoSuspendAfter")
	}

	// Verify annotation was set (controller will read annotation and suspend during reconciliation)
	var updated analyticsv1.XTrinode
	err = cli.Get(ctx, client.ObjectKey{Name: "test-1", Namespace: "team-a"}, &updated)
	if err != nil {
		t.Fatalf("Failed to get updated xtrinode: %v", err)
	}
	if updated.Annotations == nil {
		t.Error("Annotations should be set")
	}
	if val, ok := updated.Annotations["xtrinode.analytics.xtrinode.io/auto-suspend-requested"]; !ok || val != "true" {
		t.Error("Auto-suspend annotation should be set to 'true'")
	}
}

func TestAutoSuspendIfNeeded_NotIdle(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	recentTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-1",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &recentTime,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()
	server := httptest.NewServer(noQueryActivityHandler())
	defer server.Close()
	withTestCoordinatorURL(t, server.URL)

	logger := log.Log
	ctx := context.Background()

	suspended, err := AutoSuspendIfNeeded(ctx, cli, xtrinode, logger)
	if err != nil {
		t.Fatalf("AutoSuspendIfNeeded failed: %v", err)
	}
	if suspended {
		t.Error("Should not suspend when not idle")
	}
}

func TestAutoSuspendIfNeeded_FailsClosedWhenActivityCannotBeChecked(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	autoSuspendAfter := metav1.Duration{Duration: 15 * time.Minute}
	oldTime := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-fail-closed",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:             "s",
			AutoSuspendAfter: &autoSuspendAfter,
			Suspended:        false,
		},
		Status: analyticsv1.XTrinodeStatus{
			LastActivity: &oldTime,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withTestCoordinatorURL(t, server.URL)

	suspended, err := AutoSuspendIfNeeded(context.Background(), cli, xtrinode, logr.Discard())
	require.Error(t, err)
	require.False(t, suspended)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(context.Background(), client.ObjectKey{Name: "test-fail-closed", Namespace: "team-a"}, &updated))
	require.Empty(t, updated.Annotations[config.AutoSuspendRequestedAnnotation])
}

func noQueryActivityHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case config.MetricsPath:
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`trino_query_queued{state="QUEUED"} 0.0
trino_query_running{state="RUNNING"} 0.0
`)); err != nil {
				panic(err)
			}
		case config.QueryAPIPath:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`[]`)); err != nil {
				panic(err)
			}
		default:
			http.NotFound(w, r)
		}
	})
}
