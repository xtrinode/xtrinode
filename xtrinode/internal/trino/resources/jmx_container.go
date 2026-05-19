package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func buildJMXExporterContainer(xtrinode *analyticsv1.XTrinode, role string) corev1.Container {
	// Default JMX exporter configuration
	jmxImage := config.DefaultJMXExporterImage
	jmxPort := jmxExporterPort(xtrinode, role)
	jmxPullPolicy := corev1.PullPolicy("Always")
	var jmxSecurityContext *corev1.SecurityContext
	var jmxResources corev1.ResourceRequirements

	// Get role-specific JMX config (coordinator or worker)
	roleJmxConfig := roleJMXValues(xtrinode, role)

	// Parse JMX exporter configuration
	if xtrinode.Spec.KEDA != nil && xtrinode.Spec.KEDA.JMXExporter != nil {
		if xtrinode.Spec.KEDA.JMXExporter.Image != "" {
			jmxImage = xtrinode.Spec.KEDA.JMXExporter.Image
		}
	}

	// Override from valuesOverlay
	if roleJmxConfig != nil {
		if exporter, ok := roleJmxConfig["exporter"].(map[string]interface{}); ok {
			if image, ok := exporter["image"].(string); ok && image != "" {
				jmxImage = image
			}
			if pullPolicy, ok := exporter["pullPolicy"].(string); ok && pullPolicy != "" {
				jmxPullPolicy = corev1.PullPolicy(pullPolicy)
			}
			if securityContextMap, ok := exporter["securityContext"].(map[string]interface{}); ok {
				securityContext, err := buildSecurityContextFromMap(securityContextMap)
				if err == nil {
					jmxSecurityContext = securityContext
				}
			}
			if resourcesMap, ok := exporter["resources"].(map[string]interface{}); ok {
				resources, err := buildResourceRequirements(resourcesMap)
				if err == nil {
					jmxResources = *resources
				}
			}
		}
	}

	container := corev1.Container{
		Name:            "jmx-exporter",
		Image:           jmxImage,
		ImagePullPolicy: jmxPullPolicy,
		Args: []string{
			fmt.Sprintf("%d", jmxPort),
			"/etc/jmx-exporter/jmx-exporter-config.yaml",
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "jmx-exporter",
				ContainerPort: jmxPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "jmx-exporter-config-volume",
				MountPath: "/etc/jmx-exporter",
			},
		},
	}

	// Add security context if specified
	if jmxSecurityContext != nil {
		container.SecurityContext = jmxSecurityContext
	}

	// Add resources if specified
	if jmxResources.Requests != nil || jmxResources.Limits != nil {
		container.Resources = jmxResources
	}

	return container
}
