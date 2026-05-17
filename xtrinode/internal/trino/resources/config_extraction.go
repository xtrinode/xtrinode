package resources

import (
	"encoding/json"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// ComponentConfig represents the effective configuration for a component
// Used for computing component-specific rollout hashes
type ComponentConfig struct {
	// Component-specific settings from valuesOverlay
	ComponentSettings map[string]interface{}

	// Shared settings that affect this component
	Size            string
	Image           string
	ImagePullPolicy string
	ServiceAccount  string
	SecurityContext map[string]interface{}

	// Component-specific resource requirements
	Resources map[string]interface{}

	// Component-specific environment variables
	Env map[string]interface{}
}

// extractCoordinatorConfig extracts coordinator-specific configuration
// This ensures coordinator rollout hash only changes when coordinator config changes
func extractCoordinatorConfig(xtrinode *analyticsv1.XTrinode) interface{} {
	config := ComponentConfig{
		Size:              xtrinode.Spec.Size,
		ComponentSettings: make(map[string]interface{}),
	}

	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap != nil {
		// Extract coordinator-specific settings
		if coordinator, ok := valuesMap["coordinator"].(map[string]interface{}); ok {
			config.ComponentSettings = coordinator
		}

		// Extract image settings
		if image, ok := valuesMap["image"].(map[string]interface{}); ok {
			if repository, ok := image["repository"].(string); ok {
				config.Image = repository
			}
			if tag, ok := image["tag"].(string); ok {
				config.Image += ":" + tag
			}
			if pullPolicy, ok := image["pullPolicy"].(string); ok {
				config.ImagePullPolicy = pullPolicy
			}
		}

		// Extract security context
		if securityContext, ok := valuesMap["securityContext"].(map[string]interface{}); ok {
			config.SecurityContext = securityContext
		}

		// Extract service account
		if sa, ok := valuesMap["serviceAccount"].(map[string]interface{}); ok {
			if name, ok := sa["name"].(string); ok {
				config.ServiceAccount = name
			}
		}
	}

	// Include HelmChartConfig settings that affect coordinator
	if xtrinode.Spec.HelmChartConfig != nil {
		if xtrinode.Spec.HelmChartConfig.Coordinator != nil {
			config.Resources = map[string]interface{}{
				"coordinator": xtrinode.Spec.HelmChartConfig.Coordinator,
			}
		}
		if xtrinode.Spec.HelmChartConfig.AccessControl != nil {
			// Access control affects coordinator
			config.ComponentSettings["accessControl"] = xtrinode.Spec.HelmChartConfig.AccessControl
		}
	}

	return config
}

// extractWorkerConfig extracts worker-specific configuration
// This ensures worker rollout hash only changes when worker config changes
func extractWorkerConfig(xtrinode *analyticsv1.XTrinode) interface{} {
	config := ComponentConfig{
		Size:              xtrinode.Spec.Size,
		ComponentSettings: make(map[string]interface{}),
	}

	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap != nil {
		// Extract worker-specific settings
		if worker, ok := valuesMap["worker"].(map[string]interface{}); ok {
			config.ComponentSettings = worker
		}

		// Extract image settings
		if image, ok := valuesMap["image"].(map[string]interface{}); ok {
			if repository, ok := image["repository"].(string); ok {
				config.Image = repository
			}
			if tag, ok := image["tag"].(string); ok {
				config.Image += ":" + tag
			}
			if pullPolicy, ok := image["pullPolicy"].(string); ok {
				config.ImagePullPolicy = pullPolicy
			}
		}

		// Extract security context
		if securityContext, ok := valuesMap["securityContext"].(map[string]interface{}); ok {
			config.SecurityContext = securityContext
		}

		// Extract service account
		if sa, ok := valuesMap["serviceAccount"].(map[string]interface{}); ok {
			if name, ok := sa["name"].(string); ok {
				config.ServiceAccount = name
			}
		}
	}

	// Include HelmChartConfig settings that affect worker
	if xtrinode.Spec.HelmChartConfig != nil {
		if xtrinode.Spec.HelmChartConfig.Worker != nil {
			config.Resources = map[string]interface{}{
				"worker": xtrinode.Spec.HelmChartConfig.Worker,
			}
		}
	}

	return config
}

// ToJSON converts ComponentConfig to JSON for hashing
func (c ComponentConfig) MarshalJSON() ([]byte, error) { //nolint:gocritic // hugeParam: pointer receiver would prevent json.Marshal calling this method on non-addressable values
	return json.Marshal(map[string]interface{}{
		"size":              c.Size,
		"image":             c.Image,
		"imagePullPolicy":   c.ImagePullPolicy,
		"serviceAccount":    c.ServiceAccount,
		"securityContext":   c.SecurityContext,
		"resources":         c.Resources,
		"env":               c.Env,
		"componentSettings": c.ComponentSettings,
	})
}
