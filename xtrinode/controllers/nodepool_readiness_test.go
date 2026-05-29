package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

func TestWaitForNodePoolReadyReportsCoreFailureCondition(t *testing.T) {
	ctx := context.Background()
	scheme := newTestSchemeAnalyticsOnly()
	xtrinode := testReadinessXTrinode(
		&analyticsv1.NodePoolSpec{
			Provider: "aws",
			AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
		},
	)
	resource := testReadinessResource("runtime", "team-a", map[string]interface{}{
		"readyReplicas": int64(0),
		"replicas":      int64(1),
		"conditions": []interface{}{
			map[string]interface{}{
				"type":     "Ready",
				"status":   "False",
				"severity": "Error",
				"reason":   "InstanceProvisionFailed",
				"message":  "cloud quota exceeded",
			},
		},
	})
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		WithRuntimeObjects(resource).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	ready, result, err := reconciler.waitForNodePoolReady(ctx, xtrinode, newTestLogger())

	require.NoError(t, err)
	assert.False(t, ready)
	assert.Equal(t, config.NodePoolProvisioningErrorRequeueInterval, result.RequeueAfter)
	stored := &analyticsv1.XTrinode{}
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "runtime", Namespace: "team-a"}, stored))
	condition := status.GetCondition(stored, status.ConditionTypeNodePoolReady)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, status.ConditionReasonNodePoolFailed, condition.Reason)
	assert.Contains(t, condition.Message, "MachineDeployment")
	assert.Contains(t, condition.Message, "InstanceProvisionFailed")
	assert.Contains(t, condition.Message, "cloud quota exceeded")
}

func TestWaitForNodePoolReadyReportsManagedProviderFailureCondition(t *testing.T) {
	ctx := context.Background()
	scheme := newTestSchemeAnalyticsOnly()
	xtrinode := testReadinessXTrinode(
		&analyticsv1.NodePoolSpec{
			Provider:     "gcp",
			ProviderMode: "managed",
			GCP:          &analyticsv1.GCPNodePoolSpec{MachineType: "e2-standard-4"},
		},
	)
	machinePool := testReadinessResourceWithGVK(
		getMachineResourceGVK(true),
		"runtime"+config.NodePoolNameSuffix,
		"team-a",
		map[string]interface{}{"readyReplicas": int64(0), "replicas": int64(1)},
	)
	managedPool := testReadinessResourceWithGVK(
		getManagedInfrastructureGVK("gcp"),
		"runtime"+config.NodePoolNameSuffix,
		"team-a",
		map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{
					"type":    "Ready",
					"status":  "False",
					"reason":  "GKENodePoolCreateFailed",
					"message": "GKE rejected the node pool request",
				},
			},
		},
	)
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		WithRuntimeObjects(machinePool, managedPool).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	ready, result, err := reconciler.waitForNodePoolReady(ctx, xtrinode, newTestLogger())

	require.NoError(t, err)
	assert.False(t, ready)
	assert.Equal(t, config.NodePoolProvisioningErrorRequeueInterval, result.RequeueAfter)
	stored := &analyticsv1.XTrinode{}
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "runtime", Namespace: "team-a"}, stored))
	condition := status.GetCondition(stored, status.ConditionTypeNodePoolReady)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Contains(t, condition.Message, "GCPManagedMachinePool")
	assert.Contains(t, condition.Message, "GKENodePoolCreateFailed")
}

func TestWaitForNodePoolReadyReportsMachineInfrastructureRefFailure(t *testing.T) {
	ctx := context.Background()
	scheme := newTestSchemeAnalyticsOnly()
	xtrinode := testReadinessXTrinode(
		&analyticsv1.NodePoolSpec{
			Provider: "aws",
			AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
		},
	)
	machineDeployment := testReadinessResource("runtime", "team-a", map[string]interface{}{
		"readyReplicas": int64(0),
		"replicas":      int64(1),
	})
	machineDeployment.SetUID(types.UID("md-uid"))
	machineSet := testReadinessResourceWithGVK(machineSetGVK(), "runtime-pool-ms", "team-a", nil)
	machineSet.SetUID(types.UID("ms-uid"))
	machineSet.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: machineDeployment.GetAPIVersion(),
			Kind:       machineDeployment.GetKind(),
			Name:       machineDeployment.GetName(),
			UID:        machineDeployment.GetUID(),
		},
	})
	machine := testReadinessResourceWithGVK(machineGVK(), "runtime-machine-1", "team-a", map[string]interface{}{})
	machine.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: machineSet.GetAPIVersion(),
			Kind:       machineSet.GetKind(),
			Name:       machineSet.GetName(),
			UID:        machineSet.GetUID(),
		},
	})
	machine.Object["spec"] = map[string]interface{}{
		"infrastructureRef": map[string]interface{}{
			"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta2",
			"kind":       "AWSMachine",
			"name":       "runtime-machine-1",
			"namespace":  "team-a",
		},
	}
	awsMachine := testReadinessResourceWithGVK(
		schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta2", Kind: "AWSMachine"},
		"runtime-machine-1",
		"team-a",
		map[string]interface{}{
			"failureReason":  "InstanceLaunchFailed",
			"failureMessage": "EC2 quota exceeded",
		},
	)
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		WithRuntimeObjects(machineDeployment, machineSet, machine, awsMachine).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	ready, result, err := reconciler.waitForNodePoolReady(ctx, xtrinode, newTestLogger())

	require.NoError(t, err)
	assert.False(t, ready)
	assert.Equal(t, config.NodePoolProvisioningErrorRequeueInterval, result.RequeueAfter)
	stored := &analyticsv1.XTrinode{}
	require.NoError(t, cli.Get(ctx, types.NamespacedName{Name: "runtime", Namespace: "team-a"}, stored))
	condition := status.GetCondition(stored, status.ConditionTypeNodePoolReady)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Contains(t, condition.Message, "AWSMachine")
	assert.Contains(t, condition.Message, "InstanceLaunchFailed")
	assert.Contains(t, condition.Message, "EC2 quota exceeded")
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

func testReadinessResource(xtrinodeName, namespace string, resourceStatus map[string]interface{}) *unstructured.Unstructured {
	return testReadinessResourceWithGVK(
		schema.GroupVersionKind{
			Group:   "cluster.x-k8s.io",
			Version: "v1beta1",
			Kind:    "MachineDeployment",
		},
		xtrinodeName+config.NodePoolNameSuffix,
		namespace,
		resourceStatus,
	)
}

func testReadinessResourceWithGVK(gvk schema.GroupVersionKind, name, namespace string, resourceStatus map[string]interface{}) *unstructured.Unstructured {
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(gvk)
	resource.SetName(name)
	resource.SetNamespace(namespace)
	if resourceStatus != nil {
		resource.Object["status"] = resourceStatus
	}
	return resource
}
