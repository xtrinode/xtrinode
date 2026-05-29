package controllers

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const maxClusterSchedulingDiagnosticPods = 5

func (r *XTrinodeReconciler) addClusterSchedulingDiagnostics(ctx context.Context, blockers *schedulingBlockerSet, runtimePods []corev1.Pod, log logr.Logger) {
	pendingPods := pendingRuntimePods(runtimePods)
	if len(pendingPods) == 0 {
		return
	}
	if len(pendingPods) > maxClusterSchedulingDiagnosticPods {
		log.Info("Truncating runtime pod scheduling diagnostics", "pods", len(pendingPods), "limit", maxClusterSchedulingDiagnosticPods)
		pendingPods = pendingPods[:maxClusterSchedulingDiagnosticPods]
	}

	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		log.Error(err, "failed to list nodes for runtime scheduling diagnostics")
		return
	}
	if len(nodes.Items) == 0 {
		for i := range pendingPods {
			blockers.addCapacityDiagnostic(fmt.Sprintf("runtime pod %s: no cluster nodes are available for scheduling diagnostics", pendingPods[i].Name))
		}
		return
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods); err != nil {
		log.Error(err, "failed to list cluster pods for runtime scheduling diagnostics")
		return
	}

	daemonSets := &appsv1.DaemonSetList{}
	if err := r.List(ctx, daemonSets); err != nil {
		log.Error(err, "failed to list DaemonSets for runtime scheduling diagnostics")
		daemonSets = &appsv1.DaemonSetList{}
	}

	nodeStates := buildSchedulingNodeStates(nodes.Items, pods.Items, daemonSets.Items)
	for i := range pendingPods {
		addPodSchedulingDiagnostics(blockers, &pendingPods[i], nodeStates)
	}
}

func pendingRuntimePods(pods []corev1.Pod) []corev1.Pod {
	pendingPods := make([]corev1.Pod, 0)
	for i := range pods {
		pod := pods[i]
		if podTerminated(&pod) || pod.Spec.NodeName != "" {
			continue
		}
		if pod.Status.Phase == corev1.PodPending || hasUnscheduledCondition(&pod) {
			pendingPods = append(pendingPods, pod)
		}
	}
	return pendingPods
}

func hasUnscheduledCondition(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			return true
		}
	}
	return false
}

type schedulingNodeState struct {
	node     *corev1.Node
	free     corev1.ResourceList
	freePods int64
}

func buildSchedulingNodeStates(nodes []corev1.Node, pods []corev1.Pod, daemonSets []appsv1.DaemonSet) []schedulingNodeState {
	states := make([]schedulingNodeState, 0, len(nodes))
	daemonSetPodsByNode := daemonSetPodsByNode(pods)

	for i := range nodes {
		node := &nodes[i]
		if !nodeReadyForScheduling(node) {
			continue
		}
		state := schedulingNodeState{
			node:     node,
			free:     copyResourceListForScheduling(node.Status.Allocatable),
			freePods: allocatablePods(node),
		}

		for j := range pods {
			pod := &pods[j]
			if pod.Spec.NodeName != node.Name || podTerminated(pod) {
				continue
			}
			subtractSchedulingRequests(state.free, podSchedulingRequests(&pod.Spec))
			if state.freePods > 0 {
				state.freePods--
			}
		}

		for j := range daemonSets {
			daemonSet := &daemonSets[j]
			if !daemonSetShouldRunOnNode(daemonSet, node) || daemonSetPodsByNode.has(node.Name, daemonSet.Namespace, daemonSet.Name) {
				continue
			}
			subtractSchedulingRequests(state.free, podSchedulingRequests(&daemonSet.Spec.Template.Spec))
			if state.freePods > 0 {
				state.freePods--
			}
		}

		states = append(states, state)
	}
	return states
}

type daemonSetPodIndex map[string]map[string]struct{}

func daemonSetPodsByNode(pods []corev1.Pod) daemonSetPodIndex {
	index := daemonSetPodIndex{}
	for i := range pods {
		pod := &pods[i]
		if pod.Spec.NodeName == "" || podTerminated(pod) {
			continue
		}
		for _, owner := range pod.OwnerReferences {
			if owner.APIVersion == "apps/v1" && owner.Kind == "DaemonSet" {
				nodePods := index[pod.Spec.NodeName]
				if nodePods == nil {
					nodePods = map[string]struct{}{}
					index[pod.Spec.NodeName] = nodePods
				}
				nodePods[pod.Namespace+"/"+owner.Name] = struct{}{}
			}
		}
	}
	return index
}

func (i daemonSetPodIndex) has(nodeName, namespace, name string) bool {
	if i == nil {
		return false
	}
	nodePods := i[nodeName]
	if nodePods == nil {
		return false
	}
	_, ok := nodePods[namespace+"/"+name]
	return ok
}

func addPodSchedulingDiagnostics(blockers *schedulingBlockerSet, pod *corev1.Pod, nodeStates []schedulingNodeState) {
	if len(nodeStates) == 0 {
		blockers.addCapacityDiagnostic(fmt.Sprintf("runtime pod %s: no Ready schedulable nodes are available", pod.Name))
		return
	}

	placementCandidates := make([]schedulingNodeState, 0, len(nodeStates))
	for _, state := range nodeStates {
		if podMatchesNodePlacement(pod, state.node) {
			placementCandidates = append(placementCandidates, state)
		}
	}
	if len(placementCandidates) == 0 {
		blockers.addPlacementDiagnostic(fmt.Sprintf("runtime pod %s: no Ready schedulable nodes match node selector or required node affinity", pod.Name))
		return
	}

	taintCandidates := make([]schedulingNodeState, 0, len(placementCandidates))
	for _, state := range placementCandidates {
		if podToleratesNodeTaints(&pod.Spec, state.node.Spec.Taints) {
			taintCandidates = append(taintCandidates, state)
		}
	}
	if len(taintCandidates) == 0 {
		blockers.addTaintDiagnostic(fmt.Sprintf("runtime pod %s: matching nodes only have untolerated NoSchedule or NoExecute taints", pod.Name))
		return
	}

	requests := podSchedulingRequests(&pod.Spec)
	for _, state := range taintCandidates {
		if schedulingRequestsFit(requests, state.free, state.freePods) {
			return
		}
	}
	blockers.addCapacityDiagnostic(fmt.Sprintf(
		"runtime pod %s: insufficient allocatable capacity or pod slots after scheduled pods and DaemonSet overhead; requests=%s bestAvailable=%s",
		pod.Name,
		schedulingRequestsString(requests, 1),
		bestAvailableSchedulingCapacity(taintCandidates),
	))
}

func nodeReadyForScheduling(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podMatchesNodePlacement(pod *corev1.Pod, node *corev1.Node) bool {
	for key, value := range pod.Spec.NodeSelector {
		if node.Labels[key] != value {
			return false
		}
	}
	return requiredNodeAffinityMatches(pod.Spec.Affinity, node)
}

func requiredNodeAffinityMatches(affinity *corev1.Affinity, node *corev1.Node) bool {
	if affinity == nil || affinity.NodeAffinity == nil || affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return true
	}
	selector := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	for _, term := range selector.NodeSelectorTerms {
		if nodeSelectorTermMatches(term, node) {
			return true
		}
	}
	return false
}

func nodeSelectorTermMatches(term corev1.NodeSelectorTerm, node *corev1.Node) bool {
	if len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0 {
		return false
	}
	for _, expression := range term.MatchExpressions {
		if !nodeSelectorRequirementMatches(expression, node.Labels) {
			return false
		}
	}
	for _, field := range term.MatchFields {
		if !nodeFieldRequirementMatches(field, node) {
			return false
		}
	}
	return true
}

func nodeSelectorRequirementMatches(requirement corev1.NodeSelectorRequirement, nodeLabels map[string]string) bool {
	value, exists := nodeLabels[requirement.Key]
	switch requirement.Operator {
	case corev1.NodeSelectorOpIn:
		return exists && stringInSlice(value, requirement.Values)
	case corev1.NodeSelectorOpNotIn:
		return !exists || !stringInSlice(value, requirement.Values)
	case corev1.NodeSelectorOpExists:
		return exists
	case corev1.NodeSelectorOpDoesNotExist:
		return !exists
	case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
		if !exists || len(requirement.Values) != 1 {
			return false
		}
		nodeValue, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false
		}
		requiredValue, err := strconv.ParseInt(requirement.Values[0], 10, 64)
		if err != nil {
			return false
		}
		if requirement.Operator == corev1.NodeSelectorOpGt {
			return nodeValue > requiredValue
		}
		return nodeValue < requiredValue
	default:
		return false
	}
}

func nodeFieldRequirementMatches(requirement corev1.NodeSelectorRequirement, node *corev1.Node) bool {
	if requirement.Key != "metadata.name" {
		return false
	}
	return nodeSelectorRequirementMatches(requirement, map[string]string{"metadata.name": node.Name})
}

func podToleratesNodeTaints(podSpec *corev1.PodSpec, taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		tolerated := false
		for _, toleration := range podSpec.Tolerations {
			if toleration.ToleratesTaint(&taint) {
				tolerated = true
				break
			}
		}
		if !tolerated {
			return false
		}
	}
	return true
}

func daemonSetShouldRunOnNode(daemonSet *appsv1.DaemonSet, node *corev1.Node) bool {
	selector, err := metav1LabelSelectorAsSelector(daemonSet.Spec.Selector)
	if err != nil || !selector.Matches(labels.Set(daemonSet.Spec.Template.Labels)) {
		return false
	}
	templatePod := corev1.Pod{Spec: daemonSet.Spec.Template.Spec}
	return podMatchesNodePlacement(&templatePod, node) && podToleratesNodeTaints(&templatePod.Spec, node.Spec.Taints)
}

func metav1LabelSelectorAsSelector(selector *metav1.LabelSelector) (labels.Selector, error) {
	if selector == nil {
		return labels.Nothing(), nil
	}
	return metav1.LabelSelectorAsSelector(selector)
}

func podSchedulingRequests(podSpec *corev1.PodSpec) corev1.ResourceList {
	requests := corev1.ResourceList{}
	for i := range podSpec.Containers {
		addSchedulingRequests(requests, podSpec.Containers[i].Resources.Requests)
	}
	initRequests := corev1.ResourceList{}
	for i := range podSpec.InitContainers {
		keepLargerSchedulingRequests(initRequests, podSpec.InitContainers[i].Resources.Requests)
	}
	keepLargerSchedulingRequests(requests, initRequests)
	addSchedulingRequests(requests, podSpec.Overhead)
	return requests
}

func addSchedulingRequests(total, add corev1.ResourceList) {
	for _, resourceName := range schedulingResourceNames() {
		quantity := add[resourceName]
		if quantity.Sign() == 0 {
			continue
		}
		current := total[resourceName]
		current.Add(quantity)
		total[resourceName] = current
	}
}

func keepLargerSchedulingRequests(total, candidate corev1.ResourceList) {
	for _, resourceName := range schedulingResourceNames() {
		quantity := candidate[resourceName]
		if quantity.Sign() == 0 {
			continue
		}
		current := total[resourceName]
		if quantity.Cmp(current) > 0 {
			total[resourceName] = quantity.DeepCopy()
		}
	}
}

func subtractSchedulingRequests(total, used corev1.ResourceList) {
	for _, resourceName := range schedulingResourceNames() {
		quantity := used[resourceName]
		if quantity.Sign() == 0 {
			continue
		}
		current := total[resourceName]
		current.Sub(quantity)
		total[resourceName] = current
	}
}

func schedulingRequestsFit(requests, free corev1.ResourceList, freePods int64) bool {
	if freePods == 0 {
		return false
	}
	for _, resourceName := range schedulingResourceNames() {
		requested := requests[resourceName]
		if requested.Sign() == 0 {
			continue
		}
		available := free[resourceName]
		if available.Sign() <= 0 || available.Cmp(requested) < 0 {
			return false
		}
	}
	return true
}

func bestAvailableSchedulingCapacity(states []schedulingNodeState) string {
	best := corev1.ResourceList{}
	bestPods := int64(0)
	for _, state := range states {
		keepLargerSchedulingRequests(best, state.free)
		if state.freePods > bestPods {
			bestPods = state.freePods
		}
	}
	return schedulingRequestsString(best, bestPods)
}

func schedulingRequestsString(requests corev1.ResourceList, pods int64) string {
	parts := make([]string, 0, 4)
	if cpu := requests[corev1.ResourceCPU]; cpu.Sign() != 0 {
		parts = append(parts, "cpu="+cpu.String())
	}
	if memory := requests[corev1.ResourceMemory]; memory.Sign() != 0 {
		parts = append(parts, "memory="+memory.String())
	}
	if ephemeral := requests[corev1.ResourceEphemeralStorage]; ephemeral.Sign() != 0 {
		parts = append(parts, "ephemeral-storage="+ephemeral.String())
	}
	if pods >= 0 {
		parts = append(parts, fmt.Sprintf("pods=%d", pods))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func schedulingResourceNames() []corev1.ResourceName {
	return []corev1.ResourceName{
		corev1.ResourceCPU,
		corev1.ResourceMemory,
		corev1.ResourceEphemeralStorage,
	}
}

func copyResourceListForScheduling(in corev1.ResourceList) corev1.ResourceList {
	out := corev1.ResourceList{}
	for _, resourceName := range schedulingResourceNames() {
		if quantity := in[resourceName]; quantity.Sign() != 0 {
			out[resourceName] = quantity.DeepCopy()
		}
	}
	return out
}

func allocatablePods(node *corev1.Node) int64 {
	if quantity := node.Status.Allocatable[corev1.ResourcePods]; quantity.Sign() > 0 {
		return quantity.Value()
	}
	return math.MaxInt32
}

func podTerminated(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (b *schedulingBlockerSet) addPlacementDiagnostic(summary string) {
	b.all = append(b.all, summary)
	b.placement = append(b.placement, summary)
}

func (b *schedulingBlockerSet) addTaintDiagnostic(summary string) {
	b.all = append(b.all, summary)
	b.taints = append(b.taints, summary)
}

func (b *schedulingBlockerSet) addCapacityDiagnostic(summary string) {
	b.all = append(b.all, summary)
	b.capacity = append(b.capacity, summary)
}
