package apiserver

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateRuntimeRequest is a safe DTO for creating runtimes
type CreateRuntimeRequest struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Size      string            `json:"size"`
	Routing   *RoutingConfig    `json:"routing,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// RoutingConfig defines routing configuration for runtime creation
type RoutingConfig struct {
	RoutingGroup string `json:"routingGroup,omitempty"`
}

// LeaseInfo contains information about the operation lease
type LeaseInfo struct {
	Operation string `json:"operation"` // "resume" or "suspend"
	Applied   bool   `json:"applied"`   // true if K8s UPDATE was executed
	Until     string `json:"until"`     // RFC3339 timestamp when lease expires
}

// AsyncOperationResponse is returned for async operations (202 Accepted)
type AsyncOperationResponse struct {
	Status            string    `json:"status"`            // "accepted"
	Desired           string    `json:"desired"`           // "resumed" or "suspended"
	CurrentPhase      string    `json:"currentPhase"`      // Current XTrinode phase
	PollURL           string    `json:"pollUrl"`           // URL to poll for status
	Lease             LeaseInfo `json:"lease"`             // Lease information
	RetryAfterSeconds int       `json:"retryAfterSeconds"` // Suggested retry delay
}

// ResumeRuntimeRequest contains optional parameters for resume operation
type ResumeRuntimeRequest struct {
	WakeMinWorkers *int32           `json:"wakeMinWorkers,omitempty"`
	WakeTTL        *metav1.Duration `json:"wakeTTL,omitempty"`
	RequestID      string           `json:"requestId,omitempty"` // Optional: for deduplication across gateway replicas
}

// SuspendRuntimeRequest contains optional parameters for suspend operation
type SuspendRuntimeRequest struct {
	RequestID string `json:"requestId,omitempty"` // Optional: for deduplication across gateway replicas
}

// ErrorResponse is a standardized error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

type apiAction string

const (
	apiActionRuntimeRead    apiAction = "runtimes:read"
	apiActionRuntimeCreate  apiAction = "runtimes:create"
	apiActionRuntimeDelete  apiAction = "runtimes:delete"
	apiActionRuntimeResume  apiAction = "runtimes:resume"
	apiActionRuntimeSuspend apiAction = "runtimes:suspend"
)
