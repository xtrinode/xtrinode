package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

// UnifiedResumeRequest is the request body for the unified resume endpoint
type UnifiedResumeRequest struct {
	RoutingGroup string            `json:"routingGroup,omitempty"` // Routing group hint
	Candidate    *CandidateRuntime `json:"candidate,omitempty"`    // Specific runtime to resume
	Reason       string            `json:"reason,omitempty"`       // Reason for resume (logging/metrics)
	RouteName    string            `json:"routeName,omitempty"`    // Optional route name (logging)
}

// CandidateRuntime identifies a specific runtime
type CandidateRuntime struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// UnifiedResumeResponse is the response for the unified resume endpoint
type UnifiedResumeResponse struct {
	Triggered  bool   `json:"triggered"`        // True if resume was triggered
	Gated      bool   `json:"gated"`            // True if gated by active lease
	RetryAfter int    `json:"retryAfter"`       // Retry-After seconds
	Key        string `json:"key"`              // Lease key used
	KeyType    string `json:"keyType"`          // "runtime" or "pool"
	LeaseUntil string `json:"leaseUntil"`       // Lease expiration time (RFC3339)
	Error      string `json:"error,omitempty"`  // Error message if gated
	Holder     string `json:"holder,omitempty"` // Current lease holder (if gated)
}

// handleUnifiedResume handles the unified resume endpoint
// POST /api/v1/resume
// This endpoint implements pool vs runtime decision logic and K8s Lease-based gating
func (s *Server) handleUnifiedResume(w http.ResponseWriter, r *http.Request) {
	// Enforce POST method — resume is a mutating operation
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Resume requires POST method", "METHOD_NOT_ALLOWED")
		return
	}
	if !s.authorize(w, r, apiActionRuntimeResume) {
		return
	}

	start := time.Now()
	var result string
	defer func() {
		observeRequestDuration("unified_resume", result, time.Since(start).Seconds())
	}()

	// Parse request body
	var req UnifiedResumeRequest
	if r.Body != nil && r.ContentLength > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			result = "error"
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "INVALID_REQUEST")
			return
		}
	}

	// Validate request
	if req.RoutingGroup == "" && req.Candidate == nil {
		result = "error"
		s.writeError(w, http.StatusBadRequest, "Either routingGroup or candidate must be provided", "INVALID_REQUEST")
		return
	}

	if req.Candidate != nil {
		if err := validateNamespace(req.Candidate.Namespace); err != nil {
			result = "error"
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid namespace: %v", err), "INVALID_NAMESPACE")
			return
		}
		if err := validateK8sName(req.Candidate.Name); err != nil {
			result = "error"
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid name: %v", err), "INVALID_NAME")
			return
		}
	}

	// Decision: pool gate or runtime gate?
	var key string
	var keyType LeaseKeyType
	var namespace, name string

	// Try pool-level gate if routing group provided without specific candidate
	if req.RoutingGroup != "" && req.Candidate == nil {
		handled, handledResult := s.tryPoolGate(w, r, req.RoutingGroup, req.Reason)
		if handled {
			result = handledResult
			return
		}
		// Pool not empty - fall through to runtime-level gate
	}

	// Determine runtime target
	if req.Candidate != nil {
		key = MakeRuntimeKey(req.Candidate.Namespace, req.Candidate.Name)
		keyType = LeaseKeyTypeRuntime
		namespace = req.Candidate.Namespace
		name = req.Candidate.Name
		s.log.V(1).Info("Using runtime-level gate", "namespace", namespace, "name", name, "key", key, "reason", req.Reason)
	} else {
		// Pick candidate from routing group
		candidate := s.pickCandidateFromRoutes(r.Context(), req.RoutingGroup)
		if candidate == nil {
			result = "error"
			s.writeError(w, http.StatusNotFound, "No candidate runtime found for routing group", "NO_CANDIDATE")
			return
		}
		key = MakeRuntimeKey(candidate.Namespace, candidate.Name)
		keyType = LeaseKeyTypeRuntime
		namespace = candidate.Namespace
		name = candidate.Name
		s.log.V(1).Info("Using fallback candidate", "routingGroup", req.RoutingGroup, "namespace", namespace, "name", name, "key", key)
	}

	// Acquire runtime-level lease and respond
	leaseResult, err := s.leaseManager.AcquireLease(r.Context(), key, keyType)
	if err != nil {
		result = "error"
		recordK8sLeaseError(string(keyType))
		s.log.Error(err, "Failed to acquire lease", "key", key, "keyType", keyType)
		s.writeError(w, http.StatusInternalServerError, "Failed to acquire lease", "LEASE_ERROR")
		return
	}

	if leaseResult.Acquired {
		result = s.handleLeaseAcquired(w, namespace, name, key, keyType, leaseResult)
		return
	}

	result = s.handleLeaseGated(w, key, keyType, leaseResult)
}

// tryPoolGate attempts pool-level gating. Returns (handled, result) where handled=true means response was sent.
func (s *Server) tryPoolGate(w http.ResponseWriter, r *http.Request, routingGroup, reason string) (handled bool, result string) {
	key := MakePoolKey(routingGroup)
	keyType := LeaseKeyTypePool

	s.log.V(1).Info("Attempting pool-level gate", "routingGroup", routingGroup, "key", key, "reason", reason)

	// Try acquire pool lease FIRST (before expensive checks)
	leaseResult, err := s.leaseManager.AcquireLease(r.Context(), key, keyType)
	if err != nil {
		recordK8sLeaseError(string(keyType))
		s.log.Error(err, "Failed to acquire pool lease", "key", key)
		s.writeError(w, http.StatusInternalServerError, "Failed to acquire lease", "LEASE_ERROR")
		return true, "error"
	}

	if !leaseResult.Acquired {
		// Gated - return 503 immediately
		s.respondLeaseGated(w, key, keyType, leaseResult)
		return true, "gated"
	}

	// Pool lease acquired - check if pool is actually empty
	if !s.isPoolEmpty(r.Context(), routingGroup) {
		// Pool not empty - release the acquired lease before falling through to runtime gate
		if releaseErr := s.leaseManager.ReleaseLease(r.Context(), key, keyType); releaseErr != nil {
			s.log.Error(releaseErr, "Failed to release pool lease on fallthrough", "key", key)
		}
		s.log.V(1).Info("Pool not empty, released pool lease, using runtime-level gate instead", "routingGroup", routingGroup)
		return false, ""
	}

	// Pool is empty - select candidate and trigger resume
	candidate := s.pickCandidateFromRoutes(r.Context(), routingGroup)
	if candidate == nil {
		if releaseErr := s.leaseManager.ReleaseLease(r.Context(), key, keyType); releaseErr != nil {
			s.log.Error(releaseErr, "Failed to release pool lease after no candidate", "key", key)
		}
		s.writeError(w, http.StatusNotFound, "No candidate runtime found for pool gate", "NO_CANDIDATE")
		return true, "error"
	}

	s.log.V(1).Info("Pool gate acquired, triggering resume",
		"routingGroup", routingGroup,
		"namespace", candidate.Namespace,
		"name", candidate.Name)

	s.triggerResumeAsync(candidate.Namespace, candidate.Name, key, keyType)

	retryAfter := s.getRetryAfter()
	response := UnifiedResumeResponse{
		Triggered:  true,
		Gated:      false,
		RetryAfter: retryAfter,
		Key:        key,
		KeyType:    string(keyType),
		LeaseUntil: leaseResult.LeaseUntil.Format(time.RFC3339),
	}

	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	w.Header().Set("X-Lease-Acquired", "true")
	w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
	s.writeJSON(w, http.StatusAccepted, response)

	recordResumeRequest("triggered")
	return true, "triggered"
}

// handleLeaseAcquired handles the case when a lease is successfully acquired
func (s *Server) handleLeaseAcquired(w http.ResponseWriter, namespace, name, key string, keyType LeaseKeyType, leaseResult K8sLeaseResult) string {
	// Trigger resume (idempotent, async) with lease cleanup on failure.
	// Detached context with timeout: must survive client disconnect but not hang forever.
	if namespace != "" && name != "" {
		s.triggerResumeAsync(namespace, name, key, keyType)
	}

	retryAfter := s.getRetryAfter()
	response := UnifiedResumeResponse{
		Triggered:  true,
		Gated:      false,
		RetryAfter: retryAfter,
		Key:        key,
		KeyType:    string(keyType),
		LeaseUntil: leaseResult.LeaseUntil.Format(time.RFC3339),
	}

	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	w.Header().Set("X-Lease-Acquired", "true")
	w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
	s.writeJSON(w, http.StatusAccepted, response)

	recordResumeRequest("triggered")
	return "triggered"
}

func (s *Server) triggerResumeAsync(namespace, name, key string, keyType LeaseKeyType) {
	go func() {
		resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer resumeCancel()

		s.triggerResumeWithLeaseCleanup(resumeCtx, namespace, name, key, keyType)
	}()
}

// handleLeaseGated handles the case when a lease is held by another request
func (s *Server) handleLeaseGated(w http.ResponseWriter, key string, keyType LeaseKeyType, leaseResult K8sLeaseResult) string {
	s.respondLeaseGated(w, key, keyType, leaseResult)
	return "gated"
}

// respondLeaseGated writes a 503 response for gated requests
func (s *Server) respondLeaseGated(w http.ResponseWriter, key string, keyType LeaseKeyType, leaseResult K8sLeaseResult) {
	recordK8sLeaseGated(string(keyType))

	remaining := calculateRetryAfter(leaseResult.LeaseUntil)
	response := UnifiedResumeResponse{
		Triggered:  false,
		Gated:      true,
		RetryAfter: remaining,
		Key:        key,
		KeyType:    string(keyType),
		LeaseUntil: leaseResult.LeaseUntil.Format(time.RFC3339),
		Error:      "resuming",
		Holder:     leaseResult.Holder,
	}

	w.Header().Set("Retry-After", fmt.Sprintf("%d", remaining))
	w.Header().Set("X-Lease-Gated", "true")
	w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
	s.writeJSON(w, http.StatusServiceUnavailable, response)

	recordResumeRequest("gated")
}

// getRetryAfter returns the configured retry-after value with a sensible default
func (s *Server) getRetryAfter() int {
	if s.config.RetryAfterSeconds > 0 {
		return s.config.RetryAfterSeconds
	}
	return 30
}

// calculateRetryAfter calculates retry-after seconds from lease expiry, clamped to configured min/max
func calculateRetryAfter(leaseUntil time.Time) int {
	remaining := int(time.Until(leaseUntil).Seconds())
	if remaining < config.APIServerMinRetryAfterSeconds {
		return config.APIServerMinRetryAfterSeconds
	}
	if remaining > config.APIServerMaxRetryAfterSeconds {
		return config.APIServerMaxRetryAfterSeconds
	}
	return remaining
}

// isPoolEmpty checks if all backends in a routing group are not Ready (running)
func (s *Server) isPoolEmpty(ctx context.Context, routingGroup string) bool {
	// List ALL XTrinodes and filter by effective routing group
	// Cannot rely on labels as they may not be set consistently
	var xtrinodeList analyticsv1.XTrinodeList
	err := s.client.List(ctx, &xtrinodeList)
	if err != nil {
		s.log.Error(err, "Failed to list XTrinodes for pool check", "routingGroup", routingGroup)
		return true // Assume empty on error
	}

	// Filter by routing group and check for Ready backends
	hasBackends := false
	for i := range xtrinodeList.Items {
		t := &xtrinodeList.Items[i]

		// Check if this XTrinode belongs to the routing group.
		// Explicit groups are global pools; empty groups use the gateway's
		// namespace-qualified default dedicated route key.
		if xtrinodeMatchesRoutingGroup(t, routingGroup) {
			hasBackends = true
			// Phase "Ready" means running and available
			if t.Status.Phase == "Ready" {
				return false // Pool has running capacity
			}
		}
	}

	if !hasBackends {
		return true // No backends in pool
	}

	return true // All backends are not Ready
}

// pickCandidateFromRoutes selects the best runtime to resume from a routing group
func (s *Server) pickCandidateFromRoutes(ctx context.Context, routingGroup string) *CandidateRuntime {
	// List ALL XTrinodes and filter by effective routing group
	// Cannot rely on labels as they may not be set consistently
	var xtrinodeList analyticsv1.XTrinodeList
	err := s.client.List(ctx, &xtrinodeList)
	if err != nil {
		s.log.Error(err, "Failed to list XTrinodes for candidate selection", "routingGroup", routingGroup)
		return nil
	}

	// Pick best candidate: Suspended (0) > Reconciling (1) > smallest capacity
	// Correct phase values: "Ready" (running), "Suspended" (paused), "Reconciling" (resuming)
	var best *analyticsv1.XTrinode
	bestPriority := 999
	bestCapacity := 999

	for i := range xtrinodeList.Items {
		t := &xtrinodeList.Items[i]

		// Filter by routing group
		if !xtrinodeMatchesRoutingGroup(t, routingGroup) {
			continue
		}

		var priority int
		switch t.Status.Phase {
		case "Suspended": // Fully suspended - best candidate
			priority = 0
		case "Reconciling": // May be resuming - second choice
			priority = 1
		default:
			continue // Skip non-resumable states (Ready, Error, etc.)
		}

		shape, err := runtimeshape.Resolve(t)
		if err != nil {
			s.log.Error(err, "Failed to resolve runtime shape for candidate selection",
				"namespace", t.Namespace,
				"name", t.Name,
				"routingGroup", routingGroup)
			continue
		}
		capacity := int(shape.CapacityUnits)

		if best == nil ||
			priority < bestPriority ||
			(priority == bestPriority && capacity < bestCapacity) {
			best = t
			bestPriority = priority
			bestCapacity = capacity
		}
	}

	if best == nil {
		return nil
	}

	return &CandidateRuntime{
		Namespace: best.Namespace,
		Name:      best.Name,
	}
}

func xtrinodeMatchesRoutingGroup(xtrinode *analyticsv1.XTrinode, routingGroup string) bool {
	if xtrinode == nil || routingGroup == "" {
		return false
	}
	if xtrinode.Spec.Routing != nil && xtrinode.Spec.Routing.RoutingGroup != "" {
		return xtrinode.Spec.Routing.RoutingGroup == routingGroup
	}
	return config.DefaultDedicatedRoutingGroup(xtrinode.Namespace, xtrinode.Name) == routingGroup
}

// triggerResumeWithLeaseCleanup triggers resume and releases lease on failure
func (s *Server) triggerResumeWithLeaseCleanup(ctx context.Context, namespace, name, key string, keyType LeaseKeyType) {
	err := s.triggerResume(ctx, namespace, name, key, string(keyType))
	if err != nil {
		// CRITICAL: Release lease on failure to prevent long gating periods
		s.log.Error(err, "Resume trigger failed, releasing lease",
			"namespace", namespace,
			"name", name,
			"key", key,
			"keyType", keyType)

		// Release the lease so retries aren't gated for full duration
		if releaseErr := s.leaseManager.ReleaseLease(ctx, key, keyType); releaseErr != nil {
			s.log.Error(releaseErr, "Failed to release lease after trigger failure",
				"key", key,
				"keyType", keyType)
		}
	}
}

// triggerResume triggers an idempotent resume operation with retry on conflict
func (s *Server) triggerResume(ctx context.Context, namespace, name, key, keyType string) error {
	s.log.Info("Triggering resume",
		"namespace", namespace,
		"name", name,
		"key", key,
		"keyType", keyType)

	// Retry on conflict to handle concurrent updates from operator/status updates
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh XTrinode on each retry
		xtrinode := &analyticsv1.XTrinode{}
		if err := s.client.Get(ctx, types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, xtrinode); err != nil {
			return err
		}

		// Apply resume annotation (idempotent)
		if xtrinode.Annotations == nil {
			xtrinode.Annotations = make(map[string]string)
		}

		now := time.Now().UTC()
		xtrinode.Annotations[config.ResumeRequestedAnnotation] = "true"
		xtrinode.Annotations[config.ResumeRequestedAtAnnotation] = now.Format(time.RFC3339)

		// Update with retry
		return s.client.Update(ctx, xtrinode)
	})

	if err != nil {
		s.log.Error(err, "Failed to update XTrinode with resume annotation after retries",
			"namespace", namespace,
			"name", name)
		return fmt.Errorf("failed to update xtrinode: %w", err)
	}

	s.log.Info("Resume triggered successfully",
		"namespace", namespace,
		"name", name,
		"key", key,
		"keyType", keyType)
	return nil
}
