package v1

import (
	"fmt"
	"strings"

	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/sizing"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func nodePoolPlacementWarnings(t *XTrinode) admission.Warnings {
	if t == nil {
		return nil
	}
	if t.Spec.NodePool == nil || t.Spec.NodePool.SchedulePods {
		return nil
	}
	poolName := t.Spec.NodePool.Name
	if poolName == "" && t.Name != "" {
		poolName = fmt.Sprintf("%s%s", t.Name, config.NodePoolNameSuffix)
	}
	if poolName == "" {
		return nil
	}

	selectorValues := placementNodePoolSelectorValues(t.Spec.Placement)
	if len(selectorValues) == 0 {
		return admission.Warnings{
			fmt.Sprintf("spec.nodePool provisions node pool %q, but runtime pods are not bound to it; set spec.nodePool.schedulePods=true or target %s in placement", poolName, config.NodePoolSchedulingLabel),
		}
	}
	for _, value := range selectorValues {
		if value == poolName {
			return nil
		}
	}
	return admission.Warnings{
		fmt.Sprintf("placement targets node pool label %q while spec.nodePool provisions %q; use spec.nodePool.schedulePods=true or align placement with the provisioned pool", selectorValues[0], poolName),
	}
}

func placementNodePoolSelectorValues(placement *PlacementSpec) []string {
	if placement == nil {
		return nil
	}
	var values []string
	if value, ok := placement.NodeSelector[config.NodePoolSchedulingLabel]; ok {
		values = append(values, value)
	}
	if placement.Coordinator != nil {
		if value, ok := placement.Coordinator.NodeSelector[config.NodePoolSchedulingLabel]; ok {
			values = append(values, value)
		}
	}
	if placement.Worker != nil {
		if value, ok := placement.Worker.NodeSelector[config.NodePoolSchedulingLabel]; ok {
			values = append(values, value)
		}
	}
	return values
}

func nodePoolFitWarnings(t *XTrinode) admission.Warnings {
	if t == nil || t.Spec.NodePool == nil {
		return nil
	}
	provider := normalizeString(t.Spec.NodePool.Provider)
	configured := configuredNodePoolMachineType(t.Spec.NodePool)
	if provider == "" || configured == "" {
		return nil
	}
	recommended, source := t.recommendedNodePoolMachineType(provider)
	if recommended == "" {
		return nil
	}
	configuredRank, configuredKnown := nodePoolMachineRank(provider, configured)
	recommendedRank, recommendedKnown := nodePoolMachineRank(provider, recommended)
	if !configuredKnown || !recommendedKnown || configuredRank >= recommendedRank {
		return nil
	}
	return admission.Warnings{
		fmt.Sprintf("node pool machine type %q may not fit resolved worker resources; recommended for %s is %q", configured, source, recommended),
	}
}

func configuredNodePoolMachineType(np *NodePoolSpec) string {
	if np == nil {
		return ""
	}
	switch normalizeString(np.Provider) {
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
	provider = normalizeString(provider)
	machineType = normalizeString(machineType)
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
	rank, ok := ranks[provider+"/"+machineType]
	return rank, ok
}

// validateNodePool validates node pool configuration
func (t *XTrinode) validateNodePool(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	np := t.Spec.NodePool

	// Validate provider
	if np.Provider == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("provider"), "provider is required"))
	} else if !config.ValidProviders[strings.ToLower(np.Provider)] {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("provider"),
			np.Provider,
			fmt.Sprintf("provider must be one of: %v", config.ProviderList)))
	}

	allErrs = append(allErrs, t.validateNodePoolProviderConfig(np, fldPath)...)

	providerMode := normalizeString(np.ProviderMode)
	if providerMode == "" {
		providerMode = "self-managed"
	}
	if providerMode == "self-managed" && len(np.NodeTaints) > 0 {
		allErrs = append(allErrs, field.Forbidden(
			fldPath.Child("nodeTaints"),
			"self-managed node taints must be configured in the referenced bootstrap template's nodeRegistration.taints; managed node pools may use spec.nodePool.nodeTaints"))
	}
	if np.DeletionPolicy != "" {
		switch np.DeletionPolicy {
		case NodePoolDeletionPolicyDelete, NodePoolDeletionPolicyRetain, NodePoolDeletionPolicyScaleToZero:
		default:
			allErrs = append(allErrs, field.NotSupported(
				fldPath.Child("deletionPolicy"),
				np.DeletionPolicy,
				[]string{NodePoolDeletionPolicyDelete, NodePoolDeletionPolicyRetain, NodePoolDeletionPolicyScaleToZero}))
		}
	}

	bounds := config.NodePoolValidationBoundsFromEnv()

	// Validate minNodes
	if np.MinNodes != nil {
		if *np.MinNodes < bounds.MinNodesMin {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("minNodes"),
				*np.MinNodes,
				fmt.Sprintf("minNodes must be at least %d", bounds.MinNodesMin)))
		}
		if np.MaxNodes != nil && *np.MinNodes > *np.MaxNodes {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("minNodes"),
				*np.MinNodes,
				"minNodes must be less than or equal to maxNodes"))
		}
	}

	// Validate maxNodes
	if np.MaxNodes != nil {
		if *np.MaxNodes < bounds.MaxNodesMin {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("maxNodes"),
				*np.MaxNodes,
				fmt.Sprintf("maxNodes must be at least %d", bounds.MaxNodesMin)))
		}
		if *np.MaxNodes > bounds.MaxNodesMax {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("maxNodes"),
				*np.MaxNodes,
				fmt.Sprintf("maxNodes must be at most %d", bounds.MaxNodesMax)))
		}
	}

	// Validate OS disk size
	if np.OSDiskGB != nil {
		if *np.OSDiskGB < bounds.OSDiskGBMin {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("osDiskGB"),
				*np.OSDiskGB,
				fmt.Sprintf("osDiskGB must be at least %d", bounds.OSDiskGBMin)))
		}
		if *np.OSDiskGB > bounds.OSDiskGBMax {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("osDiskGB"),
				*np.OSDiskGB,
				fmt.Sprintf("osDiskGB must be at most %d", bounds.OSDiskGBMax)))
		}
	}

	effectiveMinNodes, minNodesPath := t.effectiveNodePoolMinNodes(fldPath)
	effectiveMaxNodes, _ := t.effectiveNodePoolMaxNodes(fldPath)
	if effectiveMinNodes >= 0 && effectiveMaxNodes >= 1 && effectiveMinNodes > effectiveMaxNodes {
		allErrs = append(allErrs, field.Invalid(
			minNodesPath,
			effectiveMinNodes,
			fmt.Sprintf("effective minNodes must be less than or equal to effective maxNodes (%d)", effectiveMaxNodes)))
	}

	if np.SchedulePods {
		poolName := np.Name
		if poolName == "" {
			poolName = fmt.Sprintf("%s%s", t.Name, config.NodePoolNameSuffix)
		}
		if np.NodeLabels != nil {
			if existing, ok := np.NodeLabels[config.NodePoolSchedulingLabel]; ok && existing != poolName {
				allErrs = append(allErrs, field.Invalid(
					fldPath.Child("nodeLabels").Key(config.NodePoolSchedulingLabel),
					existing,
					fmt.Sprintf("must match nodePool name %q when schedulePods is true", poolName)))
			}
		}
	}

	return allErrs
}

func (t *XTrinode) effectiveNodePoolMinNodes(fldPath *field.Path) (int32, *field.Path) {
	if t.Spec.NodePool != nil && t.Spec.NodePool.MinNodes != nil {
		return *t.Spec.NodePool.MinNodes, fldPath.Child("minNodes")
	}
	if t.Spec.OperatorNodePoolDefaults != nil && t.Spec.OperatorNodePoolDefaults.DefaultMinNodes != nil {
		return *t.Spec.OperatorNodePoolDefaults.DefaultMinNodes, field.NewPath("spec", "operatorNodePoolDefaults", "defaultMinNodes")
	}
	return config.NodePoolDefaultMinNodesValue(), fldPath.Child("minNodes")
}

func (t *XTrinode) effectiveNodePoolMaxNodes(fldPath *field.Path) (int32, *field.Path) {
	if t.Spec.NodePool != nil && t.Spec.NodePool.MaxNodes != nil {
		return *t.Spec.NodePool.MaxNodes, fldPath.Child("maxNodes")
	}
	if t.Spec.OperatorNodePoolDefaults != nil && t.Spec.OperatorNodePoolDefaults.DefaultMaxNodes != nil {
		return *t.Spec.OperatorNodePoolDefaults.DefaultMaxNodes, field.NewPath("spec", "operatorNodePoolDefaults", "defaultMaxNodes")
	}
	return config.NodePoolDefaultMaxNodesValue(), fldPath.Child("maxNodes")
}

func (t *XTrinode) validateNodePoolProviderConfig(np *NodePoolSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	provider := normalizeString(np.Provider)
	switch provider {
	case "azure":
		if np.Azure == nil || np.Azure.VMSize == "" {
			recommendation, source := t.recommendedNodePoolMachineType("azure")
			msg := "azure.vmSize is required when provider is azure"
			if recommendation != "" {
				msg = fmt.Sprintf("azure.vmSize is required when provider is azure (recommended for %s: %s)", source, recommendation)
			}
			allErrs = append(allErrs, field.Required(fldPath.Child("azure", "vmSize"), msg))
		}
	case "aws":
		if np.AWS == nil || np.AWS.InstanceType == "" {
			recommendation, source := t.recommendedNodePoolMachineType("aws")
			msg := "aws.instanceType is required when provider is aws"
			if recommendation != "" {
				msg = fmt.Sprintf("aws.instanceType is required when provider is aws (recommended for %s: %s)", source, recommendation)
			}
			allErrs = append(allErrs, field.Required(fldPath.Child("aws", "instanceType"), msg))
		}
	case "gcp":
		if np.GCP == nil || np.GCP.MachineType == "" {
			recommendation, source := t.recommendedNodePoolMachineType("gcp")
			msg := "gcp.machineType is required when provider is gcp"
			if recommendation != "" {
				msg = fmt.Sprintf("gcp.machineType is required when provider is gcp (recommended for %s: %s)", source, recommendation)
			}
			allErrs = append(allErrs, field.Required(fldPath.Child("gcp", "machineType"), msg))
		}
	}
	return allErrs
}

func (t *XTrinode) recommendedNodePoolMachineType(provider string) (machineType, source string) {
	provider = normalizeString(provider)
	size := normalizeString(t.Spec.Size)
	fallback := sizing.GetRecommendedMachineType(size, provider)
	fallbackSource := fmt.Sprintf("size '%s'", t.Spec.Size)

	resources, source, ok := t.resolvedWorkerResourcesForMachineRecommendation()
	if !ok {
		return fallback, fallbackSource
	}
	recommendationSize := recommendedPresetForWorkerResources(resources)
	if recommendationSize == "" {
		return fallback, fallbackSource
	}
	recommendation := sizing.GetRecommendedMachineType(recommendationSize, provider)
	if recommendation == "" {
		return fallback, fallbackSource
	}
	if source == runtimeRecommendationSourcePreset && recommendationSize == size {
		return recommendation, fallbackSource
	}
	return recommendation, "resolved worker resources"
}

const (
	runtimeRecommendationSourcePreset = "preset"
	runtimeRecommendationSourceTyped  = "typed"
)

func (t *XTrinode) resolvedWorkerResourcesForMachineRecommendation() (corev1.ResourceRequirements, string, bool) {
	size := normalizeString(t.Spec.Size)
	preset, ok := sizing.GetPreset(size)
	if !ok {
		return corev1.ResourceRequirements{}, "", false
	}
	resources, ok := workerResourcesFromPresetForRecommendation(&preset)
	if !ok {
		return corev1.ResourceRequirements{}, "", false
	}
	source := runtimeRecommendationSourcePreset
	if t.Spec.Resources != nil && t.Spec.Resources.Worker != nil {
		resources = *t.Spec.Resources.Worker.DeepCopy()
		source = runtimeRecommendationSourceTyped
	}
	return resources, source, true
}

func workerResourcesFromPresetForRecommendation(preset *sizing.SizePreset) (corev1.ResourceRequirements, bool) {
	cpuRequest, ok := parseRecommendationQuantity(preset.WorkerCPUReq)
	if !ok {
		return corev1.ResourceRequirements{}, false
	}
	memoryRequest, ok := parseRecommendationQuantity(preset.WorkerMemReq)
	if !ok {
		return corev1.ResourceRequirements{}, false
	}
	cpuLimit, ok := parseRecommendationQuantity(preset.WorkerCPULim)
	if !ok {
		return corev1.ResourceRequirements{}, false
	}
	memoryLimit, ok := parseRecommendationQuantity(preset.WorkerMemLim)
	if !ok {
		return corev1.ResourceRequirements{}, false
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    cpuRequest,
			corev1.ResourceMemory: memoryRequest,
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    cpuLimit,
			corev1.ResourceMemory: memoryLimit,
		},
	}, true
}

func parseRecommendationQuantity(value string) (resource.Quantity, bool) {
	quantity, err := resource.ParseQuantity(value)
	return quantity, err == nil
}

func recommendedPresetForWorkerResources(resources corev1.ResourceRequirements) string {
	needCPU, needMemory, ok := recommendationResourceNeed(resources)
	if !ok {
		return ""
	}
	for _, presetName := range []string{"xs", "s", "m", "l", "xl"} {
		preset, ok := sizing.GetPreset(presetName)
		if !ok {
			continue
		}
		presetResources, ok := workerResourcesFromPresetForRecommendation(&preset)
		if !ok {
			continue
		}
		presetCPU, presetMemory, ok := recommendationResourceNeed(presetResources)
		if !ok {
			continue
		}
		if presetCPU.Cmp(needCPU) >= 0 && presetMemory.Cmp(needMemory) >= 0 {
			return presetName
		}
	}
	return "xl"
}

func recommendationResourceNeed(resources corev1.ResourceRequirements) (cpu, memory resource.Quantity, ok bool) {
	cpu = resource.Quantity{}
	memory = resource.Quantity{}
	applyLargerQuantity(&cpu, resources.Requests[corev1.ResourceCPU])
	applyLargerQuantity(&cpu, resources.Limits[corev1.ResourceCPU])
	applyLargerQuantity(&memory, resources.Requests[corev1.ResourceMemory])
	applyLargerQuantity(&memory, resources.Limits[corev1.ResourceMemory])
	return cpu, memory, cpu.Sign() > 0 || memory.Sign() > 0
}

func applyLargerQuantity(current *resource.Quantity, candidate resource.Quantity) {
	if candidate.Sign() > 0 && candidate.Cmp(*current) > 0 {
		*current = candidate.DeepCopy()
	}
}

func (t *XTrinode) validateNodePoolSchedulePlacement(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if t == nil || t.Spec.NodePool == nil || !t.Spec.NodePool.SchedulePods {
		return allErrs
	}
	if t.Spec.Placement != nil && t.Spec.Placement.ExistingNodePool != nil {
		allErrs = append(allErrs, field.Forbidden(
			fldPath.Child("placement", "existingNodePool"),
			"cannot combine spec.nodePool.schedulePods with spec.placement.existingNodePool; use the managed node-pool binding or an existing pool selector, not both"))
	}

	poolName := t.Spec.NodePool.Name
	if poolName == "" {
		poolName = fmt.Sprintf("%s%s", t.Name, config.NodePoolNameSuffix)
	}
	if poolName == "" {
		return field.ErrorList{
			field.Required(
				fldPath.Child("nodePool").Child("name"),
				"nodePool.schedulePods requires spec.nodePool.name or metadata.name for the generated pool name"),
		}
	}

	if placement := t.Spec.Placement; placement != nil {
		allErrs = append(allErrs, validateNodePoolSelectorConflict(
			placement.NodeSelector,
			poolName,
			fldPath.Child("placement").Child("nodeSelector"),
		)...)
		if placement.Coordinator != nil {
			allErrs = append(allErrs, validateNodePoolSelectorConflict(
				placement.Coordinator.NodeSelector,
				poolName,
				fldPath.Child("placement").Child("coordinator").Child("nodeSelector"),
			)...)
		}
		if placement.Worker != nil {
			allErrs = append(allErrs, validateNodePoolSelectorConflict(
				placement.Worker.NodeSelector,
				poolName,
				fldPath.Child("placement").Child("worker").Child("nodeSelector"),
			)...)
		}
	}

	return allErrs
}

func validateExistingNodePoolSelectorConflict(selector map[string]string, key, value string, fldPath *field.Path) field.ErrorList {
	if selector == nil {
		return nil
	}
	existing, ok := selector[key]
	if !ok || existing == value {
		return nil
	}
	return field.ErrorList{
		field.Invalid(
			fldPath.Key(key),
			existing,
			fmt.Sprintf("must match existingNodePool selector value %q", value)),
	}
}

func validateNodePoolSelectorConflict(selector map[string]string, poolName string, fldPath *field.Path) field.ErrorList {
	if selector == nil {
		return nil
	}
	existing, ok := selector[config.NodePoolSchedulingLabel]
	if !ok || existing == poolName {
		return nil
	}
	return field.ErrorList{
		field.Invalid(
			fldPath.Key(config.NodePoolSchedulingLabel),
			existing,
			fmt.Sprintf("must match nodePool name %q when nodePool.schedulePods is true", poolName)),
	}
}
