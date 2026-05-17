package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildCoordinatorPodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *policyv1.PodDisruptionBudget
	}{
		{
			name: "default PDB enabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino-coordinator-pdb",
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
				Spec: policyv1.PodDisruptionBudgetSpec{
					MinAvailable: func() *intstr.IntOrString {
						v := intstr.FromInt32(1)
						return &v
					}(),
					Selector: &metav1.LabelSelector{
						MatchLabels: TrinoSelectorLabels(&analyticsv1.XTrinode{
							ObjectMeta: metav1.ObjectMeta{Name: "test-trino"},
						}, ComponentCoordinator),
					},
				},
			},
		},
		{
			name: "PDB disabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"coordinator": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled": false,
							},
						},
					}),
				},
			},
			wantNil: true,
		},
		{
			name: "PDB with minAvailable",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"coordinator": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled":      true,
								"minAvailable": int64(1),
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				Spec: policyv1.PodDisruptionBudgetSpec{
					MinAvailable: func() *intstr.IntOrString {
						v := intstr.FromInt32(1)
						return &v
					}(),
				},
			},
		},
		{
			name: "PDB with maxUnavailable",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"coordinator": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled":        true,
								"maxUnavailable": int64(1),
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				Spec: policyv1.PodDisruptionBudgetSpec{
					MaxUnavailable: func() *intstr.IntOrString {
						v := intstr.FromInt32(1)
						return &v
					}(),
				},
			},
		},
		{
			name: "PDB with minAvailable as string",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"coordinator": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled":      true,
								"minAvailable": "50%",
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				Spec: policyv1.PodDisruptionBudgetSpec{
					MinAvailable: func() *intstr.IntOrString {
						v := intstr.FromString("50%")
						return &v
					}(),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildCoordinatorPodDisruptionBudget(tt.xtrinode)
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
			assert.NotNil(t, got.Spec.Selector)
			assert.Equal(t, ComponentCoordinator, got.Spec.Selector.MatchLabels[AppComponentLabel])

			if tt.want.Spec.MinAvailable != nil {
				assert.NotNil(t, got.Spec.MinAvailable)
				if tt.want.Spec.MinAvailable.Type == intstr.String {
					assert.Equal(t, tt.want.Spec.MinAvailable.StrVal, got.Spec.MinAvailable.StrVal)
				} else {
					assert.Equal(t, tt.want.Spec.MinAvailable.IntVal, got.Spec.MinAvailable.IntVal)
				}
			}
			if tt.want.Spec.MaxUnavailable != nil {
				if got.Spec.MaxUnavailable != nil {
					if tt.want.Spec.MaxUnavailable.Type == intstr.String {
						assert.Equal(t, tt.want.Spec.MaxUnavailable.StrVal, got.Spec.MaxUnavailable.StrVal)
					} else {
						assert.Equal(t, tt.want.Spec.MaxUnavailable.IntVal, got.Spec.MaxUnavailable.IntVal)
					}
				}
			}
		})
	}
}

func TestBuildWorkerPodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *policyv1.PodDisruptionBudget
	}{
		{
			name: "default PDB enabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino-worker-pdb",
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
				Spec: policyv1.PodDisruptionBudgetSpec{
					MaxUnavailable: func() *intstr.IntOrString {
						v := intstr.FromInt32(1)
						return &v
					}(),
					Selector: &metav1.LabelSelector{
						MatchLabels: TrinoSelectorLabels(&analyticsv1.XTrinode{
							ObjectMeta: metav1.ObjectMeta{Name: "test-trino"},
						}, ComponentWorker),
					},
				},
			},
		},
		{
			name: "PDB disabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"worker": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled": false,
							},
						},
					}),
				},
			},
			wantNil: true,
		},
		{
			name: "PDB with maxUnavailable",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"worker": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled":        true,
								"maxUnavailable": int64(2),
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				Spec: policyv1.PodDisruptionBudgetSpec{
					MaxUnavailable: func() *intstr.IntOrString {
						v := intstr.FromInt32(2)
						return &v
					}(),
				},
			},
		},
		{
			name: "PDB with minAvailable",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"worker": map[string]interface{}{
							"podDisruptionBudget": map[string]interface{}{
								"enabled":      true,
								"minAvailable": int64(3),
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &policyv1.PodDisruptionBudget{
				Spec: policyv1.PodDisruptionBudgetSpec{
					MinAvailable: func() *intstr.IntOrString {
						v := intstr.FromInt32(3)
						return &v
					}(),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildWorkerPodDisruptionBudget(tt.xtrinode)
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
			assert.NotNil(t, got.Spec.Selector)
			assert.Equal(t, ComponentWorker, got.Spec.Selector.MatchLabels[AppComponentLabel])

			if tt.want.Spec.MinAvailable != nil {
				assert.NotNil(t, got.Spec.MinAvailable)
				if tt.want.Spec.MinAvailable.Type == intstr.String {
					assert.Equal(t, tt.want.Spec.MinAvailable.StrVal, got.Spec.MinAvailable.StrVal)
				} else {
					assert.Equal(t, tt.want.Spec.MinAvailable.IntVal, got.Spec.MinAvailable.IntVal)
				}
			}
			if tt.want.Spec.MaxUnavailable != nil {
				if got.Spec.MaxUnavailable != nil {
					if tt.want.Spec.MaxUnavailable.Type == intstr.String {
						assert.Equal(t, tt.want.Spec.MaxUnavailable.StrVal, got.Spec.MaxUnavailable.StrVal)
					} else {
						assert.Equal(t, tt.want.Spec.MaxUnavailable.IntVal, got.Spec.MaxUnavailable.IntVal)
					}
				}
			}
		})
	}
}
