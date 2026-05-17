package resources

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TrinoResourceSet represents all resources for a Trino cluster
type TrinoResourceSet struct {
	CoordinatorDeployment           *appsv1.Deployment
	WorkerDeployment                *appsv1.Deployment
	CoordinatorService              *corev1.Service
	WorkerService                   *corev1.Service
	CoordinatorMetricsService       *corev1.Service                        // Metrics service for coordinator (Prometheus scraping)
	WorkerMetricsService            *corev1.Service                        // Metrics service for workers (Prometheus scraping)
	Ingress                         *networkingv1.Ingress                  // Ingress resource for external access (if configured)
	NetworkPolicy                   *networkingv1.NetworkPolicy            // NetworkPolicy for network isolation (if configured)
	HorizontalPodAutoscaler         *autoscalingv2.HorizontalPodAutoscaler // Native K8s HPA (if configured)
	CoordinatorServiceMonitor       runtime.Object                         // ServiceMonitor for coordinator (Prometheus Operator, if configured)
	WorkerServiceMonitor            runtime.Object                         // ServiceMonitor for workers (Prometheus Operator, if configured)
	CoordinatorConfigMap            *corev1.ConfigMap
	WorkerConfigMap                 *corev1.ConfigMap
	CatalogConfigMap                *corev1.ConfigMap
	ServiceAccount                  *corev1.ServiceAccount
	SessionPropertyConfigMap        *corev1.ConfigMap             // Session properties ConfigMap (if configured)
	KafkaSchemasConfigMapCoord      *corev1.ConfigMap             // Kafka schemas ConfigMap for coordinator (if configured)
	KafkaSchemasConfigMapWorker     *corev1.ConfigMap             // Kafka schemas ConfigMap for worker (if configured)
	PasswordAuthSecret              *corev1.Secret                // Password authentication Secret (if passwordAuth is provided as string)
	GroupsAuthSecret                *corev1.Secret                // Groups authentication Secret (if groups is provided as string)
	CoordinatorPDB                  *policyv1.PodDisruptionBudget // PodDisruptionBudget for coordinator (if configured)
	WorkerPDB                       *policyv1.PodDisruptionBudget // PodDisruptionBudget for workers (if configured)
	CoordinatorJMXExporterConfigMap *corev1.ConfigMap             // JMX exporter ConfigMap for coordinator (if configured)
	WorkerJMXExporterConfigMap      *corev1.ConfigMap             // JMX exporter ConfigMap for worker (if configured)
	AccessControlConfigMapCoord     *corev1.ConfigMap             // Access control ConfigMap for coordinator (if configured)
	AccessControlConfigMapWorker    *corev1.ConfigMap             // Access control ConfigMap for worker graceful shutdown (if configured)
	ResourceGroupsConfigMapCoord    *corev1.ConfigMap             // Resource groups ConfigMap for coordinator (if type == "configmap")
	ResourceGroupsConfigMapWorker   *corev1.ConfigMap             // Resource groups ConfigMap for worker (if type == "configmap")
}

// AllResources returns all resources in the set as a slice of client.Object
func (r *TrinoResourceSet) AllResources() []client.Object {
	resources := []client.Object{}
	if r.ServiceAccount != nil {
		resources = append(resources, r.ServiceAccount)
	}
	if r.CoordinatorConfigMap != nil {
		resources = append(resources, r.CoordinatorConfigMap)
	}
	if r.WorkerConfigMap != nil {
		resources = append(resources, r.WorkerConfigMap)
	}
	if r.CatalogConfigMap != nil {
		resources = append(resources, r.CatalogConfigMap)
	}
	if r.SessionPropertyConfigMap != nil {
		resources = append(resources, r.SessionPropertyConfigMap)
	}
	if r.KafkaSchemasConfigMapCoord != nil {
		resources = append(resources, r.KafkaSchemasConfigMapCoord)
	}
	if r.KafkaSchemasConfigMapWorker != nil {
		resources = append(resources, r.KafkaSchemasConfigMapWorker)
	}
	if r.PasswordAuthSecret != nil {
		resources = append(resources, r.PasswordAuthSecret)
	}
	if r.GroupsAuthSecret != nil {
		resources = append(resources, r.GroupsAuthSecret)
	}
	if r.CoordinatorService != nil {
		resources = append(resources, r.CoordinatorService)
	}
	if r.WorkerService != nil {
		resources = append(resources, r.WorkerService)
	}
	if r.CoordinatorMetricsService != nil {
		resources = append(resources, r.CoordinatorMetricsService)
	}
	if r.WorkerMetricsService != nil {
		resources = append(resources, r.WorkerMetricsService)
	}
	if r.Ingress != nil {
		resources = append(resources, r.Ingress)
	}
	if r.NetworkPolicy != nil {
		resources = append(resources, r.NetworkPolicy)
	}
	if r.HorizontalPodAutoscaler != nil {
		resources = append(resources, r.HorizontalPodAutoscaler)
	}
	if r.CoordinatorServiceMonitor != nil {
		if obj, ok := r.CoordinatorServiceMonitor.(client.Object); ok {
			resources = append(resources, obj)
		}
	}
	if r.WorkerServiceMonitor != nil {
		if obj, ok := r.WorkerServiceMonitor.(client.Object); ok {
			resources = append(resources, obj)
		}
	}
	if r.CoordinatorDeployment != nil {
		resources = append(resources, r.CoordinatorDeployment)
	}
	if r.WorkerDeployment != nil {
		resources = append(resources, r.WorkerDeployment)
	}
	if r.CoordinatorPDB != nil {
		resources = append(resources, r.CoordinatorPDB)
	}
	if r.WorkerPDB != nil {
		resources = append(resources, r.WorkerPDB)
	}
	if r.CoordinatorJMXExporterConfigMap != nil {
		resources = append(resources, r.CoordinatorJMXExporterConfigMap)
	}
	if r.WorkerJMXExporterConfigMap != nil {
		resources = append(resources, r.WorkerJMXExporterConfigMap)
	}
	if r.AccessControlConfigMapCoord != nil {
		resources = append(resources, r.AccessControlConfigMapCoord)
	}
	if r.AccessControlConfigMapWorker != nil {
		resources = append(resources, r.AccessControlConfigMapWorker)
	}
	if r.ResourceGroupsConfigMapCoord != nil {
		resources = append(resources, r.ResourceGroupsConfigMapCoord)
	}
	if r.ResourceGroupsConfigMapWorker != nil {
		resources = append(resources, r.ResourceGroupsConfigMapWorker)
	}
	return resources
}
