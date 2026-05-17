package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// =============================================================================
// Regression: triggerResume must use config annotation constants
// =============================================================================

func TestRegression_TriggerResume_UsesConfigAnnotations(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	server, _ := setupTestServer()
	server.client = cli
	server.log = log

	// Create a suspended XTrinode
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-resume",
			Namespace: "default",
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

	// Call triggerResume directly
	err := server.triggerResume(context.Background(), "default", "test-resume", "rt/default/test-resume", "runtime")
	require.NoError(t, err)

	// Verify annotations use config constants, NOT hardcoded "xtrinode.io/..." keys
	var updated analyticsv1.XTrinode
	err = cli.Get(context.Background(), types.NamespacedName{
		Namespace: "default",
		Name:      "test-resume",
	}, &updated)
	require.NoError(t, err)

	// Must use config.ResumeRequestedAnnotation ("xtrinode.analytics.xtrinode.io/resume-requested")
	assert.Equal(t, "true", updated.Annotations[config.ResumeRequestedAnnotation],
		"triggerResume must use config.ResumeRequestedAnnotation, not hardcoded key")
	assert.NotEmpty(t, updated.Annotations[config.ResumeRequestedAtAnnotation],
		"triggerResume must use config.ResumeRequestedAtAnnotation, not hardcoded key")

	// Verify the OLD hardcoded keys are NOT present
	assert.Empty(t, updated.Annotations["xtrinode.io/resume-requested"],
		"Old hardcoded annotation key must NOT be present")
	assert.Empty(t, updated.Annotations["xtrinode.io/resume-requested-at"],
		"Old hardcoded annotation key must NOT be present")
}

// =============================================================================
// Regression: tryPoolGate must release lease on pool-not-empty fallthrough
// =============================================================================

func TestRegression_TryPoolGate_ReleasesLeaseOnFallthrough(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	server, _ := setupTestServer()
	server.client = cli
	server.log = log
	server.leaseManager = NewLeaseManager(cli, log, "default", 2*time.Minute, "test-holder")

	// Create a READY XTrinode in the routing group (pool is NOT empty)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "running-rt",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Routing: &analyticsv1.RoutingSpec{
				RoutingGroup: "test-pool",
			},
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	// Call tryPoolGate — pool is not empty, so it should fall through
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/resume", http.NoBody)
	w := httptest.NewRecorder()
	handled, _ := server.tryPoolGate(w, req, "test-pool", "test")

	// Should NOT be handled (falls through to runtime gate)
	assert.False(t, handled, "tryPoolGate should fall through when pool is not empty")

	// The pool lease should have been RELEASED (deleted)
	// Verify by acquiring it again — should succeed immediately
	poolKey := MakePoolKey("test-pool")
	result, err := server.leaseManager.AcquireLease(context.Background(), poolKey, LeaseKeyTypePool)
	require.NoError(t, err)
	assert.True(t, result.Acquired, "Pool lease should be available after fallthrough (released)")
}

func TestRegression_RouteGroupLookup_MatchesDefaultDedicatedRoutingGroup(t *testing.T) {
	server, cli := setupTestServer()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:    "s",
			Routing: &analyticsv1.RoutingSpec{},
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Suspended",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	candidate := server.pickCandidateFromRoutes(context.Background(), "team-a--runtime")

	require.NotNil(t, candidate)
	assert.Equal(t, "team-a", candidate.Namespace)
	assert.Equal(t, "runtime", candidate.Name)
}

func TestRegression_IsPoolEmpty_MatchesDefaultDedicatedRoutingGroup(t *testing.T) {
	server, cli := setupTestServer()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:    "s",
			Routing: &analyticsv1.RoutingSpec{},
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
		},
	}
	require.NoError(t, cli.Create(context.Background(), xtrinode))

	assert.False(t, server.isPoolEmpty(context.Background(), "team-a--runtime"))
}

func TestRegression_TryPoolGate_ReleasesLeaseWhenNoCandidate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	server, _ := setupTestServer()
	server.client = cli
	server.log = logr.Discard()
	server.leaseManager = NewLeaseManager(cli, logr.Discard(), "default", 2*time.Minute, "test-holder")

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/resume", http.NoBody)
	w := httptest.NewRecorder()
	handled, result := server.tryPoolGate(w, req, "empty-pool", "test")

	require.True(t, handled)
	assert.Equal(t, "error", result)
	assert.Equal(t, http.StatusNotFound, w.Code)

	poolKey := MakePoolKey("empty-pool")
	leaseResult, err := server.leaseManager.AcquireLease(context.Background(), poolKey, LeaseKeyTypePool)
	require.NoError(t, err)
	assert.True(t, leaseResult.Acquired, "Pool lease should be released when no candidate exists")
}

// =============================================================================
// Regression: SuspendLeaseDuration must be used for suspend operations
// =============================================================================

func TestRegression_SuspendLeaseDuration_UsedForSuspend(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	resumeDuration := 60 * time.Second
	suspendDuration := 180 * time.Second // 3x longer than resume

	lm := NewLeaseManager(cli, logr.Discard(), "test-ns", resumeDuration, "test-holder")
	lm.SetSuspendDuration(suspendDuration)

	ctx := context.Background()

	// Acquire a suspend lease
	suspendKey := MakeRuntimeKey("default", "test-rt")
	result, err := lm.AcquireLease(ctx, suspendKey, LeaseKeyTypeSuspend)
	require.NoError(t, err)
	assert.True(t, result.Acquired)

	// Verify the lease duration matches suspendDuration, not resumeDuration
	expectedExpiry := time.Now().Add(suspendDuration)
	assert.WithinDuration(t, expectedExpiry, result.LeaseUntil, 5*time.Second,
		"Suspend lease should use suspendDuration (180s), not resumeDuration (60s)")

	// Verify the K8s Lease object has the correct LeaseDurationSeconds
	leaseName := lm.makeLeaseNameFromKey(suspendKey, LeaseKeyTypeSuspend)
	lease := &coordinationv1.Lease{}
	err = cli.Get(ctx, types.NamespacedName{Namespace: "test-ns", Name: leaseName}, lease)
	require.NoError(t, err)
	assert.Equal(t, int32(suspendDuration.Seconds()), *lease.Spec.LeaseDurationSeconds,
		"K8s Lease LeaseDurationSeconds should match suspendDuration")

	// Now acquire a resume lease — should use resumeDuration
	resumeKey := MakeRuntimeKey("default", "test-rt-resume")
	resumeResult, err := lm.AcquireLease(ctx, resumeKey, LeaseKeyTypeRuntime)
	require.NoError(t, err)
	assert.True(t, resumeResult.Acquired)

	resumeExpiry := time.Now().Add(resumeDuration)
	assert.WithinDuration(t, resumeExpiry, resumeResult.LeaseUntil, 5*time.Second,
		"Resume lease should use resumeDuration (60s), not suspendDuration (180s)")
}

func TestRegression_DurationForKey(t *testing.T) {
	lm := &LeaseManager{
		leaseDuration:   60 * time.Second,
		suspendDuration: 180 * time.Second,
	}

	assert.Equal(t, 60*time.Second, lm.durationForKey(LeaseKeyTypeRuntime))
	assert.Equal(t, 60*time.Second, lm.durationForKey(LeaseKeyTypePool))
	assert.Equal(t, 180*time.Second, lm.durationForKey(LeaseKeyTypeSuspend))
}

// =============================================================================
// Regression: handleUnifiedResume must reject non-POST methods
// =============================================================================

func TestRegression_UnifiedResume_RejectNonPOST(t *testing.T) {
	server, _ := setupTestServer()

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), method, "/api/v1/resume", http.NoBody)
			w := httptest.NewRecorder()

			server.handleUnifiedResume(w, req)

			assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
				"%s should be rejected for unified resume endpoint", method)
		})
	}
}

func TestRegression_UnifiedResume_AcceptsPOST(t *testing.T) {
	server, _ := setupTestServer()

	// POST with valid body should not get 405
	body := `{"routingGroup": "test"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUnifiedResume(w, req)

	// Should NOT be 405 (it may be 404 since no xtrinodes exist, but method is accepted)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code,
		"POST should be accepted for unified resume endpoint")
}

// =============================================================================
// Additional regression: suspend and resume use separate lease key types
// =============================================================================

func TestRegression_SuspendAndResume_SeparateLeaseKeyTypes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = analyticsv1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	lm := NewLeaseManager(cli, log, "test-ns", 2*time.Minute, "test-holder")

	ctx := context.Background()
	runtimeKey := MakeRuntimeKey("default", "test-rt")

	// Acquire resume lease (runtime type)
	resumeResult, err := lm.AcquireLease(ctx, runtimeKey, LeaseKeyTypeRuntime)
	require.NoError(t, err)
	assert.True(t, resumeResult.Acquired, "Resume lease should be acquired")

	// Acquire suspend lease (suspend type) for the SAME runtime — should also succeed
	// because they use different lease key types (different lease names)
	suspendResult, err := lm.AcquireLease(ctx, runtimeKey, LeaseKeyTypeSuspend)
	require.NoError(t, err)
	assert.True(t, suspendResult.Acquired,
		"Suspend lease should be acquired independently from resume lease")
}
