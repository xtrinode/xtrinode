package resources

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func buildPodAnnotations(xtrinode *analyticsv1.XTrinode, configMapName string, catalogs []string) map[string]string {
	annotations := make(map[string]string)

	// ConfigMap names are revisioned, so this value changes when rendered coordinator config changes.
	annotations["checksum/coordinator-config"] = configMapName

	// Note: Catalog content changes are handled by rollout hash system
	// Coordinator rolls on catalog changes via trino.io/rollout-hash-coordinator annotation
	// No need for catalog annotations here - rollout hash is the source of truth

	// Add custom annotations from valuesOverlay
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if customAnnotations, ok := coordinator["annotations"].(map[string]interface{}); ok {
				for k, v := range customAnnotations {
					if vStr, ok := v.(string); ok {
						annotations[k] = vStr
					}
				}
			}
		}
	}

	return annotations
}

func buildSecurityContext(xtrinode *analyticsv1.XTrinode) *corev1.PodSecurityContext {
	// Check if pod security context is configured in valuesOverlay (matches official chart: .Values.securityContext)
	// An explicitly set empty map {} means no security context (user opted out)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if rawSecCtx, exists := xtrinode.Spec.GetValuesOverlayMap()["securityContext"]; exists {
			if securityContextMap, ok := rawSecCtx.(map[string]interface{}); ok {
				if len(securityContextMap) == 0 {
					return nil
				}
				yamlBytes, err := yaml.Marshal(securityContextMap)
				if err == nil {
					var podSecurityContext corev1.PodSecurityContext
					if err := yaml.Unmarshal(yamlBytes, &podSecurityContext); err == nil {
						return &podSecurityContext
					}
				}
			}
		}
	}
	// Default security context
	return &corev1.PodSecurityContext{
		RunAsNonRoot: func() *bool { b := true; return &b }(),
		RunAsUser:    func() *int64 { uid := int64(1000); return &uid }(),
		FSGroup:      func() *int64 { gid := int64(1000); return &gid }(),
	}
}

func buildTerminationGracePeriod(xtrinode *analyticsv1.XTrinode, role string) *int64 {
	// Default: coordinator 15 minutes, worker 60 minutes
	var gracePeriod int64 = 15 * 60 // 15 minutes
	if role == "worker" {
		gracePeriod = 60 * 60 // 60 minutes
	}

	// Override from valuesOverlay if specified
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if roleMap, ok := xtrinode.Spec.GetValuesOverlayMap()[role].(map[string]interface{}); ok {
			if tgp, ok := ParseInt64(roleMap["terminationGracePeriodSeconds"]); ok {
				gracePeriod = tgp
			}
		}
	}

	return &gracePeriod
}
