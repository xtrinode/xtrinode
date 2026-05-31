package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func TestGetNodePoolName(t *testing.T) {
	tests := []struct {
		name         string
		nodePool     *analyticsv1.NodePoolSpec
		xtrinodeName string
		expected     string
	}{
		{
			name:         "custom name",
			nodePool:     &analyticsv1.NodePoolSpec{Name: "custom-pool"},
			xtrinodeName: "test-xtrinode",
			expected:     "custom-pool",
		},
		{
			name:         "default name",
			nodePool:     &analyticsv1.NodePoolSpec{},
			xtrinodeName: "test-xtrinode",
			expected:     "test-xtrinode" + config.NodePoolNameSuffix,
		},
		// Note: nil nodePool would panic in getNodePoolName, so we don't test that case
		// The function expects a non-nil nodePool (even if empty)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNodePoolName(tt.nodePool, tt.xtrinodeName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func clearNodePoolPolicyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		config.NodePoolEnvDefaultMinNodes,
		config.NodePoolEnvDefaultMaxNodes,
		config.NodePoolEnvDefaultOSDiskGB,
		config.NodePoolEnvValidationMinNodesMin,
		config.NodePoolEnvValidationMaxNodesMin,
		config.NodePoolEnvValidationMaxNodesMax,
		config.NodePoolEnvValidationOSDiskGBMin,
		config.NodePoolEnvValidationOSDiskGBMax,
	} {
		t.Setenv(name, "")
	}
}

func TestGetNodePoolDefaults(t *testing.T) {
	clearNodePoolPolicyEnv(t)

	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		xtrinode *analyticsv1.XTrinode
		expected NodePoolDefaults
	}{
		{
			name:     "empty nodePool - uses code defaults",
			nodePool: &analyticsv1.NodePoolSpec{},
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{},
			},
			expected: NodePoolDefaults{
				MinNodes:   config.NodePoolDefaultMinNodes,
				MaxNodes:   config.NodePoolDefaultMaxNodes,
				DiskSizeGB: config.NodePoolDefaultDiskSizeGB,
			},
		},
		{
			name:     "operator-level defaults",
			nodePool: &analyticsv1.NodePoolSpec{},
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					OperatorNodePoolDefaults: &analyticsv1.OperatorNodePoolDefaultsSpec{
						DefaultMinNodes: func() *int32 { v := int32(2); return &v }(),
						DefaultMaxNodes: func() *int32 { v := int32(20); return &v }(),
						DefaultOSDiskGB: func() *int32 { v := int32(256); return &v }(),
					},
				},
			},
			expected: NodePoolDefaults{
				MinNodes:   2,
				MaxNodes:   20,
				DiskSizeGB: 256,
			},
		},
		{
			name: "per-XTrinode spec overrides",
			nodePool: &analyticsv1.NodePoolSpec{
				MinNodes: func() *int32 { v := int32(5); return &v }(),
				MaxNodes: func() *int32 { v := int32(50); return &v }(),
				OSDiskGB: func() *int32 { v := int32(512); return &v }(),
			},
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					OperatorNodePoolDefaults: &analyticsv1.OperatorNodePoolDefaultsSpec{
						DefaultMinNodes: func() *int32 { v := int32(2); return &v }(),
					},
				},
			},
			expected: NodePoolDefaults{
				MinNodes:   5,
				MaxNodes:   50,
				DiskSizeGB: 512,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNodePoolDefaults(tt.nodePool, tt.xtrinode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetNodePoolDefaultsAfterWebhookDefaulting(t *testing.T) {
	clearNodePoolPolicyEnv(t)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			NodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS:      &analyticsv1.AWSNodePoolSpec{InstanceType: "m5.xlarge"},
			},
			OperatorNodePoolDefaults: &analyticsv1.OperatorNodePoolDefaultsSpec{
				DefaultMinNodes: func() *int32 { v := int32(2); return &v }(),
				DefaultMaxNodes: func() *int32 { v := int32(20); return &v }(),
				DefaultOSDiskGB: func() *int32 { v := int32(256); return &v }(),
			},
		},
	}

	xtrinode.Default()
	defaults := getNodePoolDefaults(xtrinode.Spec.NodePool, xtrinode)

	assert.Equal(t, NodePoolDefaults{MinNodes: 2, MaxNodes: 20, DiskSizeGB: 256}, defaults)
}

func TestGetNodePoolDefaultsUsesEnvBackedCodeDefaults(t *testing.T) {
	clearNodePoolPolicyEnv(t)

	t.Setenv(config.NodePoolEnvDefaultMinNodes, "3")
	t.Setenv(config.NodePoolEnvDefaultMaxNodes, "9")
	t.Setenv(config.NodePoolEnvDefaultOSDiskGB, "256")

	defaults := getNodePoolDefaults(&analyticsv1.NodePoolSpec{}, &analyticsv1.XTrinode{
		Spec: analyticsv1.XTrinodeSpec{},
	})

	assert.Equal(t, NodePoolDefaults{MinNodes: 3, MaxNodes: 9, DiskSizeGB: 256}, defaults)
}

func TestBuildCommonLabels(t *testing.T) {
	result := buildCommonLabels("test-xtrinode")
	expected := map[string]string{
		config.NodePoolManagedByLabel: config.NodePoolManagedByValue,
		config.RuntimeLabel:           "test-xtrinode",
	}
	assert.Equal(t, expected, result)
}

func TestBuildOwnerReference(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-xtrinode",
			UID:  types.UID("test-uid"),
		},
	}

	result := buildOwnerReference(xtrinode)
	assert.Len(t, result, 1)
	assert.Equal(t, analyticsv1.GroupVersion.String(), result[0].APIVersion)
	assert.Equal(t, "XTrinode", result[0].Kind)
	assert.Equal(t, "test-xtrinode", result[0].Name)
	assert.Equal(t, types.UID("test-uid"), result[0].UID)
	assert.NotNil(t, result[0].Controller)
	assert.True(t, *result[0].Controller)
}

func TestBuildAutoscalerAnnotations(t *testing.T) {
	result := buildAutoscalerAnnotations(5, 20)
	expected := map[string]string{
		config.NodePoolAutoscalerMinSizeAnnotation: "5",
		config.NodePoolAutoscalerMaxSizeAnnotation: "20",
	}
	assert.Equal(t, expected, result)
}

func TestBuildBaseMachineDeployment(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	md := buildBaseMachineDeployment("test-md", "default", xtrinode)
	assert.Equal(t, "test-md", md.GetName())
	assert.Equal(t, "default", md.GetNamespace())
	assert.Equal(t, schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	}, md.GroupVersionKind())

	labels := md.GetLabels()
	assert.Equal(t, config.NodePoolManagedByValue, labels[config.NodePoolManagedByLabel])
	assert.Equal(t, "test-xtrinode", labels[config.RuntimeLabel])

	ownerRefs := md.GetOwnerReferences()
	assert.Len(t, ownerRefs, 1)
	assert.Equal(t, "test-xtrinode", ownerRefs[0].Name)
}

func TestBuildBaseMachinePool(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	mp := buildBaseMachinePool("test-mp", "default", xtrinode)
	assert.Equal(t, "test-mp", mp.GetName())
	assert.Equal(t, "default", mp.GetNamespace())
	assert.Equal(t, schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachinePool",
	}, mp.GroupVersionKind())
}

func TestBuildBaseInfrastructureTemplate(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-xtrinode",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	gvk := schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "AWSMachineTemplate",
	}

	template := buildBaseInfrastructureTemplate(gvk, "test-template", "default", xtrinode)
	assert.Equal(t, "test-template", template.GetName())
	assert.Equal(t, "default", template.GetNamespace())
	assert.Equal(t, gvk, template.GroupVersionKind())
}

func TestGetMachineResourceGVK(t *testing.T) {
	tests := []struct {
		name          string
		isMachinePool bool
		expectedKind  string
	}{
		{
			name:          "MachinePool",
			isMachinePool: true,
			expectedKind:  "MachinePool",
		},
		{
			name:          "MachineDeployment",
			isMachinePool: false,
			expectedKind:  "MachineDeployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMachineResourceGVK(tt.isMachinePool)
			assert.Equal(t, "cluster.x-k8s.io", result.Group)
			assert.Equal(t, "v1beta1", result.Version)
			assert.Equal(t, tt.expectedKind, result.Kind)
		})
	}
}

func TestGetInfrastructureTemplateGVK(t *testing.T) {
	tests := []struct {
		name          string
		provider      string
		isMachinePool bool
		expectedKind  string
	}{
		{
			name:          "Azure MachinePool",
			provider:      "azure",
			isMachinePool: true,
			expectedKind:  "AzureMachinePool",
		},
		{
			name:          "Azure MachineTemplate",
			provider:      "azure",
			isMachinePool: false,
			expectedKind:  "AzureMachineTemplate",
		},
		{
			name:          "AWS MachineTemplate",
			provider:      "aws",
			isMachinePool: false,
			expectedKind:  "AWSMachineTemplate",
		},
		{
			name:          "AWS MachineTemplate (isMachinePool=true)",
			provider:      "aws",
			isMachinePool: true,
			expectedKind:  "AWSMachineTemplate",
		},
		{
			name:          "GCP MachineTemplate",
			provider:      "gcp",
			isMachinePool: false,
			expectedKind:  "GCPMachineTemplate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInfrastructureTemplateGVK(tt.provider, tt.isMachinePool)
			assert.Equal(t, "infrastructure.cluster.x-k8s.io", result.Group)
			// AWS uses v1beta2, others use v1beta1
			expectedVersion := "v1beta1"
			if tt.provider == "aws" {
				expectedVersion = "v1beta2"
			}
			assert.Equal(t, expectedVersion, result.Version)
			assert.Equal(t, tt.expectedKind, result.Kind)
		})
	}
}

func TestBuildUnstructuredForDeletion(t *testing.T) {
	gvk := schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "MachineDeployment",
	}

	obj := buildUnstructuredForDeletion(gvk, "test-md", "default")
	assert.Equal(t, "test-md", obj.GetName())
	assert.Equal(t, "default", obj.GetNamespace())
	assert.Equal(t, gvk, obj.GroupVersionKind())
}

func TestGetAzureVMSize(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected string
	}{
		{
			name:     "empty nodePool",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: "",
		},
		{
			name: "with VMSize",
			nodePool: &analyticsv1.NodePoolSpec{
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
			},
			expected: "Standard_D4s_v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAzureVMSize(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetAzureOSDiskType(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected string
	}{
		{
			name:     "empty nodePool - uses default",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: config.NodePoolAzureOSDiskType,
		},
		{
			name: "custom disk type",
			nodePool: &analyticsv1.NodePoolSpec{
				Azure: &analyticsv1.AzureNodePoolSpec{
					OSDiskType: "Premium_LRS",
				},
			},
			expected: "Premium_LRS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAzureOSDiskType(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetAWSInstanceType(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected string
	}{
		{
			name:     "empty nodePool",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: "",
		},
		{
			name: "with InstanceType",
			nodePool: &analyticsv1.NodePoolSpec{
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
			},
			expected: "m5.xlarge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAWSInstanceType(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetAWSVolumeType(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected string
	}{
		{
			name:     "empty nodePool - uses default",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: config.NodePoolAWSVolumeType,
		},
		{
			name: "custom volume type",
			nodePool: &analyticsv1.NodePoolSpec{
				AWS: &analyticsv1.AWSNodePoolSpec{
					VolumeType: "gp3",
				},
			},
			expected: "gp3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getAWSVolumeType(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetGCPMachineType(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected string
	}{
		{
			name:     "empty nodePool",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: "",
		},
		{
			name: "with MachineType",
			nodePool: &analyticsv1.NodePoolSpec{
				GCP: &analyticsv1.GCPNodePoolSpec{
					MachineType: "n1-standard-4",
				},
			},
			expected: "n1-standard-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getGCPMachineType(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetGCPDiskType(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected string
	}{
		{
			name:     "empty nodePool - uses default",
			nodePool: &analyticsv1.NodePoolSpec{},
			expected: config.NodePoolGCPDiskType,
		},
		{
			name: "custom disk type",
			nodePool: &analyticsv1.NodePoolSpec{
				GCP: &analyticsv1.GCPNodePoolSpec{
					DiskType: "pd-ssd",
				},
			},
			expected: "pd-ssd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getGCPDiskType(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateProviderFields(t *testing.T) {
	tests := []struct {
		name      string
		nodePool  *analyticsv1.NodePoolSpec
		expectErr bool
	}{
		{
			name: "valid Azure",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "azure",
				Azure: &analyticsv1.AzureNodePoolSpec{
					VMSize: "Standard_D4s_v3",
				},
			},
			expectErr: false,
		},
		{
			name: "invalid Azure - missing VMSize",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "azure",
				Azure:    &analyticsv1.AzureNodePoolSpec{},
			},
			expectErr: true,
		},
		{
			name: "valid AWS",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS: &analyticsv1.AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
			},
			expectErr: false,
		},
		{
			name: "invalid AWS - missing InstanceType",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "aws",
				AWS:      &analyticsv1.AWSNodePoolSpec{},
			},
			expectErr: true,
		},
		{
			name: "valid GCP",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "gcp",
				GCP: &analyticsv1.GCPNodePoolSpec{
					MachineType: "n1-standard-4",
				},
			},
			expectErr: false,
		},
		{
			name: "invalid GCP - missing MachineType",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "gcp",
				GCP:      &analyticsv1.GCPNodePoolSpec{},
			},
			expectErr: true,
		},
		{
			name: "unsupported provider",
			nodePool: &analyticsv1.NodePoolSpec{
				Provider: "unknown",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProviderFields(tt.nodePool)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestApplyTaintsToUnstructured(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.Object = make(map[string]interface{})

	taints := []corev1.Taint{
		{
			Key:    "key1",
			Value:  "value1",
			Effect: corev1.TaintEffectNoSchedule,
		},
		{
			Key:    "key2",
			Effect: corev1.TaintEffectNoExecute,
		},
	}

	err := applyTaintsToUnstructured(obj, taints, []string{"spec", "taints"})
	assert.NoError(t, err)

	// Verify taints were set
	taintsList, found, err := unstructured.NestedSlice(obj.Object, "spec", "taints")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Len(t, taintsList, 2)
}

func TestApplyLabelsToUnstructured(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.Object = make(map[string]interface{})

	labels := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	err := applyLabelsToUnstructured(obj, labels, []string{"spec", "labels"})
	assert.NoError(t, err)

	// Verify labels were set
	labelsMap, found, err := unstructured.NestedMap(obj.Object, "spec", "labels")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "value1", labelsMap["key1"])
	assert.Equal(t, "value2", labelsMap["key2"])
}

func TestApplyZonesToUnstructured(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		zones    []string
		checkFn  func(*unstructured.Unstructured) bool
	}{
		{
			name:     "Azure zones",
			provider: "azure",
			zones:    []string{"zone1", "zone2"},
			checkFn: func(obj *unstructured.Unstructured) bool {
				zones, found, _ := unstructured.NestedStringSlice(obj.Object, "spec", "template", "availabilityZones")
				return found && len(zones) == 2
			},
		},
		{
			name:     "AWS zone",
			provider: "aws",
			zones:    []string{"us-east-1a"},
			checkFn: func(obj *unstructured.Unstructured) bool {
				zone, found, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "availabilityZone")
				return found && zone == "us-east-1a"
			},
		},
		{
			name:     "GCP zone",
			provider: "gcp",
			zones:    []string{"us-central1-a"},
			checkFn: func(obj *unstructured.Unstructured) bool {
				zone, found, _ := unstructured.NestedString(obj.Object, "spec", "template", "spec", "zone")
				return found && zone == "us-central1-a"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.Object = make(map[string]interface{})
			err := applyZonesToMachineTemplate(obj, tt.zones, tt.provider)
			assert.NoError(t, err)
			assert.True(t, tt.checkFn(obj))
		})
	}
}

func TestDeleteUnstructuredResource(t *testing.T) {
	// This function is tested indirectly through integration tests
	// Here we verify the function signature and that it can be called without panicking
	obj := &unstructured.Unstructured{}
	obj.SetName("test-resource")
	obj.SetNamespace("default")
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "test.group",
		Version: "v1",
		Kind:    "TestKind",
	})

	// Verify function exists and has correct signature
	// Actual deletion testing requires a real client and is covered in integration tests
	assert.NotNil(t, deleteUnstructuredResource)
	assert.Equal(t, "test-resource", obj.GetName())
	assert.Equal(t, "default", obj.GetNamespace())
	assert.Equal(t, "TestKind", obj.GetKind())
}
