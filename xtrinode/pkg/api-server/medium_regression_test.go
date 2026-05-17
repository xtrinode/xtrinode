package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// =============================================================================
// M1: All error responses must be structured JSON (not plain text)
// =============================================================================

func TestMedium_ListRuntimes_ErrorIsJSON(t *testing.T) {
	server, _ := setupTestServer()

	// Force error by using a client that can't list (no scheme registered)
	badScheme := runtime.NewScheme()
	badClient := fake.NewClientBuilder().WithScheme(badScheme).Build()
	server.client = badClient

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes", http.NoBody)
	w := httptest.NewRecorder()

	server.listRuntimes(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	// Must be JSON, not plain text
	var errResp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err, "Error response must be valid JSON")
	assert.Equal(t, "LIST_FAILED", errResp.Code)
}

func TestMedium_GetRuntime_NotFoundIsJSON(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/nonexistent", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var errResp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err, "Not-found response must be valid JSON")
	assert.Equal(t, "NOT_FOUND", errResp.Code)
}

func TestMedium_DeleteRuntime_NotFoundIsJSON(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/runtimes/team-a/nonexistent", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var errResp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err, "Delete not-found response must be valid JSON")
	assert.Equal(t, "NOT_FOUND", errResp.Code)
}

func TestMedium_GetRuntimeStatus_NotFoundIsJSON(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/runtimes/team-a/nonexistent/status", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var errResp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err, "Status not-found response must be valid JSON")
	assert.Equal(t, "NOT_FOUND", errResp.Code)
}

// =============================================================================
// M2: ReadHeaderTimeout must be set on http.Server
// =============================================================================

func TestMedium_ServerHasReadHeaderTimeout(t *testing.T) {
	server, _ := setupTestServer()
	assert.Greater(t, server.server.ReadHeaderTimeout, time.Duration(0),
		"ReadHeaderTimeout must be set to protect against Slowloris attacks")
}

// =============================================================================
// M7: createRuntime must return 409 Conflict for AlreadyExists
// =============================================================================

func TestMedium_CreateRuntime_AlreadyExists_Returns409(t *testing.T) {
	server, cli := setupTestServer()

	// Create runtime first
	existing := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-rt",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}
	require.NoError(t, cli.Create(context.Background(), existing))

	// Try to create again with same name/namespace
	runtimeReq := CreateRuntimeRequest{
		Name:      "existing-rt",
		Namespace: "team-a",
		Size:      "m",
	}
	body, _ := json.Marshal(runtimeReq)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleRuntimes(w, req)

	assert.Equal(t, http.StatusConflict, w.Code, "Duplicate create should return 409 Conflict")
	var errResp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_EXISTS", errResp.Code)
}

// =============================================================================
// M8: sanitizeDNS1123 must trim leading/trailing hyphens
// =============================================================================

func TestMedium_SanitizeDNS1123_TrimsHyphens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "leading slash becomes trimmed",
			input:    "/foo/bar",
			expected: "foo-bar",
		},
		{
			name:     "trailing slash becomes trimmed",
			input:    "foo/bar/",
			expected: "foo-bar",
		},
		{
			name:     "both leading and trailing",
			input:    "/foo/",
			expected: "foo",
		},
		{
			name:     "multiple special chars at edges",
			input:    "///foo///",
			expected: "foo",
		},
		{
			name:     "already clean",
			input:    "foo-bar",
			expected: "foo-bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeDNS1123(tt.input)
			assert.Equal(t, tt.expected, result)
			// Must not start or end with hyphen
			if result != "" {
				assert.NotEqual(t, '-', rune(result[0]), "Must not start with hyphen")
				assert.NotEqual(t, '-', rune(result[len(result)-1]), "Must not end with hyphen")
			}
		})
	}
}

// =============================================================================
// M6: resumeRuntime must release lease on Get/Update failure
// =============================================================================

func TestMedium_ResumeRuntime_ReleasesLeaseOnNotFound(t *testing.T) {
	server, cli := setupTestServer()

	// Resume a non-existent runtime — should acquire lease, fail on Get, release lease
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/nonexistent/resume", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	// Verify lease was released — a second attempt should acquire (not be gated)
	req2 := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/nonexistent/resume", http.NoBody)
	w2 := httptest.NewRecorder()
	server.handleRuntime(w2, req2)

	// Should also be 404 (not 503 gated), proving lease was released
	assert.Equal(t, http.StatusNotFound, w2.Code,
		"Second attempt should get 404 (not 503), proving lease was released")

	_ = cli // use cli to avoid unused warning
}

// =============================================================================
// M10: CreateRuntimeRequest labels must be validated
// =============================================================================

func TestMedium_CreateRuntime_InvalidLabels(t *testing.T) {
	server, _ := setupTestServer()

	tests := []struct {
		name   string
		labels map[string]string
	}{
		{
			name:   "invalid label key with spaces",
			labels: map[string]string{"invalid key": "value"},
		},
		{
			name:   "label value too long",
			labels: map[string]string{"key": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		{
			name:   "invalid label value with spaces",
			labels: map[string]string{"key": "invalid value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeReq := CreateRuntimeRequest{
				Name:      "test-rt",
				Namespace: "default",
				Size:      "s",
				Labels:    tt.labels,
			}
			body, _ := json.Marshal(runtimeReq)
			req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleRuntimes(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var errResp ErrorResponse
			err := json.Unmarshal(w.Body.Bytes(), &errResp)
			require.NoError(t, err)
			assert.Equal(t, "INVALID_LABELS", errResp.Code)
		})
	}
}

func TestMedium_CreateRuntime_ValidLabels(t *testing.T) {
	server, _ := setupTestServer()

	runtimeReq := CreateRuntimeRequest{
		Name:      "labeled-rt",
		Namespace: "default",
		Size:      "s",
		Labels: map[string]string{
			"app.kubernetes.io/name": "test",
			"env":                    "prod",
		},
	}
	body, _ := json.Marshal(runtimeReq)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleRuntimes(w, req)

	assert.Equal(t, http.StatusCreated, w.Code, "Valid labels should be accepted")
}

// =============================================================================
// M9: validateLabels unit tests
// =============================================================================

func TestMedium_ValidateLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		wantErr bool
	}{
		{
			name:    "valid simple labels",
			labels:  map[string]string{"app": "test", "env": "prod"},
			wantErr: false,
		},
		{
			name:    "valid prefixed label",
			labels:  map[string]string{"app.kubernetes.io/name": "test"},
			wantErr: false,
		},
		{
			name:    "empty labels",
			labels:  map[string]string{},
			wantErr: false,
		},
		{
			name:    "empty value is valid",
			labels:  map[string]string{"key": ""},
			wantErr: false,
		},
		{
			name:    "invalid key with space",
			labels:  map[string]string{"bad key": "value"},
			wantErr: true,
		},
		{
			name:    "value exceeds 63 chars",
			labels:  map[string]string{"key": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLabels(tt.labels)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// M9: createLease no longer recurses — returns gated on AlreadyExists race
// =============================================================================

func TestMedium_CreateLease_NoRecursionOnAlreadyExists(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, coordinationv1.AddToScheme(scheme))

	// Pre-create a lease to simulate race condition
	holderIdentity := "race-winner"
	leaseDuration := int32(120)
	renewTime := metav1.NewMicroTime(time.Now())

	existingLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-resume-runtime-rt-default-race-test",
			Namespace: "test-ns",
			Labels: map[string]string{
				"xtrinode.io/lease-key-type": "runtime",
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holderIdentity,
			LeaseDurationSeconds: &leaseDuration,
			RenewTime:            &renewTime,
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingLease).
		Build()

	log := zap.New(zap.UseDevMode(true))
	lm := NewLeaseManager(cli, log, "test-ns", 120*time.Second, "new-holder")

	// This should NOT recurse — should return gated with race winner's info
	result, err := lm.AcquireLease(context.Background(), "rt/default/race-test", LeaseKeyTypeRuntime)
	require.NoError(t, err)
	assert.False(t, result.Acquired, "Should be gated by race winner")
	assert.Equal(t, "race-winner", result.Holder)
}

// =============================================================================
// M4: handleRuntimes method not allowed returns JSON
// =============================================================================

func TestMedium_HandleRuntimes_MethodNotAllowedIsJSON(t *testing.T) {
	server, _ := setupTestServer()

	req := httptest.NewRequestWithContext(context.Background(), "PATCH", "/api/v1/runtimes", http.NoBody)
	w := httptest.NewRecorder()

	server.handleRuntimes(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	var errResp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err, "Method not allowed response must be valid JSON")
	assert.Equal(t, "METHOD_NOT_ALLOWED", errResp.Code)
}

// =============================================================================
// M5: verify resumeRuntime actually works (RetryOnConflict doesn't break it)
// =============================================================================

func TestMedium_ResumeRuntime_StillWorks(t *testing.T) {
	server, cli := setupTestServer()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "works-rt",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: true,
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Suspended",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/runtimes/team-a/works-rt/resume", http.NoBody)
	w := httptest.NewRecorder()
	server.handleRuntime(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	// Verify annotation was applied despite RetryOnConflict wrapper
	var updated analyticsv1.XTrinode
	err := cli.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "works-rt"}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "true", updated.Annotations["xtrinode.analytics.xtrinode.io/resume-requested"])
}
