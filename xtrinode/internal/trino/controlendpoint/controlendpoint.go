package controlendpoint

import (
	"strconv"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// HTTPPort returns the Trino HTTP service port used for generated Services and internal control calls.
func HTTPPort(xtrinode *analyticsv1.XTrinode) int {
	port := config.TrinoPortHTTP
	if xtrinode == nil {
		return port
	}
	values := xtrinode.Spec.GetValuesOverlayMap()
	if values == nil {
		return port
	}
	service, ok := values["service"].(map[string]interface{})
	if !ok {
		return port
	}
	if svcPort, ok := parsePort(service["port"]); ok {
		return svcPort
	}
	return port
}

// CoordinatorURL returns the HTTP coordinator service URL used by gateway routes and lifecycle checks.
func CoordinatorURL(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode == nil {
		return ""
	}
	return config.BuildCoordinatorURLWithPort(xtrinode.Name, xtrinode.Namespace, HTTPPort(xtrinode))
}

func parsePort(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return v, true
		}
	case int32:
		if v > 0 {
			return int(v), true
		}
	case int64:
		if v > 0 {
			return int(v), true
		}
	case float64:
		if v > 0 && v == float64(int(v)) {
			return int(v), true
		}
	case string:
		parsed, err := strconv.Atoi(v)
		if err == nil && parsed > 0 {
			return parsed, true
		}
	}
	return 0, false
}
