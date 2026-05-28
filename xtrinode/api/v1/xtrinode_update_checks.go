package v1

import (
	"reflect"
)

// checkSizeChange validates size changes
func checkSizeChange(c *updateCtx, oldObj, newObj *XTrinode) {
	if oldObj.Spec.Size == newObj.Spec.Size {
		return
	}

	if isSizeUpgrade(oldObj.Spec.Size, newObj.Spec.Size) {
		c.warn(buildSizeUpgradeWarning(oldObj.Spec.Size, newObj.Spec.Size))
	} else if isSizeDowngrade(oldObj.Spec.Size, newObj.Spec.Size) {
		c.requireBreakGlass("spec.size (downgrade)")
		if c.breakGlassEnabled {
			c.warn(buildSizeDowngradeWarning(oldObj.Spec.Size, newObj.Spec.Size))
		}
	}
}

// checkWorkerScaling validates worker scaling changes
func checkWorkerScaling(c *updateCtx, oldObj, newObj *XTrinode) {
	if workerScalingChanged(oldObj, newObj) {
		if oldObj.Spec.MaxWorkers != nil && newObj.Spec.MaxWorkers != nil {
			c.warn(buildWorkerScalingWarning(*oldObj.Spec.MaxWorkers, *newObj.Spec.MaxWorkers))
		}
	}
}

// checkSuspended validates suspended changes
func checkSuspended(c *updateCtx, oldObj, newObj *XTrinode) {
	if oldObj.Spec.Suspended != newObj.Spec.Suspended && newObj.Spec.Suspended {
		c.warn(buildSuspendedChangeWarning())
	}
}

// checkNodePoolChanges validates all nodePool changes
func checkNodePoolChanges(c *updateCtx, oldObj, newObj *XTrinode) {
	// NodePool presence changes
	if nodePoolPresenceChanged(oldObj, newObj) {
		c.requireBreakGlass("spec.nodePool (presence)")
		if c.breakGlassEnabled {
			c.warn(buildNodePoolPresenceChangeWarning(newObj.Spec.NodePool != nil))
		}
	}

	// NodePool identity changes
	if nodePoolIdentityChanged(oldObj, newObj) {
		var changedFields []string
		if oldObj.Spec.NodePool != nil && newObj.Spec.NodePool != nil {
			if normalizeString(oldObj.Spec.NodePool.Provider) != normalizeString(newObj.Spec.NodePool.Provider) {
				changedFields = append(changedFields, "provider")
			}
			if oldObj.Spec.NodePool.Name != newObj.Spec.NodePool.Name {
				changedFields = append(changedFields, "name")
			}
			if oldObj.Spec.NodePool.ClusterName != newObj.Spec.NodePool.ClusterName {
				changedFields = append(changedFields, "clusterName")
			}
		}
		c.requireBreakGlass("spec.nodePool identity")
		if c.breakGlassEnabled {
			c.warn(buildNodePoolIdentityChangeWarning(changedFields))
		}
	}

	// NodePool shape changes
	if nodePoolShapeChanged(oldObj, newObj) {
		c.requireBreakGlass("spec.nodePool shape")
		if c.breakGlassEnabled {
			c.warn(buildNodePoolShapeChangeWarning())
		}
	}

	// NodePool scheduling changes
	if nodePoolSchedulingChanged(oldObj, newObj) {
		if oldObj.Spec.NodePool != nil && newObj.Spec.NodePool != nil {
			if compareStringSlices(oldObj.Spec.NodePool.Zones, newObj.Spec.NodePool.Zones) {
				c.warn(buildNodePoolSchedulingChangeWarning("zones"))
			}
			if compareStringMaps(oldObj.Spec.NodePool.NodeLabels, newObj.Spec.NodePool.NodeLabels) {
				c.warn(buildNodePoolSchedulingChangeWarning("node labels"))
			}
			if compareStringMaps(oldObj.Spec.NodePool.ResourceTags, newObj.Spec.NodePool.ResourceTags) {
				c.warn(buildNodePoolSchedulingChangeWarning("resource tags"))
			}
			if !reflect.DeepEqual(oldObj.Spec.NodePool.NodeTaints, newObj.Spec.NodePool.NodeTaints) {
				c.warn(buildNodePoolSchedulingChangeWarning("node taints"))
			}
		}
	}

	// NodePool prewarm changes
	if oldObj.Spec.NodePool != nil && newObj.Spec.NodePool != nil {
		if !reflect.DeepEqual(oldObj.Spec.NodePool.Prewarm, newObj.Spec.NodePool.Prewarm) {
			c.warn(buildNodePoolPrewarmChangeWarning())
		}
		if nodePoolDeletionPolicyChanged(oldObj, newObj) {
			oldPolicy := normalizeNodePoolDeletionPolicy(oldObj.Spec.NodePool)
			newPolicy := normalizeNodePoolDeletionPolicy(newObj.Spec.NodePool)
			if oldPolicy != NodePoolDeletionPolicyDelete && newPolicy == NodePoolDeletionPolicyDelete {
				c.requireBreakGlass("spec.nodePool.deletionPolicy")
			}
			c.warn(buildNodePoolDeletionPolicyChangeWarning())
		}
	}
}

// checkRoutingChanges validates all routing changes
func checkRoutingChanges(c *updateCtx, oldObj, newObj *XTrinode) {
	// Routing presence changes
	if routingPresenceChanged(oldObj, newObj) {
		c.requireBreakGlass("spec.routing (presence)")
		if c.breakGlassEnabled {
			c.warn(buildRoutingPresenceChangeWarning(newObj.Spec.Routing != nil))
		}
	}

	// Routing identity changes
	if routingIdentityChanged(oldObj, newObj) {
		var changedFields []string
		if oldObj.Spec.Routing != nil && newObj.Spec.Routing != nil {
			if oldObj.Spec.Routing.RoutingGroup != newObj.Spec.Routing.RoutingGroup {
				changedFields = append(changedFields, "routingGroup")
			}
			if oldObj.Spec.Routing.Hostname != newObj.Spec.Routing.Hostname {
				changedFields = append(changedFields, "hostname")
			}
			if oldObj.Spec.Routing.HostnameDomain != newObj.Spec.Routing.HostnameDomain {
				changedFields = append(changedFields, "hostnameDomain")
			}
			if oldObj.Spec.Routing.Header != newObj.Spec.Routing.Header {
				changedFields = append(changedFields, "header")
			}
			if oldObj.Spec.Routing.Default != newObj.Spec.Routing.Default {
				changedFields = append(changedFields, "default")
			}
		}
		c.requireBreakGlass("spec.routing identity")
		if c.breakGlassEnabled {
			c.warn(buildRoutingIdentityChangeWarning(changedFields))
		}
	}
}

// checkCatalogSelector validates catalogSelector changes
func checkCatalogSelector(c *updateCtx, oldObj, newObj *XTrinode) {
	if !reflect.DeepEqual(oldObj.Spec.CatalogSelector, newObj.Spec.CatalogSelector) {
		c.warn(buildCatalogSelectorChangeWarning())
	}
}

// checkResourceGroupsProfile validates resourceGroupsProfile changes
func checkResourceGroupsProfile(c *updateCtx, oldObj, newObj *XTrinode) {
	if oldObj.Spec.ResourceGroupsProfile != newObj.Spec.ResourceGroupsProfile {
		c.warn(buildResourceGroupsProfileChangeWarning())
	}
}

// checkCustomConfigMaps validates customConfigMaps changes
func checkCustomConfigMaps(c *updateCtx, oldObj, newObj *XTrinode) {
	if compareStringSlices(oldObj.Spec.CustomConfigMaps, newObj.Spec.CustomConfigMaps) {
		c.warn(buildCustomConfigMapsChangeWarning())
	}
}

// checkLimits validates limits changes
func checkLimits(c *updateCtx, oldObj, newObj *XTrinode) {
	if !reflect.DeepEqual(oldObj.Spec.Limits, newObj.Spec.Limits) {
		c.warn(buildLimitsChangeWarning())
	}
}

// checkValuesOverlay validates valuesOverlay changes
func checkValuesOverlay(c *updateCtx, oldObj, newObj *XTrinode) {
	if compareValuesOverlay(oldObj.Spec.ValuesOverlay, newObj.Spec.ValuesOverlay) {
		c.warn(buildValuesOverlayChangeWarning())
	}
}

// checkFaultTolerantExecution validates fault-tolerant execution changes.
func checkFaultTolerantExecution(c *updateCtx, oldObj, newObj *XTrinode) {
	if !reflect.DeepEqual(oldObj.Spec.FaultTolerantExecution, newObj.Spec.FaultTolerantExecution) {
		c.warn(buildFaultTolerantChangeWarning())
	}
}

// checkKEDAChanges validates all KEDA changes
func checkKEDAChanges(c *updateCtx, oldObj, newObj *XTrinode) {
	// KEDA presence changes
	if kedaPresenceChanged(oldObj, newObj) {
		c.warn(buildKEDAPresenceChangeWarning(newObj.Spec.KEDA != nil))
	}

	// KEDA config changes
	if oldObj.Spec.KEDA != nil && newObj.Spec.KEDA != nil {
		if oldObj.Spec.KEDA.ScalerType != newObj.Spec.KEDA.ScalerType {
			c.warn(buildKEDAConfigChangeWarning("scalerType"))
		}
		if oldObj.Spec.KEDA.ScalingMetric != newObj.Spec.KEDA.ScalingMetric {
			c.warn(buildKEDAConfigChangeWarning("scalingMetric"))
		}
		if comparePointerBool(oldObj.Spec.KEDA.Enabled, newObj.Spec.KEDA.Enabled) {
			c.warn(buildKEDAConfigChangeWarning("enabled"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.Threshold, newObj.Spec.KEDA.Threshold) {
			c.warn(buildKEDAConfigChangeWarning("threshold"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.PrometheusServer, newObj.Spec.KEDA.PrometheusServer) {
			c.warn(buildKEDAConfigChangeWarning("prometheusServer"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.PrometheusQuery, newObj.Spec.KEDA.PrometheusQuery) {
			c.warn(buildKEDAConfigChangeWarning("prometheusQuery"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.HTTPEndpoint, newObj.Spec.KEDA.HTTPEndpoint) {
			c.warn(buildKEDAConfigChangeWarning("httpEndpoint"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.HTTPValueLocation, newObj.Spec.KEDA.HTTPValueLocation) {
			c.warn(buildKEDAConfigChangeWarning("httpValueLocation"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.ScaleDownCooldown, newObj.Spec.KEDA.ScaleDownCooldown) {
			c.warn(buildKEDAConfigChangeWarning("scaleDownCooldown"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.ScaleUpCooldown, newObj.Spec.KEDA.ScaleUpCooldown) {
			c.warn(buildKEDAConfigChangeWarning("scaleUpCooldown"))
		}
		if !reflect.DeepEqual(oldObj.Spec.KEDA.JMXExporter, newObj.Spec.KEDA.JMXExporter) {
			c.warn(buildKEDAConfigChangeWarning("jmxExporter"))
		}
	}
}

// checkTLSChanges validates TLS changes
func checkTLSChanges(c *updateCtx, oldObj, newObj *XTrinode) {
	if !reflect.DeepEqual(oldObj.Spec.TLS, newObj.Spec.TLS) {
		c.warn(buildTLSChangeWarning())
	}
}

// checkHelmChartConfigChanges validates HelmChartConfig changes
func checkHelmChartConfigChanges(c *updateCtx, oldObj, newObj *XTrinode) {
	// Only check presence or deep equality, not both.
	if helmChartConfigPresenceChanged(oldObj, newObj) {
		c.warn(buildHelmChartConfigChangeWarning())
	} else if !reflect.DeepEqual(oldObj.Spec.HelmChartConfig, newObj.Spec.HelmChartConfig) {
		c.warn(buildHelmChartConfigChangeWarning())
	}
}

// checkOperatorNodePoolDefaults validates operatorNodePoolDefaults changes
func checkOperatorNodePoolDefaults(c *updateCtx, oldObj, newObj *XTrinode) {
	if !reflect.DeepEqual(oldObj.Spec.OperatorNodePoolDefaults, newObj.Spec.OperatorNodePoolDefaults) {
		c.warn(buildOperatorNodePoolDefaultsChangeWarning())
	}
}
