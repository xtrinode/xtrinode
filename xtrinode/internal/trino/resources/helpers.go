package resources

import (
	"fmt"
	"strings"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// isKEDAEnabled mirrors the controller default: fixed worker replicas unless KEDA is explicitly enabled.
func isKEDAEnabled(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.KEDA == nil || xtrinode.Spec.KEDA.Enabled == nil {
		return false
	}
	return *xtrinode.Spec.KEDA.Enabled && hasKEDAMetricConfig(xtrinode.Spec.KEDA)
}

func isNativeHPAEnabled(xtrinode *analyticsv1.XTrinode) bool {
	valuesMap := xtrinode.Spec.GetValuesOverlayMap()
	if valuesMap == nil {
		return false
	}
	server, ok := valuesMap["server"].(map[string]interface{})
	if !ok {
		return false
	}
	autoscaling, ok := server["autoscaling"].(map[string]interface{})
	if !ok {
		return false
	}
	enabled, ok := autoscaling["enabled"].(bool)
	if !ok || !enabled {
		return false
	}

	cpuTarget := int32(config.DefaultHPACPUTargetPercentage)
	if parsed, ok := ParseInt32(autoscaling["targetCPUUtilizationPercentage"]); ok {
		cpuTarget = parsed
	} else if cpuTargetStr, ok := autoscaling["targetCPUUtilizationPercentage"].(string); ok && cpuTargetStr == "" {
		cpuTarget = 0
	}

	memoryTarget := int32(config.DefaultHPAMemoryTargetPercentage)
	if parsed, ok := ParseInt32(autoscaling["targetMemoryUtilizationPercentage"]); ok {
		memoryTarget = parsed
	} else if memoryTargetStr, ok := autoscaling["targetMemoryUtilizationPercentage"].(string); ok && memoryTargetStr == "" {
		memoryTarget = 0
	}

	return cpuTarget > 0 || memoryTarget > 0
}

func hasKEDAMetricConfig(k *analyticsv1.KEDASpec) bool {
	return k.ScalerType != "" ||
		k.ScalingMetric != "" ||
		(k.PrometheusServer != nil && strings.TrimSpace(*k.PrometheusServer) != "") ||
		(k.PrometheusQuery != nil && strings.TrimSpace(*k.PrometheusQuery) != "") ||
		(k.HTTPEndpoint != nil && strings.TrimSpace(*k.HTTPEndpoint) != "")
}

func roleValuesOverlay(xtrinode *analyticsv1.XTrinode, role string) map[string]interface{} {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return nil
	}
	roleConfig, ok := xtrinode.Spec.GetValuesOverlayMap()[role].(map[string]interface{})
	if !ok {
		return nil
	}
	return roleConfig
}

func trinoHTTPPort(xtrinode *analyticsv1.XTrinode) int32 {
	port := int32(config.TrinoPortHTTP)
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if service, ok := xtrinode.Spec.GetValuesOverlayMap()["service"].(map[string]interface{}); ok {
			if svcPort, ok := ParseInt32(service["port"]); ok {
				port = svcPort
			}
		}
	}
	return port
}

func workerGracefulShutdownSettings(xtrinode *analyticsv1.XTrinode) (enabled bool, gracePeriodSeconds int64) {
	gracePeriodSeconds = int64(config.DefaultGracefulShutdownSeconds)

	if xtrinode.Spec.HelmChartConfig != nil &&
		xtrinode.Spec.HelmChartConfig.Worker != nil &&
		xtrinode.Spec.HelmChartConfig.Worker.GracefulShutdown != nil {
		enabled = xtrinode.Spec.HelmChartConfig.Worker.GracefulShutdown.Enabled
		if xtrinode.Spec.HelmChartConfig.Worker.GracefulShutdown.GracePeriodSeconds > 0 {
			gracePeriodSeconds = xtrinode.Spec.HelmChartConfig.Worker.GracefulShutdown.GracePeriodSeconds
		}
	}

	if worker := roleValuesOverlay(xtrinode, "worker"); worker != nil {
		if gracefulShutdown, ok := worker["gracefulShutdown"].(map[string]interface{}); ok {
			if overlayEnabled, ok := gracefulShutdown["enabled"].(bool); ok {
				enabled = overlayEnabled
			}
			if gracePeriod, ok := ParseInt64(gracefulShutdown["gracePeriodSeconds"]); ok && gracePeriod > 0 {
				gracePeriodSeconds = gracePeriod
			}
		}
	}

	return enabled, gracePeriodSeconds
}

func shouldMountAccessControlVolume(xtrinode *analyticsv1.XTrinode, role string) bool {
	if role == "worker" {
		enabled, _ := workerGracefulShutdownSettings(xtrinode)
		return enabled
	}
	return xtrinode.Spec.HelmChartConfig != nil &&
		xtrinode.Spec.HelmChartConfig.AccessControl != nil &&
		xtrinode.Spec.HelmChartConfig.AccessControl.Type == "configmap"
}

func networkPolicyEnabled(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode.Spec.HelmChartConfig != nil &&
		xtrinode.Spec.HelmChartConfig.NetworkPolicy != nil &&
		xtrinode.Spec.HelmChartConfig.NetworkPolicy.Enabled {
		return true
	}

	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if networkPolicy, ok := xtrinode.Spec.GetValuesOverlayMap()["networkPolicy"].(map[string]interface{}); ok {
			if enabled, ok := networkPolicy["enabled"].(bool); ok {
				return enabled
			}
		}
	}

	return false
}

func isKEDAJMXExporterEnabled(xtrinode *analyticsv1.XTrinode) bool {
	return xtrinode.Spec.KEDA != nil &&
		xtrinode.Spec.KEDA.JMXExporter != nil &&
		xtrinode.Spec.KEDA.JMXExporter.Enabled
}

func roleJMXValues(xtrinode *analyticsv1.XTrinode, role string) map[string]interface{} {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return nil
	}

	jmx, ok := xtrinode.Spec.GetValuesOverlayMap()["jmx"].(map[string]interface{})
	if !ok {
		return nil
	}

	values := make(map[string]interface{})
	for key, value := range jmx {
		if key == "coordinator" || key == "worker" {
			continue
		}
		values[key] = value
	}

	if roleOverrides, ok := jmx[role].(map[string]interface{}); ok {
		for key, value := range roleOverrides {
			if key == "exporter" {
				if baseExporter, ok := values[key].(map[string]interface{}); ok {
					if roleExporter, ok := value.(map[string]interface{}); ok {
						merged := make(map[string]interface{}, len(baseExporter)+len(roleExporter))
						for baseKey, baseValue := range baseExporter {
							merged[baseKey] = baseValue
						}
						for roleKey, roleValue := range roleExporter {
							merged[roleKey] = roleValue
						}
						values[key] = merged
						continue
					}
				}
			}
			values[key] = value
		}
	}

	if len(values) == 0 {
		return nil
	}
	return values
}

func jmxEnabled(xtrinode *analyticsv1.XTrinode, role string) bool {
	if isKEDAJMXExporterEnabled(xtrinode) {
		return true
	}
	if values := roleJMXValues(xtrinode, role); values != nil {
		if enabled, ok := values["enabled"].(bool); ok {
			return enabled
		}
	}
	return false
}

func jmxExporterEnabled(xtrinode *analyticsv1.XTrinode, role string) bool {
	if isKEDAJMXExporterEnabled(xtrinode) {
		return true
	}
	if values := roleJMXValues(xtrinode, role); values != nil {
		if exporter, ok := values["exporter"].(map[string]interface{}); ok {
			if enabled, ok := exporter["enabled"].(bool); ok {
				return enabled
			}
		}
	}
	return false
}

func jmxRegistryPort(xtrinode *analyticsv1.XTrinode, role string) int32 {
	if values := roleJMXValues(xtrinode, role); values != nil {
		if port, ok := ParseInt32(values["registryPort"]); ok && port > 0 {
			return port
		}
	}
	return config.TrinoJMXPort
}

func jmxServerPort(xtrinode *analyticsv1.XTrinode, role string) int32 {
	if values := roleJMXValues(xtrinode, role); values != nil {
		if port, ok := ParseInt32(values["serverPort"]); ok && port > 0 {
			return port
		}
	}
	return config.TrinoJMXServerPort
}

func jmxExporterPort(xtrinode *analyticsv1.XTrinode, role string) int32 {
	port := int32(config.JMXExporterPort)
	if xtrinode.Spec.KEDA != nil && xtrinode.Spec.KEDA.JMXExporter != nil && xtrinode.Spec.KEDA.JMXExporter.Port != nil {
		port = *xtrinode.Spec.KEDA.JMXExporter.Port
	}
	if values := roleJMXValues(xtrinode, role); values != nil {
		if exporter, ok := values["exporter"].(map[string]interface{}); ok {
			if overlayPort, ok := ParseInt32(exporter["port"]); ok && overlayPort > 0 {
				port = overlayPort
			}
		}
	}
	return port
}

func jmxExporterConfigMapName(xtrinode *analyticsv1.XTrinode, role string) string {
	if xtrinode.Spec.KEDA != nil &&
		xtrinode.Spec.KEDA.JMXExporter != nil &&
		xtrinode.Spec.KEDA.JMXExporter.ConfigMap != "" {
		return xtrinode.Spec.KEDA.JMXExporter.ConfigMap
	}
	return fmt.Sprintf("trino-%s-jmx-exporter-config-%s", xtrinode.Name, role)
}

// ParseInt64 accepts the numeric shapes commonly produced by YAML/JSON decoding.
func ParseInt64(val interface{}) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	default:
		return 0, false
	}
}

// ParseInt32 parses a numeric value to int32
func ParseInt32(val interface{}) (value int32, ok bool) {
	i64, ok := ParseInt64(val)
	if !ok {
		return 0, false
	}
	return int32(i64), true
}

// ParseBool parses a boolean value
func ParseBool(val interface{}) (value, ok bool) {
	if b, ok := val.(bool); ok {
		return b, true
	}
	return false, false
}

// ParseString parses a string value
func ParseString(val interface{}) (string, bool) {
	if s, ok := val.(string); ok {
		return s, true
	}
	return "", false
}

// ParseMap parses a map[string]interface{} value
func ParseMap(val interface{}) (map[string]interface{}, bool) {
	if m, ok := val.(map[string]interface{}); ok {
		return m, true
	}
	return nil, false
}

// ParseSlice parses a []interface{} value
func ParseSlice(val interface{}) ([]interface{}, bool) {
	if s, ok := val.([]interface{}); ok {
		return s, true
	}
	return nil, false
}

// GetInt64FromMap safely gets an int64 value from a map
func GetInt64FromMap(m map[string]interface{}, key string) (int64, error) {
	val, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("key %s not found", key)
	}
	i64, ok := ParseInt64(val)
	if !ok {
		return 0, fmt.Errorf("key %s is not a numeric value", key)
	}
	return i64, nil
}

// GetInt32FromMap safely gets an int32 value from a map
func GetInt32FromMap(m map[string]interface{}, key string) (int32, error) {
	val, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("key %s not found", key)
	}
	i32, ok := ParseInt32(val)
	if !ok {
		return 0, fmt.Errorf("key %s is not a numeric value", key)
	}
	return i32, nil
}
