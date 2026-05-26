package v1

import (
	"encoding/json"
	"reflect"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// normalizeString converts a string to lowercase for case-insensitive comparison
func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// hasBreakGlassAnnotation checks if the break-glass annotation is set to "true"
func hasBreakGlassAnnotation(t *XTrinode) bool {
	if t.Annotations == nil {
		return false
	}
	return t.Annotations[AnnotationAllowBreakingUpdate] == "true"
}

// comparePointerInt32 compares two *int32 values
// Returns true if they are different
func comparePointerInt32(old, newVal *int32) bool {
	if old == nil && newVal == nil {
		return false
	}
	if old == nil || newVal == nil {
		return true
	}
	return *old != *newVal
}

// comparePointerBool compares two *bool values
// Returns true if they are different
func comparePointerBool(old, newVal *bool) bool {
	if old == nil && newVal == nil {
		return false
	}
	if old == nil || newVal == nil {
		return true
	}
	return *old != *newVal
}

// compareStringSlices compares two string slices
// Returns true if they are different
func compareStringSlices(old, newVal []string) bool {
	if len(old) != len(newVal) {
		return true
	}
	for i := range old {
		if old[i] != newVal[i] {
			return true
		}
	}
	return false
}

// compareStringMaps compares two string maps
// Returns true if they are different
func compareStringMaps(old, newVal map[string]string) bool {
	if len(old) != len(newVal) {
		return true
	}
	for k, v := range old {
		if newVal[k] != v {
			return true
		}
	}
	return false
}

// compareValuesOverlay compares two ValuesOverlay fields
// Returns true if they are different
// Normalizes JSON to avoid format-only diffs
func compareValuesOverlay(old, newVal *apiextensionsv1.JSON) bool {
	if old == nil && newVal == nil {
		return false
	}
	if old == nil || newVal == nil {
		return true
	}

	// Compare raw bytes first (fast path)
	if reflect.DeepEqual(old.Raw, newVal.Raw) {
		return false
	}

	// Normalize and compare JSON
	var oldMap, newMap map[string]interface{}
	if err := json.Unmarshal(old.Raw, &oldMap); err != nil {
		// If unmarshal fails, compare raw bytes
		return !reflect.DeepEqual(old.Raw, newVal.Raw)
	}
	if err := json.Unmarshal(newVal.Raw, &newMap); err != nil {
		// If unmarshal fails, compare raw bytes
		return !reflect.DeepEqual(old.Raw, newVal.Raw)
	}

	return !reflect.DeepEqual(oldMap, newMap)
}

// nodePoolPresenceChanged returns true if nodePool was added or removed
func nodePoolPresenceChanged(old, newVal *XTrinode) bool {
	return (old.Spec.NodePool == nil) != (newVal.Spec.NodePool == nil)
}

// routingPresenceChanged returns true if routing was added or removed
func routingPresenceChanged(old, newVal *XTrinode) bool {
	return (old.Spec.Routing == nil) != (newVal.Spec.Routing == nil)
}

// kedaPresenceChanged returns true if keda was added or removed
func kedaPresenceChanged(old, newVal *XTrinode) bool {
	return (old.Spec.KEDA == nil) != (newVal.Spec.KEDA == nil)
}

// helmChartConfigPresenceChanged returns true if helmChartConfig was added or removed
func helmChartConfigPresenceChanged(old, newVal *XTrinode) bool {
	return (old.Spec.HelmChartConfig == nil) != (newVal.Spec.HelmChartConfig == nil)
}

// routingIdentityChanged checks if any routing identity field changed
func routingIdentityChanged(old, newVal *XTrinode) bool {
	if old.Spec.Routing == nil && newVal.Spec.Routing == nil {
		return false
	}
	if old.Spec.Routing == nil || newVal.Spec.Routing == nil {
		return true
	}

	oldR := old.Spec.Routing
	newR := newVal.Spec.Routing

	return oldR.RoutingGroup != newR.RoutingGroup ||
		oldR.Hostname != newR.Hostname ||
		oldR.HostnameDomain != newR.HostnameDomain ||
		oldR.Header != newR.Header ||
		oldR.Default != newR.Default
}

// nodePoolIdentityChanged checks if any nodePool identity field changed
func nodePoolIdentityChanged(old, newVal *XTrinode) bool {
	if old.Spec.NodePool == nil && newVal.Spec.NodePool == nil {
		return false
	}
	if old.Spec.NodePool == nil || newVal.Spec.NodePool == nil {
		return true
	}

	oldNP := old.Spec.NodePool
	newNP := newVal.Spec.NodePool

	return normalizeString(oldNP.Provider) != normalizeString(newNP.Provider) ||
		oldNP.Name != newNP.Name ||
		oldNP.ClusterName != newNP.ClusterName
}

// nodePoolShapeChanged checks if any nodePool shape field changed
func nodePoolShapeChanged(old, newVal *XTrinode) bool {
	if old.Spec.NodePool == nil || newVal.Spec.NodePool == nil {
		return false
	}

	oldNP := old.Spec.NodePool
	newNP := newVal.Spec.NodePool

	// Check common shape fields
	if comparePointerInt32(oldNP.OSDiskGB, newNP.OSDiskGB) {
		return true
	}

	// Check spot configuration
	if !reflect.DeepEqual(oldNP.Spot, newNP.Spot) {
		return true
	}

	// Check provider-specific shape fields
	switch normalizeString(newNP.Provider) {
	case "azure":
		if oldNP.Azure != nil && newNP.Azure != nil {
			if oldNP.Azure.VMSize != newNP.Azure.VMSize ||
				oldNP.Azure.OSDiskType != newNP.Azure.OSDiskType {
				return true
			}
		}
	case "aws":
		if oldNP.AWS != nil && newNP.AWS != nil {
			if oldNP.AWS.InstanceType != newNP.AWS.InstanceType ||
				oldNP.AWS.VolumeType != newNP.AWS.VolumeType {
				return true
			}
		}
	case "gcp":
		if oldNP.GCP != nil && newNP.GCP != nil {
			if oldNP.GCP.MachineType != newNP.GCP.MachineType ||
				oldNP.GCP.DiskType != newNP.GCP.DiskType {
				return true
			}
		}
	}

	return false
}

// nodePoolSchedulingChanged checks if scheduling/placement fields changed
func nodePoolSchedulingChanged(old, newVal *XTrinode) bool {
	if old.Spec.NodePool == nil || newVal.Spec.NodePool == nil {
		return false
	}

	oldNP := old.Spec.NodePool
	newNP := newVal.Spec.NodePool

	return compareStringSlices(oldNP.Zones, newNP.Zones) ||
		compareStringMaps(oldNP.NodeLabels, newNP.NodeLabels) ||
		compareStringMaps(oldNP.ResourceTags, newNP.ResourceTags) ||
		!reflect.DeepEqual(oldNP.NodeTaints, newNP.NodeTaints)
}

func nodePoolDeletionPolicyChanged(old, newVal *XTrinode) bool {
	if old.Spec.NodePool == nil || newVal.Spec.NodePool == nil {
		return false
	}
	return normalizeNodePoolDeletionPolicy(old.Spec.NodePool) != normalizeNodePoolDeletionPolicy(newVal.Spec.NodePool)
}

func normalizeNodePoolDeletionPolicy(nodePool *NodePoolSpec) string {
	if nodePool == nil || nodePool.DeletionPolicy == "" {
		return NodePoolDeletionPolicyDelete
	}
	return nodePool.DeletionPolicy
}

// workerScalingChanged checks if worker scaling knobs changed significantly
// Returns true if change is >2x (triggers warning)
func workerScalingChanged(old, newXTrinode *XTrinode) bool {
	if old.Spec.MaxWorkers == nil || newXTrinode.Spec.MaxWorkers == nil {
		return false
	}

	oldVal := *old.Spec.MaxWorkers
	newVal := *newXTrinode.Spec.MaxWorkers

	if oldVal == 0 {
		return newVal > 0
	}

	ratio := float64(newVal) / float64(oldVal)
	return ratio > 2.0 || ratio < 0.5
}
