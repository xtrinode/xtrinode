package apiserver

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLeaseManager_AcquireLease_CreateNew(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, coordinationv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	lm := NewLeaseManager(fakeClient, logr.Discard(), "test-ns", 120*time.Second, "test-holder")

	ctx := context.Background()
	key := "rt/default/test-runtime"
	keyType := LeaseKeyTypeRuntime

	result, err := lm.AcquireLease(ctx, key, keyType)

	require.NoError(t, err)
	assert.True(t, result.Acquired, "Lease should be acquired for new key")
	assert.Equal(t, "test-holder", result.Holder)
	assert.WithinDuration(t, time.Now().Add(120*time.Second), result.LeaseUntil, 5*time.Second)

	// Verify lease was created in K8s
	leaseName := lm.makeLeaseNameFromKey(key, keyType)
	lease := &coordinationv1.Lease{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "test-ns", Name: leaseName}, lease)
	require.NoError(t, err)
	assert.Equal(t, "test-holder", *lease.Spec.HolderIdentity)
}

func TestLeaseManager_AcquireLease_Gated(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, coordinationv1.AddToScheme(scheme))

	// Create existing lease that's still valid (standard semantics)
	// RenewTime = now, LeaseDurationSeconds = 120
	// Expiry = RenewTime + LeaseDurationSeconds = now + 120s
	now := metav1.Now()
	renewTime := metav1.NewMicroTime(now.Time)
	holderIdentity := "existing-holder"
	leaseDuration := int32(120)

	existingLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-resume-runtime-rt-default-test-runtime",
			Namespace: "test-ns",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holderIdentity,
			LeaseDurationSeconds: &leaseDuration,
			RenewTime:            &renewTime,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingLease).
		Build()

	lm := NewLeaseManager(fakeClient, logr.Discard(), "test-ns", 120*time.Second, "test-holder")

	ctx := context.Background()
	key := "rt/default/test-runtime"
	keyType := LeaseKeyTypeRuntime

	result, err := lm.AcquireLease(ctx, key, keyType)

	require.NoError(t, err)
	assert.False(t, result.Acquired, "Lease should be gated (already held)")
	assert.Equal(t, "existing-holder", result.Holder)
	// Calculate expected expiry: RenewTime + LeaseDurationSeconds
	expectedExpiry := renewTime.Add(time.Duration(leaseDuration) * time.Second)
	assert.WithinDuration(t, expectedExpiry, result.LeaseUntil, 1*time.Second)
}

func TestLeaseManager_AcquireLease_UpdateExpired(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, coordinationv1.AddToScheme(scheme))

	// Create expired lease (standard semantics)
	now := metav1.Now()
	// RenewTime was 130 seconds ago, LeaseDurationSeconds = 120
	// So expiry = RenewTime + 120s = 10 seconds ago (expired)
	expiredRenewTime := metav1.NewMicroTime(now.Add(-130 * time.Second))
	holderIdentity := "old-holder"
	leaseDuration := int32(120)

	expiredLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-resume-runtime-rt-default-test-runtime",
			Namespace: "test-ns",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holderIdentity,
			LeaseDurationSeconds: &leaseDuration,
			RenewTime:            &expiredRenewTime,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(expiredLease).
		Build()

	lm := NewLeaseManager(fakeClient, logr.Discard(), "test-ns", 120*time.Second, "new-holder")

	ctx := context.Background()
	key := "rt/default/test-runtime"
	keyType := LeaseKeyTypeRuntime

	result, err := lm.AcquireLease(ctx, key, keyType)

	require.NoError(t, err)
	assert.True(t, result.Acquired, "Expired lease should be acquired")
	assert.Equal(t, "new-holder", result.Holder)
	assert.WithinDuration(t, time.Now().Add(120*time.Second), result.LeaseUntil, 5*time.Second)

	// Verify lease was updated
	lease := &coordinationv1.Lease{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "test-ns", Name: expiredLease.Name}, lease)
	require.NoError(t, err)
	assert.Equal(t, "new-holder", *lease.Spec.HolderIdentity)
}

func TestMakeRuntimeKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		wname     string
		expected  string
	}{
		{
			name:      "simple key",
			namespace: "default",
			wname:     "runtime-a",
			expected:  "rt/default/runtime-a",
		},
		{
			name:      "with special chars",
			namespace: "prod-team",
			wname:     "runtime_123",
			expected:  "rt/prod-team/runtime_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeRuntimeKey(tt.namespace, tt.wname)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMakePoolKey(t *testing.T) {
	tests := []struct {
		name         string
		routingGroup string
		expected     string
	}{
		{
			name:         "simple pool",
			routingGroup: "shared",
			expected:     "pool/shared",
		},
		{
			name:         "with special chars",
			routingGroup: "team-data-eng",
			expected:     "pool/team-data-eng",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakePoolKey(tt.routingGroup)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLeaseManager_makeLeaseNameFromKey_DNS1123Compliance(t *testing.T) {
	lm := NewLeaseManager(nil, logr.Discard(), "test-ns", 120*time.Second, "test-holder")

	tests := []struct {
		name     string
		key      string
		keyType  LeaseKeyType
		validate func(t *testing.T, leaseName string)
	}{
		{
			name:    "simple runtime key",
			key:     "rt/default/test-runtime",
			keyType: LeaseKeyTypeRuntime,
			validate: func(t *testing.T, leaseName string) {
				assert.Equal(t, "xtrinode-resume-runtime-rt-default-test-runtime", leaseName)
				assert.LessOrEqual(t, len(leaseName), 63, "Lease name must be <= 63 chars")
			},
		},
		{
			name:    "simple pool key",
			key:     "pool/shared",
			keyType: LeaseKeyTypePool,
			validate: func(t *testing.T, leaseName string) {
				assert.Equal(t, "xtrinode-resume-pool-pool-shared", leaseName)
				assert.LessOrEqual(t, len(leaseName), 63)
			},
		},
		{
			name:    "long key with special chars",
			key:     "rt/very-long-namespace-name/very-long-runtime-name-that-exceeds-dns-limit",
			keyType: LeaseKeyTypeRuntime,
			validate: func(t *testing.T, leaseName string) {
				assert.LessOrEqual(t, len(leaseName), 63, "Long key should be truncated with hash")
				assert.Regexp(t, "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", leaseName, "Must be DNS-1123 compliant")
			},
		},
		{
			name:    "key with uppercase and special chars",
			key:     "rt/Default/Runtime_123",
			keyType: LeaseKeyTypeRuntime,
			validate: func(t *testing.T, leaseName string) {
				assert.Regexp(t, "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", leaseName, "Must be lowercase and DNS-1123 compliant")
				assert.NotContains(t, leaseName, "_", "Underscores should be replaced with hyphens")
				assert.NotContains(t, leaseName, "D", "Uppercase should be converted to lowercase")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leaseName := lm.makeLeaseNameFromKey(tt.key, tt.keyType)
			tt.validate(t, leaseName)
		})
	}
}

func TestSanitizeDNS1123(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already compliant",
			input:    "test-runtime-123",
			expected: "test-runtime-123",
		},
		{
			name:     "uppercase to lowercase",
			input:    "TestRuntime",
			expected: "testruntime",
		},
		{
			name:     "special chars to hyphens",
			input:    "test_runtime/123",
			expected: "test-runtime-123",
		},
		{
			name:     "mixed case and special chars",
			input:    "Test_Runtime/123",
			expected: "test-runtime-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeDNS1123(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLeaseManager_ConcurrentAcquisition(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, coordinationv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	lm := NewLeaseManager(fakeClient, logr.Discard(), "test-ns", 120*time.Second, "test-holder")

	ctx := context.Background()
	key := "rt/default/test-runtime"
	keyType := LeaseKeyTypeRuntime

	// Simulate concurrent acquisition attempts
	results := make(chan K8sLeaseResult, 5)
	for i := 0; i < 5; i++ {
		go func() {
			result, err := lm.AcquireLease(ctx, key, keyType)
			if err == nil {
				results <- result
			}
		}()
	}

	// Collect results
	acquiredCount := 0
	gatedCount := 0
	for i := 0; i < 5; i++ {
		result := <-results
		if result.Acquired {
			acquiredCount++
		} else {
			gatedCount++
		}
	}

	// At most 1 should have acquired the lease
	// Note: With fake client, race conditions might not be perfectly simulated
	// In real K8s, only 1 would succeed
	assert.LessOrEqual(t, acquiredCount, 1, "At most 1 goroutine should acquire the lease")
}
