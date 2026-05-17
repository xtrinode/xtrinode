package resources

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// BuildNetworkPolicy builds a NetworkPolicy resource for Trino pods
// Returns nil if networkPolicy is disabled or not configured
func BuildNetworkPolicy(xtrinode *analyticsv1.XTrinode) *networkingv1.NetworkPolicy {
	networkPolicySpec := effectiveNetworkPolicySpec(xtrinode)
	if networkPolicySpec == nil {
		return nil
	}
	if !networkPolicySpec.Enabled {
		return nil
	}

	// Check if service type is NodePort (not supported)
	serviceType := config.DefaultServiceType
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if service, ok := xtrinode.Spec.GetValuesOverlayMap()["service"].(map[string]interface{}); ok {
			if svcType, ok := service["type"].(string); ok {
				serviceType = svcType
			}
		}
	}
	if serviceType == "NodePort" {
		// NetworkPolicy is not supported with NodePort services
		// Return nil instead of failing (let user handle this)
		return nil
	}

	// Build pod selector - matches all Trino pods (coordinator + workers)
	// Use matchExpressions to match both coordinator and worker pods.
	podSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			AppNameLabel:                         "trino",
			AppInstanceLabel:                     xtrinode.Name,
			AppManagedByLabel:                    ManagedByValue,
			"trino.io/network-policy-protection": "enabled",
		},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      AppComponentLabel,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{ComponentCoordinator, ComponentWorker},
			},
		},
	}

	// Build policy types
	policyTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	if len(networkPolicySpec.Egress) > 0 {
		policyTypes = append(policyTypes, networkingv1.PolicyTypeEgress)
	}

	// Build ingress rules
	ingressRules := []networkingv1.NetworkPolicyIngressRule{
		{
			// Default rule: allow traffic from Trino pods in the same namespace
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							AppNameLabel:                         "trino",
							AppInstanceLabel:                     xtrinode.Name,
							AppManagedByLabel:                    ManagedByValue,
							"trino.io/network-policy-protection": "enabled",
						},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      AppComponentLabel,
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{ComponentCoordinator, ComponentWorker},
							},
						},
					},
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": xtrinode.Namespace,
						},
					},
				},
			},
		},
	}

	// Add additional ingress rules from spec
	for _, ingressSpec := range networkPolicySpec.Ingress {
		rule := networkingv1.NetworkPolicyIngressRule{}

		// Build "from" peers
		for _, peerSpec := range ingressSpec.From {
			peer := networkingv1.NetworkPolicyPeer{}
			if peerSpec.PodSelector != nil {
				peer.PodSelector = peerSpec.PodSelector
			}
			if peerSpec.NamespaceSelector != nil {
				peer.NamespaceSelector = peerSpec.NamespaceSelector
			}
			if peerSpec.IPBlock != nil {
				peer.IPBlock = peerSpec.IPBlock
			}
			rule.From = append(rule.From, peer)
		}

		// Build ports
		for _, portSpec := range ingressSpec.Ports {
			port := networkingv1.NetworkPolicyPort{}
			if portSpec.Protocol != nil {
				port.Protocol = portSpec.Protocol
			}
			if portSpec.Port != nil {
				port.Port = portSpec.Port
			}
			rule.Ports = append(rule.Ports, port)
		}

		ingressRules = append(ingressRules, rule)
	}

	// Build egress rules
	var egressRules []networkingv1.NetworkPolicyEgressRule
	for _, egressSpec := range networkPolicySpec.Egress {
		rule := networkingv1.NetworkPolicyEgressRule{}

		// Build "to" peers
		for _, peerSpec := range egressSpec.To {
			peer := networkingv1.NetworkPolicyPeer{}
			if peerSpec.PodSelector != nil {
				peer.PodSelector = peerSpec.PodSelector
			}
			if peerSpec.NamespaceSelector != nil {
				peer.NamespaceSelector = peerSpec.NamespaceSelector
			}
			if peerSpec.IPBlock != nil {
				peer.IPBlock = peerSpec.IPBlock
			}
			rule.To = append(rule.To, peer)
		}

		// Build ports
		for _, portSpec := range egressSpec.Ports {
			port := networkingv1.NetworkPolicyPort{}
			if portSpec.Protocol != nil {
				port.Protocol = portSpec.Protocol
			}
			if portSpec.Port != nil {
				port.Port = portSpec.Port
			}
			rule.Ports = append(rule.Ports, port)
		}

		egressRules = append(egressRules, rule)
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:            config.BuildCoordinatorServiceName(xtrinode.Name), // NetworkPolicy name matches coordinator service
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: podSelector,
			PolicyTypes: policyTypes,
			Ingress:     ingressRules,
			Egress:      egressRules,
		},
	}
}

func effectiveNetworkPolicySpec(xtrinode *analyticsv1.XTrinode) *analyticsv1.NetworkPolicySpec {
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.NetworkPolicy != nil {
		return xtrinode.Spec.HelmChartConfig.NetworkPolicy
	}

	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return nil
	}

	networkPolicy, ok := xtrinode.Spec.GetValuesOverlayMap()["networkPolicy"].(map[string]interface{})
	if !ok {
		return nil
	}

	yamlBytes, err := yaml.Marshal(networkPolicy)
	if err != nil {
		return nil
	}
	var spec analyticsv1.NetworkPolicySpec
	if err := yaml.Unmarshal(yamlBytes, &spec); err != nil {
		return nil
	}
	return &spec
}
