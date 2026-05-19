package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func buildEnvVars(xtrinode *analyticsv1.XTrinode) []corev1.EnvVar {
	envVars := []corev1.EnvVar{}

	// Add environment variables from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if env, ok := xtrinode.Spec.GetValuesOverlayMap()["env"].([]interface{}); ok {
			for _, envItem := range env {
				if envMap, ok := envItem.(map[string]interface{}); ok {
					envVar := corev1.EnvVar{}
					if name, ok := envMap["name"].(string); ok {
						envVar.Name = name
					}
					if value, ok := envMap["value"].(string); ok {
						envVar.Value = value
					}
					envVars = append(envVars, envVar)
				}
			}
		}
	}

	return envVars
}

func getTrinoImage(xtrinode *analyticsv1.XTrinode) string {
	// Check valuesOverlay for image configuration
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if image, ok := xtrinode.Spec.GetValuesOverlayMap()["image"].(map[string]interface{}); ok {
			// Check if useRepositoryAsSoleImageReference is true
			if useSoleRef, ok := image["useRepositoryAsSoleImageReference"].(bool); ok && useSoleRef {
				if repo, ok := image["repository"].(string); ok && repo != "" {
					return repo
				}
			}

			repository := config.DefaultTrinoImageRepository
			if repo, ok := image["repository"].(string); ok && repo != "" {
				repository = repo
			}

			// Check for registry
			registry := ""
			if reg, ok := image["registry"].(string); ok && reg != "" {
				registry = reg
			}

			// Check for digest (takes precedence over tag)
			if digest, ok := image["digest"].(string); ok && digest != "" {
				if registry != "" {
					return fmt.Sprintf("%s/%s@%s", registry, repository, digest)
				}
				return fmt.Sprintf("%s@%s", repository, digest)
			}

			// Use tag
			tag := config.DefaultTrinoImageTag
			if t, ok := image["tag"].(string); ok && t != "" {
				tag = t
			}

			if registry != "" {
				return fmt.Sprintf("%s/%s:%s", registry, repository, tag)
			}
			return fmt.Sprintf("%s:%s", repository, tag)
		}
	}
	// Default image
	return fmt.Sprintf("%s:%s", config.DefaultTrinoImageRepository, config.DefaultTrinoImageTag)
}
