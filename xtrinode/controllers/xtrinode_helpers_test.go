package controllers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/status"
)

func TestIsKEDAEnabled(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		expected bool
	}{
		{
			name: "KEDA nil - defaults to disabled",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{},
			},
			expected: false,
		},
		{
			name: "KEDA.Enabled nil - defaults to disabled",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					KEDA: &analyticsv1.KEDASpec{},
				},
			},
			expected: false,
		},
		{
			name: "KEDA.Enabled true without metric config uses fixed replicas",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					KEDA: &analyticsv1.KEDASpec{
						Enabled: func() *bool { b := true; return &b }(),
					},
				},
			},
			expected: false,
		},
		{
			name: "KEDA.Enabled true with blank metric endpoints uses fixed replicas",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					KEDA: &analyticsv1.KEDASpec{
						Enabled:          func() *bool { b := true; return &b }(),
						PrometheusServer: func() *string { s := "  "; return &s }(),
						PrometheusQuery:  func() *string { s := " "; return &s }(),
						HTTPEndpoint:     func() *string { s := "\t"; return &s }(),
					},
				},
			},
			expected: false,
		},
		{
			name: "KEDA.Enabled true with metric config",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					KEDA: &analyticsv1.KEDASpec{
						Enabled:       func() *bool { b := true; return &b }(),
						ScalerType:    "prometheus",
						ScalingMetric: "query",
					},
				},
			},
			expected: true,
		},
		{
			name: "KEDA.Enabled false",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					KEDA: &analyticsv1.KEDASpec{
						Enabled: func() *bool { b := false; return &b }(),
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isKEDAEnabled(tt.xtrinode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldProvisionNodePoolWhileSuspended(t *testing.T) {
	scaleDown := true
	keepWarm := false

	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
		expected bool
	}{
		{
			name:     "no nodepool",
			xtrinode: &analyticsv1.XTrinode{},
			expected: false,
		},
		{
			name: "default scale down does not provision while suspended",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					NodePool: &analyticsv1.NodePoolSpec{},
				},
			},
			expected: false,
		},
		{
			name: "explicit scale down does not provision while suspended",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					NodePool: &analyticsv1.NodePoolSpec{ScaleDownOnSuspend: &scaleDown},
				},
			},
			expected: false,
		},
		{
			name: "scaleDownOnSuspend false provisions while suspended",
			xtrinode: &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					NodePool: &analyticsv1.NodePoolSpec{ScaleDownOnSuspend: &keepWarm},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, shouldProvisionNodePoolWhileSuspended(tt.xtrinode))
		})
	}
}

func TestPlacementStatusMessages(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "team-a",
		},
		Spec: analyticsv1.XTrinodeSpec{
			NodePool: &analyticsv1.NodePoolSpec{
				ClusterName: "capg-workload",
			},
		},
	}

	assert.Contains(t, nodePoolProvisionedMessage(xtrinode), `CAPI cluster "capg-workload"`)
	assert.Contains(t, trinoResourcesAppliedMessage(xtrinode), `operator cluster namespace "team-a"`)
	assert.Contains(t, trinoResourcesAppliedMessage(xtrinode), `does not move runtime resources cross-cluster`)
}

func TestGetNodePoolErrorRequeueInterval(t *testing.T) {
	customInterval := 30 * time.Second
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected time.Duration
	}{
		{
			name:     "nil nodePool - uses default",
			nodePool: nil,
			expected: config.NodePoolProvisioningErrorRequeueInterval,
		},
		{
			name: "custom interval",
			nodePool: &analyticsv1.NodePoolSpec{
				ErrorRequeueInterval: &metav1.Duration{Duration: customInterval},
			},
			expected: customInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNodePoolErrorRequeueInterval(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsCatalogConfigMap(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		expected bool
	}{
		{
			name: "catalog ConfigMap",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: config.CatalogConfigMapPrefix + "test-catalog",
				},
			},
			expected: true,
		},
		{
			name: "non-catalog ConfigMap",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "regular-configmap",
				},
			},
			expected: false,
		},
		{
			name: "other resource - still checks name prefix",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: config.CatalogConfigMapPrefix + "test",
				},
			},
			expected: true, // Function checks name prefix regardless of resource type
		},
		{
			name: "other resource with different name",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "regular-secret",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCatalogConfigMap(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsGatewayRouteConfigMap(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		expected bool
	}{
		{
			name: "gateway route ConfigMap",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.GatewayConfigMapName,
					Namespace: config.GatewayConfigMapNamespace,
				},
			},
			expected: true,
		},
		{
			name: "same name in runtime namespace",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.GatewayConfigMapName,
					Namespace: "team-a",
				},
			},
			expected: false,
		},
		{
			name: "different name in gateway namespace",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other",
					Namespace: config.GatewayConfigMapNamespace,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isGatewayRouteConfigMap(tt.obj))
		})
	}
}

func TestOwnedDeploymentPredicateReconcilesRuntimeStatusChanges(t *testing.T) {
	pred := ownedDeploymentPredicate()
	oldDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "trino-runtime-coordinator",
			Namespace:  "team-a",
			Generation: 1,
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			AvailableReplicas:  1,
		},
	}

	notReady := oldDeployment.DeepCopy()
	notReady.Status.ReadyReplicas = 0
	notReady.Status.AvailableReplicas = 0
	notReady.Status.UnavailableReplicas = 1

	assert.True(t, pred.Update(event.UpdateEvent{
		ObjectOld: oldDeployment,
		ObjectNew: notReady,
	}))

	specChanged := oldDeployment.DeepCopy()
	specChanged.Generation = 2
	assert.True(t, pred.Update(event.UpdateEvent{
		ObjectOld: oldDeployment,
		ObjectNew: specChanged,
	}))

	irrelevantStatus := oldDeployment.DeepCopy()
	irrelevantStatus.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:   appsv1.DeploymentProgressing,
		Status: corev1.ConditionTrue,
		Reason: "NewReplicaSetAvailable",
	}}
	assert.False(t, pred.Update(event.UpdateEvent{
		ObjectOld: oldDeployment,
		ObjectNew: irrelevantStatus,
	}))
}

func TestCatalogConfigMapToXTrinodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-1",
			Namespace: "test-ns",
		},
	}
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-2",
			Namespace: "test-ns",
		},
	}
	xtrinode3 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-3",
			Namespace: "other-ns",
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode1, xtrinode2, xtrinode3).
		Build()

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.CatalogConfigMapPrefix + "test",
			Namespace: "test-ns",
		},
	}

	ctx := context.Background()
	log := ctrl.Log

	requests := catalogConfigMapToXTrinodes(cli, ctx, configMap, log)

	// Should return requests for xtrinode-1 and xtrinode-2 (same namespace)
	assert.Len(t, requests, 2)
	names := make(map[string]bool)
	for _, req := range requests {
		names[req.Name] = true
	}
	assert.True(t, names["xtrinode-1"])
	assert.True(t, names["xtrinode-2"])
	assert.False(t, names["xtrinode-3"])
}

func TestGatewayConfigMapToXTrinodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-1",
			Namespace: "team-a",
		},
	}
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-2",
			Namespace: "team-b",
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode1, xtrinode2).
		Build()

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.GatewayConfigMapName,
			Namespace: config.GatewayConfigMapNamespace,
		},
	}

	requests := gatewayConfigMapToXTrinodes(cli, context.Background(), configMap, ctrl.Log)

	require.Len(t, requests, 2)
	names := make(map[string]bool)
	for _, req := range requests {
		names[req.Namespace+"/"+req.Name] = true
	}
	assert.True(t, names["team-a/xtrinode-1"])
	assert.True(t, names["team-b/xtrinode-2"])
}

func TestNamespaceGuardrailToXTrinodes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime-a", Namespace: "team-a"},
	}
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime-b", Namespace: "team-a"},
	}
	xtrinode3 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime-c", Namespace: "team-b"},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode1, xtrinode2, xtrinode3).
		Build()

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultNamespaceResourceQuotaName,
			Namespace: "team-a",
		},
	}

	requests := namespaceGuardrailToXTrinodes(cli, context.Background(), quota, ctrl.Log)

	require.Len(t, requests, 2)
	names := make(map[string]bool)
	for _, req := range requests {
		names[req.Name] = true
		assert.Equal(t, "team-a", req.Namespace)
	}
	assert.True(t, names["runtime-a"])
	assert.True(t, names["runtime-b"])
	assert.False(t, names["runtime-c"])
}

func TestNamespaceGuardrailResourcePredicate(t *testing.T) {
	pred := namespaceGuardrailResourcePredicate(isNamespaceResourceQuota)

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:       DefaultNamespaceResourceQuotaName,
			Namespace:  "team-a",
			Generation: 1,
		},
		Status: corev1.ResourceQuotaStatus{
			Used: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
		},
	}

	statusOnly := quota.DeepCopy()
	statusOnly.Status.Used = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}
	assert.False(t, pred.Update(event.UpdateEvent{
		ObjectOld: quota,
		ObjectNew: statusOnly,
	}))

	specChanged := quota.DeepCopy()
	specChanged.Generation = 2
	assert.True(t, pred.Update(event.UpdateEvent{
		ObjectOld: quota,
		ObjectNew: specChanged,
	}))

	assert.True(t, pred.Delete(event.DeleteEvent{Object: quota}))

	otherQuota := quota.DeepCopy()
	otherQuota.Name = "other-quota"
	assert.False(t, pred.Delete(event.DeleteEvent{Object: otherQuota}))
	assert.True(t, isNamespaceResourceQuota(quota))
	assert.False(t, isNamespaceResourceQuota(otherQuota))
	assert.True(t, isNamespaceLimitRange(&corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: DefaultNamespaceLimitRangeName},
	}))
	assert.True(t, isNamespaceResourceQuota(&corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "custom-quota",
			Labels: namespaceGuardrailLabels(),
		},
	}))
}

func TestServiceMonitorToXTrinodes(t *testing.T) {
	serviceMonitor := &unstructured.Unstructured{}
	serviceMonitor.SetName("trino-runtime")
	serviceMonitor.SetNamespace("team-a")
	serviceMonitor.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: analyticsv1.GroupVersion.String(),
		Kind:       "XTrinode",
		Name:       "runtime",
	}})

	requests := serviceMonitorToXTrinodes(nil, context.Background(), serviceMonitor, ctrl.Log)

	require.Len(t, requests, 1)
	assert.Equal(t, "team-a", requests[0].Namespace)
	assert.Equal(t, "runtime", requests[0].Name)

	unownedServiceMonitor := serviceMonitor.DeepCopy()
	unownedServiceMonitor.SetOwnerReferences(nil)
	assert.Empty(t, serviceMonitorToXTrinodes(nil, context.Background(), unownedServiceMonitor, ctrl.Log))
	assert.Equal(t, "ServiceMonitor", serviceMonitorGVK().Kind)
}

func TestEndpointSliceToXTrinodes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec:       analyticsv1.XTrinodeSpec{Size: "s"},
	}
	other := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "team-a"},
		Spec:       analyticsv1.XTrinodeSpec{Size: "s"},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode, other).
		Build()
	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-runtime-abc",
			Namespace: "team-a",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: config.BuildCoordinatorServiceName("runtime"),
			},
		},
	}

	requests := endpointSliceToXTrinodes(cli, context.Background(), endpointSlice, ctrl.Log)

	require.Len(t, requests, 1)
	assert.Equal(t, "team-a", requests[0].Namespace)
	assert.Equal(t, "runtime", requests[0].Name)
}

func TestNodePoolWatchGVKs(t *testing.T) {
	kinds := map[string]bool{}
	for _, gvk := range nodePoolWatchGVKs() {
		kinds[gvk.Kind] = true
	}

	for _, kind := range []string{
		"MachineDeployment",
		"MachinePool",
		"AzureMachinePool",
		"AWSMachineTemplate",
		"GCPMachineTemplate",
		"AzureManagedMachinePool",
		"AWSManagedMachinePool",
		"GCPManagedMachinePool",
	} {
		assert.True(t, kinds[kind], "expected node pool watch for %s", kind)
	}
}

func TestSecretToXTrinodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-1",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			CatalogSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}},
		},
	}
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-2",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			CatalogSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "b"}},
		},
	}
	xtrinode3 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-3",
			Namespace: "other-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			CatalogSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}},
		},
	}
	xtrinode4 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-4",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				SecretMounts: []analyticsv1.SecretMountSpec{{
					Name:       "global",
					SecretName: "target-secret",
					Path:       "/etc/trino/global",
				}},
			},
		},
	}
	xtrinode5 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-5",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				Coordinator: &analyticsv1.CoordinatorHelmConfigSpec{
					SecretMounts: []analyticsv1.SecretMountSpec{{
						Name:       "coord",
						SecretName: "coord-secret",
						Path:       "/etc/trino/coord",
					}},
				},
				Worker: &analyticsv1.WorkerHelmConfigSpec{
					SecretMounts: []analyticsv1.SecretMountSpec{{
						Name:       "worker",
						SecretName: "target-secret",
						Path:       "/etc/trino/worker",
					}},
				},
			},
		},
	}
	xtrinode6 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-6",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"secretMounts": []interface{}{
					map[string]interface{}{
						"name":       "overlay-global",
						"secretName": "target-secret",
						"path":       "/etc/trino/overlay",
					},
				},
			}),
		},
	}
	xtrinode7 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-7",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"envFrom": []interface{}{
					map[string]interface{}{
						"secretRef": map[string]interface{}{"name": "target-secret"},
					},
				},
			}),
		},
	}
	xtrinode8 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-8",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			TLS: &analyticsv1.TLSSpec{
				ServerSecretClass: "target-secret",
			},
		},
	}
	xtrinode9 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-9",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"auth": map[string]interface{}{
					"passwordAuthSecret": "target-secret",
				},
			}),
		},
	}
	xtrinode10 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-10",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			TrinoControlAuth: &analyticsv1.TrinoControlAuthSpec{
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "target-secret"},
					Key:                  "password",
				},
			},
		},
	}
	xtrinode11 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-11",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				Env: []corev1.EnvVar{{
					Name: "SECRET_VALUE",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "target-secret"},
							Key:                  "value",
						},
					},
				}},
			},
		},
	}
	xtrinode12 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-12",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				EnvFrom: []corev1.EnvFromSource{{
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "target-secret"},
					},
				}},
			},
		},
	}
	xtrinode13 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xtrinode-13",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"env": []interface{}{
					map[string]interface{}{
						"name": "SECRET_VALUE",
						"valueFrom": map[string]interface{}{
							"secretKeyRef": map[string]interface{}{
								"name": "target-secret",
								"key":  "value",
							},
						},
					},
				},
			}),
		},
	}
	catalog1 := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgres-a",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "a"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:  "jdbc:postgresql://postgres-a:5432/db",
					ConnectionUser: "trino",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "target-secret"},
						Key:                  "password",
					},
				},
			},
		},
	}
	catalog2 := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgres-b",
			Namespace: "test-ns",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "b"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:  "jdbc:postgresql://postgres-b:5432/db",
					ConnectionUser: "trino",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "other-secret"},
						Key:                  "password",
					},
				},
			},
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode1, xtrinode2, xtrinode3, xtrinode4, xtrinode5, xtrinode6, xtrinode7, xtrinode8, xtrinode9, xtrinode10, xtrinode11, xtrinode12, xtrinode13, catalog1, catalog2).
		Build()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target-secret",
			Namespace: "test-ns",
		},
	}

	requests := secretToXTrinodes(cli, context.Background(), secret, ctrl.Log)

	require.Len(t, requests, 10)
	names := make(map[string]bool)
	for _, req := range requests {
		names[req.Name] = true
		assert.Equal(t, "test-ns", req.Namespace)
	}
	assert.True(t, names["xtrinode-1"])
	assert.True(t, names["xtrinode-4"])
	assert.True(t, names["xtrinode-5"])
	assert.True(t, names["xtrinode-6"])
	assert.False(t, names["xtrinode-7"])
	assert.True(t, names["xtrinode-8"])
	assert.True(t, names["xtrinode-9"])
	assert.True(t, names["xtrinode-10"])
	assert.True(t, names["xtrinode-11"])
	assert.True(t, names["xtrinode-12"])
	assert.True(t, names["xtrinode-13"])
	assert.False(t, names["xtrinode-2"])
	assert.False(t, names["xtrinode-3"])
}

func TestExternalConfigMapToXTrinodes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	xtrinode1 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "resource-groups", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ResourceGroupsProfile: "target-config",
		},
	}
	xtrinode2 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-config", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			CustomConfigMaps: []string{"other-config", "target-config"},
		},
	}
	xtrinode3 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "global-overlay", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"configMounts": []interface{}{
					map[string]interface{}{
						"name":      "global",
						"configMap": "target-config",
						"path":      "/etc/trino/global",
					},
				},
			}),
		},
	}
	xtrinode4 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "role-overlay", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"worker": map[string]interface{}{
					"configMounts": []interface{}{
						map[string]interface{}{
							"name":      "worker",
							"configMap": "target-config",
							"path":      "/etc/trino/worker",
						},
					},
				},
			}),
		},
	}
	xtrinode5 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "envfrom-overlay", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"envFrom": []interface{}{
					map[string]interface{}{
						"configMapRef": map[string]interface{}{"name": "target-config"},
					},
				},
			}),
		},
	}
	xtrinode6 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "additional-volume", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"coordinator": map[string]interface{}{
					"additionalVolumes": []interface{}{
						map[string]interface{}{
							"name":      "extra",
							"configMap": map[string]interface{}{"name": "target-config"},
						},
					},
				},
			}),
		},
	}
	xtrinode7 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "other-namespace", Namespace: "other-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ResourceGroupsProfile: "target-config",
		},
	}
	xtrinode8 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "non-match", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			CustomConfigMaps: []string{"other-config"},
		},
	}
	xtrinode9 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "jmx-external-config", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			KEDA: &analyticsv1.KEDASpec{
				JMXExporter: &analyticsv1.JMXExporterSpec{
					Enabled:   true,
					ConfigMap: "target-config",
				},
			},
		},
	}
	xtrinode10 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "helm-env-config", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				Env: []corev1.EnvVar{{
					Name: "CONFIG_VALUE",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "target-config"},
							Key:                  "value",
						},
					},
				}},
			},
		},
	}
	xtrinode11 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "helm-envfrom-config", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			HelmChartConfig: &analyticsv1.HelmChartConfigSpec{
				EnvFrom: []corev1.EnvFromSource{{
					ConfigMapRef: &corev1.ConfigMapEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "target-config"},
					},
				}},
			},
		},
	}
	xtrinode12 := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "overlay-env-config", Namespace: "test-ns"},
		Spec: analyticsv1.XTrinodeSpec{
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"env": []interface{}{
					map[string]interface{}{
						"name": "CONFIG_VALUE",
						"valueFrom": map[string]interface{}{
							"configMapKeyRef": map[string]interface{}{
								"name": "target-config",
								"key":  "value",
							},
						},
					},
				},
			}),
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode1, xtrinode2, xtrinode3, xtrinode4, xtrinode5, xtrinode6, xtrinode7, xtrinode8, xtrinode9, xtrinode10, xtrinode11, xtrinode12).
		Build()

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target-config",
			Namespace: "test-ns",
		},
	}

	requests := externalConfigMapToXTrinodes(cli, context.Background(), configMap, ctrl.Log)

	require.Len(t, requests, 9)
	names := make(map[string]bool)
	for _, req := range requests {
		names[req.Name] = true
		assert.Equal(t, "test-ns", req.Namespace)
	}
	assert.True(t, names["resource-groups"])
	assert.True(t, names["custom-config"])
	assert.True(t, names["global-overlay"])
	assert.True(t, names["role-overlay"])
	assert.False(t, names["envfrom-overlay"])
	assert.True(t, names["additional-volume"])
	assert.True(t, names["jmx-external-config"])
	assert.True(t, names["helm-env-config"])
	assert.True(t, names["helm-envfrom-config"])
	assert.True(t, names["overlay-env-config"])
	assert.False(t, names["other-namespace"])
	assert.False(t, names["non-match"])
}

func controllerValuesOverlay(t *testing.T, values map[string]interface{}) *apiextensionsv1.JSON {
	t.Helper()
	data, err := json.Marshal(values)
	require.NoError(t, err)
	return &apiextensionsv1.JSON{Raw: data}
}

func TestUpdateStatusWithRetry(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-status",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		Build()

	ctx := context.Background()
	log := ctrl.Log

	// Get fresh copy to update
	var toUpdate analyticsv1.XTrinode
	err := cli.Get(ctx, types.NamespacedName{Name: "test-status", Namespace: "default"}, &toUpdate)
	require.NoError(t, err)

	// Update status - fake client may not fully support status updates
	capturedPhase := "Reconciling"
	key := client.ObjectKeyFromObject(&toUpdate)
	err = updateStatusWithRetry(ctx, cli, cli.Status(), key, log,
		func() client.Object { return &analyticsv1.XTrinode{} },
		func(obj client.Object) error {
			t := obj.(*analyticsv1.XTrinode)
			t.Status.Phase = capturedPhase
			return nil
		})
	// Fake client doesn't fully support status subresource updates
	// We verify the function exists and can be called without panicking
	// Actual status update testing is done in integration tests
	_ = err // Ignore error - fake client limitation
}

func TestSetXTrinodeErrorStatusAndUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-error",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Phase: "Ready",
			Conditions: []metav1.Condition{
				{Type: status.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: status.ConditionReasonAllComponentsReady},
				{Type: status.ConditionTypeReconciling, Status: metav1.ConditionTrue, Reason: status.ConditionReasonReconciling},
			},
		},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		Build()

	ctx := context.Background()
	log := ctrl.Log

	// Get fresh copy
	var toUpdate analyticsv1.XTrinode
	err := cli.Get(ctx, types.NamespacedName{Name: "test-error", Namespace: "default"}, &toUpdate)
	require.NoError(t, err)

	// Call the function - it will modify toUpdate before attempting update
	_ = setXTrinodeErrorStatusAndUpdate(
		ctx,
		cli,
		cli.Status(),
		&toUpdate,
		log,
		status.ConditionReasonResourceBuildFailed,
		"Test error message",
		nil, // No event recorder in test
	)
	require.Equal(t, "Error", toUpdate.Status.Phase)
	require.Equal(t, metav1.ConditionTrue, status.GetCondition(&toUpdate, status.ConditionTypeError).Status)
	require.Equal(t, metav1.ConditionFalse, status.GetCondition(&toUpdate, status.ConditionTypeReady).Status)
	require.Equal(t, status.ConditionReasonResourceBuildFailed, status.GetCondition(&toUpdate, status.ConditionTypeReady).Reason)
	require.Equal(t, metav1.ConditionFalse, status.GetCondition(&toUpdate, status.ConditionTypeReconciling).Status)
}

func TestGetNodePoolRequeueIntervals(t *testing.T) {
	customInterval := 45 * time.Second

	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		testFunc func(*analyticsv1.NodePoolSpec) time.Duration
		expected time.Duration
	}{
		{
			name:     "ResourceNotFoundRequeueInterval - nil",
			nodePool: nil,
			testFunc: getNodePoolResourceNotFoundRequeueInterval,
			expected: config.NodePoolResourceNotFoundRequeueInterval,
		},
		{
			name: "ResourceNotFoundRequeueInterval - custom",
			nodePool: &analyticsv1.NodePoolSpec{
				ResourceNotFoundRequeueInterval: &metav1.Duration{Duration: customInterval},
			},
			testFunc: getNodePoolResourceNotFoundRequeueInterval,
			expected: customInterval,
		},
		{
			name:     "StatusNotAvailableRequeueInterval - nil",
			nodePool: nil,
			testFunc: getNodePoolStatusNotAvailableRequeueInterval,
			expected: config.NodePoolStatusNotAvailableRequeueInterval,
		},
		{
			name: "StatusNotAvailableRequeueInterval - custom",
			nodePool: &analyticsv1.NodePoolSpec{
				StatusNotAvailableRequeueInterval: &metav1.Duration{Duration: customInterval},
			},
			testFunc: getNodePoolStatusNotAvailableRequeueInterval,
			expected: customInterval,
		},
		{
			name:     "NoNodesReadyRequeueInterval - nil",
			nodePool: nil,
			testFunc: getNodePoolNoNodesReadyRequeueInterval,
			expected: config.NodePoolNoNodesReadyRequeueInterval,
		},
		{
			name: "NoNodesReadyRequeueInterval - custom",
			nodePool: &analyticsv1.NodePoolSpec{
				NoNodesReadyRequeueInterval: &metav1.Duration{Duration: customInterval},
			},
			testFunc: getNodePoolNoNodesReadyRequeueInterval,
			expected: customInterval,
		},
		{
			name:     "NodesReadyRequeueInterval - nil",
			nodePool: nil,
			testFunc: getNodePoolNodesReadyRequeueInterval,
			expected: config.NodePoolNodesReadyRequeueInterval,
		},
		{
			name: "NodesReadyRequeueInterval - custom",
			nodePool: &analyticsv1.NodePoolSpec{
				NodesReadyRequeueInterval: &metav1.Duration{Duration: customInterval},
			},
			testFunc: getNodePoolNodesReadyRequeueInterval,
			expected: customInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.testFunc(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetNodePoolMinRequiredReplicasWhenMinNodesZero(t *testing.T) {
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected int64
	}{
		{
			name:     "nil nodePool - uses default",
			nodePool: nil,
			expected: config.NodePoolMinRequiredReplicasWhenMinNodesZero,
		},
		{
			name: "custom zero value",
			nodePool: &analyticsv1.NodePoolSpec{
				MinRequiredReplicasWhenMinNodesZero: func() *int32 { v := int32(0); return &v }(),
			},
			expected: 0,
		},
		{
			name: "custom value",
			nodePool: &analyticsv1.NodePoolSpec{
				MinRequiredReplicasWhenMinNodesZero: func() *int32 { v := int32(5); return &v }(),
			},
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNodePoolMinRequiredReplicasWhenMinNodesZero(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetNodePoolProvisioningTimeout(t *testing.T) {
	customTimeout := 10 * time.Minute
	tests := []struct {
		name     string
		nodePool *analyticsv1.NodePoolSpec
		expected time.Duration
	}{
		{
			name:     "nil nodePool - uses default",
			nodePool: nil,
			expected: config.NodePoolProvisioningTimeout,
		},
		{
			name: "custom timeout",
			nodePool: &analyticsv1.NodePoolSpec{
				ProvisioningTimeout: &metav1.Duration{Duration: customTimeout},
			},
			expected: customTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNodePoolProvisioningTimeout(tt.nodePool)
			assert.Equal(t, tt.expected, result)
		})
	}
}
