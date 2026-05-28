package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func TestSelfManagedNodePoolProviderContracts(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		primaryGVK       schema.GroupVersionKind
		infraGVK         schema.GroupVersionKind
		infraKind        string
		infraAPIVersion  string
		failureDomain    string
		assertProviderCR func(t *testing.T, obj *unstructured.Unstructured)
	}{
		{
			name:            "azure machine pool",
			provider:        "azure",
			primaryGVK:      getMachineResourceGVK(true),
			infraGVK:        getInfrastructureTemplateGVK("azure", true),
			infraKind:       "AzureMachinePool",
			infraAPIVersion: config.NodePoolInfrastructureAPIVersion,
			failureDomain:   "1",
			assertProviderCR: func(t *testing.T, obj *unstructured.Unstructured) {
				t.Helper()
				vmSize, found, err := unstructured.NestedString(obj.Object, "spec", "template", "vmSize")
				require.NoError(t, err)
				assert.True(t, found)
				assert.Equal(t, "Standard_D4s_v3", vmSize)
			},
		},
		{
			name:            "aws machine deployment",
			provider:        "aws",
			primaryGVK:      getMachineResourceGVK(false),
			infraGVK:        getInfrastructureTemplateGVK("aws", false),
			infraKind:       "AWSMachineTemplate",
			infraAPIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			failureDomain:   "us-east-1a",
			assertProviderCR: func(t *testing.T, obj *unstructured.Unstructured) {
				t.Helper()
				instanceType, found, err := unstructured.NestedString(obj.Object, "spec", "template", "spec", "instanceType")
				require.NoError(t, err)
				assert.True(t, found)
				assert.Equal(t, "m5.xlarge", instanceType)
			},
		},
		{
			name:            "gcp machine deployment",
			provider:        "gcp",
			primaryGVK:      getMachineResourceGVK(false),
			infraGVK:        getInfrastructureTemplateGVK("gcp", false),
			infraKind:       "GCPMachineTemplate",
			infraAPIVersion: config.NodePoolInfrastructureAPIVersion,
			failureDomain:   "us-central1-a",
			assertProviderCR: func(t *testing.T, obj *unstructured.Unstructured) {
				t.Helper()
				machineType, found, err := unstructured.NestedString(obj.Object, "spec", "template", "spec", "machineType")
				require.NoError(t, err)
				assert.True(t, found)
				assert.Equal(t, "n1-standard-4", machineType)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			xtrinode := testNodePoolXTrinode(tt.provider, "self-managed")
			cli := newTestClient(newTestSchemeAnalyticsOnly())
			adapter := NewNodePoolAdapter(cli, newTestLogger())

			err := adapter.EnsureNodePool(ctx, xtrinode)
			require.NoError(t, err)

			poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)
			primary := testFetchUnstructured(t, cli, tt.primaryGVK, poolName, xtrinode.Namespace)
			assertXTrinodeNodePoolPrimaryContract(t, primary, xtrinode, tt.infraKind, tt.infraAPIVersion, tt.failureDomain)

			infra := testFetchUnstructured(t, cli, tt.infraGVK, poolName+config.NodePoolTemplateSuffix, xtrinode.Namespace)
			assertControllerXTrinodeOwner(t, infra)
			tt.assertProviderCR(t, infra)
		})
	}
}

func TestSelfManagedNodePoolProvidersDoNotOverwriteReplicasOnUpdate(t *testing.T) {
	tests := []struct {
		provider   string
		primaryGVK schema.GroupVersionKind
	}{
		{provider: "azure", primaryGVK: getMachineResourceGVK(true)},
		{provider: "aws", primaryGVK: getMachineResourceGVK(false)},
		{provider: "gcp", primaryGVK: getMachineResourceGVK(false)},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			ctx := context.Background()
			xtrinode := testNodePoolXTrinode(tt.provider, "self-managed")
			cli := newTestClient(newTestSchemeAnalyticsOnly())
			adapter := NewNodePoolAdapter(cli, newTestLogger())

			err := adapter.EnsureNodePool(ctx, xtrinode)
			require.NoError(t, err)

			poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)
			primary := testUnstructured(tt.primaryGVK, poolName, xtrinode.Namespace)
			replicaPatch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":8}}`))
			require.NoError(t, cli.Patch(ctx, primary, replicaPatch))

			xtrinode.Spec.NodePool.MinNodes = int32Ptr(4)
			xtrinode.Spec.NodePool.MaxNodes = int32Ptr(12)
			err = adapter.EnsureNodePool(ctx, xtrinode)
			require.NoError(t, err)

			updated := testFetchUnstructured(t, cli, tt.primaryGVK, poolName, xtrinode.Namespace)
			replicas, found, err := unstructured.NestedInt64(updated.Object, "spec", "replicas")
			require.NoError(t, err)
			assert.True(t, found)
			assert.Equal(t, int64(8), replicas, "replicas must remain owned by Cluster Autoscaler after creation")

			annotations := updated.GetAnnotations()
			assert.Equal(t, "4", annotations[config.NodePoolAutoscalerMinSizeAnnotation])
			assert.Equal(t, "12", annotations[config.NodePoolAutoscalerMaxSizeAnnotation])
		})
	}
}

func TestManagedNodePoolProvidersDoNotOverwriteReplicasOnAnnotationUpdate(t *testing.T) {
	for _, provider := range []string{"azure", "aws", "gcp"} {
		t.Run(provider, func(t *testing.T) {
			ctx := context.Background()
			xtrinode := testNodePoolXTrinode(provider, "managed")
			cli := newTestClient(newTestSchemeAnalyticsOnly())
			adapter := NewNodePoolAdapter(cli, newTestLogger())

			err := adapter.EnsureNodePool(ctx, xtrinode)
			require.NoError(t, err)

			poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)
			primary := testUnstructured(getMachineResourceGVK(true), poolName, xtrinode.Namespace)
			replicaPatch := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":8}}`))
			require.NoError(t, cli.Patch(ctx, primary, replicaPatch))

			xtrinode.Spec.NodePool.MinNodes = int32Ptr(4)
			xtrinode.Spec.NodePool.MaxNodes = int32Ptr(12)
			err = adapter.EnsureNodePool(ctx, xtrinode)
			require.NoError(t, err)

			updated := testFetchUnstructured(t, cli, getMachineResourceGVK(true), poolName, xtrinode.Namespace)
			replicas, found, err := unstructured.NestedInt64(updated.Object, "spec", "replicas")
			require.NoError(t, err)
			assert.True(t, found)
			assert.Equal(t, int64(8), replicas, "managed MachinePool replicas must remain externally owned after creation")

			annotations := updated.GetAnnotations()
			assert.Equal(t, "4", annotations[config.NodePoolAutoscalerMinSizeAnnotation])
			assert.Equal(t, "12", annotations[config.NodePoolAutoscalerMaxSizeAnnotation])
		})
	}
}

func TestDeleteNodePoolRemovesPrimaryAndInfrastructureResources(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		mode       string
		primaryGVK schema.GroupVersionKind
		infraGVK   schema.GroupVersionKind
		infraName  func(string) string
	}{
		{
			name:       "azure self-managed",
			provider:   "azure",
			mode:       "self-managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getInfrastructureTemplateGVK("azure", true),
			infraName:  func(poolName string) string { return poolName + config.NodePoolTemplateSuffix },
		},
		{
			name:       "aws self-managed",
			provider:   "aws",
			mode:       "self-managed",
			primaryGVK: getMachineResourceGVK(false),
			infraGVK:   getInfrastructureTemplateGVK("aws", false),
			infraName:  func(poolName string) string { return poolName + config.NodePoolTemplateSuffix },
		},
		{
			name:       "gcp self-managed",
			provider:   "gcp",
			mode:       "self-managed",
			primaryGVK: getMachineResourceGVK(false),
			infraGVK:   getInfrastructureTemplateGVK("gcp", false),
			infraName:  func(poolName string) string { return poolName + config.NodePoolTemplateSuffix },
		},
		{
			name:       "azure managed",
			provider:   "azure",
			mode:       "managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getManagedInfrastructureGVK("azure"),
			infraName:  func(poolName string) string { return poolName },
		},
		{
			name:       "aws managed",
			provider:   "aws",
			mode:       "managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getManagedInfrastructureGVK("aws"),
			infraName:  func(poolName string) string { return poolName },
		},
		{
			name:       "gcp managed",
			provider:   "gcp",
			mode:       "managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getManagedInfrastructureGVK("gcp"),
			infraName:  func(poolName string) string { return poolName },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			xtrinode := testNodePoolXTrinode(tt.provider, tt.mode)
			cli := newTestClient(newTestSchemeAnalyticsOnly())
			adapter := NewNodePoolAdapter(cli, newTestLogger())
			poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

			primary := testUnstructured(tt.primaryGVK, poolName, xtrinode.Namespace)
			infra := testUnstructured(tt.infraGVK, tt.infraName(poolName), xtrinode.Namespace)
			require.NoError(t, cli.Create(ctx, primary))
			require.NoError(t, cli.Create(ctx, infra))

			err := adapter.DeleteNodePool(ctx, xtrinode)
			require.NoError(t, err)

			assertUnstructuredNotFound(t, cli, tt.primaryGVK, poolName, xtrinode.Namespace)
			assertUnstructuredNotFound(t, cli, tt.infraGVK, tt.infraName(poolName), xtrinode.Namespace)
		})
	}
}

func TestRetainNodePoolRemovesXTrinodeOwnerReferences(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		mode       string
		primaryGVK schema.GroupVersionKind
		infraGVK   schema.GroupVersionKind
		infraName  func(string) string
	}{
		{
			name:       "azure self-managed",
			provider:   "azure",
			mode:       "self-managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getInfrastructureTemplateGVK("azure", true),
			infraName:  func(poolName string) string { return poolName + config.NodePoolTemplateSuffix },
		},
		{
			name:       "aws self-managed",
			provider:   "aws",
			mode:       "self-managed",
			primaryGVK: getMachineResourceGVK(false),
			infraGVK:   getInfrastructureTemplateGVK("aws", false),
			infraName:  func(poolName string) string { return poolName + config.NodePoolTemplateSuffix },
		},
		{
			name:       "gcp self-managed",
			provider:   "gcp",
			mode:       "self-managed",
			primaryGVK: getMachineResourceGVK(false),
			infraGVK:   getInfrastructureTemplateGVK("gcp", false),
			infraName:  func(poolName string) string { return poolName + config.NodePoolTemplateSuffix },
		},
		{
			name:       "azure managed",
			provider:   "azure",
			mode:       "managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getManagedInfrastructureGVK("azure"),
			infraName:  func(poolName string) string { return poolName },
		},
		{
			name:       "aws managed",
			provider:   "aws",
			mode:       "managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getManagedInfrastructureGVK("aws"),
			infraName:  func(poolName string) string { return poolName },
		},
		{
			name:       "gcp managed",
			provider:   "gcp",
			mode:       "managed",
			primaryGVK: getMachineResourceGVK(true),
			infraGVK:   getManagedInfrastructureGVK("gcp"),
			infraName:  func(poolName string) string { return poolName },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			xtrinode := testNodePoolXTrinode(tt.provider, tt.mode)
			cli := newTestClient(newTestSchemeAnalyticsOnly())
			adapter := NewNodePoolAdapter(cli, newTestLogger())
			poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)
			otherOwner := metav1.OwnerReference{
				APIVersion: "example.test/v1",
				Kind:       "ExternalOwner",
				Name:       "keep-me",
				UID:        types.UID("other-uid"),
			}

			primary := testUnstructured(tt.primaryGVK, poolName, xtrinode.Namespace)
			primary.SetOwnerReferences(append(buildOwnerReference(xtrinode), otherOwner))
			infra := testUnstructured(tt.infraGVK, tt.infraName(poolName), xtrinode.Namespace)
			infra.SetOwnerReferences(buildOwnerReference(xtrinode))
			require.NoError(t, cli.Create(ctx, primary))
			require.NoError(t, cli.Create(ctx, infra))

			err := adapter.RetainNodePool(ctx, xtrinode)
			require.NoError(t, err)

			retainedPrimary := testFetchUnstructured(t, cli, tt.primaryGVK, poolName, xtrinode.Namespace)
			assert.Equal(t, []metav1.OwnerReference{otherOwner}, retainedPrimary.GetOwnerReferences())

			retainedInfra := testFetchUnstructured(t, cli, tt.infraGVK, tt.infraName(poolName), xtrinode.Namespace)
			assert.Empty(t, retainedInfra.GetOwnerReferences())
		})
	}
}

func TestGetManagedInfrastructureGVK(t *testing.T) {
	tests := []struct {
		provider string
		version  string
		kind     string
	}{
		{provider: "azure", version: "v1beta1", kind: "AzureManagedMachinePool"},
		{provider: "aws", version: "v1beta2", kind: "AWSManagedMachinePool"},
		{provider: "gcp", version: "v1beta1", kind: "GCPManagedMachinePool"},
		{provider: "unknown", version: "v1beta1", kind: "ManagedMachinePool"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			gvk := getManagedInfrastructureGVK(tt.provider)

			assert.Equal(t, "infrastructure.cluster.x-k8s.io", gvk.Group)
			assert.Equal(t, tt.version, gvk.Version)
			assert.Equal(t, tt.kind, gvk.Kind)
		})
	}
}

func assertXTrinodeNodePoolPrimaryContract(
	t *testing.T,
	obj *unstructured.Unstructured,
	xtrinode *analyticsv1.XTrinode,
	infraKind string,
	infraAPIVersion string,
	failureDomain string,
) {
	t.Helper()

	replicas, found, err := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int64(3), replicas)

	annotations := obj.GetAnnotations()
	assert.Equal(t, "3", annotations[config.NodePoolAutoscalerMinSizeAnnotation])
	assert.Equal(t, "10", annotations[config.NodePoolAutoscalerMaxSizeAnnotation])

	clusterName, found, err := unstructured.NestedString(obj.Object, "spec", "clusterName")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "provider-cluster", clusterName)

	templateClusterName, found, err := unstructured.NestedString(obj.Object, "spec", "template", "spec", "clusterName")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "provider-cluster", templateClusterName)

	version, found, err := unstructured.NestedString(obj.Object, "spec", "template", "spec", "version")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v1.28.0", version)

	bootstrapRef, found, err := unstructured.NestedMap(obj.Object, "spec", "template", "spec", "bootstrap", "configRef")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "bootstrap.cluster.x-k8s.io/v1beta1", bootstrapRef["apiVersion"])
	assert.Equal(t, "KubeadmConfigTemplate", bootstrapRef["kind"])
	assert.Equal(t, "worker-bootstrap", bootstrapRef["name"])

	infraRef, found, err := unstructured.NestedMap(obj.Object, "spec", "template", "spec", "infrastructureRef")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, infraAPIVersion, infraRef["apiVersion"])
	assert.Equal(t, infraKind, infraRef["kind"])
	assert.Equal(t, getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)+config.NodePoolTemplateSuffix, infraRef["name"])

	actualFailureDomain, found, err := unstructured.NestedString(obj.Object, "spec", "template", "spec", "failureDomain")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, failureDomain, actualFailureDomain)
}

func testNodePoolXTrinode(provider, mode string) *analyticsv1.XTrinode {
	nodePool := &analyticsv1.NodePoolSpec{
		Provider:          provider,
		ProviderMode:      mode,
		KubernetesVersion: "v1.28.0",
		ClusterName:       "provider-cluster",
		BootstrapConfigRef: &corev1.ObjectReference{
			APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
			Kind:       "KubeadmConfigTemplate",
			Name:       "worker-bootstrap",
		},
		MinNodes: int32Ptr(3),
		MaxNodes: int32Ptr(10),
		OSDiskGB: int32Ptr(128),
		ResourceTags: map[string]string{
			"Environment": "test",
			"environment": "test",
		},
	}

	switch provider {
	case "azure":
		nodePool.Azure = &analyticsv1.AzureNodePoolSpec{
			VMSize: "Standard_D4s_v3",
		}
		nodePool.Zones = []string{"1", "2"}
	case "aws":
		nodePool.AWS = &analyticsv1.AWSNodePoolSpec{
			InstanceType: "m5.xlarge",
			VolumeType:   "gp3",
		}
		nodePool.Zones = []string{"us-east-1a", "us-east-1b"}
	case "gcp":
		nodePool.GCP = &analyticsv1.GCPNodePoolSpec{
			MachineType: "n1-standard-4",
			DiskType:    "pd-balanced",
		}
		nodePool.Zones = []string{"us-central1-a", "us-central1-b"}
	}

	if mode == "managed" {
		nodePool.BootstrapConfigRef = nil
	}

	return &analyticsv1.XTrinode{
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
			NodePool: nodePool,
		},
	}
}

func testFetchUnstructured(
	t *testing.T,
	cli client.Client,
	gvk schema.GroupVersionKind,
	name string,
	namespace string,
) *unstructured.Unstructured {
	t.Helper()

	obj := testUnstructured(gvk, name, namespace)
	err := cli.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
	require.NoError(t, err)
	return obj
}

func testUnstructured(gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	return obj
}

func assertUnstructuredNotFound(
	t *testing.T,
	cli client.Client,
	gvk schema.GroupVersionKind,
	name string,
	namespace string,
) {
	t.Helper()

	obj := testUnstructured(gvk, name, namespace)
	err := cli.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
	assert.True(t, k8serrors.IsNotFound(err), "expected %s %s to be deleted, got %v", gvk.Kind, name, err)
}

func assertControllerXTrinodeOwner(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()

	ownerRefs := obj.GetOwnerReferences()
	require.Len(t, ownerRefs, 1)
	assert.Equal(t, "XTrinode", ownerRefs[0].Kind)
	require.NotNil(t, ownerRefs[0].Controller)
	assert.True(t, *ownerRefs[0].Controller)
}
