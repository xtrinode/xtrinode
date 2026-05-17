package autosuspend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/httpclient"
	"github.com/xtrinode/xtrinode/internal/retry"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
	"github.com/xtrinode/xtrinode/internal/trino/controlendpoint"
	"github.com/xtrinode/xtrinode/pkg/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var buildCoordinatorURL = controlendpoint.CoordinatorURL

// CheckAutoSuspend checks if a XTrinode should be auto-suspended based on idle time
// Returns true if the XTrinode should be suspended, false otherwise
func CheckAutoSuspend(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error) {
	// If auto-suspend is not configured, don't auto-suspend
	if xtrinode.Spec.AutoSuspendAfter == nil {
		return false, nil
	}

	// If already suspended, no need to check
	if xtrinode.Spec.Suspended {
		return false, nil
	}

	// Never auto-suspend during an active wake window.
	// The wake window represents explicit user intent to keep the cluster available.
	// Without this guard, a stale lastActivity timestamp could cause immediate
	// auto-suspend right after resume, defeating the wake window's purpose.
	if xtrinode.Status.Wake != nil && time.Now().Before(xtrinode.Status.Wake.ExpiresAt.Time) {
		log.V(1).Info("Skipping auto-suspend check - wake window is active",
			"xtrinode", xtrinode.Name,
			"wakeExpiresAt", xtrinode.Status.Wake.ExpiresAt.Time)
		return false, nil
	}

	// Get last activity time
	lastActivity := xtrinode.Status.LastActivity
	if lastActivity == nil {
		// If no last activity recorded, use creation time
		lastActivity = &xtrinode.CreationTimestamp
	}

	// Calculate idle duration
	idleDuration := time.Since(lastActivity.Time)
	autoSuspendAfter := xtrinode.Spec.AutoSuspendAfter.Duration

	// Update idle time metric
	metrics.AutoSuspendIdleTime.WithLabelValues(xtrinode.Namespace, xtrinode.Name).Set(idleDuration.Seconds())

	// Check if idle duration exceeds auto-suspend threshold
	if idleDuration >= autoSuspendAfter {
		log.Info("XTrinode idle for longer than autoSuspendAfter, should auto-suspend",
			"xtrinode", xtrinode.Name,
			"idleDuration", idleDuration,
			"autoSuspendAfter", autoSuspendAfter,
			"lastActivity", lastActivity.Time)
		return true, nil
	}

	return false, nil
}

// UpdateLastActivity updates the last activity timestamp in XTrinode status
func UpdateLastActivity(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	return UpdateLastActivityAt(ctx, cli, xtrinode, time.Now(), log)
}

// UpdateLastActivityAt updates last activity to a specific query-observed timestamp.
func UpdateLastActivityAt(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, activityTime time.Time, log logr.Logger) error {
	if activityTime.After(time.Now()) {
		activityTime = time.Now()
	}
	lastActivity := metav1.NewTime(activityTime.UTC())
	if err := retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
		func() error {
			key := client.ObjectKeyFromObject(xtrinode)
			return cli.Get(ctx, key, xtrinode)
		},
		func() error {
			xtrinode.Status.LastActivity = &lastActivity
			return cli.Status().Update(ctx, xtrinode)
		},
	); err != nil {
		return fmt.Errorf("failed to update last activity: %w", err)
	}

	xtrinode.Status.LastActivity = &lastActivity
	log.V(1).Info("Updated last activity", "xtrinode", xtrinode.Name, "time", activityTime.UTC())
	return nil
}

// AutoSuspendIfNeeded checks auto-suspend conditions and suspends if needed
func AutoSuspendIfNeeded(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error) {
	// First, check for active queries and update lastActivity if queries are running
	if err := UpdateLastActivityIfQueriesActive(ctx, cli, xtrinode, log); err != nil {
		return false, fmt.Errorf("failed to check query activity before auto-suspend: %w", err)
	} else {
		var observedLastActivity *metav1.Time
		if xtrinode.Status.LastActivity != nil {
			observedLastActivity = xtrinode.Status.LastActivity.DeepCopy()
		}
		// If lastActivity was updated, refresh xtrinode object to get latest status
		// This ensures CheckAutoSuspend uses the updated lastActivity timestamp
		if err := cli.Get(ctx, client.ObjectKey{
			Name:      xtrinode.Name,
			Namespace: xtrinode.Namespace,
		}, xtrinode); err != nil {
			log.V(1).Info("Failed to refresh xtrinode after lastActivity update", "error", err)
			// Continue with the object already in hand; a later reconcile observes the refreshed status.
		}
		if observedLastActivity != nil &&
			(xtrinode.Status.LastActivity == nil || observedLastActivity.After(xtrinode.Status.LastActivity.Time)) {
			xtrinode.Status.LastActivity = observedLastActivity
		}
	}

	shouldSuspend, err := CheckAutoSuspend(ctx, cli, xtrinode, log)
	if err != nil {
		return false, err
	}

	if !shouldSuspend {
		return false, nil
	}

	// Use annotation-based coordination to ensure WorkerPool mutex protection
	// Controller will read annotation and apply changes during reconciliation
	if xtrinode.Annotations == nil {
		xtrinode.Annotations = make(map[string]string)
	}
	xtrinode.Annotations[config.AutoSuspendRequestedAnnotation] = "true"
	xtrinode.Annotations[config.AutoSuspendRequestedAtAnnotation] = metav1.Now().Format(time.RFC3339)

	// Update with retry logic
	if err := retry.OnConflictWithRefresh(ctx, retry.DefaultConfig(), log,
		func() error {
			key := client.ObjectKeyFromObject(xtrinode)
			return cli.Get(ctx, key, xtrinode)
		},
		func() error {
			if xtrinode.Annotations == nil {
				xtrinode.Annotations = make(map[string]string)
			}
			xtrinode.Annotations[config.AutoSuspendRequestedAnnotation] = "true"
			xtrinode.Annotations[config.AutoSuspendRequestedAtAnnotation] = metav1.Now().Format(time.RFC3339)
			return cli.Update(ctx, xtrinode)
		},
	); err != nil {
		return false, fmt.Errorf("failed to request auto-suspend: %w", err)
	}

	log.Info("Auto-suspended XTrinode due to idle period",
		"xtrinode", xtrinode.Name,
		"autoSuspendAfter", xtrinode.Spec.AutoSuspendAfter.Duration)

	return true, nil
}

// UpdateLastActivityIfQueriesActive checks Trino coordinator for active queries and updates lastActivity
// This prevents auto-suspend when queries are actively running
// Uses the Trino HTTP API directly.
func UpdateLastActivityIfQueriesActive(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	// Query the generated Trino coordinator HTTP service endpoint directly.
	coordinatorURL := buildCoordinatorURL(xtrinode)
	credential, err := controlauth.CredentialFromXTrinode(ctx, cli, xtrinode)
	if err != nil {
		return fmt.Errorf("failed to load Trino control credential: %w", err)
	}

	// Try Trino /metrics endpoint first (Prometheus format)
	metricsURL := coordinatorURL + config.MetricsPath
	activeQueries, err := queryTrinoMetricsWithCredential(ctx, metricsURL, credential, log)
	if err != nil {
		metricsErr := err
		log.V(1).Info("Failed to query Trino metrics endpoint, trying /v1/query", "error", err)
		// Fallback: Try Trino REST API /v1/query endpoint
		queryURL := coordinatorURL + config.QueryAPIPath
		activity, activityErr := queryTrinoQueryActivityWithCredential(ctx, queryURL, credential, log)
		err = activityErr
		if err != nil {
			return fmt.Errorf("failed to query Trino activity via metrics and query API: metrics: %v; query API: %w", metricsErr, err)
		}
		return updateLastActivityFromQueryActivity(ctx, cli, xtrinode, activity, log)
	}

	// If there are active queries, update lastActivity
	if activeQueries > 0 {
		log.V(1).Info("Active queries detected, updating lastActivity",
			"xtrinode", xtrinode.Name,
			"activeQueries", activeQueries)
		return UpdateLastActivity(ctx, cli, xtrinode, log)
	}

	// Metrics only expose current activity. A short query can start and finish
	// between autosuspend polls, so inspect Trino's query history before
	// declaring the runtime idle.
	queryURL := coordinatorURL + config.QueryAPIPath
	activity, err := queryTrinoQueryActivityWithCredential(ctx, queryURL, credential, log)
	if err != nil {
		return fmt.Errorf("failed to query Trino query history after zero active metrics: %w", err)
	}
	return updateLastActivityFromQueryActivity(ctx, cli, xtrinode, activity, log)
}

func updateLastActivityFromQueryActivity(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, activity queryActivity, log logr.Logger) error {
	if activity.ActiveQueries > 0 {
		log.V(1).Info("Active queries detected, updating lastActivity",
			"xtrinode", xtrinode.Name,
			"activeQueries", activity.ActiveQueries)
		return UpdateLastActivity(ctx, cli, xtrinode, log)
	}

	if activity.LatestActivity == nil {
		return nil
	}

	lastActivity := xtrinode.Status.LastActivity
	if lastActivity == nil {
		lastActivity = &xtrinode.CreationTimestamp
	}
	if !activity.LatestActivity.After(lastActivity.Time) {
		return nil
	}

	log.V(1).Info("Recent completed query detected, updating lastActivity",
		"xtrinode", xtrinode.Name,
		"queryActivity", *activity.LatestActivity,
		"previousLastActivity", lastActivity.Time)
	return UpdateLastActivityAt(ctx, cli, xtrinode, *activity.LatestActivity, log)
}

type queryActivity struct {
	ActiveQueries  float64
	LatestActivity *time.Time
}

// queryTrinoQueryActivity queries Trino REST API /v1/query endpoint and returns
// current active count plus the newest timestamp observed in query history.
func queryTrinoQueryActivity(ctx context.Context, queryURL string, log logr.Logger) (queryActivity, error) {
	return queryTrinoQueryActivityWithCredential(ctx, queryURL, controlauth.Credential{Username: config.TrinoOperatorUser}, log)
}

func queryTrinoQueryActivityWithCredential(ctx context.Context, queryURL string, credential controlauth.Credential, log logr.Logger) (queryActivity, error) {
	var activity queryActivity

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, http.NoBody)
	if err != nil {
		return activity, fmt.Errorf("failed to create request: %w", err)
	}
	controlauth.ApplyRequestAuth(req, credential)

	// Use retry client for resilient query API calls
	httpClient := httpclient.NewRetryClient(log)
	resp, err := httpClient.Do(req)
	if err != nil {
		return activity, fmt.Errorf("failed to query Trino API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return activity, fmt.Errorf("trino API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return activity, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse JSON response
	// Format: [{"queryId":"...","query":"...","state":"RUNNING",...}, ...]
	var queries []map[string]interface{}
	if err := json.Unmarshal(body, &queries); err != nil {
		return activity, fmt.Errorf("failed to parse Trino API response: %w", err)
	}

	// Count active queries (state: QUEUED or RUNNING)
	for _, query := range queries {
		if state, ok := query["state"].(string); ok {
			if state == "QUEUED" || state == "RUNNING" {
				activity.ActiveQueries++
			}
		}
		if queryTime, ok := latestQueryTimestamp(query); ok {
			if activity.LatestActivity == nil || queryTime.After(*activity.LatestActivity) {
				queryTime = queryTime.UTC()
				activity.LatestActivity = &queryTime
			}
		}
	}

	return activity, nil
}

// queryTrinoMetrics queries Trino /metrics endpoint (Prometheus format) and returns active queries count
func queryTrinoMetrics(ctx context.Context, metricsURL string, log logr.Logger) (float64, error) {
	return queryTrinoMetricsWithCredential(ctx, metricsURL, controlauth.Credential{Username: config.TrinoOperatorUser}, log)
}

func queryTrinoMetricsWithCredential(ctx context.Context, metricsURL string, credential controlauth.Credential, log logr.Logger) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", metricsURL, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	controlauth.ApplyRequestAuth(req, credential)

	// Use retry client for resilient metrics queries
	httpClient := httpclient.NewRetryClient(log)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query Trino metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("trino metrics returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse Prometheus format metrics
	// Format: trino_query_queued{...} 5.0\n trino_query_running{...} 2.0\n
	metricsText := string(body)

	// Extract trino_query_queued and trino_query_running values using regex.
	// Trino/JMX exporters may emit these metrics with or without labels.
	metricValue := `([-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?)`
	reQueued := regexp.MustCompile(`trino_query_queued(?:\{[^}]*\})?\s+` + metricValue)
	reRunning := regexp.MustCompile(`trino_query_running(?:\{[^}]*\})?\s+` + metricValue)

	var total float64

	// Sum all queued queries
	matches := reQueued.FindAllStringSubmatch(metricsText, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			if val, err := strconv.ParseFloat(match[1], 64); err == nil {
				total += val
			}
		}
	}

	// Sum all running queries
	matches = reRunning.FindAllStringSubmatch(metricsText, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			if val, err := strconv.ParseFloat(match[1], 64); err == nil {
				total += val
			}
		}
	}

	return total, nil
}

func latestQueryTimestamp(query map[string]interface{}) (time.Time, bool) {
	fields := []string{"endTime", "updateTime", "lastHeartbeat", "executionStartTime", "createTime", "created"}
	var latest time.Time
	found := false

	for _, field := range fields {
		if ts, ok := parseQueryTimestamp(query[field]); ok {
			if !found || ts.After(latest) {
				latest = ts
				found = true
			}
		}
	}

	if stats, ok := query["queryStats"].(map[string]interface{}); ok {
		for _, field := range fields {
			if ts, ok := parseQueryTimestamp(stats[field]); ok {
				if !found || ts.After(latest) {
					latest = ts
					found = true
				}
			}
		}
	}

	return latest, found
}

func parseQueryTimestamp(value interface{}) (time.Time, bool) {
	text, ok := value.(string)
	if !ok || text == "" {
		return time.Time{}, false
	}

	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.000 MST",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05.000 -0700",
		"2006-01-02 15:04:05 -0700",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, text); err == nil {
			return parsed.UTC(), true
		}
	}

	return time.Time{}, false
}
