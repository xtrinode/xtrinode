package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestTrinoLabels(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		want     map[string]string
	}{
		{
			name: "basic labels",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			want: map[string]string{
				AppNameLabel:      "trino",
				AppInstanceLabel:  "test-trino",
				AppVersionLabel:   "480",
				AppManagedByLabel: ManagedByValue,
			},
		},
		{
			name: "labels with custom version",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"image": map[string]interface{}{
							"tag": "450",
						},
					}),
				},
			},
			want: map[string]string{
				AppNameLabel:      "trino",
				AppInstanceLabel:  "test-trino",
				AppVersionLabel:   "450",
				AppManagedByLabel: ManagedByValue,
			},
		},
		{
			name: "labels with commonLabels",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"commonLabels": map[string]interface{}{
							"team":    "data-platform",
							"project": "analytics",
						},
					}),
				},
			},
			want: map[string]string{
				AppNameLabel:      "trino",
				AppInstanceLabel:  "test-trino",
				AppVersionLabel:   "480",
				AppManagedByLabel: ManagedByValue,
				"team":            "data-platform",
				"project":         "analytics",
			},
		},
		{
			name: "labels with coordinator custom labels",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"coordinator": map[string]interface{}{
							"labels": map[string]interface{}{
								"component": "coordinator",
							},
						},
					}),
				},
			},
			want: map[string]string{
				AppNameLabel:      "trino",
				AppInstanceLabel:  "test-trino",
				AppVersionLabel:   "480",
				AppManagedByLabel: ManagedByValue,
				"component":       "coordinator",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]string
			if tt.name == "labels with coordinator custom labels" {
				got = TrinoLabelsForComponent(tt.xtrinode, "coordinator")
			} else {
				got = TrinoLabels(tt.xtrinode)
			}
			for k, v := range tt.want {
				assert.Equal(t, v, got[k], "label %s should match", k)
			}
			// Verify standard labels are always present
			assert.Equal(t, "trino", got[AppNameLabel])
			assert.Equal(t, tt.xtrinode.Name, got[AppInstanceLabel])
			assert.Equal(t, ManagedByValue, got[AppManagedByLabel])
		})
	}
}

func TestTrinoSelectorLabels(t *testing.T) {
	tests := []struct {
		name      string
		xtrinode  *analyticsv1.XTrinode
		component string
		want      map[string]string
	}{
		{
			name: "coordinator selector labels",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			component: ComponentCoordinator,
			want: map[string]string{
				AppNameLabel:      "trino",
				AppInstanceLabel:  "test-trino",
				AppManagedByLabel: ManagedByValue,
				AppComponentLabel: ComponentCoordinator,
				// NOTE: AppVersionLabel is intentionally NOT in selector labels
				// Selectors must be immutable - version goes in pod labels only
			},
		},
		{
			name: "worker selector labels",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			component: ComponentWorker,
			want: map[string]string{
				AppNameLabel:      "trino",
				AppInstanceLabel:  "test-trino",
				AppManagedByLabel: ManagedByValue,
				AppComponentLabel: ComponentWorker,
				// NOTE: AppVersionLabel is intentionally NOT in selector labels
				// Selectors must be immutable - version goes in pod labels only
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TrinoSelectorLabels(tt.xtrinode, tt.component)
			for k, v := range tt.want {
				assert.Equal(t, v, got[k], "label %s should match", k)
			}
			assert.Equal(t, tt.component, got[AppComponentLabel], "component label should be set")
		})
	}
}

func TestOwnerReference(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		want     metav1.OwnerReference
	}{
		{
			name: "owner reference",
			xtrinode: &analyticsv1.XTrinode{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "wrong.example.com/v1",
					Kind:       "XTrinode",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
					UID:       "test-uid-123",
				},
			},
			want: metav1.OwnerReference{
				APIVersion:         analyticsv1.GroupVersion.String(),
				Kind:               "XTrinode",
				Name:               "test-trino",
				UID:                "test-uid-123",
				Controller:         func() *bool { b := true; return &b }(),
				BlockOwnerDeletion: func() *bool { b := true; return &b }(),
			},
		},
		{
			name: "owner reference without typemeta",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
					UID:       "test-uid-456",
				},
			},
			want: metav1.OwnerReference{
				APIVersion:         analyticsv1.GroupVersion.String(),
				Kind:               "XTrinode",
				Name:               "test-trino",
				UID:                "test-uid-456",
				Controller:         func() *bool { b := true; return &b }(),
				BlockOwnerDeletion: func() *bool { b := true; return &b }(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OwnerReference(tt.xtrinode)
			assert.Equal(t, tt.want.APIVersion, got.APIVersion)
			assert.Equal(t, tt.want.Kind, got.Kind)
			assert.Equal(t, tt.want.Name, got.Name)
			assert.Equal(t, tt.want.UID, got.UID)
			assert.NotNil(t, got.Controller)
			assert.True(t, *got.Controller)
			assert.NotNil(t, got.BlockOwnerDeletion)
			assert.True(t, *got.BlockOwnerDeletion)
		})
	}
}
