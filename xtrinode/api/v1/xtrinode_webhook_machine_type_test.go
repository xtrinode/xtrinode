package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestXTrinode_Default_MachineTypeAutoPopulation tests that machine types are auto-populated from size presets
func TestXTrinode_Default_MachineTypeAutoPopulation(t *testing.T) {
	tests := []struct {
		name                 string
		xtrinode             *XTrinode
		expectedVMSize       string
		expectedInstanceType string
		expectedMachineType  string
	}{
		{
			name: "auto-populates Azure vmSize for size 's'",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
					},
				},
			},
			expectedVMSize: "Standard_D8as_v5",
		},
		{
			name: "auto-populates AWS instanceType for size 'm'",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "m",
					NodePool: &NodePoolSpec{
						Provider: "aws",
					},
				},
			},
			expectedInstanceType: "m5.4xlarge",
		},
		{
			name: "auto-populates GCP machineType for size 'l'",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "l",
					NodePool: &NodePoolSpec{
						Provider: "gcp",
					},
				},
			},
			expectedMachineType: "n1-standard-32",
		},
		{
			name: "does not override existing Azure vmSize",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
						Azure: &AzureNodePoolSpec{
							VMSize: "Standard_D16as_v5", // User override
						},
					},
				},
			},
			expectedVMSize: "Standard_D16as_v5", // Should keep user's choice
		},
		{
			name: "auto-populates for size 'xs'",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "xs",
					NodePool: &NodePoolSpec{
						Provider: "azure",
					},
				},
			},
			expectedVMSize: "Standard_D2as_v5",
		},
		{
			name: "auto-populates for size 'xl'",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "xl",
					NodePool: &NodePoolSpec{
						Provider: "aws",
					},
				},
			},
			expectedInstanceType: "m5.16xlarge",
		},
		{
			name: "auto-populates from typed worker resources",
			xtrinode: &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test-dummy"},
				Spec: XTrinodeSpec{
					Size: "s",
					Resources: &RuntimeResourcesSpec{
						Worker: &corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("32"),
								corev1.ResourceMemory: resource.MustParse("128Gi"),
							},
						},
					},
					NodePool: &NodePoolSpec{
						Provider: "gcp",
					},
				},
			},
			expectedMachineType: "n1-standard-32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.xtrinode.Default()

			if tt.expectedVMSize != "" {
				assert.NotNil(t, tt.xtrinode.Spec.NodePool.Azure)
				assert.Equal(t, tt.expectedVMSize, tt.xtrinode.Spec.NodePool.Azure.VMSize)
			}
			if tt.expectedInstanceType != "" {
				assert.NotNil(t, tt.xtrinode.Spec.NodePool.AWS)
				assert.Equal(t, tt.expectedInstanceType, tt.xtrinode.Spec.NodePool.AWS.InstanceType)
			}
			if tt.expectedMachineType != "" {
				assert.NotNil(t, tt.xtrinode.Spec.NodePool.GCP)
				assert.Equal(t, tt.expectedMachineType, tt.xtrinode.Spec.NodePool.GCP.MachineType)
			}
		})
	}
}

// TestValidateNodePool_WithRecommendations tests that validation errors include machine type recommendations
func TestValidateNodePool_WithRecommendations(t *testing.T) {
	tests := []struct {
		name             string
		xtrinode         *XTrinode
		wantErr          bool
		errorContains    string
		recommendedValue string
	}{
		{
			name: "Azure missing vmSize shows recommendation",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
					NodePool: &NodePoolSpec{
						Provider: "azure",
					},
				},
			},
			wantErr:          true,
			errorContains:    "recommended for size 's'",
			recommendedValue: "Standard_D8as_v5",
		},
		{
			name: "AWS missing instanceType shows recommendation",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "m",
					NodePool: &NodePoolSpec{
						Provider: "aws",
					},
				},
			},
			wantErr:          true,
			errorContains:    "recommended for size 'm'",
			recommendedValue: "m5.4xlarge",
		},
		{
			name: "GCP missing machineType shows recommendation",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "l",
					NodePool: &NodePoolSpec{
						Provider: "gcp",
					},
				},
			},
			wantErr:          true,
			errorContains:    "recommended for size 'l'",
			recommendedValue: "n1-standard-32",
		},
		{
			name: "GCP missing machineType recommends from typed worker resources",
			xtrinode: &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
					Resources: &RuntimeResourcesSpec{
						Worker: &corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("16"),
								corev1.ResourceMemory: resource.MustParse("64Gi"),
							},
						},
					},
					NodePool: &NodePoolSpec{
						Provider: "gcp",
					},
				},
			},
			wantErr:          true,
			errorContains:    "recommended for resolved worker resources",
			recommendedValue: "n1-standard-16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
				assert.Contains(t, err.Error(), tt.recommendedValue)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
