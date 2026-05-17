package resources

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// ServiceMonitor represents a Prometheus ServiceMonitor resource
// Note: This is a generic object since ServiceMonitor is a CRD from prometheus-operator
// We'll use unstructured.Unstructured to represent it
type ServiceMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceMonitorSpec `json:"spec,omitempty"`
}

// ServiceMonitorSpec defines the spec for ServiceMonitor
type ServiceMonitorSpec struct {
	Selector          metav1.LabelSelector `json:"selector"`
	NamespaceSelector NamespaceSelector    `json:"namespaceSelector,omitempty"`
	Endpoints         []Endpoint           `json:"endpoints"`
}

// NamespaceSelector defines namespace selector for ServiceMonitor
type NamespaceSelector struct {
	MatchNames []string `json:"matchNames,omitempty"`
}

// Endpoint defines an endpoint for ServiceMonitor
type Endpoint struct {
	Port     string `json:"port"`
	Interval string `json:"interval,omitempty"`
	Path     string `json:"path,omitempty"`
	Scheme   string `json:"scheme,omitempty"`
}

// BuildCoordinatorServiceMonitor builds a ServiceMonitor for the coordinator.
// ServiceMonitor is enabled by default and can be disabled with helmChartConfig.serviceMonitor.enabled=false.
func BuildCoordinatorServiceMonitor(xtrinode *analyticsv1.XTrinode) runtime.Object {
	serviceMonitorSpec := effectiveServiceMonitorSpec(xtrinode)
	if serviceMonitorSpec == nil {
		return nil
	}

	// Check coordinator-specific override
	enabled := serviceMonitorSpec.Enabled
	labels := serviceMonitorSpec.Labels
	interval := serviceMonitorSpec.Interval
	apiVersion := serviceMonitorSpec.APIVersion
	scrapeTimeout := serviceMonitorSpec.ScrapeTimeout

	if serviceMonitorSpec.Coordinator != nil {
		if !serviceMonitorSpec.Coordinator.Enabled {
			return nil // Coordinator ServiceMonitor disabled
		}
		if len(serviceMonitorSpec.Coordinator.Labels) > 0 {
			// Merge labels
			if labels == nil {
				labels = make(map[string]string)
			}
			for k, v := range serviceMonitorSpec.Coordinator.Labels {
				labels[k] = v
			}
		}
		if serviceMonitorSpec.Coordinator.ScrapeTimeout != "" {
			scrapeTimeout = serviceMonitorSpec.Coordinator.ScrapeTimeout
		}
	}

	if !enabled {
		return nil
	}

	// Default values
	if apiVersion == "" {
		apiVersion = "monitoring.coreos.com/v1"
	}
	if interval == "" {
		interval = "30s"
	}

	// Scrape the metrics service port, or the JMX exporter port when enabled.
	port := "metrics"
	if jmxExporterEnabled(xtrinode, "coordinator") {
		port = "jmx-exporter"
	}

	// Build ServiceMonitor using unstructured.Unstructured (ServiceMonitor is a CRD)
	unstructuredObj := &unstructured.Unstructured{}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil
	}
	unstructuredObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    "ServiceMonitor",
	})
	unstructuredObj.SetName(config.BuildCoordinatorServiceName(xtrinode.Name))
	unstructuredObj.SetNamespace(xtrinode.Namespace)
	unstructuredObj.SetLabels(mergeLabels(TrinoLabels(xtrinode), labels))
	unstructuredObj.SetOwnerReferences([]metav1.OwnerReference{OwnerReference(xtrinode)})

	// Set spec fields using unstructured helpers
	selectorLabels := make(map[string]interface{})
	for k, v := range TrinoSelectorLabels(xtrinode, ComponentCoordinator) {
		selectorLabels[k] = v
	}
	if err := unstructured.SetNestedMap(unstructuredObj.Object, selectorLabels, "spec", "selector", "matchLabels"); err != nil {
		return nil
	}
	if err := unstructured.SetNestedStringSlice(unstructuredObj.Object, []string{xtrinode.Namespace}, "spec", "namespaceSelector", "matchNames"); err != nil {
		return nil
	}
	endpoint := map[string]interface{}{
		"port":     port,
		"interval": interval,
	}
	if scrapeTimeout != "" {
		endpoint["scrapeTimeout"] = scrapeTimeout
	}
	endpoints := []interface{}{endpoint}
	if err := unstructured.SetNestedSlice(unstructuredObj.Object, endpoints, "spec", "endpoints"); err != nil {
		return nil
	}

	return unstructuredObj
}

// BuildWorkerServiceMonitor builds a ServiceMonitor for the workers.
// ServiceMonitor is enabled by default and can be disabled with helmChartConfig.serviceMonitor.enabled=false.
func BuildWorkerServiceMonitor(xtrinode *analyticsv1.XTrinode) runtime.Object {
	serviceMonitorSpec := effectiveServiceMonitorSpec(xtrinode)
	if serviceMonitorSpec == nil {
		return nil
	}

	// Check worker-specific override
	enabled := serviceMonitorSpec.Enabled
	labels := serviceMonitorSpec.Labels
	interval := serviceMonitorSpec.Interval
	apiVersion := serviceMonitorSpec.APIVersion
	scrapeTimeout := serviceMonitorSpec.ScrapeTimeout

	if serviceMonitorSpec.Worker != nil {
		if !serviceMonitorSpec.Worker.Enabled {
			return nil // Worker ServiceMonitor disabled
		}
		if len(serviceMonitorSpec.Worker.Labels) > 0 {
			// Merge labels
			if labels == nil {
				labels = make(map[string]string)
			}
			for k, v := range serviceMonitorSpec.Worker.Labels {
				labels[k] = v
			}
		}
		if serviceMonitorSpec.Worker.ScrapeTimeout != "" {
			scrapeTimeout = serviceMonitorSpec.Worker.ScrapeTimeout
		}
	}

	if !enabled {
		return nil
	}

	// Default values
	if apiVersion == "" {
		apiVersion = "monitoring.coreos.com/v1"
	}
	if interval == "" {
		interval = "30s"
	}

	// Scrape the metrics service port, or the JMX exporter port when enabled.
	port := "metrics"
	if jmxExporterEnabled(xtrinode, "worker") {
		port = "jmx-exporter"
	}

	// Build ServiceMonitor using unstructured.Unstructured (ServiceMonitor is a CRD)
	unstructuredObj := &unstructured.Unstructured{}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil
	}
	unstructuredObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    "ServiceMonitor",
	})
	unstructuredObj.SetName(fmt.Sprintf("%s-worker", config.BuildCoordinatorServiceName(xtrinode.Name)))
	unstructuredObj.SetNamespace(xtrinode.Namespace)
	unstructuredObj.SetLabels(mergeLabels(TrinoLabels(xtrinode), labels))
	unstructuredObj.SetOwnerReferences([]metav1.OwnerReference{OwnerReference(xtrinode)})

	// Set spec fields using unstructured helpers
	selectorLabels := make(map[string]interface{})
	for k, v := range TrinoSelectorLabels(xtrinode, ComponentWorker) {
		selectorLabels[k] = v
	}
	if err := unstructured.SetNestedMap(unstructuredObj.Object, selectorLabels, "spec", "selector", "matchLabels"); err != nil {
		return nil
	}
	if err := unstructured.SetNestedStringSlice(unstructuredObj.Object, []string{xtrinode.Namespace}, "spec", "namespaceSelector", "matchNames"); err != nil {
		return nil
	}
	endpoint := map[string]interface{}{
		"port":     port,
		"interval": interval,
	}
	if scrapeTimeout != "" {
		endpoint["scrapeTimeout"] = scrapeTimeout
	}
	endpoints := []interface{}{endpoint}
	if err := unstructured.SetNestedSlice(unstructuredObj.Object, endpoints, "spec", "endpoints"); err != nil {
		return nil
	}

	return unstructuredObj
}

func effectiveServiceMonitorSpec(xtrinode *analyticsv1.XTrinode) *analyticsv1.ServiceMonitorSpec {
	if xtrinode.Spec.HelmChartConfig == nil || xtrinode.Spec.HelmChartConfig.ServiceMonitor == nil {
		return &analyticsv1.ServiceMonitorSpec{
			Enabled: true,
		}
	}
	if !xtrinode.Spec.HelmChartConfig.ServiceMonitor.Enabled {
		return nil
	}
	return xtrinode.Spec.HelmChartConfig.ServiceMonitor
}

// mergeLabels merges two label maps
func mergeLabels(base, additional map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range base {
		result[k] = v
	}
	for k, v := range additional {
		result[k] = v
	}
	return result
}
