package resources

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// BuildHorizontalPodAutoscaler builds a HorizontalPodAutoscaler for worker autoscaling
// Returns nil if autoscaling is disabled or not configured
func BuildHorizontalPodAutoscaler(xtrinode *analyticsv1.XTrinode) *autoscalingv2.HorizontalPodAutoscaler {
	if isKEDAEnabled(xtrinode) {
		return nil
	}

	// Check if autoscaling is enabled via valuesOverlay
	enabled := false
	maxReplicas := int32(config.DefaultHPAMaxReplicas)
	minReplicas := int32(config.DefaultHPAMinReplicas)
	targetCPUUtilizationPercentage := int32(config.DefaultHPACPUTargetPercentage)
	targetMemoryUtilizationPercentage := int32(config.DefaultHPAMemoryTargetPercentage)
	var behavior *autoscalingv2.HorizontalPodAutoscalerBehavior

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if server, ok := xtrinode.Spec.GetValuesOverlayMap()["server"].(map[string]interface{}); ok {
			if autoscaling, ok := server["autoscaling"].(map[string]interface{}); ok {
				if enabledVal, ok := autoscaling["enabled"].(bool); ok {
					enabled = enabledVal
				}
				if !enabled {
					return nil
				}

				// Parse maxReplicas
				if maxRep, ok := ParseInt32(autoscaling["maxReplicas"]); ok {
					maxReplicas = maxRep
				}

				// Parse minReplicas (defaults to server.workers)
				if minRep, ok := ParseInt32(autoscaling["minReplicas"]); ok {
					minReplicas = minRep
				} else if workers, ok := ParseInt32(server["workers"]); ok {
					minReplicas = workers
				}

				// Parse targetCPUUtilizationPercentage
				if cpuTarget, ok := ParseInt32(autoscaling["targetCPUUtilizationPercentage"]); ok {
					targetCPUUtilizationPercentage = cpuTarget
				} else if cpuTargetStr, ok := autoscaling["targetCPUUtilizationPercentage"].(string); ok && cpuTargetStr == "" {
					// Empty string means disable CPU scaling
					targetCPUUtilizationPercentage = 0
				}

				// Parse targetMemoryUtilizationPercentage
				if memTarget, ok := ParseInt32(autoscaling["targetMemoryUtilizationPercentage"]); ok {
					targetMemoryUtilizationPercentage = memTarget
				} else if memTargetStr, ok := autoscaling["targetMemoryUtilizationPercentage"].(string); ok && memTargetStr == "" {
					// Empty string means disable memory scaling
					targetMemoryUtilizationPercentage = 0
				}

				// Parse behavior
				if behaviorMap, ok := autoscaling["behavior"].(map[string]interface{}); ok {
					behavior = buildHPABehavior(behaviorMap)
				}
			}
		}
	}

	if !enabled {
		return nil
	}

	// Build metrics
	var metrics []autoscalingv2.MetricSpec

	// CPU metric
	if targetCPUUtilizationPercentage > 0 {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &targetCPUUtilizationPercentage,
				},
			},
		})
	}

	// Memory metric
	if targetMemoryUtilizationPercentage > 0 {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &targetMemoryUtilizationPercentage,
				},
			},
		})
	}

	// If no metrics configured, return nil
	if len(metrics) == 0 {
		return nil
	}

	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:            config.BuildWorkerServiceName(xtrinode.Name), // HPA name matches worker deployment
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       config.BuildWorkerServiceName(xtrinode.Name),
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
			Metrics:     metrics,
			Behavior:    behavior,
		},
	}
}

// buildHPABehavior builds HPA behavior from valuesOverlay map
func buildHPABehavior(behaviorMap map[string]interface{}) *autoscalingv2.HorizontalPodAutoscalerBehavior {
	behavior := &autoscalingv2.HorizontalPodAutoscalerBehavior{}

	// Parse scaleDown
	if scaleDownMap, ok := behaviorMap["scaleDown"].(map[string]interface{}); ok {
		scaleDown := &autoscalingv2.HPAScalingRules{}
		if val, ok := ParseInt32(scaleDownMap["stabilizationWindowSeconds"]); ok {
			scaleDown.StabilizationWindowSeconds = &val
		}
		if policies, ok := scaleDownMap["policies"].([]interface{}); ok {
			for _, policy := range policies {
				if policyMap, ok := policy.(map[string]interface{}); ok {
					hpaPolicy := buildHPAScalingPolicy(policyMap)
					if hpaPolicy != nil {
						scaleDown.Policies = append(scaleDown.Policies, *hpaPolicy)
					}
				}
			}
		}
		if selectPolicy, ok := scaleDownMap["selectPolicy"].(string); ok {
			policy := autoscalingv2.ScalingPolicySelect(selectPolicy)
			scaleDown.SelectPolicy = &policy
		}
		behavior.ScaleDown = scaleDown
	}

	// Parse scaleUp
	if scaleUpMap, ok := behaviorMap["scaleUp"].(map[string]interface{}); ok {
		scaleUp := &autoscalingv2.HPAScalingRules{}
		if val, ok := ParseInt32(scaleUpMap["stabilizationWindowSeconds"]); ok {
			scaleUp.StabilizationWindowSeconds = &val
		}
		if policies, ok := scaleUpMap["policies"].([]interface{}); ok {
			for _, policy := range policies {
				if policyMap, ok := policy.(map[string]interface{}); ok {
					hpaPolicy := buildHPAScalingPolicy(policyMap)
					if hpaPolicy != nil {
						scaleUp.Policies = append(scaleUp.Policies, *hpaPolicy)
					}
				}
			}
		}
		if selectPolicy, ok := scaleUpMap["selectPolicy"].(string); ok {
			policy := autoscalingv2.ScalingPolicySelect(selectPolicy)
			scaleUp.SelectPolicy = &policy
		}
		behavior.ScaleUp = scaleUp
	}

	return behavior
}

// buildHPAScalingPolicy builds HPA scaling policy from map
func buildHPAScalingPolicy(policyMap map[string]interface{}) *autoscalingv2.HPAScalingPolicy {
	policy := &autoscalingv2.HPAScalingPolicy{}

	if policyType, ok := policyMap["type"].(string); ok {
		policy.Type = autoscalingv2.HPAScalingPolicyType(policyType)
	}

	if value, ok := ParseInt32(policyMap["value"]); ok {
		policy.Value = value
	}

	if periodSeconds, ok := ParseInt32(policyMap["periodSeconds"]); ok {
		policy.PeriodSeconds = periodSeconds
	}

	return policy
}
