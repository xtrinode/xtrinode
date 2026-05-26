package controllers

import (
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetNodePoolFitConditionReportsTooSmallMachineType(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			Resources: &analyticsv1.RuntimeResourcesSpec{
				Worker: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("16"),
						corev1.ResourceMemory: resource.MustParse("64Gi"),
					},
				},
			},
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.large",
				},
			},
		},
	}

	setNodePoolFitCondition(xtrinode)

	condition := status.GetCondition(xtrinode, status.ConditionTypeNodePoolFitReady)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, status.ConditionReasonNodePoolFitFailed, condition.Reason)
	require.Contains(t, condition.Message, "m5.4xlarge")
}
