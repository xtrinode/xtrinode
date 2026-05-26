package v1

import (
	"fmt"
	"strings"
)

// Warning message builders for "allow + warn" and "break-glass accepted" scenarios

// buildSizeUpgradeWarning returns a warning for size upgrades
func buildSizeUpgradeWarning(oldSize, newSize string) string {
	return fmt.Sprintf("size change from %s to %s will roll out new pods; expect coordinator/worker restarts", oldSize, newSize)
}

// buildSizeDowngradeWarning returns a warning for size downgrades (break-glass accepted)
func buildSizeDowngradeWarning(oldSize, newSize string) string {
	return fmt.Sprintf("size downgrade from %s to %s can cause resource pressure; prefer drain+replace; break-glass accepted", oldSize, newSize)
}

// buildRoutingIdentityChangeWarning returns a warning for routing identity changes
func buildRoutingIdentityChangeWarning(fields []string) string {
	return fmt.Sprintf("routing selector change (%s) can break clients and reroute traffic; break-glass accepted", strings.Join(fields, ", "))
}

// buildNodePoolIdentityChangeWarning returns a warning for nodePool identity changes
func buildNodePoolIdentityChangeWarning(fields []string) string {
	return fmt.Sprintf("node pool identity change (%s) triggers infra re-provisioning; break-glass accepted", strings.Join(fields, ", "))
}

// buildNodePoolShapeChangeWarning returns a warning for nodePool shape changes
func buildNodePoolShapeChangeWarning() string {
	return "node pool shape change likely triggers node replacement; break-glass accepted"
}

// buildNodePoolPresenceChangeWarning returns a warning for adding/removing nodePool
func buildNodePoolPresenceChangeWarning(added bool) string {
	if added {
		return "adding node pool changes infra ownership; break-glass accepted"
	}
	return "removing node pool releases infra ownership; break-glass accepted"
}

// buildRoutingPresenceChangeWarning returns a warning for adding/removing routing
func buildRoutingPresenceChangeWarning(added bool) string {
	if added {
		return "adding routing changes how clients must target the runtime; break-glass accepted"
	}
	return "removing routing can make the route unreachable; break-glass accepted"
}

// buildValuesOverlayChangeWarning returns a warning for valuesOverlay changes
func buildValuesOverlayChangeWarning() string {
	return "valuesOverlay is privileged input that can alter pod security, volumes, images, networking, and rollout behavior; review carefully"
}

// buildWorkerScalingWarning returns a warning for large worker scaling changes
func buildWorkerScalingWarning(oldMax, newMax int32) string {
	return fmt.Sprintf("maxWorkers change from %d to %d (>2x) changes quota/limits and can amplify blast radius", oldMax, newMax)
}

// buildCatalogSelectorChangeWarning returns a warning for catalog selector changes
func buildCatalogSelectorChangeWarning() string {
	return "catalogSelector change can trigger mounts/config changes and a rollout"
}

// buildResourceGroupsProfileChangeWarning returns a warning for resource groups profile changes
func buildResourceGroupsProfileChangeWarning() string {
	return "resourceGroupsProfile change likely triggers rollout"
}

// buildCustomConfigMapsChangeWarning returns a warning for custom config maps changes
func buildCustomConfigMapsChangeWarning() string {
	return "customConfigMaps change likely triggers rollout"
}

// buildLimitsChangeWarning returns a warning for limits changes
func buildLimitsChangeWarning() string {
	return "limits change alters runtime guardrails"
}

// buildFaultTolerantChangeWarning returns a warning for fault tolerant changes
func buildFaultTolerantChangeWarning() string {
	return "fault-tolerant execution changes can materially change cluster behavior/perf characteristics"
}

// buildKEDAPresenceChangeWarning returns a warning for adding/removing KEDA
func buildKEDAPresenceChangeWarning(added bool) string {
	if added {
		return "adding KEDA creates autoscaling resources; behavior shift expected"
	}
	return "removing KEDA deletes autoscaling resources; behavior shift expected"
}

// buildKEDAConfigChangeWarning returns a warning for KEDA config changes
func buildKEDAConfigChangeWarning(field string) string {
	return fmt.Sprintf("KEDA %s change alters autoscaling behavior", field)
}

// buildTLSChangeWarning returns a warning for TLS changes
func buildTLSChangeWarning() string {
	return "TLS change usually forces restarts and can break connectivity if misconfigured"
}

// buildHelmChartConfigChangeWarning returns a warning for helmChartConfig changes
func buildHelmChartConfigChangeWarning() string {
	return "helmChartConfig change can trigger rollouts"
}

// buildNodePoolSchedulingChangeWarning returns a warning for nodePool scheduling changes
func buildNodePoolSchedulingChangeWarning(field string) string {
	return fmt.Sprintf("node pool %s change often triggers node replacement/rebalancing", field)
}

// buildNodePoolPrewarmChangeWarning returns a warning for nodePool prewarm changes
func buildNodePoolPrewarmChangeWarning() string {
	return "node pool prewarm change alters node provisioning behavior"
}

// buildNodePoolDeletionPolicyChangeWarning returns a warning for nodePool deletion policy changes
func buildNodePoolDeletionPolicyChangeWarning() string {
	return "nodePool deletionPolicy change affects whether provider node-pool resources are deleted or retained during finalization"
}

// buildSuspendedChangeWarning returns a warning for suspended changes
func buildSuspendedChangeWarning() string {
	return "suspending runtime implies service disruption"
}

// buildBreakGlassNotNeededWarning returns a warning when break-glass is present but not needed
func buildBreakGlassNotNeededWarning() string {
	return "break-glass annotation present but no gated changes detected"
}

// buildOperatorNodePoolDefaultsChangeWarning returns a warning for operator nodePool defaults changes
func buildOperatorNodePoolDefaultsChangeWarning() string {
	return "operatorNodePoolDefaults change can affect node pool defaults and infra outcomes"
}
