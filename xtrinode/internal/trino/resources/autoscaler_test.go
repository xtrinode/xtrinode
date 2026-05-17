package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestBuildHorizontalPodAutoscaler(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		wantNil  bool
		want     *autoscalingv2.HorizontalPodAutoscaler
	}{
		{
			name: "autoscaling disabled returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"server": map[string]interface{}{
							"autoscaling": map[string]interface{}{
								"enabled": false,
							},
						},
					}),
				},
			},
			wantNil: true,
		},
		{
			name: "autoscaling enabled with CPU and memory",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"server": map[string]interface{}{
							"autoscaling": map[string]interface{}{
								"enabled":                           true,
								"maxReplicas":                       int64(10),
								"minReplicas":                       int64(2),
								"targetCPUUtilizationPercentage":    int64(70),
								"targetMemoryUtilizationPercentage": int64(80),
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					MinReplicas: func() *int32 { v := int32(2); return &v }(),
					MaxReplicas: 10,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: func() *int32 { v := int32(70); return &v }(),
								},
							},
						},
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceMemory,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: func() *int32 { v := int32(80); return &v }(),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "autoscaling with CPU only",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"server": map[string]interface{}{
							"autoscaling": map[string]interface{}{
								"enabled":                           true,
								"maxReplicas":                       int64(10),
								"minReplicas":                       int64(2),
								"targetCPUUtilizationPercentage":    int64(70),
								"targetMemoryUtilizationPercentage": "",
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					MinReplicas: func() *int32 { v := int32(2); return &v }(),
					MaxReplicas: 10,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: func() *int32 { v := int32(70); return &v }(),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "autoscaling with minReplicas from workers",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"server": map[string]interface{}{
							"workers": int64(3),
							"autoscaling": map[string]interface{}{
								"enabled":                           true,
								"maxReplicas":                       int64(10),
								"targetCPUUtilizationPercentage":    int64(70),
								"targetMemoryUtilizationPercentage": int64(80),
							},
						},
					}),
				},
			},
			wantNil: false,
			want: &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					MinReplicas: func() *int32 { v := int32(3); return &v }(),
					MaxReplicas: 10,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: func() *int32 { v := int32(70); return &v }(),
								},
							},
						},
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceMemory,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: func() *int32 { v := int32(80); return &v }(),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "no metrics configured returns nil",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trino",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
					ValuesOverlay: mustValuesOverlay(map[string]interface{}{
						"server": map[string]interface{}{
							"autoscaling": map[string]interface{}{
								"enabled":                           true,
								"maxReplicas":                       int64(10),
								"targetCPUUtilizationPercentage":    "",
								"targetMemoryUtilizationPercentage": "",
							},
						},
					}),
				},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildHorizontalPodAutoscaler(tt.xtrinode)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			assert.Equal(t, tt.want.Spec.MaxReplicas, got.Spec.MaxReplicas)
			if tt.want.Spec.MinReplicas != nil {
				assert.NotNil(t, got.Spec.MinReplicas)
				assert.Equal(t, *tt.want.Spec.MinReplicas, *got.Spec.MinReplicas)
			}
			assert.Len(t, got.Spec.Metrics, len(tt.want.Spec.Metrics))
			for i, wantMetric := range tt.want.Spec.Metrics {
				if i < len(got.Spec.Metrics) {
					gotMetric := got.Spec.Metrics[i]
					assert.Equal(t, wantMetric.Type, gotMetric.Type)
					if wantMetric.Resource != nil {
						assert.NotNil(t, gotMetric.Resource)
						assert.Equal(t, wantMetric.Resource.Name, gotMetric.Resource.Name)
						if wantMetric.Resource.Target.AverageUtilization != nil {
							assert.NotNil(t, gotMetric.Resource.Target.AverageUtilization)
							assert.Equal(t, *wantMetric.Resource.Target.AverageUtilization, *gotMetric.Resource.Target.AverageUtilization)
						}
					}
				}
			}
		})
	}
}
