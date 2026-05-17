package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildNetworkPolicy(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *networkingv1.NetworkPolicy
	}{
		{
			name: "network policy disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						NetworkPolicy: &analyticsv1.NetworkPolicySpec{
							Enabled: false,
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "no network policy config returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			wantNil: true,
		},
		{
			name: "network policy with NodePort service returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"service": map[string]interface{}{
							"type": "NodePort",
						},
					}),
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						NetworkPolicy: &analyticsv1.NetworkPolicySpec{
							Enabled: true,
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "network policy enabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						NetworkPolicy: &analyticsv1.NetworkPolicySpec{
							Enabled: true,
							Ingress: []analyticsv1.NetworkPolicyIngressSpec{
								{
									From: []analyticsv1.NetworkPolicyPeerSpec{
										{
											PodSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{
													"app": "prometheus",
												},
											},
										},
									},
									Ports: []analyticsv1.NetworkPolicyPortSpec{
										{
											Protocol: func() *corev1.Protocol { p := corev1.ProtocolTCP; return &p }(),
											Port:     func() *intstr.IntOrString { p := intstr.FromInt(8080); return &p }(),
										},
									},
								},
							},
						},
					},
				},
			},
			wantNil: false,
			want: &networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trino-test-trino",
					Namespace: "default",
					Labels: map[string]string{
						AppNameLabel:      "trino",
						AppInstanceLabel:  "test-trino",
						AppVersionLabel:   "480",
						AppManagedByLabel: ManagedByValue,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "analytics.xtrinode.com/v1",
							Kind:               "XTrinode",
							Name:               "test-trino",
							Controller:         func() *bool { b := true; return &b }(),
							BlockOwnerDeletion: func() *bool { b := true; return &b }(),
						},
					},
				},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"trino.io/network-policy-protection": "enabled",
						},
					},
					PolicyTypes: []networkingv1.PolicyType{
						networkingv1.PolicyTypeIngress,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildNetworkPolicy(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			assert.Equal(t, tt.want.Name, got.Name)
			assert.Equal(t, tt.want.Namespace, got.Namespace)
			assert.NotNil(t, got.Spec.PodSelector)
			assert.Contains(t, got.Spec.PodSelector.MatchLabels, "trino.io/network-policy-protection")
			assert.Len(t, got.Spec.PolicyTypes, len(tt.want.Spec.PolicyTypes))
			if len(tt.want.Spec.Ingress) > 0 {
				assert.Greater(t, len(got.Spec.Ingress), 0, "should have at least default ingress rule")
			}
		})
	}
}

func TestNetworkPolicyEnabledLabelsPods(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				NetworkPolicy: &analyticsv1.NetworkPolicySpec{
					Enabled: true,
				},
			},
		},
	}

	labels := TrinoPodLabels(xtrinode, ComponentCoordinator)
	assert.Equal(t, "enabled", labels["trino.io/network-policy-protection"])
}
