package controllers

import (
	"fmt"
	"strings"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/runtimeshape"
	"github.com/xtrinode/xtrinode/internal/sizing"
	"github.com/xtrinode/xtrinode/internal/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func setNodePoolFitCondition(xtrinode *analyticsv1.XTrinode) {
	if xtrinode == nil || xtrinode.Spec.NodePool == nil {
		return
	}
	provider := normalizeNodePoolString(xtrinode.Spec.NodePool.Provider)
	configured := configuredNodePoolMachineType(xtrinode.Spec.NodePool)
	if provider == "" || configured == "" {
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolFitReady, metav1.ConditionUnknown, status.ConditionReasonNodePoolFitUnknown, "Node-pool machine type is not configured yet")
		return
	}
	configuredRank, configuredKnown := nodePoolMachineRank(provider, configured)
	if !configuredKnown {
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolFitReady, metav1.ConditionUnknown, status.ConditionReasonNodePoolFitUnknown, fmt.Sprintf("Node-pool machine type %q is not in the built-in fit table", configured))
		return
	}
	shape, err := runtimeshape.Resolve(xtrinode)
	if err != nil {
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolFitReady, metav1.ConditionUnknown, status.ConditionReasonNodePoolFitUnknown, fmt.Sprintf("Failed to resolve runtime shape for node-pool fit: %v", err))
		return
	}
	requiredPreset := requiredPresetForWorkerResources(shape.Worker)
	recommended := sizing.GetRecommendedMachineType(requiredPreset, provider)
	recommendedRank, recommendedKnown := nodePoolMachineRank(provider, recommended)
	if requiredPreset == "" || recommended == "" || !recommendedKnown {
		status.SetCondition(xtrinode, status.ConditionTypeNodePoolFitReady, metav1.ConditionUnknown, status.ConditionReasonNodePoolFitUnknown, "Node-pool fit could not be checked for the resolved worker resources")
		return
	}
	if configuredRank < recommendedRank {
		status.SetCondition(
			xtrinode,
			status.ConditionTypeNodePoolFitReady,
			metav1.ConditionFalse,
			status.ConditionReasonNodePoolFitFailed,
			fmt.Sprintf("Node-pool machine type %q may not fit resolved worker resources; recommended %s machine type is %q", configured, provider, recommended),
		)
		return
	}
	status.SetCondition(xtrinode, status.ConditionTypeNodePoolFitReady, metav1.ConditionTrue, status.ConditionReasonNodePoolFitOK, fmt.Sprintf("Node-pool machine type %q fits the built-in recommendation for resolved worker resources", configured))
}

func configuredNodePoolMachineType(np *analyticsv1.NodePoolSpec) string {
	if np == nil {
		return ""
	}
	switch normalizeNodePoolString(np.Provider) {
	case "azure":
		if np.Azure != nil {
			return np.Azure.VMSize
		}
	case "aws":
		if np.AWS != nil {
			return np.AWS.InstanceType
		}
	case "gcp":
		if np.GCP != nil {
			return np.GCP.MachineType
		}
	}
	return ""
}

func nodePoolMachineRank(provider, machineType string) (int, bool) {
	ranks := map[string]int{
		"azure/standard_d2as_v5":  1,
		"azure/standard_d8as_v5":  2,
		"azure/standard_d16as_v5": 3,
		"azure/standard_d32as_v5": 4,
		"azure/standard_d64as_v5": 5,
		"aws/m5.large":            1,
		"aws/m5.2xlarge":          2,
		"aws/m5.4xlarge":          3,
		"aws/m5.8xlarge":          4,
		"aws/m5.16xlarge":         5,
		"gcp/n1-standard-2":       1,
		"gcp/n1-standard-8":       2,
		"gcp/n1-standard-16":      3,
		"gcp/n1-standard-32":      4,
		"gcp/n1-standard-64":      5,
	}
	rank, ok := ranks[normalizeNodePoolString(provider)+"/"+normalizeNodePoolString(machineType)]
	return rank, ok
}

func requiredPresetForWorkerResources(resources corev1.ResourceRequirements) string {
	needCPU, needMemory, ok := workerResourceNeed(resources)
	if !ok {
		return ""
	}
	for _, presetName := range []string{"xs", "s", "m", "l", "xl"} {
		preset, ok := sizing.GetPreset(presetName)
		if !ok {
			continue
		}
		presetCPU, err := resource.ParseQuantity(preset.WorkerCPULim)
		if err != nil {
			continue
		}
		presetMemory, err := resource.ParseQuantity(preset.WorkerMemLim)
		if err != nil {
			continue
		}
		if presetCPU.Cmp(needCPU) >= 0 && presetMemory.Cmp(needMemory) >= 0 {
			return presetName
		}
	}
	return "xl"
}

func workerResourceNeed(resources corev1.ResourceRequirements) (cpu, memory resource.Quantity, ok bool) {
	applyLargerNodePoolQuantity(&cpu, resources.Requests[corev1.ResourceCPU])
	applyLargerNodePoolQuantity(&cpu, resources.Limits[corev1.ResourceCPU])
	applyLargerNodePoolQuantity(&memory, resources.Requests[corev1.ResourceMemory])
	applyLargerNodePoolQuantity(&memory, resources.Limits[corev1.ResourceMemory])
	return cpu, memory, cpu.Sign() > 0 || memory.Sign() > 0
}

func applyLargerNodePoolQuantity(current *resource.Quantity, candidate resource.Quantity) {
	if candidate.Sign() > 0 && candidate.Cmp(*current) > 0 {
		*current = candidate.DeepCopy()
	}
}

func normalizeNodePoolString(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
