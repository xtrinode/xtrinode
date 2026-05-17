package resources

import (
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AppNameLabel      = "app.kubernetes.io/name"
	AppInstanceLabel  = "app.kubernetes.io/instance"
	AppVersionLabel   = "app.kubernetes.io/version"
	AppManagedByLabel = "app.kubernetes.io/managed-by"
	AppComponentLabel = "app.kubernetes.io/component"

	ComponentCoordinator = "coordinator"
	ComponentWorker      = "worker"

	ManagedByValue = "xtrinode-operator"
)

// TrinoLabels returns standard labels for Trino resources (non-component-specific)
func TrinoLabels(xtrinode *analyticsv1.XTrinode) map[string]string {
	labels := map[string]string{
		AppNameLabel:      "trino",
		AppInstanceLabel:  xtrinode.Name,
		AppVersionLabel:   getTrinoVersion(xtrinode),
		AppManagedByLabel: ManagedByValue,
	}

	// Add common labels from valuesOverlay (applied to all resources)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if commonLabels, ok := xtrinode.Spec.GetValuesOverlayMap()["commonLabels"].(map[string]interface{}); ok {
			for k, v := range commonLabels {
				if vStr, ok := v.(string); ok {
					labels[k] = vStr
				}
			}
		}
	}

	return labels
}

// TrinoLabelsForComponent returns labels for a specific component (coordinator or worker)
// TrinoLabelsForComponent includes common labels plus role-specific custom labels.
func TrinoLabelsForComponent(xtrinode *analyticsv1.XTrinode, component string) map[string]string {
	labels := TrinoLabels(xtrinode)

	// Add component-specific custom labels from valuesOverlay
	if component != "" && xtrinode.Spec.GetValuesOverlayMap() != nil {
		if componentConfig, ok := xtrinode.Spec.GetValuesOverlayMap()[component].(map[string]interface{}); ok {
			if customLabels, ok := componentConfig["labels"].(map[string]interface{}); ok {
				for k, v := range customLabels {
					if vStr, ok := v.(string); ok {
						labels[k] = vStr
					}
				}
			}
		}
	}

	return labels
}

// TrinoSelectorLabels returns STABLE selector labels for Deployments and Services
// TrinoSelectorLabels returns immutable selector labels.
// Excludes: version, commonLabels, custom labels, network-policy (these go in pod template only)
func TrinoSelectorLabels(xtrinode *analyticsv1.XTrinode, component string) map[string]string {
	labels := map[string]string{
		AppNameLabel:      "trino",
		AppInstanceLabel:  xtrinode.Name,
		AppManagedByLabel: ManagedByValue,
	}

	// Only set component label if component is specified
	if component != "" {
		labels[AppComponentLabel] = component
	}

	return labels
}

// TrinoPodLabels returns ALL labels for pod templates (includes version, custom labels, etc.)
// This is what should be used for pod metadata, not selectors
func TrinoPodLabels(xtrinode *analyticsv1.XTrinode, component string) map[string]string {
	// Start with stable selector labels
	labels := TrinoSelectorLabels(xtrinode, component)

	// Add version (can change)
	labels[AppVersionLabel] = getTrinoVersion(xtrinode)

	// Add common labels from valuesOverlay (can change)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if commonLabels, ok := xtrinode.Spec.GetValuesOverlayMap()["commonLabels"].(map[string]interface{}); ok {
			for k, v := range commonLabels {
				if vStr, ok := v.(string); ok {
					labels[k] = vStr
				}
			}
		}

		// Add component-specific custom labels (can change)
		if component != "" {
			if componentConfig, ok := xtrinode.Spec.GetValuesOverlayMap()[component].(map[string]interface{}); ok {
				if customLabels, ok := componentConfig["labels"].(map[string]interface{}); ok {
					for k, v := range customLabels {
						if vStr, ok := v.(string); ok {
							labels[k] = vStr
						}
					}
				}
			}
		}

	}

	if networkPolicyEnabled(xtrinode) {
		labels["trino.io/network-policy-protection"] = "enabled"
	} else {
		labels["trino.io/network-policy-protection"] = "disabled"
	}

	return labels
}

// OwnerReference creates an owner reference for a XTrinode
func OwnerReference(xtrinode *analyticsv1.XTrinode) metav1.OwnerReference {
	controller := true
	blockOwnerDeletion := true
	return metav1.OwnerReference{
		APIVersion:         analyticsv1.GroupVersion.String(),
		Kind:               "XTrinode",
		Name:               xtrinode.Name,
		UID:                xtrinode.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
}

// getTrinoVersion extracts Trino version from XTrinode spec
func getTrinoVersion(xtrinode *analyticsv1.XTrinode) string {
	// Check valuesOverlay for image tag
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if image, ok := xtrinode.Spec.GetValuesOverlayMap()["image"].(map[string]interface{}); ok {
			if tag, ok := image["tag"].(string); ok && tag != "" {
				return tag
			}
		}
	}
	// Default version
	return config.DefaultTrinoImageTag
}
