package runtimeshape

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/sizing"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// BaselineCPUMilliPerCapacityUnit preserves the existing fixed-size route
	// capacities for preset-only runtimes with the default two workers.
	BaselineCPUMilliPerCapacityUnit int64 = 2000

	SourcePreset  = "preset"
	SourceTyped   = "typed"
	SourceOverlay = "valuesOverlay"

	AutoscalingModeFixed = "fixed"
	AutoscalingModeKEDA  = "keda"
	AutoscalingModeHPA   = "hpa"
)

// ResolvedRuntimeShape is the resolved runtime shape consumed by pod builders,
// guardrails, routing, resume ranking, node-pool binding, and status.
type ResolvedRuntimeShape struct {
	PresetName string
	Preset     sizing.SizePreset

	Coordinator corev1.ResourceRequirements
	Worker      corev1.ResourceRequirements

	MinWorkers      int32
	MaxWorkers      int32
	FixedWorkers    *int32
	QuotaWorkers    int32
	CapacityWorkers int32

	CapacityUnits   int32
	AutoscalingMode string

	Placement PlacementShape
	NodePool  NodePoolShape

	Source RuntimeShapeSource
	Hash   string
}

type PlacementShape struct {
	Coordinator SchedulingShape
	Worker      SchedulingShape
}

type SchedulingShape struct {
	NodeSelector              map[string]string
	Tolerations               []corev1.Toleration
	Affinity                  *corev1.Affinity
	TopologySpreadConstraints []corev1.TopologySpreadConstraint
}

type NodePoolShape struct {
	ProvisioningRequested bool
	Provider              string
	ProviderMode          string
	Name                  string
	MinNodes              int32
	MaxNodes              int32
	MachineType           string
	SchedulePods          bool
	DeletionPolicy        string
}

type RuntimeShapeSource struct {
	CoordinatorResources string
	WorkerResources      string
	WorkerCount          string
	Capacity             string
	Placement            string
	NodePool             string
}

// Resolve returns the resolved runtime shape for an XTrinode.
func Resolve(xtrinode *analyticsv1.XTrinode) (*ResolvedRuntimeShape, error) {
	if xtrinode == nil {
		return nil, fmt.Errorf("xtrinode is nil")
	}

	presetName := strings.ToLower(strings.TrimSpace(xtrinode.Spec.Size))
	preset, ok := sizing.GetPreset(presetName)
	if !ok {
		return nil, fmt.Errorf("invalid size preset: %s", xtrinode.Spec.Size)
	}

	coordinatorResources, err := coordinatorResourcesFromPreset(&preset)
	if err != nil {
		return nil, err
	}
	workerResources, err := workerResourcesFromPreset(&preset)
	if err != nil {
		return nil, err
	}

	shape := &ResolvedRuntimeShape{
		PresetName:      presetName,
		Preset:          preset,
		Coordinator:     coordinatorResources,
		Worker:          workerResources,
		AutoscalingMode: AutoscalingModeFixed,
		Source: RuntimeShapeSource{
			CoordinatorResources: SourcePreset,
			WorkerResources:      SourcePreset,
			WorkerCount:          SourcePreset,
			Capacity:             SourcePreset,
			Placement:            SourcePreset,
			NodePool:             SourcePreset,
		},
	}

	applyTypedResources(shape, xtrinode)
	err = applyTypedPlacement(shape, xtrinode)
	if err != nil {
		return nil, err
	}
	applyNodePoolShape(shape, xtrinode)

	valuesOverlay := xtrinode.Spec.GetValuesOverlayMap()
	err = resolveWorkerCounts(shape, xtrinode, valuesOverlay)
	if err != nil {
		return nil, err
	}
	err = applyNodePoolSchedulingBinding(shape)
	if err != nil {
		return nil, err
	}
	resolveCapacity(shape, xtrinode)
	err = validate(shape)
	if err != nil {
		return nil, err
	}
	hash, err := hashShape(shape)
	if err != nil {
		return nil, err
	}
	shape.Hash = hash
	return shape, nil
}

func coordinatorResourcesFromPreset(preset *sizing.SizePreset) (corev1.ResourceRequirements, error) {
	return resourcesFromStrings(
		preset.CoordinatorCPUReq,
		preset.CoordinatorMemReq,
		preset.CoordinatorCPULim,
		preset.CoordinatorMemLim,
		"coordinator",
	)
}

func workerResourcesFromPreset(preset *sizing.SizePreset) (corev1.ResourceRequirements, error) {
	return resourcesFromStrings(
		preset.WorkerCPUReq,
		preset.WorkerMemReq,
		preset.WorkerCPULim,
		preset.WorkerMemLim,
		"worker",
	)
}

func resourcesFromStrings(cpuReq, memReq, cpuLimit, memLimit, role string) (corev1.ResourceRequirements, error) {
	parsedCPUReq, err := resource.ParseQuantity(cpuReq)
	if err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("invalid %s CPU request %q: %w", role, cpuReq, err)
	}
	parsedMemReq, err := resource.ParseQuantity(memReq)
	if err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("invalid %s memory request %q: %w", role, memReq, err)
	}
	parsedCPULimit, err := resource.ParseQuantity(cpuLimit)
	if err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("invalid %s CPU limit %q: %w", role, cpuLimit, err)
	}
	parsedMemLimit, err := resource.ParseQuantity(memLimit)
	if err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("invalid %s memory limit %q: %w", role, memLimit, err)
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    parsedCPUReq,
			corev1.ResourceMemory: parsedMemReq,
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    parsedCPULimit,
			corev1.ResourceMemory: parsedMemLimit,
		},
	}, nil
}

func applyTypedResources(shape *ResolvedRuntimeShape, xtrinode *analyticsv1.XTrinode) {
	if xtrinode.Spec.Resources == nil {
		return
	}
	if xtrinode.Spec.Resources.Coordinator != nil {
		shape.Coordinator = *xtrinode.Spec.Resources.Coordinator.DeepCopy()
		shape.Source.CoordinatorResources = SourceTyped
	}
	if xtrinode.Spec.Resources.Worker != nil {
		shape.Worker = *xtrinode.Spec.Resources.Worker.DeepCopy()
		shape.Source.WorkerResources = SourceTyped
	}
}

func applyTypedPlacement(shape *ResolvedRuntimeShape, xtrinode *analyticsv1.XTrinode) error {
	if xtrinode.Spec.Placement == nil {
		return nil
	}
	placement := xtrinode.Spec.Placement
	common := SchedulingShape{
		NodeSelector: copyStringMap(placement.NodeSelector),
		Tolerations:  copyTolerations(placement.Tolerations),
		Affinity:     placement.Affinity.DeepCopy(),
	}
	shape.Placement.Coordinator = copySchedulingShape(common)
	shape.Placement.Worker = copySchedulingShape(common)
	applyRolePlacementSpec(&shape.Placement.Coordinator, placement.Coordinator)
	applyRolePlacementSpec(&shape.Placement.Worker, placement.Worker)
	if placement.ExistingNodePool != nil {
		key, value, ok := config.ExistingNodePoolSelector(placement.ExistingNodePool.Provider, placement.ExistingNodePool.Name)
		if !ok {
			return fmt.Errorf("unsupported existingNodePool provider %q", placement.ExistingNodePool.Provider)
		}
		if err := addSelectorWithConflict(&shape.Placement.Coordinator, key, value); err != nil {
			return fmt.Errorf("coordinator placement conflicts with existingNodePool: %w", err)
		}
		if err := addSelectorWithConflict(&shape.Placement.Worker, key, value); err != nil {
			return fmt.Errorf("worker placement conflicts with existingNodePool: %w", err)
		}
	}
	shape.Source.Placement = SourceTyped
	return nil
}

func applyRolePlacementSpec(shape *SchedulingShape, spec *analyticsv1.RolePlacementSpec) {
	if spec == nil {
		return
	}
	if spec.NodeSelector != nil {
		shape.NodeSelector = copyStringMap(spec.NodeSelector)
	}
	if spec.Tolerations != nil {
		shape.Tolerations = copyTolerations(spec.Tolerations)
	}
	if spec.Affinity != nil {
		shape.Affinity = spec.Affinity.DeepCopy()
	}
	if spec.TopologySpreadConstraints != nil {
		shape.TopologySpreadConstraints = copyTopologySpreadConstraints(spec.TopologySpreadConstraints)
	}
}

func applyNodePoolShape(shape *ResolvedRuntimeShape, xtrinode *analyticsv1.XTrinode) {
	if xtrinode.Spec.NodePool == nil {
		return
	}
	nodePool := xtrinode.Spec.NodePool
	poolName := nodePool.Name
	if poolName == "" {
		poolName = fmt.Sprintf("%s%s", xtrinode.Name, config.NodePoolNameSuffix)
	}
	shape.NodePool = NodePoolShape{
		ProvisioningRequested: true,
		Provider:              strings.ToLower(strings.TrimSpace(nodePool.Provider)),
		ProviderMode:          strings.ToLower(strings.TrimSpace(nodePool.ProviderMode)),
		Name:                  poolName,
		MachineType:           nodePoolMachineType(nodePool),
		SchedulePods:          nodePool.SchedulePods,
		DeletionPolicy:        resolvedNodePoolDeletionPolicy(nodePool),
	}
	if nodePool.MinNodes != nil {
		shape.NodePool.MinNodes = *nodePool.MinNodes
	}
	if nodePool.MaxNodes != nil {
		shape.NodePool.MaxNodes = *nodePool.MaxNodes
	}
	shape.Source.NodePool = SourceTyped
}

func resolvedNodePoolDeletionPolicy(nodePool *analyticsv1.NodePoolSpec) string {
	if nodePool == nil || nodePool.DeletionPolicy == "" {
		return analyticsv1.NodePoolDeletionPolicyDelete
	}
	return nodePool.DeletionPolicy
}

func nodePoolMachineType(nodePool *analyticsv1.NodePoolSpec) string {
	switch strings.ToLower(strings.TrimSpace(nodePool.Provider)) {
	case "azure":
		if nodePool.Azure != nil {
			return nodePool.Azure.VMSize
		}
	case "aws":
		if nodePool.AWS != nil {
			return nodePool.AWS.InstanceType
		}
	case "gcp":
		if nodePool.GCP != nil {
			return nodePool.GCP.MachineType
		}
	}
	return ""
}

func resolveWorkerCounts(shape *ResolvedRuntimeShape, xtrinode *analyticsv1.XTrinode, valuesOverlay map[string]interface{}) error {
	switch {
	case IsKEDAActive(xtrinode):
		minWorkers := int32(0)
		if xtrinode.Spec.MinWorkers != nil {
			minWorkers = *xtrinode.Spec.MinWorkers
		}
		maxWorkers := int32(config.KEDADefaultMaxWorkers)
		if xtrinode.Spec.MaxWorkers != nil {
			maxWorkers = *xtrinode.Spec.MaxWorkers
		}
		if maxWorkers < minWorkers {
			return fmt.Errorf("keda maxWorkers %d is less than minWorkers %d", maxWorkers, minWorkers)
		}
		shape.MinWorkers = minWorkers
		shape.MaxWorkers = maxWorkers
		shape.FixedWorkers = nil
		shape.QuotaWorkers = maxWorkers
		shape.CapacityWorkers = maxWorkers
		shape.AutoscalingMode = AutoscalingModeKEDA
		shape.Source.WorkerCount = SourceTyped
	case IsNativeHPAActive(valuesOverlay):
		minWorkers, maxWorkers, err := resolveNativeHPAWorkers(valuesOverlay)
		if err != nil {
			return err
		}
		shape.MinWorkers = minWorkers
		shape.MaxWorkers = maxWorkers
		shape.FixedWorkers = nil
		shape.QuotaWorkers = maxWorkers
		shape.CapacityWorkers = maxWorkers
		shape.AutoscalingMode = AutoscalingModeHPA
		shape.Source.WorkerCount = SourceOverlay
	default:
		fixedWorkers, source, err := resolveFixedWorkers(xtrinode)
		if err != nil {
			return err
		}
		shape.MinWorkers = fixedWorkers
		shape.MaxWorkers = fixedWorkers
		shape.FixedWorkers = int32Ptr(fixedWorkers)
		shape.QuotaWorkers = fixedWorkers
		shape.CapacityWorkers = fixedWorkers
		shape.AutoscalingMode = AutoscalingModeFixed
		shape.Source.WorkerCount = source
	}
	return nil
}

func IsKEDAActive(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode == nil || xtrinode.Spec.KEDA == nil || xtrinode.Spec.KEDA.Enabled == nil {
		return false
	}
	return *xtrinode.Spec.KEDA.Enabled && hasKEDAMetricConfig(xtrinode.Spec.KEDA)
}

func hasKEDAMetricConfig(kedaSpec *analyticsv1.KEDASpec) bool {
	return kedaSpec.ScalerType != "" ||
		kedaSpec.ScalingMetric != "" ||
		(kedaSpec.PrometheusServer != nil && strings.TrimSpace(*kedaSpec.PrometheusServer) != "") ||
		(kedaSpec.PrometheusQuery != nil && strings.TrimSpace(*kedaSpec.PrometheusQuery) != "") ||
		(kedaSpec.HTTPEndpoint != nil && strings.TrimSpace(*kedaSpec.HTTPEndpoint) != "")
}

func IsNativeHPAActive(valuesOverlay map[string]interface{}) bool {
	autoscaling, ok := overlayAutoscaling(valuesOverlay)
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

func resolveNativeHPAWorkers(valuesOverlay map[string]interface{}) (minWorkers, maxWorkers int32, err error) {
	server, ok := overlayServer(valuesOverlay)
	if !ok {
		return 0, 0, fmt.Errorf("native HPA overlay server config is missing")
	}
	autoscaling, ok := server["autoscaling"].(map[string]interface{})
	if !ok {
		return 0, 0, fmt.Errorf("native HPA autoscaling config is missing")
	}
	minWorkers = int32(config.DefaultHPAMinReplicas)
	maxWorkers = int32(config.DefaultHPAMaxReplicas)
	if maxReplicas, ok := ParseInt32(autoscaling["maxReplicas"]); ok {
		maxWorkers = maxReplicas
	}
	if minReplicas, ok := ParseInt32(autoscaling["minReplicas"]); ok {
		minWorkers = minReplicas
	}
	if minWorkers < 0 {
		return 0, 0, fmt.Errorf("native HPA minReplicas must be non-negative")
	}
	if maxWorkers < 1 {
		return 0, 0, fmt.Errorf("native HPA maxReplicas must be at least 1")
	}
	if maxWorkers < minWorkers {
		return 0, 0, fmt.Errorf("native HPA maxReplicas %d is less than minReplicas %d", maxWorkers, minWorkers)
	}
	return minWorkers, maxWorkers, nil
}

func resolveFixedWorkers(xtrinode *analyticsv1.XTrinode) (workers int32, source string, err error) {
	workers = int32(config.DefaultWorkerReplicas)
	source = SourcePreset
	if xtrinode.Spec.MaxWorkers != nil {
		workers = *xtrinode.Spec.MaxWorkers
		source = SourceTyped
	}
	if xtrinode.Spec.MinWorkers != nil && *xtrinode.Spec.MinWorkers > workers {
		workers = *xtrinode.Spec.MinWorkers
		source = SourceTyped
	}
	if workers < 0 {
		return 0, "", fmt.Errorf("fixed worker count must be non-negative")
	}
	return workers, source, nil
}

func overlayServer(valuesOverlay map[string]interface{}) (map[string]interface{}, bool) {
	if valuesOverlay == nil {
		return nil, false
	}
	server, ok := valuesOverlay["server"].(map[string]interface{})
	return server, ok
}

func overlayAutoscaling(valuesOverlay map[string]interface{}) (map[string]interface{}, bool) {
	server, ok := overlayServer(valuesOverlay)
	if !ok {
		return nil, false
	}
	autoscaling, ok := server["autoscaling"].(map[string]interface{})
	return autoscaling, ok
}

func applyNodePoolSchedulingBinding(shape *ResolvedRuntimeShape) error {
	if !shape.NodePool.ProvisioningRequested || !shape.NodePool.SchedulePods {
		return nil
	}
	if shape.NodePool.Name == "" {
		return fmt.Errorf("nodePool.schedulePods requires a resolved node pool name")
	}
	if err := addNodePoolSelector(&shape.Placement.Coordinator, shape.NodePool.Name); err != nil {
		return fmt.Errorf("coordinator placement conflicts with nodePool.schedulePods: %w", err)
	}
	if err := addNodePoolSelector(&shape.Placement.Worker, shape.NodePool.Name); err != nil {
		return fmt.Errorf("worker placement conflicts with nodePool.schedulePods: %w", err)
	}
	shape.Source.Placement = SourceTyped
	return nil
}

func addNodePoolSelector(shape *SchedulingShape, poolName string) error {
	return addSelectorWithConflict(shape, config.NodePoolSchedulingLabel, poolName)
}

func addSelectorWithConflict(shape *SchedulingShape, key, value string) error {
	if shape.NodeSelector == nil {
		shape.NodeSelector = map[string]string{}
	}
	if existing, ok := shape.NodeSelector[key]; ok && existing != value {
		return fmt.Errorf("%s=%q does not match %q", key, existing, value)
	}
	shape.NodeSelector[key] = value
	return nil
}

func resolveCapacity(shape *ResolvedRuntimeShape, xtrinode *analyticsv1.XTrinode) {
	if xtrinode.Spec.Routing != nil && xtrinode.Spec.Routing.CapacityUnits != nil {
		shape.CapacityUnits = *xtrinode.Spec.Routing.CapacityUnits
		shape.Source.Capacity = SourceTyped
		return
	}
	shape.CapacityUnits = deriveCapacityUnits(shape.Worker, shape.CapacityWorkers)
	shape.Source.Capacity = shape.Source.WorkerResources
}

func deriveCapacityUnits(worker corev1.ResourceRequirements, workers int32) int32 {
	if workers <= 0 {
		return 1
	}
	cpuRequest, ok := worker.Requests[corev1.ResourceCPU]
	if !ok || cpuRequest.Sign() <= 0 {
		return 1
	}
	totalMilli := cpuRequest.MilliValue() * int64(workers)
	units := (totalMilli + BaselineCPUMilliPerCapacityUnit - 1) / BaselineCPUMilliPerCapacityUnit
	if units < 1 {
		return 1
	}
	return int32(units)
}

func validate(shape *ResolvedRuntimeShape) error {
	if err := validateResourceRequirements(shape.Coordinator, "coordinator"); err != nil {
		return err
	}
	if err := validateResourceRequirements(shape.Worker, "worker"); err != nil {
		return err
	}
	if shape.MinWorkers < 0 || shape.MaxWorkers < 0 || shape.QuotaWorkers < 0 || shape.CapacityWorkers < 0 {
		return fmt.Errorf("worker counts must be non-negative")
	}
	if shape.MaxWorkers < shape.MinWorkers {
		return fmt.Errorf("maxWorkers %d is less than minWorkers %d", shape.MaxWorkers, shape.MinWorkers)
	}
	if shape.CapacityUnits < 1 {
		return fmt.Errorf("capacityUnits must be at least 1")
	}
	return nil
}

func validateResourceRequirements(requirements corev1.ResourceRequirements, role string) error {
	for _, resourceName := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, hasRequest := requirements.Requests[resourceName]
		limit, hasLimit := requirements.Limits[resourceName]
		if hasRequest && request.Sign() < 0 {
			return fmt.Errorf("%s %s request must be non-negative", role, resourceName)
		}
		if hasLimit && limit.Sign() < 0 {
			return fmt.Errorf("%s %s limit must be non-negative", role, resourceName)
		}
		if hasRequest && hasLimit && limit.Cmp(request) < 0 {
			return fmt.Errorf("%s %s limit %s is less than request %s", role, resourceName, limit.String(), request.String())
		}
	}
	return nil
}

func hashShape(shape *ResolvedRuntimeShape) (string, error) {
	input := struct {
		PresetName      string
		Coordinator     resourceRequirementsHash
		Worker          resourceRequirementsHash
		MinWorkers      int32
		MaxWorkers      int32
		FixedWorkers    *int32
		QuotaWorkers    int32
		CapacityWorkers int32
		CapacityUnits   int32
		AutoscalingMode string
		Placement       PlacementShape
		NodePool        NodePoolShape
		Source          RuntimeShapeSource
	}{
		PresetName:      shape.PresetName,
		Coordinator:     hashResources(shape.Coordinator),
		Worker:          hashResources(shape.Worker),
		MinWorkers:      shape.MinWorkers,
		MaxWorkers:      shape.MaxWorkers,
		FixedWorkers:    shape.FixedWorkers,
		QuotaWorkers:    shape.QuotaWorkers,
		CapacityWorkers: shape.CapacityWorkers,
		CapacityUnits:   shape.CapacityUnits,
		AutoscalingMode: shape.AutoscalingMode,
		Placement:       shape.Placement,
		NodePool:        shape.NodePool,
		Source:          shape.Source,
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to hash resolved runtime shape: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16], nil
}

type resourceRequirementsHash struct {
	Requests map[string]string
	Limits   map[string]string
}

func hashResources(requirements corev1.ResourceRequirements) resourceRequirementsHash {
	return resourceRequirementsHash{
		Requests: hashResourceList(requirements.Requests),
		Limits:   hashResourceList(requirements.Limits),
	}
}

func hashResourceList(list corev1.ResourceList) map[string]string {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]string, len(list))
	for name, quantity := range list {
		out[string(name)] = quantity.String()
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyTolerations(in []corev1.Toleration) []corev1.Toleration {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.Toleration, len(in))
	copy(out, in)
	return out
}

func copyTopologySpreadConstraints(in []corev1.TopologySpreadConstraint) []corev1.TopologySpreadConstraint {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.TopologySpreadConstraint, len(in))
	copy(out, in)
	return out
}

func copySchedulingShape(in SchedulingShape) SchedulingShape {
	return SchedulingShape{
		NodeSelector:              copyStringMap(in.NodeSelector),
		Tolerations:               copyTolerations(in.Tolerations),
		Affinity:                  in.Affinity.DeepCopy(),
		TopologySpreadConstraints: copyTopologySpreadConstraints(in.TopologySpreadConstraints),
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}

// ParseInt32 accepts the JSON number shapes used by unstructured settings.
func ParseInt32(value interface{}) (int32, bool) {
	switch typed := value.(type) {
	case int:
		return int32(typed), true
	case int32:
		return typed, true
	case int64:
		return int32(typed), true
	case float64:
		return int32(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 32)
		if err != nil {
			return 0, false
		}
		return int32(parsed), true
	default:
		return 0, false
	}
}

// ObservedStatus converts the resolved shape to the compact XTrinode status shape.
func (shape *ResolvedRuntimeShape) ObservedStatus() *analyticsv1.ObservedRuntimeShapeStatus {
	if shape == nil {
		return nil
	}
	return &analyticsv1.ObservedRuntimeShapeStatus{
		Version:         analyticsv1.ObservedRuntimeShapeStatusVersion,
		Hash:            shape.Hash,
		Preset:          shape.PresetName,
		AutoscalingMode: shape.AutoscalingMode,
		Coordinator: analyticsv1.ObservedRuntimeResourcesStatus{
			Requests: copyResourceList(shape.Coordinator.Requests),
			Limits:   copyResourceList(shape.Coordinator.Limits),
		},
		Worker: analyticsv1.ObservedRuntimeResourcesStatus{
			Requests: copyResourceList(shape.Worker.Requests),
			Limits:   copyResourceList(shape.Worker.Limits),
		},
		Workers: analyticsv1.ObservedRuntimeWorkersStatus{
			Fixed:    copyInt32Ptr(shape.FixedWorkers),
			Min:      shape.MinWorkers,
			Max:      shape.MaxWorkers,
			Quota:    shape.QuotaWorkers,
			Capacity: shape.CapacityWorkers,
		},
		CapacityUnits: shape.CapacityUnits,
		NodePool: analyticsv1.ObservedRuntimeNodePoolStatus{
			ProvisioningRequested: shape.NodePool.ProvisioningRequested,
			Provider:              shape.NodePool.Provider,
			ProviderMode:          shape.NodePool.ProviderMode,
			Name:                  shape.NodePool.Name,
			SchedulePods:          shape.NodePool.SchedulePods,
			DeletionPolicy:        shape.NodePool.DeletionPolicy,
		},
	}
}

func copyResourceList(in corev1.ResourceList) corev1.ResourceList {
	if len(in) == 0 {
		return nil
	}
	out := make(corev1.ResourceList, len(in))
	for key, value := range in {
		out[key] = value.DeepCopy()
	}
	return out
}

func copyInt32Ptr(in *int32) *int32 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
