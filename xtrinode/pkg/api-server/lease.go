package apiserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/xtrinode/xtrinode/internal/config"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Use config constants for lease parameters
var (
	MaxLeaseNameLength = config.APIServerMaxLeaseNameLength
	LeasePrefix        = config.APIServerLeasePrefix
)

// LeaseKeyType represents the type of lease key (runtime or pool)
type LeaseKeyType string

const (
	LeaseKeyTypeRuntime LeaseKeyType = "runtime"
	LeaseKeyTypePool    LeaseKeyType = "pool"
	LeaseKeyTypeSuspend LeaseKeyType = "suspend"
)

// LeaseManager manages Kubernetes Lease objects for resume gating
type LeaseManager struct {
	client          client.Client
	log             logr.Logger
	leaseNamespace  string
	leaseDuration   time.Duration
	suspendDuration time.Duration
	holderIdentity  string
}

// NewLeaseManager creates a new LeaseManager
func NewLeaseManager(cli client.Client, log logr.Logger, leaseNamespace string, leaseDuration time.Duration, holderIdentity string) *LeaseManager {
	return &LeaseManager{
		client:          cli,
		log:             log,
		leaseNamespace:  leaseNamespace,
		leaseDuration:   leaseDuration,
		suspendDuration: leaseDuration, // Default: same as resume duration
		holderIdentity:  holderIdentity,
	}
}

// SetSuspendDuration sets a separate lease duration for suspend operations
func (lm *LeaseManager) SetSuspendDuration(d time.Duration) {
	lm.suspendDuration = d
}

// durationForKey returns the lease duration appropriate for the given key type
func (lm *LeaseManager) durationForKey(keyType LeaseKeyType) time.Duration {
	// Suspend runtime endpoints use the dedicated suspend duration
	// All other operations (resume, pool) use the default lease duration
	if keyType == LeaseKeyTypeSuspend {
		return lm.suspendDuration
	}
	return lm.leaseDuration
}

// K8sLeaseResult contains the result of a Kubernetes Lease acquisition attempt
type K8sLeaseResult struct {
	Acquired   bool      // True if this request acquired the lease
	LeaseUntil time.Time // When the lease expires
	Holder     string    // Current lease holder identity
}

// AcquireLease attempts to acquire a lease for the given key
// Returns K8sLeaseResult indicating whether the lease was acquired and when it expires
func (lm *LeaseManager) AcquireLease(ctx context.Context, key string, keyType LeaseKeyType) (K8sLeaseResult, error) {
	leaseName := lm.makeLeaseNameFromKey(key, keyType)
	now := metav1.Now()
	// Standard lease semantics: leaseUntil = RenewTime + LeaseDurationSeconds
	// Use per-operation duration (suspend vs resume/pool)
	leaseUntil := now.Add(lm.durationForKey(keyType))

	lease := &coordinationv1.Lease{}
	err := lm.client.Get(ctx, types.NamespacedName{
		Namespace: lm.leaseNamespace,
		Name:      leaseName,
	}, lease)

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Lease doesn't exist - create it
			return lm.createLease(ctx, leaseName, now, leaseUntil, key, keyType)
		}
		// Other error
		return K8sLeaseResult{}, fmt.Errorf("failed to get lease: %w", err)
	}

	// Lease exists - check if expired using standard semantics
	// leaseExpiry = RenewTime + LeaseDurationSeconds
	var leaseExpiry time.Time
	if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
		leaseExpiry = lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	} else if lease.Spec.RenewTime != nil {
		// RenewTime-only leases expire at their recorded renew time.
		leaseExpiry = lease.Spec.RenewTime.Time
	} else {
		leaseExpiry = now.Time
	}

	if leaseExpiry.Before(now.Time) {
		// Lease expired - try to update it
		return lm.updateLease(ctx, lease, now, leaseUntil)
	}

	// Lease is still held by someone
	holder := "unknown"
	if lease.Spec.HolderIdentity != nil {
		holder = *lease.Spec.HolderIdentity
	}

	lm.log.V(1).Info("Lease currently held",
		"leaseName", leaseName,
		"holder", holder,
		"expiresAt", leaseExpiry.Format(time.RFC3339))

	return K8sLeaseResult{
		Acquired:   false,
		LeaseUntil: leaseExpiry,
		Holder:     holder,
	}, nil
}

// createLease creates a new lease
func (lm *LeaseManager) createLease(ctx context.Context, leaseName string, now metav1.Time, leaseUntil time.Time, key string, keyType LeaseKeyType) (K8sLeaseResult, error) {
	// Standard lease semantics: RenewTime = now, LeaseDurationSeconds = D
	renewMicro := metav1.NewMicroTime(now.Time)
	acquireMicro := metav1.NewMicroTime(now.Time)
	leaseDurationSeconds := int32(lm.durationForKey(keyType).Seconds())

	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: lm.leaseNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "xtrinode-operator",
				"app.kubernetes.io/component": "api-server",
				"xtrinode.io/lease-type":      "resume",
				"xtrinode.io/lease-key-type":  string(keyType),
			},
			Annotations: map[string]string{
				"xtrinode.io/lease-key": key,
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &lm.holderIdentity,
			LeaseDurationSeconds: &leaseDurationSeconds,
			AcquireTime:          &acquireMicro,
			RenewTime:            &renewMicro,
		},
	}

	err := lm.client.Create(ctx, lease)
	if err != nil {
		// Race condition: someone else created it — re-GET once to return current holder info
		// No recursion: if the re-GET also fails, return error instead of looping
		if client.IgnoreAlreadyExists(err) == nil {
			lm.log.V(1).Info("Lease already exists (race condition), fetching current holder",
				"leaseName", leaseName)
			existing := &coordinationv1.Lease{}
			getErr := lm.client.Get(ctx, types.NamespacedName{
				Namespace: lm.leaseNamespace,
				Name:      leaseName,
			}, existing)
			if getErr != nil {
				return K8sLeaseResult{}, fmt.Errorf("failed to get existing lease after create race: %w", getErr)
			}
			// Return the existing lease holder info as gated
			holder := "unknown"
			if existing.Spec.HolderIdentity != nil {
				holder = *existing.Spec.HolderIdentity
			}
			var leaseExpiry time.Time
			if existing.Spec.RenewTime != nil && existing.Spec.LeaseDurationSeconds != nil {
				leaseExpiry = existing.Spec.RenewTime.Add(time.Duration(*existing.Spec.LeaseDurationSeconds) * time.Second)
			} else {
				leaseExpiry = now.Add(lm.durationForKey(keyType))
			}
			return K8sLeaseResult{
				Acquired:   false,
				LeaseUntil: leaseExpiry,
				Holder:     holder,
			}, nil
		}
		return K8sLeaseResult{}, fmt.Errorf("failed to create lease: %w", err)
	}

	lm.log.Info("Lease acquired (created)",
		"leaseName", leaseName,
		"key", key,
		"keyType", keyType,
		"holder", lm.holderIdentity,
		"expiresAt", leaseUntil.Format(time.RFC3339))

	recordK8sLeaseAcquired(string(keyType))

	return K8sLeaseResult{
		Acquired:   true,
		LeaseUntil: leaseUntil,
		Holder:     lm.holderIdentity,
	}, nil
}

// updateLease updates an expired lease
func (lm *LeaseManager) updateLease(ctx context.Context, lease *coordinationv1.Lease, now metav1.Time, leaseUntil time.Time) (K8sLeaseResult, error) {
	// Standard lease semantics: RenewTime = now, LeaseDurationSeconds = D
	renewMicro := metav1.NewMicroTime(now.Time)
	// Infer key type from lease labels for correct duration selection
	keyType := LeaseKeyType(lease.Labels["xtrinode.io/lease-key-type"])
	leaseDurationSeconds := int32(lm.durationForKey(keyType).Seconds())

	// Update lease spec
	lease.Spec.HolderIdentity = &lm.holderIdentity
	lease.Spec.RenewTime = &renewMicro
	lease.Spec.LeaseDurationSeconds = &leaseDurationSeconds

	err := lm.client.Update(ctx, lease)
	if err != nil {
		// Race condition: someone else updated it
		lm.log.V(1).Info("Failed to update lease (race condition)",
			"leaseName", lease.Name,
			"error", err)

		// CRITICAL: Re-GET the lease to return actual winner's holder + expiry
		// The mutated lease object has OUR identity/times, not the winner's
		actualLease := &coordinationv1.Lease{}
		getErr := lm.client.Get(ctx, types.NamespacedName{
			Namespace: lm.leaseNamespace,
			Name:      lease.Name,
		}, actualLease)

		if getErr != nil {
			// If we can't re-GET, return error (not gated with wrong info)
			return K8sLeaseResult{}, fmt.Errorf("failed to re-get lease after conflict: %w", getErr)
		}

		// Return actual winner's info using standard lease semantics
		holder := "unknown"
		if actualLease.Spec.HolderIdentity != nil {
			holder = *actualLease.Spec.HolderIdentity
		}

		// Calculate expiry: RenewTime + LeaseDurationSeconds
		var leaseExpiry time.Time
		if actualLease.Spec.RenewTime != nil && actualLease.Spec.LeaseDurationSeconds != nil {
			leaseExpiry = actualLease.Spec.RenewTime.Add(time.Duration(*actualLease.Spec.LeaseDurationSeconds) * time.Second)
		} else if actualLease.Spec.RenewTime != nil {
			leaseExpiry = actualLease.Spec.RenewTime.Time
		} else {
			leaseExpiry = now.Time
		}

		return K8sLeaseResult{
			Acquired:   false,
			LeaseUntil: leaseExpiry,
			Holder:     holder,
		}, nil
	}

	key := lease.Annotations["xtrinode.io/lease-key"]

	lm.log.Info("Lease acquired (updated expired)",
		"leaseName", lease.Name,
		"key", key,
		"keyType", keyType,
		"holder", lm.holderIdentity,
		"expiresAt", leaseUntil.Format(time.RFC3339))

	recordK8sLeaseAcquired(string(keyType))

	return K8sLeaseResult{
		Acquired:   true,
		LeaseUntil: leaseUntil,
		Holder:     lm.holderIdentity,
	}, nil
}

// ReleaseLease releases a lease by deleting it or shortening its duration
// This prevents long gating periods when resume/suspend operations fail
func (lm *LeaseManager) ReleaseLease(ctx context.Context, key string, keyType LeaseKeyType) error {
	leaseName := lm.makeLeaseNameFromKey(key, keyType)

	lease := &coordinationv1.Lease{}
	err := lm.client.Get(ctx, types.NamespacedName{
		Namespace: lm.leaseNamespace,
		Name:      leaseName,
	}, lease)

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Lease doesn't exist - nothing to release
			return nil
		}
		return fmt.Errorf("failed to get lease for release: %w", err)
	}

	// Check if we're the holder
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != lm.holderIdentity {
		// Not our lease - don't release
		lm.log.V(1).Info("Not releasing lease - not the holder",
			"leaseName", leaseName,
			"holder", lease.Spec.HolderIdentity)
		return nil
	}

	// Delete the lease to release it immediately
	err = lm.client.Delete(ctx, lease)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Already deleted - success
			return nil
		}
		return fmt.Errorf("failed to delete lease: %w", err)
	}

	lm.log.Info("Lease released",
		"leaseName", leaseName,
		"key", key,
		"keyType", keyType)

	return nil
}

// makeLeaseNameFromKey creates a DNS-1123 compliant lease name from a key
// Format: xtrinode-resume-{type}-{sanitized-key}
// If too long, uses hash suffix
func (lm *LeaseManager) makeLeaseNameFromKey(key string, keyType LeaseKeyType) string {
	// Sanitize key: replace invalid chars with hyphens
	sanitized := sanitizeDNS1123(key)

	// Build name: xtrinode-resume-{type}-{sanitized}
	name := fmt.Sprintf("%s%s-%s", LeasePrefix, keyType, sanitized)

	// If too long, truncate and add hash suffix
	if len(name) > MaxLeaseNameLength {
		// Hash the full key for uniqueness
		hash := sha256.Sum256([]byte(key))
		hashStr := hex.EncodeToString(hash[:])[:8] // Use first 8 chars of hash

		// Truncate to fit: {prefix}{type}-{hash}
		maxPrefixLen := MaxLeaseNameLength - len(hashStr) - 1 // -1 for hyphen
		prefix := fmt.Sprintf("%s%s", LeasePrefix, keyType)
		if len(prefix) > maxPrefixLen {
			prefix = prefix[:maxPrefixLen]
		}

		name = fmt.Sprintf("%s-%s", prefix, hashStr)
	}

	return name
}

// MakeRuntimeKey creates a runtime key for lease gating
// Format: rt/{namespace}/{name}
func MakeRuntimeKey(namespace, name string) string {
	return fmt.Sprintf("rt/%s/%s", namespace, name)
}

// MakePoolKey creates a pool key for lease gating
// Format: pool/{routingGroup}
func MakePoolKey(routingGroup string) string {
	return fmt.Sprintf("pool/%s", routingGroup)
}

// sanitizeDNS1123 sanitizes a string to be DNS-1123 compliant
// Replaces invalid characters with hyphens, converts to lowercase,
// and trims leading/trailing hyphens to ensure valid DNS-1123 names.
func sanitizeDNS1123(s string) string {
	var result strings.Builder
	result.Grow(len(s))

	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			result.WriteRune(r + ('a' - 'A')) // Convert to lowercase
		} else {
			result.WriteRune('-')
		}
	}

	// DNS-1123 requires names to start and end with alphanumeric characters
	return strings.Trim(result.String(), "-")
}
