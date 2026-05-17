package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// =============================================================================
// Regression: ScaleNodePoolMinNodes must actually write changes
// =============================================================================

func TestScaleNodePoolMinNodes_ActuallyPatchesAnnotations(t *testing.T) {
	// Create a pre-existing MachineDeployment to scale
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	})
	existing.SetName("test-xtrinode" + config.NodePoolNameSuffix)
	existing.SetNamespace("default")
	// Set initial replicas
	_ = unstructured.SetNestedField(existing.Object, int64(1), "spec", "replicas")

	scheme := newTestSchemeAnalyticsOnly()
	cli := newTestClient(scheme)
	// Create the resource in the fake client
	ctx := context.Background()
	err := cli.Create(ctx, existing)
	require.NoError(t, err)

	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				MinNodes:          int32Ptr(1),
				MaxNodes:          int32Ptr(10),
				AutoscalerEnabled: func() *bool { b := false; return &b }(),
			},
		},
	}

	// Scale to 5
	err = adapter.ScaleNodePoolMinNodes(ctx, xtrinode, 5)
	require.NoError(t, err)

	// Verify the resource was actually patched
	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	})
	err = cli.Get(ctx, types.NamespacedName{
		Name:      "test-xtrinode" + config.NodePoolNameSuffix,
		Namespace: "default",
	}, updated)
	require.NoError(t, err)

	// Check annotations were actually written (this was the no-op behavior)
	annotations := updated.GetAnnotations()
	assert.Equal(t, "5", annotations[config.NodePoolAutoscalerMinSizeAnnotation],
		"MinSize annotation should be updated to 5")
	assert.Equal(t, "10", annotations[config.NodePoolAutoscalerMaxSizeAnnotation],
		"MaxSize annotation should be updated to 10")

	// Check replicas were updated (autoscaler disabled)
	replicas, found, _ := unstructured.NestedInt64(updated.Object, "spec", "replicas")
	assert.True(t, found, "replicas field should exist")
	assert.Equal(t, int64(5), replicas, "replicas should be updated to 5 when autoscaler is disabled")
}

func TestScaleNodePoolMinNodes_AutoscalerEnabled_OnlyAnnotations(t *testing.T) {
	// Create a pre-existing MachineDeployment
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	})
	existing.SetName("test-xtrinode" + config.NodePoolNameSuffix)
	existing.SetNamespace("default")
	_ = unstructured.SetNestedField(existing.Object, int64(3), "spec", "replicas")

	scheme := newTestSchemeAnalyticsOnly()
	cli := newTestClient(scheme)
	ctx := context.Background()
	err := cli.Create(ctx, existing)
	require.NoError(t, err)

	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				MinNodes: int32Ptr(1),
				MaxNodes: int32Ptr(10),
				// AutoscalerEnabled defaults to true (nil)
			},
		},
	}

	// Scale to 5 — with autoscaler enabled, replicas should NOT change
	err = adapter.ScaleNodePoolMinNodes(ctx, xtrinode, 5)
	require.NoError(t, err)

	// Verify
	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	})
	err = cli.Get(ctx, types.NamespacedName{
		Name:      "test-xtrinode" + config.NodePoolNameSuffix,
		Namespace: "default",
	}, updated)
	require.NoError(t, err)

	// Annotations should be updated
	annotations := updated.GetAnnotations()
	assert.Equal(t, "5", annotations[config.NodePoolAutoscalerMinSizeAnnotation])
	assert.Equal(t, "10", annotations[config.NodePoolAutoscalerMaxSizeAnnotation])

	// Replicas should NOT be changed when autoscaler is enabled
	replicas, found, _ := unstructured.NestedInt64(updated.Object, "spec", "replicas")
	assert.True(t, found)
	assert.Equal(t, int64(3), replicas, "replicas should not change when autoscaler is enabled")
}

func TestScaleNodePoolMinNodes_ResourceNotFound(t *testing.T) {
	scheme := newTestSchemeAnalyticsOnly()
	cli := newTestClient(scheme)
	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
			},
		},
	}

	// Should return nil (not error) when resource doesn't exist
	err := adapter.ScaleNodePoolMinNodes(context.Background(), xtrinode, 5)
	assert.NoError(t, err)
}

func TestScaleNodePoolMinNodes_NoNodePool(t *testing.T) {
	scheme := newTestSchemeAnalyticsOnly()
	cli := newTestClient(scheme)
	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{},
	}

	err := adapter.ScaleNodePoolMinNodes(context.Background(), xtrinode, 5)
	assert.NoError(t, err)
}

// =============================================================================
// Regression: AWS infra template version consistency
// =============================================================================

func TestGetInfrastructureAPIVersionShort(t *testing.T) {
	tests := []struct {
		provider string
		expected string
	}{
		{"aws", "v1beta2"},
		{"azure", "v1beta1"},
		{"gcp", "v1beta1"},
		{"unknown", "v1beta1"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			assert.Equal(t, tt.expected, getInfrastructureAPIVersionShort(tt.provider))
		})
	}
}

func TestAWSInfraTemplateVersionMatchesInfraRef(t *testing.T) {
	// The template GVK version should match what getInfrastructureAPIVersion returns
	templateGVK := getInfrastructureTemplateGVK("aws", false)
	infraAPIVersion := getInfrastructureAPIVersion("aws")

	expectedFull := fmt.Sprintf("infrastructure.cluster.x-k8s.io/%s", templateGVK.Version)
	assert.Equal(t, expectedFull, infraAPIVersion,
		"AWS infra template version must match infrastructureRef apiVersion")
}

func TestAzureInfraTemplateVersionMatchesInfraRef(t *testing.T) {
	templateGVK := getInfrastructureTemplateGVK("azure", false)
	infraAPIVersion := getInfrastructureAPIVersion("azure")

	expectedFull := fmt.Sprintf("infrastructure.cluster.x-k8s.io/%s", templateGVK.Version)
	assert.Equal(t, expectedFull, infraAPIVersion)
}

func TestGCPInfraTemplateVersionMatchesInfraRef(t *testing.T) {
	templateGVK := getInfrastructureTemplateGVK("gcp", false)
	infraAPIVersion := getInfrastructureAPIVersion("gcp")

	expectedFull := fmt.Sprintf("infrastructure.cluster.x-k8s.io/%s", templateGVK.Version)
	assert.Equal(t, expectedFull, infraAPIVersion)
}

// =============================================================================
// Regression: Managed pools should only set replicas on creation
// =============================================================================

func TestManagedAzure_DoesNotOverwriteReplicasOnUpdate(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider:          "azure",
				ProviderMode:      "managed",
				KubernetesVersion: "v1.28.0",
				ClusterName:       "my-cluster",
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	// First call — should create and set replicas
	err := adapter.ensureAzureManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	// Verify replicas were set on creation
	mp := &unstructured.Unstructured{}
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})
	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)

	replicas, found, _ := unstructured.NestedInt64(mp.Object, "spec", "replicas")
	assert.True(t, found)
	assert.Equal(t, int64(3), replicas, "replicas should be set to minNodes on first creation")

	// Simulate autoscaler changing replicas to 8
	_ = unstructured.SetNestedField(mp.Object, int64(8), "spec", "replicas")
	err = cli.Update(ctx, mp)
	require.NoError(t, err)

	// Second call — should NOT overwrite replicas
	err = adapter.ensureAzureManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	// Re-read
	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)

	replicas, _, _ = unstructured.NestedInt64(mp.Object, "spec", "replicas")
	assert.Equal(t, int64(8), replicas, "replicas should NOT be overwritten on update (autoscaler owns them)")
}

func TestManagedAWS_DoesNotOverwriteReplicasOnUpdate(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider:          "aws",
				ProviderMode:      "managed",
				KubernetesVersion: "v1.28.0",
				ClusterName:       "my-cluster",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	// First call
	err := adapter.ensureAWSManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	// Simulate autoscaler scaling to 8
	mp := &unstructured.Unstructured{}
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})
	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)
	_ = unstructured.SetNestedField(mp.Object, int64(8), "spec", "replicas")
	err = cli.Update(ctx, mp)
	require.NoError(t, err)

	// Second call — should NOT overwrite replicas
	err = adapter.ensureAWSManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)
	replicas, _, _ := unstructured.NestedInt64(mp.Object, "spec", "replicas")
	assert.Equal(t, int64(8), replicas, "replicas should NOT be overwritten on update")
}

func TestManagedGCP_DoesNotOverwriteReplicasOnUpdate(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider:          "gcp",
				ProviderMode:      "managed",
				KubernetesVersion: "v1.28.0",
				ClusterName:       "my-cluster",
				GCP: &analyticsv1.GCPNodePoolSpec{
					MachineType: "n1-standard-4",
				},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	// First call
	err := adapter.ensureGCPManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	// Simulate autoscaler scaling to 8
	mp := &unstructured.Unstructured{}
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})
	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)
	_ = unstructured.SetNestedField(mp.Object, int64(8), "spec", "replicas")
	err = cli.Update(ctx, mp)
	require.NoError(t, err)

	// Second call — should NOT overwrite
	err = adapter.ensureGCPManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)
	replicas, _, _ := unstructured.NestedInt64(mp.Object, "spec", "replicas")
	assert.Equal(t, int64(8), replicas, "replicas should NOT be overwritten on update")
}

func TestSelfManagedAWS_DoesNotOverwriteReplicasOnUpdate(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider:          "aws",
				ProviderMode:      "self-managed",
				KubernetesVersion: "v1.28.0",
				ClusterName:       "my-cluster",
				BootstrapConfigRef: &corev1.ObjectReference{
					APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
					Kind:       "KubeadmConfigTemplate",
					Name:       "test-bootstrap",
				},
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	err := adapter.ensureAWSMachineDeployment(ctx, xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)
	md := &unstructured.Unstructured{}
	md.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachineDeployment",
	})
	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, md)
	require.NoError(t, err)

	replicas, found, _ := unstructured.NestedInt64(md.Object, "spec", "replicas")
	assert.True(t, found)
	assert.Equal(t, int64(3), replicas, "replicas should be set to minNodes on first creation")

	_ = unstructured.SetNestedField(md.Object, int64(8), "spec", "replicas")
	err = cli.Update(ctx, md)
	require.NoError(t, err)

	err = adapter.ensureAWSMachineDeployment(ctx, xtrinode)
	require.NoError(t, err)

	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, md)
	require.NoError(t, err)
	replicas, _, _ = unstructured.NestedInt64(md.Object, "spec", "replicas")
	assert.Equal(t, int64(8), replicas, "replicas should NOT be overwritten on update")
}

// =============================================================================
// Regression: Managed pools must use getClusterName, not xtrinode.Name
// =============================================================================

func TestManagedAzure_UsesClusterNameFromSpec(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "analytics-prod",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider:          "azure",
				ProviderMode:      "managed",
				KubernetesVersion: "v1.28.0",
				ClusterName:       "prod-aks-cluster",
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
				MinNodes: int32Ptr(1),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	err := adapter.ensureAzureManagedMachinePool(ctx, xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	mp := &unstructured.Unstructured{}
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})
	err = cli.Get(ctx, types.NamespacedName{Name: poolName, Namespace: "default"}, mp)
	require.NoError(t, err)

	clusterName, found, _ := unstructured.NestedString(mp.Object, "spec", "clusterName")
	assert.True(t, found)
	assert.Equal(t, "prod-aks-cluster", clusterName,
		"Managed pool must use spec.nodePool.clusterName, NOT xtrinode.Name")
}

// =============================================================================
// Regression: setXTrinodeOwnerReference must use correct APIVersion/Kind
// =============================================================================

func TestSetXTrinodeOwnerReference_CorrectAPIVersionKind(t *testing.T) {
	// Simulate what controller-runtime does: TypeMeta is empty after Get()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-xtrinode",
			UID:  types.UID("test-uid"),
		},
		// TypeMeta intentionally left empty (simulates controller-runtime Get behavior)
	}

	obj := &unstructured.Unstructured{}
	obj.Object = make(map[string]interface{})

	err := setXTrinodeOwnerReference(obj, xtrinode)
	require.NoError(t, err)

	ownerRefs := obj.GetOwnerReferences()
	require.Len(t, ownerRefs, 1)

	// Must use the analyticsv1.GroupVersion, not the empty xtrinode.APIVersion
	assert.Equal(t, analyticsv1.GroupVersion.String(), ownerRefs[0].APIVersion,
		"OwnerReference must use analyticsv1.GroupVersion, not empty xtrinode.APIVersion")
	assert.Equal(t, "XTrinode", ownerRefs[0].Kind,
		"OwnerReference must use 'XTrinode', not empty xtrinode.Kind")
	assert.Equal(t, "test-xtrinode", ownerRefs[0].Name)
	assert.Equal(t, types.UID("test-uid"), ownerRefs[0].UID)
}

// =============================================================================
// Regression: Azure self-managed must validate before creation
// =============================================================================

func TestAzureSelfManaged_RequiresKubernetesVersion(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider:     "azure",
				ProviderMode: "self-managed",
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
				// Missing: KubernetesVersion and BootstrapConfigRef
			},
		},
	}

	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	err := adapter.ensureAzureMachinePool(ctx, xtrinode)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubernetesVersion",
		"Azure self-managed should require kubernetesVersion")
}

// =============================================================================
// Regression: setMachinePoolSpec must set version and bootstrap
// =============================================================================

func TestSetMachinePoolSpec_SetsKubernetesVersion(t *testing.T) {
	mp := &unstructured.Unstructured{}
	mp.Object = make(map[string]interface{})
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})

	err := setMachinePoolSpec(mp, "my-cluster", "template-name", 3, "AzureMachinePool", true, "v1.28.0", nil)
	require.NoError(t, err)

	version, found, _ := unstructured.NestedString(mp.Object, "spec", "template", "spec", "version")
	assert.True(t, found, "version should be set in MachinePool spec")
	assert.Equal(t, "v1.28.0", version)
}

func TestSetMachinePoolSpec_SetsBootstrapConfigRef(t *testing.T) {
	mp := &unstructured.Unstructured{}
	mp.Object = make(map[string]interface{})
	mp.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})

	bootstrapRef := &corev1.ObjectReference{
		APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
		Kind:       "KubeadmConfigTemplate",
		Name:       "worker-bootstrap",
	}

	err := setMachinePoolSpec(mp, "my-cluster", "template-name", 3, "AzureMachinePool", true, "v1.28.0", bootstrapRef)
	require.NoError(t, err)

	ref, found, _ := unstructured.NestedMap(mp.Object, "spec", "template", "spec", "bootstrap", "configRef")
	assert.True(t, found, "bootstrap configRef should be set")
	assert.Equal(t, "bootstrap.cluster.x-k8s.io/v1beta1", ref["apiVersion"])
	assert.Equal(t, "KubeadmConfigTemplate", ref["kind"])
	assert.Equal(t, "worker-bootstrap", ref["name"])
}

func TestSetMachinePoolSpec_NoBootstrapWhenNil(t *testing.T) {
	mp := &unstructured.Unstructured{}
	mp.Object = make(map[string]interface{})

	err := setMachinePoolSpec(mp, "my-cluster", "template-name", 3, "AzureMachinePool", true, "v1.28.0", nil)
	require.NoError(t, err)

	_, found, _ := unstructured.NestedMap(mp.Object, "spec", "template", "spec", "bootstrap", "configRef")
	assert.False(t, found, "bootstrap configRef should not be set when nil")
}

func TestSetMachinePoolSpec_ReplicasOnlyOnCreate(t *testing.T) {
	// Test setOnCreate=true
	mp1 := &unstructured.Unstructured{}
	mp1.Object = make(map[string]interface{})
	err := setMachinePoolSpec(mp1, "cluster", "template", 3, "Kind", true, "", nil)
	require.NoError(t, err)

	replicas, found, _ := unstructured.NestedInt64(mp1.Object, "spec", "replicas")
	assert.True(t, found)
	assert.Equal(t, int64(3), replicas)

	// Test setOnCreate=false
	mp2 := &unstructured.Unstructured{}
	mp2.Object = make(map[string]interface{})
	err = setMachinePoolSpec(mp2, "cluster", "template", 3, "Kind", false, "", nil)
	require.NoError(t, err)

	_, found, _ = unstructured.NestedInt64(mp2.Object, "spec", "replicas")
	assert.False(t, found, "replicas should NOT be set when setOnCreate=false")
}

// =============================================================================
// Regression: isMachinePoolProvider must respect providerMode
// =============================================================================

func TestIsMachinePoolProvider(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected bool
	}{
		{
			name:     "Azure always MachinePool (self-managed)",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "azure", ProviderMode: "self-managed"},
			expected: true,
		},
		{
			name:     "Azure always MachinePool (managed)",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "azure", ProviderMode: "managed"},
			expected: true,
		},
		{
			name:     "Azure always MachinePool (empty mode)",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "azure"},
			expected: true,
		},
		{
			name:     "AWS self-managed uses MachineDeployment",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "aws", ProviderMode: "self-managed"},
			expected: false,
		},
		{
			name:     "AWS managed uses MachinePool",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "aws", ProviderMode: "managed"},
			expected: true,
		},
		{
			name:     "AWS empty mode uses MachineDeployment",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "aws"},
			expected: false,
		},
		{
			name:     "GCP self-managed uses MachineDeployment",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "gcp", ProviderMode: "self-managed"},
			expected: false,
		},
		{
			name:     "GCP managed uses MachinePool",
			nodePool: &analyticsv1.NodePoolSpec{Provider: "gcp", ProviderMode: "managed"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isMachinePoolProvider(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// DeleteNodePool tests
// =============================================================================

func TestDeleteNodePool_NoNodePool(t *testing.T) {
	adapter := NewNodePoolAdapter(newTestClient(newTestSchemeAnalyticsOnly()), newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		Spec: analyticsv1.XTrinodeSpec{},
	}

	err := adapter.DeleteNodePool(context.Background(), xtrinode)
	assert.NoError(t, err)
}

func TestDeleteNodePool_UnsupportedProvider(t *testing.T) {
	adapter := NewNodePoolAdapter(newTestClient(newTestSchemeAnalyticsOnly()), newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "unsupported",
			},
		},
	}

	err := adapter.DeleteNodePool(context.Background(), xtrinode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider")
}

func TestDeleteNodePool_Azure(t *testing.T) {
	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())
	ctx := context.Background()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "azure",
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
			},
		},
	}

	// Should not error even if resources don't exist (idempotent delete)
	err := adapter.DeleteNodePool(ctx, xtrinode)
	assert.NoError(t, err)
}

func TestDeleteNodePool_AWS(t *testing.T) {
	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
			},
		},
	}

	err := adapter.DeleteNodePool(context.Background(), xtrinode)
	assert.NoError(t, err)
}

func TestDeleteNodePool_GCP(t *testing.T) {
	cli := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "gcp",
				GCP: &analyticsv1.GCPNodePoolSpec{
					MachineType: "n1-standard-4",
				},
			},
		},
	}

	err := adapter.DeleteNodePool(context.Background(), xtrinode)
	assert.NoError(t, err)
}

// =============================================================================
// Validation regression tests
// =============================================================================

func TestValidateNodePoolForCreation_SelfManaged_RequiresBootstrap(t *testing.T) {
	nodePool := &analyticsv1.NodePoolSpec{
		Provider:          "aws",
		ProviderMode:      "self-managed",
		KubernetesVersion: "v1.28.0",
		AWS: &analyticsv1.AWSNodePoolSpec{
			InstanceType: "m5.xlarge",
		},
		// Missing BootstrapConfigRef
	}

	err := validateNodePoolForCreation(nodePool, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bootstrapConfigRef")
}

func TestValidateNodePoolForCreation_SelfManaged_RejectsNodeTaints(t *testing.T) {
	nodePool := &analyticsv1.NodePoolSpec{
		Provider:          "aws",
		ProviderMode:      "self-managed",
		KubernetesVersion: "v1.28.0",
		AWS: &analyticsv1.AWSNodePoolSpec{
			InstanceType: "m5.xlarge",
		},
		BootstrapConfigRef: &corev1.ObjectReference{
			APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
			Kind:       "KubeadmConfigTemplate",
			Name:       "worker",
		},
		NodeTaints: []corev1.Taint{
			{Key: "dedicated", Value: "trino", Effect: corev1.TaintEffectNoSchedule},
		},
	}

	err := validateNodePoolForCreation(nodePool, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nodeRegistration.taints")
}

func TestValidateNodePoolForCreation_Managed_RejectsBootstrap(t *testing.T) {
	nodePool := &analyticsv1.NodePoolSpec{
		Provider:          "aws",
		ProviderMode:      "managed",
		KubernetesVersion: "v1.28.0",
		AWS: &analyticsv1.AWSNodePoolSpec{
			InstanceType: "m5.xlarge",
		},
		BootstrapConfigRef: &corev1.ObjectReference{
			APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
			Kind:       "KubeadmConfigTemplate",
			Name:       "worker",
		},
	}

	err := validateNodePoolForCreation(nodePool, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "should not be set for managed")
}

func TestValidateNodePoolForCreation_ExistingResource_Skips(t *testing.T) {
	nodePool := &analyticsv1.NodePoolSpec{
		Provider:     "aws",
		ProviderMode: "self-managed",
		// Missing required fields, but resource already exists
		AWS: &analyticsv1.AWSNodePoolSpec{
			InstanceType: "m5.xlarge",
		},
	}

	err := validateNodePoolForCreation(nodePool, true)
	assert.NoError(t, err, "should skip validation when resource already exists")
}

func TestIsAutoscalerEnabled(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected bool
	}{
		{
			name:     "nil defaults to true",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: true,
		},
		{
			name: "explicitly true",
			nodePool: &analyticsv1.NodePoolSpec{
				AutoscalerEnabled: func() *bool { b := true; return &b }(),
			},
			expected: true,
		},
		{
			name: "explicitly false",
			nodePool: &analyticsv1.NodePoolSpec{
				AutoscalerEnabled: func() *bool { b := false; return &b }(),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isAutoscalerEnabled(tt.nodePool))
		})
	}
}

// =============================================================================
// ScaleNodePoolMinNodes with MachinePool (Azure / managed AWS/GCP)
// =============================================================================

func TestScaleNodePoolMinNodes_MachinePool(t *testing.T) {
	// Create a pre-existing MachinePool (Azure uses MachinePool)
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	existing.SetName("test-xtrinode" + config.NodePoolNameSuffix)
	existing.SetNamespace("default")
	_ = unstructured.SetNestedField(existing.Object, int64(1), "spec", "replicas")

	scheme := newTestSchemeAnalyticsOnly()
	cli := newTestClient(scheme)
	ctx := context.Background()
	err := cli.Create(ctx, existing)
	require.NoError(t, err)

	adapter := NewNodePoolAdapter(cli, newTestLogger())

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "azure",
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
				MinNodes:          int32Ptr(1),
				MaxNodes:          int32Ptr(10),
				AutoscalerEnabled: func() *bool { b := false; return &b }(),
			},
		},
	}

	err = adapter.ScaleNodePoolMinNodes(ctx, xtrinode, 5)
	require.NoError(t, err)

	// Verify
	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "MachinePool",
	})
	err = cli.Get(ctx, types.NamespacedName{
		Name: "test-xtrinode" + config.NodePoolNameSuffix, Namespace: "default",
	}, updated)
	require.NoError(t, err)

	annotations := updated.GetAnnotations()
	assert.Equal(t, "5", annotations[config.NodePoolAutoscalerMinSizeAnnotation])

	replicas, _, _ := unstructured.NestedInt64(updated.Object, "spec", "replicas")
	assert.Equal(t, int64(5), replicas)
}
