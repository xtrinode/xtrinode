package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWaitForNodePoolReady(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name                 string
		xtrinode             *analyticsv1.XTrinode
		resource             *unstructured.Unstructured
		wantReady            bool
		wantRequeueAfter     bool
		expectedRequeueAfter string
	}{
		{
			name: "no node pool is ready",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
				Spec:       analyticsv1.XTrinodeSpec{Size: "s"},
			},
			wantReady: true,
		},
		{
			name: "resource missing requeues",
			xtrinode: testReadinessXTrinode(
				&analyticsv1.NodePoolSpec{
					Provider: "aws",
					AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
				},
			),
			wantReady:            false,
			wantRequeueAfter:     true,
			expectedRequeueAfter: config.NodePoolResourceNotFoundRequeueInterval.String(),
		},
		{
			name: "status missing requeues",
			xtrinode: testReadinessXTrinode(
				&analyticsv1.NodePoolSpec{
					Provider: "aws",
					AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
				},
			),
			resource:             testReadinessResource("runtime", "team-a", nil),
			wantReady:            false,
			wantRequeueAfter:     true,
			expectedRequeueAfter: config.NodePoolStatusNotAvailableRequeueInterval.String(),
		},
		{
			name: "ready replicas below required requeues",
			xtrinode: testReadinessXTrinode(
				&analyticsv1.NodePoolSpec{
					Provider: "aws",
					AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
					MinNodes: int32Ptr(2),
				},
			),
			resource:             testReadinessResource("runtime", "team-a", map[string]interface{}{"readyReplicas": int64(1), "replicas": int64(2)}),
			wantReady:            false,
			wantRequeueAfter:     true,
			expectedRequeueAfter: config.NodePoolNodesReadyRequeueInterval.String(),
		},
		{
			name: "zero ready replicas uses no-nodes interval",
			xtrinode: testReadinessXTrinode(
				&analyticsv1.NodePoolSpec{
					Provider: "aws",
					AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
					MinNodes: int32Ptr(2),
				},
			),
			resource:             testReadinessResource("runtime", "team-a", map[string]interface{}{"readyReplicas": int64(0), "replicas": int64(2)}),
			wantReady:            false,
			wantRequeueAfter:     true,
			expectedRequeueAfter: config.NodePoolNoNodesReadyRequeueInterval.String(),
		},
		{
			name: "ready replicas satisfy min nodes",
			xtrinode: testReadinessXTrinode(
				&analyticsv1.NodePoolSpec{
					Provider: "aws",
					AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
					MinNodes: int32Ptr(2),
				},
			),
			resource:  testReadinessResource("runtime", "team-a", map[string]interface{}{"readyReplicas": int64(2), "replicas": int64(2)}),
			wantReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestSchemeAnalyticsOnly()
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.resource != nil {
				builder = builder.WithRuntimeObjects(tt.resource)
			}
			reconciler := newTestReconciler(builder.Build(), scheme)

			ready, result, err := reconciler.waitForNodePoolReady(ctx, tt.xtrinode, newTestLogger())
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, ready)
			assert.Equal(t, tt.wantRequeueAfter, result.RequeueAfter > 0)
			if tt.expectedRequeueAfter != "" {
				assert.Equal(t, tt.expectedRequeueAfter, result.RequeueAfter.String())
			}
		})
	}
}

func testReadinessXTrinode(nodePool *analyticsv1.NodePoolSpec) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			Size:     "s",
			NodePool: nodePool,
		},
	}
}

func testReadinessResource(xtrinodeName, namespace string, status map[string]interface{}) *unstructured.Unstructured {
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	})
	resource.SetName(xtrinodeName + config.NodePoolNameSuffix)
	resource.SetNamespace(namespace)
	if status != nil {
		resource.Object["status"] = status
	}
	return resource
}
