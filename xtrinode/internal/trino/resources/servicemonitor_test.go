package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildCoordinatorServiceMonitor(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *unstructured.Unstructured
	}{
		{
			name: "service monitor disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceMonitor: &analyticsv1.ServiceMonitorSpec{
							Enabled: false,
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "no service monitor config uses default enabled",
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
		},
		{
			name: "coordinator service monitor disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceMonitor: &analyticsv1.ServiceMonitorSpec{
							Enabled: true,
							Coordinator: &analyticsv1.ServiceMonitorRoleSpec{
								Enabled: false,
							},
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "service monitor enabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceMonitor: &analyticsv1.ServiceMonitorSpec{
							Enabled:    true,
							Interval:   "30s",
							APIVersion: "monitoring.coreos.com/v1",
						},
					},
				},
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildCoordinatorServiceMonitor(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			if obj, ok := got.(*unstructured.Unstructured); ok {
				assert.Equal(t, "ServiceMonitor", obj.GetKind())
				assert.Equal(t, "monitoring.coreos.com/v1", obj.GetAPIVersion())
				assert.Equal(t, "trino-test-trino", obj.GetName())
				assert.Equal(t, "default", obj.GetNamespace())

				// Verify spec fields
				spec, found, err := unstructured.NestedMap(obj.Object, "spec")
				assert.NoError(t, err)
				assert.True(t, found)
				assert.NotNil(t, spec)

				// Verify selector
				selector, found, err := unstructured.NestedMap(spec, "selector", "matchLabels")
				assert.NoError(t, err)
				assert.True(t, found)
				assert.NotNil(t, selector)

				// Verify endpoints
				endpoints, found, err := unstructured.NestedSlice(spec, "endpoints")
				assert.NoError(t, err)
				assert.True(t, found)
				assert.Greater(t, len(endpoints), 0)
			}
		})
	}
}

func TestBuildWorkerServiceMonitor(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *unstructured.Unstructured
	}{
		{
			name: "service monitor disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceMonitor: &analyticsv1.ServiceMonitorSpec{
							Enabled: false,
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "no service monitor config uses default enabled",
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
		},
		{
			name: "worker service monitor disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceMonitor: &analyticsv1.ServiceMonitorSpec{
							Enabled: true,
							Worker: &analyticsv1.ServiceMonitorRoleSpec{
								Enabled: false,
							},
						},
					},
				},
			},
			wantNil: true,
		},
		{
			name: "service monitor enabled",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
						ServiceMonitor: &analyticsv1.ServiceMonitorSpec{
							Enabled:    true,
							Interval:   "30s",
							APIVersion: "monitoring.coreos.com/v1",
						},
					},
				},
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildWorkerServiceMonitor(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			if obj, ok := got.(*unstructured.Unstructured); ok {
				assert.Equal(t, "ServiceMonitor", obj.GetKind())
				assert.Equal(t, "monitoring.coreos.com/v1", obj.GetAPIVersion())
				assert.Equal(t, "trino-test-trino-worker", obj.GetName())
				assert.Equal(t, "default", obj.GetNamespace())

				// Verify spec fields
				spec, found, err := unstructured.NestedMap(obj.Object, "spec")
				assert.NoError(t, err)
				assert.True(t, found)
				assert.NotNil(t, spec)

				// Verify selector
				selector, found, err := unstructured.NestedMap(spec, "selector", "matchLabels")
				assert.NoError(t, err)
				assert.True(t, found)
				assert.NotNil(t, selector)

				// Verify endpoints
				endpoints, found, err := unstructured.NestedSlice(spec, "endpoints")
				assert.NoError(t, err)
				assert.True(t, found)
				assert.Greater(t, len(endpoints), 0)
			}
		})
	}
}
