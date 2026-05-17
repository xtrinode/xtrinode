package controllers

import (
	"context"
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

func TestEnsureAzureManagedMachinePool(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "analytics.xtrinode.io/v1",
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
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
				NodeLabels: map[string]string{
					"workload": "trino",
					"tier":     "compute",
				},
				NodeTaints: []corev1.Taint{
					{
						Key:    "dedicated",
						Value:  "trino",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Zones: []string{"1", "2", "3"},
				ResourceTags: map[string]string{
					"Environment": "production",
				},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	client := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(client, newTestLogger())

	err := adapter.ensureAzureManagedMachinePool(context.Background(), xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	// Check AzureManagedMachinePool (provider infra CRD)
	infraPool := &unstructured.Unstructured{}
	infraPool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "AzureManagedMachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      poolName,
		Namespace: xtrinode.Namespace,
	}, infraPool)
	require.NoError(t, err)
	assertNonControllerXTrinodeOwner(t, infraPool)

	// Verify infra pool fields
	sku, _, _ := unstructured.NestedString(infraPool.Object, "spec", "sku")
	assert.Equal(t, "Standard_D4s_v3", sku)

	nodeLabels, _, _ := unstructured.NestedStringMap(infraPool.Object, "spec", "nodeLabels")
	assert.Equal(t, "trino", nodeLabels["workload"])

	taints, _, _ := unstructured.NestedSlice(infraPool.Object, "spec", "taints")
	require.Len(t, taints, 1)
	assert.Equal(t, map[string]interface{}{
		"key":    "dedicated",
		"value":  "trino",
		"effect": "NoSchedule",
	}, taints[0])

	// Check MachinePool (CAPI core)
	machinePool := &unstructured.Unstructured{}
	machinePool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      poolName,
		Namespace: xtrinode.Namespace,
	}, machinePool)
	require.NoError(t, err)

	// Verify MachinePool has autoscaler annotations
	annotations := machinePool.GetAnnotations()
	assert.Equal(t, "3", annotations[config.NodePoolAutoscalerMinSizeAnnotation])
	assert.Equal(t, "10", annotations[config.NodePoolAutoscalerMaxSizeAnnotation])

	// Verify infrastructureRef points to AzureManagedMachinePool
	infraRef, _, _ := unstructured.NestedMap(machinePool.Object, "spec", "template", "spec", "infrastructureRef")
	assert.Equal(t, "AzureManagedMachinePool", infraRef["kind"])
	assert.Equal(t, poolName, infraRef["name"])
}

func TestEnsureAWSManagedMachinePool(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "analytics.xtrinode.io/v1",
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
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				NodeLabels: map[string]string{
					"workload": "trino",
				},
				NodeTaints: []corev1.Taint{
					{
						Key:    "dedicated",
						Value:  "trino",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	client := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(client, newTestLogger())

	err := adapter.ensureAWSManagedMachinePool(context.Background(), xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	// Check AWSManagedMachinePool
	infraPool := &unstructured.Unstructured{}
	infraPool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta2",
		Kind:    "AWSManagedMachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      poolName,
		Namespace: xtrinode.Namespace,
	}, infraPool)
	require.NoError(t, err)
	assertNonControllerXTrinodeOwner(t, infraPool)

	instanceType, _, _ := unstructured.NestedString(infraPool.Object, "spec", "instanceType")
	assert.Equal(t, "m5.xlarge", instanceType)

	taints, _, _ := unstructured.NestedSlice(infraPool.Object, "spec", "taints")
	require.Len(t, taints, 1)
	assert.Equal(t, map[string]interface{}{
		"key":    "dedicated",
		"value":  "trino",
		"effect": "NoSchedule",
	}, taints[0])

	// Check MachinePool
	machinePool := &unstructured.Unstructured{}
	machinePool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      poolName,
		Namespace: xtrinode.Namespace,
	}, machinePool)
	require.NoError(t, err)

	annotations := machinePool.GetAnnotations()
	assert.Equal(t, "3", annotations[config.NodePoolAutoscalerMinSizeAnnotation])
}

func TestEnsureGCPManagedMachinePool(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "analytics.xtrinode.io/v1",
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
				GCP: &analyticsv1.GCPNodePoolSpec{
					MachineType: "n1-standard-4",
					DiskType:    "pd-balanced",
				},
				OSDiskGB: int32Ptr(128),
				NodeLabels: map[string]string{
					"workload": "trino",
				},
				NodeTaints: []corev1.Taint{
					{
						Key:    "dedicated",
						Value:  "trino",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				ResourceTags: map[string]string{
					"environment": "test",
				},
				Zones:    []string{"us-central1-a", "us-central1-b"},
				MinNodes: int32Ptr(3),
				MaxNodes: int32Ptr(10),
			},
		},
	}

	client := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(client, newTestLogger())

	err := adapter.ensureGCPManagedMachinePool(context.Background(), xtrinode)
	require.NoError(t, err)

	poolName := getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name)

	// Check GCPManagedMachinePool
	infraPool := &unstructured.Unstructured{}
	infraPool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "GCPManagedMachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      poolName,
		Namespace: xtrinode.Namespace,
	}, infraPool)
	require.NoError(t, err)
	assertNonControllerXTrinodeOwner(t, infraPool)

	machineType, _, _ := unstructured.NestedString(infraPool.Object, "spec", "machineType")
	assert.Equal(t, "n1-standard-4", machineType)

	nodePoolName, _, _ := unstructured.NestedString(infraPool.Object, "spec", "nodePoolName")
	assert.Equal(t, poolName, nodePoolName)

	diskType, _, _ := unstructured.NestedString(infraPool.Object, "spec", "diskType")
	assert.Equal(t, "pd-balanced", diskType)

	diskSizeGB, _, _ := unstructured.NestedInt64(infraPool.Object, "spec", "diskSizeGB")
	assert.Equal(t, int64(128), diskSizeGB)

	kubernetesLabels, _, _ := unstructured.NestedStringMap(infraPool.Object, "spec", "kubernetesLabels")
	assert.Equal(t, "trino", kubernetesLabels["workload"])

	kubernetesTaints, _, _ := unstructured.NestedSlice(infraPool.Object, "spec", "kubernetesTaints")
	require.Len(t, kubernetesTaints, 1)
	assert.Equal(t, map[string]interface{}{
		"key":    "dedicated",
		"value":  "trino",
		"effect": "NoSchedule",
	}, kubernetesTaints[0])

	additionalLabels, _, _ := unstructured.NestedStringMap(infraPool.Object, "spec", "additionalLabels")
	assert.Equal(t, "test", additionalLabels["environment"])
	assert.Equal(t, "on-demand", additionalLabels["goog-gke-node-pool-provisioning-model"])

	// Check MachinePool
	machinePool := &unstructured.Unstructured{}
	machinePool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      poolName,
		Namespace: xtrinode.Namespace,
	}, machinePool)
	require.NoError(t, err)

	annotations := machinePool.GetAnnotations()
	assert.Equal(t, "3", annotations[config.NodePoolAutoscalerMinSizeAnnotation])

	_, found, _ := unstructured.NestedString(machinePool.Object, "spec", "template", "spec", "version")
	assert.False(t, found, "bare GKE versions should be omitted to avoid repeated GKE upgrade operations")
}

func TestEnsureGCPManagedMachinePool_RetainsExactGKEVersion(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "analytics.xtrinode.io/v1",
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
				KubernetesVersion: "v1.35.3-gke.1993000",
				GCP: &analyticsv1.GCPNodePoolSpec{
					MachineType: "n1-standard-4",
				},
			},
		},
	}

	client := newTestClient(newTestSchemeAnalyticsOnly())
	adapter := NewNodePoolAdapter(client, newTestLogger())

	err := adapter.ensureGCPManagedMachinePool(context.Background(), xtrinode)
	require.NoError(t, err)

	machinePool := &unstructured.Unstructured{}
	machinePool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	})
	err = client.Get(context.Background(), types.NamespacedName{
		Name:      getNodePoolName(xtrinode.Spec.NodePool, xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}, machinePool)
	require.NoError(t, err)

	version, found, _ := unstructured.NestedString(machinePool.Object, "spec", "template", "spec", "version")
	require.True(t, found)
	assert.Equal(t, "v1.35.3-gke.1993000", version)
}

func TestConvertTaintsToUnstructuredSlice(t *testing.T) {
	result := convertTaintsToUnstructuredSlice([]corev1.Taint{
		{
			Key:    "dedicated",
			Value:  "trino",
			Effect: corev1.TaintEffectNoSchedule,
		},
	})

	require.Len(t, result, 1)
	taint := result[0].(map[string]interface{})
	assert.Equal(t, "dedicated", taint["key"])
	assert.Equal(t, "trino", taint["value"])
	assert.Equal(t, "NoSchedule", taint["effect"])
}

func assertNonControllerXTrinodeOwner(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()

	ownerRefs := obj.GetOwnerReferences()
	require.Len(t, ownerRefs, 1)
	assert.Equal(t, "XTrinode", ownerRefs[0].Kind)
	assert.Nil(t, ownerRefs[0].Controller)
}
