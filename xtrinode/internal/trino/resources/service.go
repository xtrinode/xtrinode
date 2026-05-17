package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// BuildCoordinatorService builds the coordinator Service
func BuildCoordinatorService(xtrinode *analyticsv1.XTrinode) *corev1.Service {
	servicePort := trinoHTTPPort(xtrinode)

	ports := []corev1.ServicePort{
		{
			Name:       "http",
			Port:       servicePort,
			TargetPort: intstr.FromString("http"),
			Protocol:   corev1.ProtocolTCP,
		},
	}

	// Add HTTPS port if TLS enabled
	if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.ServerSecretClass != "" {
		ports = append(ports, corev1.ServicePort{
			Name:       "https",
			Port:       config.TrinoPortHTTPS,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	// Add JMX exporter port if enabled
	if jmxExporterEnabled(xtrinode, "coordinator") {
		jmxPort := jmxExporterPort(xtrinode, "coordinator")
		ports = append(ports, corev1.ServicePort{
			Name:       "jmx-exporter",
			Port:       jmxPort,
			TargetPort: intstr.FromString("jmx-exporter"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	serviceType, annotations, nodePort := getServiceConfig(xtrinode)
	// Add additional exposed ports from coordinator config
	addAdditionalExposedPorts(&ports, xtrinode, "coordinator")

	// Set nodePort on HTTP port if specified and service type supports it
	if nodePort != nil && (serviceType == corev1.ServiceTypeNodePort || serviceType == corev1.ServiceTypeLoadBalancer) {
		if len(ports) > 0 {
			ports[0].NodePort = *nodePort
		}
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            coordinatorServiceName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: corev1.ServiceSpec{
			Type:     serviceType,
			Ports:    ports,
			Selector: TrinoSelectorLabels(xtrinode, ComponentCoordinator),
		},
	}
}

// BuildWorkerService builds the worker Service
// Worker service is headless (clusterIP: None) per Helm chart pattern
func BuildWorkerService(xtrinode *analyticsv1.XTrinode) *corev1.Service {
	servicePort := trinoHTTPPort(xtrinode)
	ports := []corev1.ServicePort{
		{
			Name:       "http",
			Port:       servicePort,
			TargetPort: intstr.FromString("http"),
			Protocol:   corev1.ProtocolTCP,
		},
	}

	// Add HTTPS port if TLS enabled
	if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.ServerSecretClass != "" {
		ports = append(ports, corev1.ServicePort{
			Name:       "https",
			Port:       config.TrinoPortHTTPS,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	// Add JMX exporter port if enabled
	if jmxExporterEnabled(xtrinode, "worker") {
		jmxPort := jmxExporterPort(xtrinode, "worker")
		ports = append(ports, corev1.ServicePort{
			Name:       "jmx-exporter",
			Port:       jmxPort,
			TargetPort: intstr.FromString("jmx-exporter"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	// Add additional exposed ports from worker config
	addAdditionalExposedPorts(&ports, xtrinode, "worker")

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            workerServiceName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: config.HeadlessServiceClusterIP, // Headless service per Helm chart pattern
			Ports:     ports,
			Selector:  TrinoSelectorLabels(xtrinode, ComponentWorker),
		},
	}
}

func coordinatorServiceName(xtrinode *analyticsv1.XTrinode) string {
	return config.BuildCoordinatorServiceName(xtrinode.Name)
}

func workerServiceName(xtrinode *analyticsv1.XTrinode) string {
	return config.BuildWorkerServiceName(xtrinode.Name)
}

// BuildCoordinatorMetricsService builds a dedicated metrics service for the coordinator
// This service is headless (clusterIP: None) and includes Prometheus annotations for auto-discovery
// It exposes only the metrics port (8080 for Trino native metrics or JMX exporter port if enabled)
// Returns nil if metrics service is disabled via valuesOverlay
func BuildCoordinatorMetricsService(xtrinode *analyticsv1.XTrinode) *corev1.Service {
	// Check if metrics service is disabled via valuesOverlay
	enabled := true
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if metricsService, ok := coordinator["metricsService"].(map[string]interface{}); ok {
				if enabledVal, ok := metricsService["enabled"].(bool); ok {
					enabled = enabledVal
				}
			}
		}
	}

	if !enabled {
		return nil
	}

	// Determine metrics port: prefer JMX exporter if enabled, otherwise use Trino native metrics
	metricsPort := trinoHTTPPort(xtrinode)
	metricsPortName := "metrics"
	metricsPath := config.MetricsPath
	metricsScheme := "http"

	// Check if JMX exporter is enabled (JMX metrics take precedence)
	if jmxExporterEnabled(xtrinode, "coordinator") {
		metricsPort = jmxExporterPort(xtrinode, "coordinator")
		metricsPortName = "jmx-exporter"
	}

	// Override metrics port from valuesOverlay if specified
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if metricsService, ok := coordinator["metricsService"].(map[string]interface{}); ok {
				if portVal, ok := ParseInt32(metricsService["port"]); ok {
					metricsPort = portVal
				}
				if pathVal, ok := metricsService["path"].(string); ok {
					metricsPath = pathVal
				}
				if schemeVal, ok := metricsService["scheme"].(string); ok {
					metricsScheme = schemeVal
				}
			}
		}
	}

	// Build Prometheus annotations (following Stackable pattern)
	annotations := map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/path":   metricsPath,
		"prometheus.io/port":   fmt.Sprintf("%d", metricsPort),
		"prometheus.io/scheme": metricsScheme,
	}

	// Override annotations from valuesOverlay if specified
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if metricsService, ok := coordinator["metricsService"].(map[string]interface{}); ok {
				if annotationsVal, ok := metricsService["annotations"].(map[string]interface{}); ok {
					for k, v := range annotationsVal {
						if vStr, ok := v.(string); ok {
							annotations[k] = vStr
						}
					}
				}
			}
		}
	}

	// Build Prometheus labels from stable selector labels so ServiceMonitor can match.
	labels := TrinoSelectorLabels(xtrinode, ComponentCoordinator)
	labels["prometheus.io/scrape"] = "true"

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            coordinatorMetricsServiceName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: corev1.ServiceSpec{
			Type:                     corev1.ServiceTypeClusterIP,
			ClusterIP:                config.HeadlessServiceClusterIP, // Headless service for direct pod access
			PublishNotReadyAddresses: true,                            // Allow scraping even if pods are not ready
			Ports: []corev1.ServicePort{
				{
					Name:       metricsPortName,
					Port:       metricsPort,
					TargetPort: intstr.FromInt(int(metricsPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: TrinoSelectorLabels(xtrinode, ComponentCoordinator),
		},
	}
}

// BuildWorkerMetricsService builds a dedicated metrics service for the workers
// This service is headless (clusterIP: None) and includes Prometheus annotations for auto-discovery
// It exposes only the metrics port (8080 for Trino native metrics or JMX exporter port if enabled)
// Returns nil if metrics service is disabled via valuesOverlay
func BuildWorkerMetricsService(xtrinode *analyticsv1.XTrinode) *corev1.Service {
	// Check if metrics service is disabled via valuesOverlay
	enabled := true
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if metricsService, ok := worker["metricsService"].(map[string]interface{}); ok {
				if enabledVal, ok := metricsService["enabled"].(bool); ok {
					enabled = enabledVal
				}
			}
		}
	}

	if !enabled {
		return nil
	}

	// Determine metrics port: prefer JMX exporter if enabled, otherwise use Trino native metrics
	metricsPort := trinoHTTPPort(xtrinode)
	metricsPortName := "metrics"
	metricsPath := config.MetricsPath
	metricsScheme := "http"

	// Check if JMX exporter is enabled (JMX metrics take precedence)
	if jmxExporterEnabled(xtrinode, "worker") {
		metricsPort = jmxExporterPort(xtrinode, "worker")
		metricsPortName = "jmx-exporter"
	}

	// Override metrics port from valuesOverlay if specified
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if metricsService, ok := worker["metricsService"].(map[string]interface{}); ok {
				if portVal, ok := ParseInt32(metricsService["port"]); ok {
					metricsPort = portVal
				}
				if pathVal, ok := metricsService["path"].(string); ok {
					metricsPath = pathVal
				}
				if schemeVal, ok := metricsService["scheme"].(string); ok {
					metricsScheme = schemeVal
				}
			}
		}
	}

	// Build Prometheus annotations (following Stackable pattern)
	annotations := map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/path":   metricsPath,
		"prometheus.io/port":   fmt.Sprintf("%d", metricsPort),
		"prometheus.io/scheme": metricsScheme,
	}

	// Override annotations from valuesOverlay if specified
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if metricsService, ok := worker["metricsService"].(map[string]interface{}); ok {
				if annotationsVal, ok := metricsService["annotations"].(map[string]interface{}); ok {
					for k, v := range annotationsVal {
						if vStr, ok := v.(string); ok {
							annotations[k] = vStr
						}
					}
				}
			}
		}
	}

	// Build Prometheus labels from stable selector labels so ServiceMonitor can match.
	labels := TrinoSelectorLabels(xtrinode, ComponentWorker)
	labels["prometheus.io/scrape"] = "true"

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            workerMetricsServiceName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: corev1.ServiceSpec{
			Type:                     corev1.ServiceTypeClusterIP,
			ClusterIP:                config.HeadlessServiceClusterIP, // Headless service for direct pod access
			PublishNotReadyAddresses: true,                            // Allow scraping even if pods are not ready
			Ports: []corev1.ServicePort{
				{
					Name:       metricsPortName,
					Port:       metricsPort,
					TargetPort: intstr.FromInt(int(metricsPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: TrinoSelectorLabels(xtrinode, ComponentWorker),
		},
	}
}

// coordinatorMetricsServiceName returns the name for the coordinator metrics service
func coordinatorMetricsServiceName(xtrinode *analyticsv1.XTrinode) string {
	return config.BuildCoordinatorMetricsServiceName(xtrinode.Name)
}

// workerMetricsServiceName returns the name for the worker metrics service
func workerMetricsServiceName(xtrinode *analyticsv1.XTrinode) string {
	return config.BuildWorkerMetricsServiceName(xtrinode.Name)
}

// getServiceConfig extracts service type, annotations, and nodePort from valuesOverlay
func getServiceConfig(xtrinode *analyticsv1.XTrinode) (serviceType corev1.ServiceType, annotations map[string]string, nodePort *int32) {
	serviceType = corev1.ServiceTypeClusterIP
	annotations = make(map[string]string)

	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return serviceType, annotations, nodePort
	}

	service, ok := xtrinode.Spec.GetValuesOverlayMap()["service"].(map[string]interface{})
	if !ok {
		return serviceType, annotations, nodePort
	}

	if svcType, ok := service["type"].(string); ok {
		serviceType = corev1.ServiceType(svcType)
	}

	if svcAnnotations, ok := service["annotations"].(map[string]interface{}); ok {
		for k, v := range svcAnnotations {
			if vStr, ok := v.(string); ok {
				annotations[k] = vStr
			}
		}
	}

	if nodePortVal, ok := ParseInt32(service["nodePort"]); ok {
		nodePort = &nodePortVal
	}

	return serviceType, annotations, nodePort
}

// addAdditionalExposedPorts adds additional exposed ports from valuesOverlay
func addAdditionalExposedPorts(ports *[]corev1.ServicePort, xtrinode *analyticsv1.XTrinode, role string) {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return
	}

	var roleConfig map[string]interface{}
	var ok bool

	switch role {
	case "coordinator":
		roleConfig, ok = xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{})
	case "worker":
		roleConfig, ok = xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{})
	default:
		return
	}

	if !ok {
		return
	}

	additionalPorts, ok := roleConfig["additionalExposedPorts"].(map[string]interface{})
	if !ok {
		return
	}

	for portName, portValue := range additionalPorts {
		portMap, ok := portValue.(map[string]interface{})
		if !ok {
			continue
		}

		port, err := buildServicePortFromMap(portMap)
		if err != nil {
			continue
		}

		if port.Name == "" {
			port.Name = portName
		}

		if nodePortVal, ok := ParseInt32(portMap["nodePort"]); ok {
			port.NodePort = nodePortVal
		}

		*ports = append(*ports, port)
	}
}

// buildServicePortFromMap converts a Helm values port map to corev1.ServicePort
// Used for additionalExposedPorts configuration
func buildServicePortFromMap(portMap map[string]interface{}) (port corev1.ServicePort, err error) {
	port = corev1.ServicePort{
		Protocol: corev1.ProtocolTCP, // Default
	}

	// Parse name
	if name, ok := portMap["name"].(string); ok {
		port.Name = name
	}

	// Parse servicePort (port exposed by service)
	if svcPort, ok := ParseInt32(portMap["servicePort"]); ok {
		port.Port = svcPort
	}

	// Parse port (target port in container)
	switch v := portMap["port"].(type) {
	case int64:
		port.TargetPort = intstr.FromInt(int(v))
	case float64:
		port.TargetPort = intstr.FromInt(int(v))
	case string:
		port.TargetPort = intstr.FromString(v)
	}

	// Parse protocol
	if protocol, ok := portMap["protocol"].(string); ok {
		port.Protocol = corev1.Protocol(protocol)
	}

	// Parse nodePort (optional, only for NodePort/LoadBalancer services)
	if nodePort, ok := ParseInt32(portMap["nodePort"]); ok {
		port.NodePort = nodePort
	}

	return port, nil
}
