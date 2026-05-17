package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildIngress(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *networkingv1.Ingress
	}{
		{
			name: "ingress disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						Ingress: &analyticsv1.IngressSpec{
							Enabled: false,
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "no ingress config returns nil",
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
			name: "ingress with single host",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						Ingress: &analyticsv1.IngressSpec{
							Enabled:   true,
							ClassName: "nginx",
							Hosts: []analyticsv1.IngressHostSpec{
								{
									Host: "trino.example.com",
									Paths: []analyticsv1.IngressPathSpec{
										{
											Path:     "/",
											PathType: "Prefix",
										},
									},
								},
							},
						},
					},
				},
			},
			wantNil: false,
			want: &networkingv1.Ingress{
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
				Spec: networkingv1.IngressSpec{
					IngressClassName: func() *string { s := "nginx"; return &s }(),
					Rules: []networkingv1.IngressRule{
						{
							Host: "trino.example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: func() *networkingv1.PathType { pt := networkingv1.PathTypePrefix; return &pt }(),
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "trino-test-trino",
													Port: networkingv1.ServiceBackendPort{
														Number: 8080,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "ingress with TLS",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					TLS: &analyticsv1.TLSSpec{ //nolint:gosec // test fixture: TLS class names are not credentials
						ServerSecretClass: "trino-tls",
					},
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						Ingress: &analyticsv1.IngressSpec{
							Enabled:   true,
							ClassName: "nginx",
							Hosts: []analyticsv1.IngressHostSpec{
								{
									Host: "trino.example.com",
								},
							},
							TLS: []analyticsv1.IngressTLSSpec{
								{ //nolint:gosec // test fixture: TLS secret names are not credentials
									SecretName: "trino-tls-secret",
									Hosts:      []string{"trino.example.com"},
								},
							},
						},
					},
				},
			},
			wantNil: false,
			want: &networkingv1.Ingress{
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
				Spec: networkingv1.IngressSpec{
					TLS: []networkingv1.IngressTLS{
						{ //nolint:gosec // test fixture: TLS secret names are not credentials, used for testing purposes only
							SecretName: "trino-tls-secret",
							Hosts:      []string{"trino.example.com"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildIngress(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			if tt.want.Name != "" {
				assert.Equal(t, tt.want.Name, got.Name)
			}
			if tt.want.Namespace != "" {
				assert.Equal(t, tt.want.Namespace, got.Namespace)
			}
			if tt.want.Spec.IngressClassName != nil {
				assert.NotNil(t, got.Spec.IngressClassName)
				assert.Equal(t, *tt.want.Spec.IngressClassName, *got.Spec.IngressClassName)
			}
			if len(tt.want.Spec.Rules) > 0 {
				assert.Len(t, got.Spec.Rules, len(tt.want.Spec.Rules))
			}
			if len(tt.want.Spec.TLS) > 0 {
				assert.Len(t, got.Spec.TLS, len(tt.want.Spec.TLS))
				for i, wantTLS := range tt.want.Spec.TLS {
					if i < len(got.Spec.TLS) {
						gotTLS := got.Spec.TLS[i]
						assert.Equal(t, wantTLS.SecretName, gotTLS.SecretName)
						assert.Equal(t, wantTLS.Hosts, gotTLS.Hosts)
					}
				}
			}
		})
	}
}
