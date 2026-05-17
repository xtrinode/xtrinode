package v1

import (
	"github.com/xtrinode/xtrinode/internal/config"
)

// Policy classification for field changes
type ChangePolicy int

const (
	// PolicyAllow - safe to change in-place
	PolicyAllow ChangePolicy = iota
	// PolicyAllowWarn - allowed but emits warning (likely triggers rollout)
	PolicyAllowWarn
	// PolicyRequireBreakGlass - allowed only with break-glass annotation
	PolicyRequireBreakGlass
	// PolicyReject - never allowed (Kubernetes-enforced identity)
	PolicyReject
)

// Break-glass annotations
const (
	AnnotationAllowBreakingUpdate  = "xtrinode.analytics.xtrinode.io/allow-breaking-spec-update"
	AnnotationBreakingUpdateReason = "xtrinode.analytics.xtrinode.io/breaking-spec-update-reason"
)

// getSizeOrder returns the numeric order of a size (case-insensitive)
// Returns 0 if size is invalid
func getSizeOrder(size string) int {
	return config.SizeOrder[normalizeString(size)]
}

// isSizeUpgrade returns true if newSize > oldSize
func isSizeUpgrade(oldSize, newSize string) bool {
	return getSizeOrder(newSize) > getSizeOrder(oldSize)
}

// isSizeDowngrade returns true if newSize < oldSize
func isSizeDowngrade(oldSize, newSize string) bool {
	return getSizeOrder(newSize) < getSizeOrder(oldSize)
}

// Valid scaler types for KEDA
var validScalerTypes = map[string]bool{
	"prometheus": true,
	"http":       true,
}

// Valid scaling metrics for KEDA
var validScalingMetrics = map[string]bool{
	"query":  true,
	"memory": true,
	"cpu":    true,
}

// Valid access control types
var validAccessControlTypes = map[string]bool{
	"configmap":  true,
	"properties": true,
}

// Valid Trino retry policies for fault-tolerant execution.
var validFaultTolerantRetryPolicies = map[string]bool{
	"task":  true,
	"query": true,
}
