package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestValidateRouting tests routing validation
func TestValidateRouting(t *testing.T) {
	tests := []struct {
		name    string
		routing *RoutingSpec
		wantErr bool
	}{
		{
			name: "valid routing with hostname",
			routing: &RoutingSpec{
				Hostname: "dummy.trino.company",
			},
			wantErr: false,
		},
		{
			name: "valid routing with hostnameDomain",
			routing: &RoutingSpec{
				HostnameDomain: "trino-gw.company.com",
			},
			wantErr: false,
		},
		{
			name: "valid routing with header",
			routing: &RoutingSpec{
				Header: "X-Trino-XTrinode=dummy",
			},
			wantErr: false,
		},
		{
			name: "valid routing with default",
			routing: &RoutingSpec{
				Default: true,
			},
			wantErr: false,
		},
		{
			name: "invalid routing - no selector",
			routing: &RoutingSpec{
				RoutingGroup: "shared",
			},
			wantErr: true,
		},
		{
			name: "invalid routing - both hostname and hostnameDomain",
			routing: &RoutingSpec{
				Hostname:       "dummy.trino.company",
				HostnameDomain: "trino-gw.company.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size:    "s",
					Routing: tt.routing,
				},
			}
			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateKEDA tests KEDA validation
func TestValidateKEDA(t *testing.T) {
	tests := []struct {
		name    string
		keda    *KEDASpec
		wantErr bool
	}{
		{
			name: "valid KEDA with prometheus",
			keda: &KEDASpec{
				ScalerType:       "prometheus",
				ScalingMetric:    "query",
				PrometheusServer: stringPtr("http://prometheus:9090"),
			},
			wantErr: false,
		},
		{
			name: "valid KEDA with http",
			keda: &KEDASpec{
				ScalerType:    "http",
				ScalingMetric: "memory",
				HTTPEndpoint:  stringPtr("coordinator"),
			},
			wantErr: false,
		},
		{
			name: "invalid scalerType",
			keda: &KEDASpec{
				ScalerType: "invalid",
			},
			wantErr: true,
		},
		{
			name: "invalid scalingMetric",
			keda: &KEDASpec{
				ScalerType:    "http",
				ScalingMetric: "invalid",
			},
			wantErr: true,
		},
		{
			name: "prometheus uses defaults without explicit server or query",
			keda: &KEDASpec{
				ScalerType: "prometheus",
			},
			wantErr: false,
		},
		{
			name: "http with jmx endpoint but jmxExporter not enabled",
			keda: &KEDASpec{
				ScalerType:   "http",
				HTTPEndpoint: stringPtr("jmx"),
			},
			wantErr: true,
		},
		{
			name: "valid http with jmx endpoint and jmxExporter enabled",
			keda: &KEDASpec{
				ScalerType:   "http",
				HTTPEndpoint: stringPtr("jmx"),
				JMXExporter: &JMXExporterSpec{
					Enabled: true,
				},
			},
			wantErr: false,
		},
		{
			name:    "valid default KEDA config",
			keda:    &KEDASpec{},
			wantErr: false,
		},
		{
			name: "invalid aggregator endpoint with default scaler type",
			keda: &KEDASpec{
				ScalingMetric: "CPU",
				HTTPEndpoint:  stringPtr(" Aggregator "),
			},
			wantErr: true,
		},
		{
			name: "valid custom http endpoint",
			keda: &KEDASpec{
				ScalerType:    "http",
				ScalingMetric: "memory",
				HTTPEndpoint:  stringPtr("http://custom-metrics.default.svc:8080/metrics"),
			},
			wantErr: false,
		},
		{
			name: "valid query scaler",
			keda: &KEDASpec{
				ScalerType:    "http",
				ScalingMetric: "query",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
					KEDA: tt.keda,
				},
			}
			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateFaultTolerantExecution(t *testing.T) {
	tests := []struct {
		name                   string
		faultTolerantExecution *FaultTolerantExecutionSpec
		wantErr                bool
	}{
		{
			name: "valid task retry with s3 filesystem exchange",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				RetryPolicy: "TASK",
				ExchangeManager: &ExchangeManagerSpec{
					Name:            "filesystem",
					BaseDirectories: []string{"s3://trino-exchange/runtime-a"},
					Properties: map[string]string{
						"exchange.s3.region": "us-east-1",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid lower-case query retry",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				RetryPolicy: "query",
			},
			wantErr: false,
		},
		{
			name: "valid query retry without exchange manager",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				RetryPolicy: "QUERY",
				ExchangeManager: &ExchangeManagerSpec{
					Enabled: boolPtr(false),
				},
			},
			wantErr: false,
		},
		{
			name: "invalid retry policy",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				RetryPolicy: "worker",
			},
			wantErr: true,
		},
		{
			name: "invalid task retry without exchange manager",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				RetryPolicy: "TASK",
				ExchangeManager: &ExchangeManagerSpec{
					Enabled: boolPtr(false),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid empty base directory",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				ExchangeManager: &ExchangeManagerSpec{
					BaseDirectories: []string{""},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid comma in base directory",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				ExchangeManager: &ExchangeManagerSpec{
					BaseDirectories: []string{"s3://bucket/a,s3://bucket/b"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid reserved property",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				ExchangeManager: &ExchangeManagerSpec{
					Properties: map[string]string{
						"exchange-manager.name": "filesystem",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid property newline",
			faultTolerantExecution: &FaultTolerantExecutionSpec{
				ExchangeManager: &ExchangeManagerSpec{
					Properties: map[string]string{
						"exchange.s3.region": "us-east-1\nbad=true",
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size:                   "s",
					FaultTolerantExecution: tt.faultTolerantExecution,
				},
			}
			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateTLS tests TLS validation
func TestValidateTLS(t *testing.T) {
	tests := []struct {
		name    string
		tls     *TLSSpec
		wantErr bool
	}{
		{
			name: "unsupported TLS - both set",
			tls: &TLSSpec{
				ServerSecretClass:   "server-tls",
				InternalSecretClass: "internal-tls",
			},
			wantErr: true,
		},
		{
			name: "valid TLS - both empty",
			tls: &TLSSpec{
				ServerSecretClass:   "",
				InternalSecretClass: "",
			},
			wantErr: false,
		},
		{
			name: "invalid TLS - only server set",
			tls: &TLSSpec{
				ServerSecretClass: "server-tls",
			},
			wantErr: true,
		},
		{
			name: "invalid TLS - only internal set",
			tls: &TLSSpec{
				InternalSecretClass: "internal-tls",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size: "s",
					TLS:  tt.tls,
				},
			}
			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateLimits tests limits validation
func TestValidateLimits(t *testing.T) {
	tests := []struct {
		name    string
		limits  *LimitsSpec
		wantErr bool
	}{
		{
			name: "valid limits",
			limits: &LimitsSpec{
				HardConcurrencyPerGroup: int32Ptr(10),
				MaxQueuedPerGroup:       int32Ptr(100),
			},
			wantErr: false,
		},
		{
			name: "invalid hardConcurrencyPerGroup - too low",
			limits: &LimitsSpec{
				HardConcurrencyPerGroup: int32Ptr(0),
			},
			wantErr: true,
		},
		{
			name: "invalid maxQueuedPerGroup - negative",
			limits: &LimitsSpec{
				MaxQueuedPerGroup: int32Ptr(-1),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size:   "s",
					Limits: tt.limits,
				},
			}
			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateLimitsRejectsInvalidTrinoDataSize(t *testing.T) {
	xtrinode := &XTrinode{
		Spec: XTrinodeSpec{
			Size: "s",
			Limits: &LimitsSpec{
				Session: &SessionLimits{
					MaxQueryMemory: "four-gigabytes",
				},
			},
		},
	}

	_, err := xtrinode.ValidateCreate()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.limits.session.maxQueryMemory")
}

func TestValidateTrinoMemoryAgainstRuntimeShapeRejectsOversizedPerNodeMemory(t *testing.T) {
	xtrinode := &XTrinode{
		Spec: XTrinodeSpec{
			Size: "s",
			Resources: &RuntimeResourcesSpec{
				Worker: &corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
			Limits: &LimitsSpec{
				Session: &SessionLimits{
					MaxTotalMemoryPerNode: "8GiB",
				},
			},
		},
	}

	_, err := xtrinode.ValidateCreate()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.limits.session.maxTotalMemoryPerNode")
	assert.Contains(t, err.Error(), "must not exceed resolved worker memory limit")
}

func TestValidateCreateWarnsWhenNodePoolMachineTypeIsTooSmallForTypedResources(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime"},
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
				Name:     "runtime-pool",
				Provider: "aws",
				AWS: &AWSNodePoolSpec{
					InstanceType: "m5.large",
				},
			},
		},
	}

	warnings, err := xtrinode.ValidateCreate()

	assert.NoError(t, err)
	assert.Contains(t, warnings, `node pool machine type "m5.large" may not fit resolved worker resources; recommended for resolved worker resources is "m5.4xlarge"`)
}

// TestValidateHelmChartConfig tests HelmChartConfig validation
func TestValidateHelmChartConfig(t *testing.T) {
	tests := []struct {
		name    string
		hcc     *HelmChartConfigSpec
		wantErr bool
	}{
		{
			name: "valid access control - configmap",
			hcc: &HelmChartConfigSpec{
				AccessControl: &AccessControlSpec{
					Type:       "configmap",
					ConfigFile: "access-control.json",
				},
			},
			wantErr: false,
		},
		{
			name: "valid access control - properties",
			hcc: &HelmChartConfigSpec{
				AccessControl: &AccessControlSpec{
					Type:       "properties",
					Properties: "access-control.properties",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid access control type",
			hcc: &HelmChartConfigSpec{
				AccessControl: &AccessControlSpec{
					Type: "invalid",
				},
			},
			wantErr: true,
		},
		{
			name: "configmap type without configFile",
			hcc: &HelmChartConfigSpec{
				AccessControl: &AccessControlSpec{
					Type: "configmap",
				},
			},
			wantErr: true,
		},
		{
			name: "valid ingress",
			hcc: &HelmChartConfigSpec{
				Ingress: &IngressSpec{
					Enabled: true,
					Hosts: []IngressHostSpec{
						{Host: "trino.company.com"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid ingress - enabled but no hosts",
			hcc: &HelmChartConfigSpec{
				Ingress: &IngressSpec{
					Enabled: true,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid ingress - empty host",
			hcc: &HelmChartConfigSpec{
				Ingress: &IngressSpec{
					Enabled: true,
					Hosts: []IngressHostSpec{
						{Host: ""},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid worker graceful shutdown",
			hcc: &HelmChartConfigSpec{
				Worker: &WorkerHelmConfigSpec{
					GracefulShutdown: &GracefulShutdownSpec{
						Enabled:            true,
						GracePeriodSeconds: 120,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid worker graceful shutdown - zero grace period",
			hcc: &HelmChartConfigSpec{
				Worker: &WorkerHelmConfigSpec{
					GracefulShutdown: &GracefulShutdownSpec{
						Enabled:            true,
						GracePeriodSeconds: 0,
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				Spec: XTrinodeSpec{
					Size:            "s",
					HelmChartConfig: tt.hcc,
				},
			}
			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateUpdate_SizeChanges tests size upgrade/downgrade policies
func TestValidateUpdate_SizeChanges(t *testing.T) {
	tests := []struct {
		name             string
		oldSize          string
		newSize          string
		breakGlass       bool
		wantErr          bool
		wantWarningCount int
	}{
		{
			name:             "size upgrade - allowed with warning",
			oldSize:          "s",
			newSize:          "m",
			breakGlass:       false,
			wantErr:          false,
			wantWarningCount: 1,
		},
		{
			name:             "size downgrade - requires break-glass",
			oldSize:          "l",
			newSize:          "m",
			breakGlass:       false,
			wantErr:          true,
			wantWarningCount: 0,
		},
		{
			name:             "size downgrade - allowed with break-glass",
			oldSize:          "l",
			newSize:          "m",
			breakGlass:       true,
			wantErr:          false,
			wantWarningCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec:       XTrinodeSpec{Size: tt.oldSize},
			}
			updated := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: XTrinodeSpec{Size: tt.newSize},
			}
			if tt.breakGlass {
				updated.Annotations = map[string]string{
					AnnotationAllowBreakingUpdate: "true",
				}
			}

			warnings, err := updated.ValidateUpdate(old)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantWarningCount, len(warnings))
		})
	}
}

// TestValidateUpdate_RoutingIdentityChanges tests routing identity change policies
func TestValidateUpdate_RoutingIdentityChanges(t *testing.T) {
	tests := []struct {
		name       string
		oldRouting *RoutingSpec
		newRouting *RoutingSpec
		breakGlass bool
		wantErr    bool
	}{
		{
			name: "routing hostname change - requires break-glass",
			oldRouting: &RoutingSpec{
				Hostname: "old.trino.company",
			},
			newRouting: &RoutingSpec{
				Hostname: "new.trino.company",
			},
			breakGlass: false,
			wantErr:    true,
		},
		{
			name: "routing hostname change - allowed with break-glass",
			oldRouting: &RoutingSpec{
				Hostname: "old.trino.company",
			},
			newRouting: &RoutingSpec{
				Hostname: "new.trino.company",
			},
			breakGlass: true,
			wantErr:    false,
		},
		{
			name: "routing default change - requires break-glass",
			oldRouting: &RoutingSpec{
				Hostname: "dummy.trino.company",
				Default:  false,
			},
			newRouting: &RoutingSpec{
				Hostname: "dummy.trino.company",
				Default:  true,
			},
			breakGlass: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: XTrinodeSpec{
					Size:    "s",
					Routing: tt.oldRouting,
				},
			}
			updated := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: XTrinodeSpec{
					Size:    "s",
					Routing: tt.newRouting,
				},
			}
			if tt.breakGlass {
				updated.Annotations = map[string]string{
					AnnotationAllowBreakingUpdate: "true",
				}
			}

			_, err := updated.ValidateUpdate(old)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateUpdate_NodePoolIdentityChanges tests nodePool identity change policies
func TestValidateUpdate_NodePoolIdentityChanges(t *testing.T) {
	tests := []struct {
		name        string
		oldNodePool *NodePoolSpec
		newNodePool *NodePoolSpec
		breakGlass  bool
		wantErr     bool
	}{
		{
			name: "nodePool provider change - requires break-glass",
			oldNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
			newNodePool: &NodePoolSpec{
				Provider: "aws",
				AWS:      &AWSNodePoolSpec{InstanceType: "m5.xlarge"},
			},
			breakGlass: false,
			wantErr:    true,
		},
		{
			name: "nodePool provider change - allowed with break-glass",
			oldNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
			newNodePool: &NodePoolSpec{
				Provider: "aws",
				AWS:      &AWSNodePoolSpec{InstanceType: "m5.xlarge"},
			},
			breakGlass: true,
			wantErr:    false,
		},
		{
			name: "nodePool name change - requires break-glass",
			oldNodePool: &NodePoolSpec{
				Name:     "old-pool",
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
			newNodePool: &NodePoolSpec{
				Name:     "new-pool",
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
			breakGlass: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: XTrinodeSpec{
					Size:     "s",
					NodePool: tt.oldNodePool,
				},
			}
			updated := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: XTrinodeSpec{
					Size:     "s",
					NodePool: tt.newNodePool,
				},
			}
			if tt.breakGlass {
				updated.Annotations = map[string]string{
					AnnotationAllowBreakingUpdate: "true",
				}
			}

			_, err := updated.ValidateUpdate(old)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateUpdate_NodePoolShapeChanges tests nodePool shape change policies
func TestValidateUpdate_NodePoolShapeChanges(t *testing.T) {
	tests := []struct {
		name        string
		oldNodePool *NodePoolSpec
		newNodePool *NodePoolSpec
		breakGlass  bool
		wantErr     bool
	}{
		{
			name: "vmSize change - requires break-glass",
			oldNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
			newNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D16as_v5"},
			},
			breakGlass: false,
			wantErr:    true,
		},
		{
			name: "vmSize change - allowed with break-glass",
			oldNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
			newNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D16as_v5"},
			},
			breakGlass: true,
			wantErr:    false,
		},
		{
			name: "osDiskGB change - requires break-glass",
			oldNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
				OSDiskGB: int32Ptr(128),
			},
			newNodePool: &NodePoolSpec{
				Provider: "azure",
				Azure:    &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
				OSDiskGB: int32Ptr(256),
			},
			breakGlass: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: XTrinodeSpec{
					Size:     "s",
					NodePool: tt.oldNodePool,
				},
			}
			updated := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: XTrinodeSpec{
					Size:     "s",
					NodePool: tt.newNodePool,
				},
			}
			if tt.breakGlass {
				updated.Annotations = map[string]string{
					AnnotationAllowBreakingUpdate: "true",
				}
			}

			_, err := updated.ValidateUpdate(old)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUpdate_NodePoolDeletionPolicyRetainToDeleteRequiresBreakGlass(t *testing.T) {
	old := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: XTrinodeSpec{
			Size: "s",
			NodePool: &NodePoolSpec{
				Provider:       "gcp",
				DeletionPolicy: NodePoolDeletionPolicyRetain,
				GCP:            &GCPNodePoolSpec{MachineType: "n1-standard-8"},
			},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.NodePool.DeletionPolicy = NodePoolDeletionPolicyDelete

	warnings, err := updated.ValidateUpdate(old)

	assert.Error(t, err)
	assert.Contains(t, warnings, buildNodePoolDeletionPolicyChangeWarning())
	assert.Contains(t, err.Error(), "spec.nodePool.deletionPolicy")

	updated.Annotations = map[string]string{AnnotationAllowBreakingUpdate: "true"}
	_, err = updated.ValidateUpdate(old)
	assert.NoError(t, err)
}

func TestValidateUpdate_NodePoolDeletionPolicyScaleToZeroToDeleteRequiresBreakGlass(t *testing.T) {
	old := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: XTrinodeSpec{
			Size: "s",
			NodePool: &NodePoolSpec{
				Provider:       "gcp",
				DeletionPolicy: NodePoolDeletionPolicyScaleToZero,
				GCP:            &GCPNodePoolSpec{MachineType: "n1-standard-8"},
			},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.NodePool.DeletionPolicy = NodePoolDeletionPolicyDelete

	warnings, err := updated.ValidateUpdate(old)

	assert.Error(t, err)
	assert.Contains(t, warnings, buildNodePoolDeletionPolicyChangeWarning())
	assert.Contains(t, err.Error(), "spec.nodePool.deletionPolicy")

	updated.Annotations = map[string]string{AnnotationAllowBreakingUpdate: "true"}
	_, err = updated.ValidateUpdate(old)
	assert.NoError(t, err)
}

// TestValidateUpdate_BreakGlassNotNeeded tests warning when break-glass is present but not needed
func TestValidateUpdate_BreakGlassNotNeeded(t *testing.T) {
	old := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: XTrinodeSpec{
			Size:       "s",
			MaxWorkers: int32Ptr(10),
		},
	}
	updated := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			Annotations: map[string]string{
				AnnotationAllowBreakingUpdate: "true",
			},
		},
		Spec: XTrinodeSpec{
			Size:       "s",
			MaxWorkers: int32Ptr(12), // Small change, no break-glass needed
		},
	}

	warnings, err := updated.ValidateUpdate(old)
	assert.NoError(t, err)
	assert.Contains(t, warnings, "break-glass annotation present but no gated changes detected")
}
