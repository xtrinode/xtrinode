package resources

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func pruneDisabledTrinoResources(ctx context.Context, c client.Client, xtrinode *analyticsv1.XTrinode, resources *TrinoResourceSet) error {
	if resources.WorkerDeployment == nil {
		if err := pruneWorkerStack(ctx, c, xtrinode); err != nil {
			return err
		}
	}

	staleObjects := []client.Object{}
	if resources.ServiceAccount == nil {
		staleObjects = append(staleObjects, namedServiceAccount(xtrinode, serviceAccountName(xtrinode)))
	}
	if resources.CoordinatorMetricsService == nil {
		staleObjects = append(staleObjects, namedService(xtrinode, coordinatorMetricsServiceName(xtrinode)))
	}
	if resources.WorkerMetricsService == nil {
		staleObjects = append(staleObjects, namedService(xtrinode, workerMetricsServiceName(xtrinode)))
	}
	if resources.SessionPropertyConfigMap == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-session-property-config", xtrinode.Name)))
	}
	if resources.PasswordAuthSecret == nil {
		staleObjects = append(staleObjects, namedSecretForPrune(xtrinode, passwordSecretName(xtrinode)))
	}
	if resources.GroupsAuthSecret == nil {
		staleObjects = append(staleObjects, namedSecretForPrune(xtrinode, groupsSecretName(xtrinode)))
	}
	if resources.CoordinatorPDB == nil {
		staleObjects = append(staleObjects, namedPDB(xtrinode, coordinatorPDBName(xtrinode)))
	}
	if resources.WorkerPDB == nil {
		staleObjects = append(staleObjects, namedPDB(xtrinode, workerPDBName(xtrinode)))
	}
	if resources.Ingress == nil {
		staleObjects = append(staleObjects, namedIngress(xtrinode, ingressName(xtrinode)))
	}
	if resources.NetworkPolicy == nil {
		staleObjects = append(staleObjects, namedNetworkPolicy(xtrinode, config.BuildCoordinatorServiceName(xtrinode.Name)))
	}
	if resources.HorizontalPodAutoscaler == nil {
		staleObjects = append(staleObjects, namedHPA(xtrinode, config.BuildWorkerServiceName(xtrinode.Name)))
	}
	if resources.CoordinatorJMXExporterConfigMap == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, jmxExporterConfigMapName(xtrinode, "coordinator")))
	}
	if resources.WorkerJMXExporterConfigMap == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, jmxExporterConfigMapName(xtrinode, "worker")))
	}
	if resources.AccessControlConfigMapCoord == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-access-control-volume-coordinator", xtrinode.Name)))
	}
	if resources.AccessControlConfigMapWorker == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-access-control-volume-worker", xtrinode.Name)))
	}
	if resources.ResourceGroupsConfigMapCoord == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-resource-groups-volume-coordinator", xtrinode.Name)))
	}
	if resources.ResourceGroupsConfigMapWorker == nil {
		staleObjects = append(staleObjects, namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-resource-groups-volume-worker", xtrinode.Name)))
	}

	for _, obj := range staleObjects {
		if err := deleteOwnedObjectIfPresent(ctx, c, xtrinode, obj); err != nil {
			return err
		}
	}

	if resources.CoordinatorServiceMonitor == nil {
		if err := deleteOwnedObjectIfPresent(ctx, c, xtrinode, serviceMonitorObject(xtrinode, config.BuildCoordinatorServiceName(xtrinode.Name))); err != nil {
			return err
		}
	}
	if resources.WorkerServiceMonitor == nil {
		if err := deleteOwnedObjectIfPresent(ctx, c, xtrinode, serviceMonitorObject(xtrinode, fmt.Sprintf("%s-worker", config.BuildCoordinatorServiceName(xtrinode.Name)))); err != nil {
			return err
		}
	}

	return nil
}

func pruneWorkerStack(ctx context.Context, c client.Client, xtrinode *analyticsv1.XTrinode) error {
	staleObjects := []client.Object{
		namedDeployment(xtrinode, workerDeploymentName(xtrinode)),
		namedService(xtrinode, workerServiceName(xtrinode)),
		namedService(xtrinode, workerMetricsServiceName(xtrinode)),
		namedPDB(xtrinode, workerPDBName(xtrinode)),
		namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-schemas-volume-worker", xtrinode.Name)),
		namedConfigMapForPrune(xtrinode, jmxExporterConfigMapName(xtrinode, "worker")),
		namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-access-control-volume-worker", xtrinode.Name)),
		namedConfigMapForPrune(xtrinode, fmt.Sprintf("trino-%s-resource-groups-volume-worker", xtrinode.Name)),
		serviceMonitorObject(xtrinode, fmt.Sprintf("%s-worker", config.BuildCoordinatorServiceName(xtrinode.Name))),
	}
	for _, obj := range staleObjects {
		if err := deleteOwnedObjectIfPresent(ctx, c, xtrinode, obj); err != nil {
			return err
		}
	}
	return deleteOwnedConfigMapsWithPrefix(ctx, c, xtrinode, fmt.Sprintf("trino-%s-worker-", xtrinode.Name))
}

func deleteOwnedConfigMapsWithPrefix(ctx context.Context, c client.Client, xtrinode *analyticsv1.XTrinode, prefix string) error {
	configMapList := &corev1.ConfigMapList{}
	if err := c.List(ctx, configMapList, client.InNamespace(xtrinode.Namespace)); err != nil {
		return fmt.Errorf("failed to list ConfigMaps for pruning: %w", err)
	}
	for i := range configMapList.Items {
		cm := &configMapList.Items[i]
		if !strings.HasPrefix(cm.Name, prefix) || !isOwnedByXTrinode(cm, xtrinode) {
			continue
		}
		if err := c.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete stale ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
		}
	}
	return nil
}

func deleteOwnedObjectIfPresent(ctx context.Context, c client.Client, xtrinode *analyticsv1.XTrinode, obj client.Object) error {
	if obj == nil {
		return nil
	}

	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) || strings.Contains(err.Error(), "no kind is registered") {
			return nil
		}
		return fmt.Errorf("failed to get stale resource %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	if !isOwnedByXTrinode(obj, xtrinode) {
		return nil
	}
	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete stale resource %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

func isOwnedByXTrinode(obj client.Object, xtrinode *analyticsv1.XTrinode) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.APIVersion != analyticsv1.GroupVersion.String() || owner.Kind != "XTrinode" || owner.Name != xtrinode.Name {
			continue
		}
		return xtrinode.UID == "" || owner.UID == xtrinode.UID
	}
	return false
}

func namedDeployment(xtrinode *analyticsv1.XTrinode, name string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedService(xtrinode *analyticsv1.XTrinode, name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedServiceAccount(xtrinode *analyticsv1.XTrinode, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedConfigMapForPrune(xtrinode *analyticsv1.XTrinode, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedSecretForPrune(xtrinode *analyticsv1.XTrinode, name string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedPDB(xtrinode *analyticsv1.XTrinode, name string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedIngress(xtrinode *analyticsv1.XTrinode, name string) *networkingv1.Ingress {
	return &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedNetworkPolicy(xtrinode *analyticsv1.XTrinode, name string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func namedHPA(xtrinode *analyticsv1.XTrinode, name string) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: xtrinode.Namespace}}
}

func serviceMonitorObject(xtrinode *analyticsv1.XTrinode, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})
	obj.SetName(name)
	obj.SetNamespace(xtrinode.Namespace)
	return obj
}
