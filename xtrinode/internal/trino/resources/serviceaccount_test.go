package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildServiceAccount(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		want     *corev1.ServiceAccount
	}{
		{
			name: "basic service account",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			want: &corev1.ServiceAccount{
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
			},
		},
		{
			name: "service account with annotations",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceAccount: &analyticsv1.ServiceAccountSpec{
							Annotations: map[string]string{
								"eks.amazonaws.com/role-arn": "arn:aws:iam::123456789012:role/trino-role",
							},
						},
					},
				},
			},
			want: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trino-test-trino",
					Namespace: "default",
					Labels: map[string]string{
						AppNameLabel:      "trino",
						AppInstanceLabel:  "test-trino",
						AppVersionLabel:   "480",
						AppManagedByLabel: ManagedByValue,
					},
					Annotations: map[string]string{
						"eks.amazonaws.com/role-arn": "arn:aws:iam::123456789012:role/trino-role",
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
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildServiceAccount(tt.xtrinode)
			assert.NotNil(t, got)
			assert.Equal(t, tt.want.Name, got.Name)
			assert.Equal(t, tt.want.Namespace, got.Namespace)
			assert.Equal(t, tt.want.Labels[AppNameLabel], got.Labels[AppNameLabel])
			assert.Equal(t, tt.want.Labels[AppInstanceLabel], got.Labels[AppInstanceLabel])
			assert.Equal(t, tt.want.Labels[AppManagedByLabel], got.Labels[AppManagedByLabel])
			assert.NotNil(t, got.OwnerReferences)
			assert.Len(t, got.OwnerReferences, 1)
			assert.True(t, *got.OwnerReferences[0].Controller)
			assert.True(t, *got.OwnerReferences[0].BlockOwnerDeletion)

			// Verify annotations if specified
			if tt.want.Annotations != nil {
				for k, v := range tt.want.Annotations {
					assert.Equal(t, v, got.Annotations[k], "annotation %s should match", k)
				}
			}
		})
	}
}
