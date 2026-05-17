package resources

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// BuildIngress builds an Ingress resource for the coordinator service
// Returns nil if ingress is disabled or not configured
func BuildIngress(xtrinode *analyticsv1.XTrinode) *networkingv1.Ingress {
	// Check if ingress is enabled via HelmChartConfig
	if xtrinode.Spec.HelmChartConfig == nil || xtrinode.Spec.HelmChartConfig.Ingress == nil {
		return nil
	}

	ingressSpec := xtrinode.Spec.HelmChartConfig.Ingress
	if !ingressSpec.Enabled {
		return nil
	}

	// Build ingress rules from hosts
	var rules []networkingv1.IngressRule
	for _, hostSpec := range ingressSpec.Hosts {
		rule := networkingv1.IngressRule{
			Host: hostSpec.Host,
		}

		// Build paths
		var paths []networkingv1.HTTPIngressPath
		if len(hostSpec.Paths) > 0 {
			for _, pathSpec := range hostSpec.Paths {
				pathType := networkingv1.PathTypePrefix
				if pathSpec.PathType != "" {
					switch pathSpec.PathType {
					case "Exact":
						pathType = networkingv1.PathTypeExact
					case "Prefix":
						pathType = networkingv1.PathTypePrefix
					case "ImplementationSpecific":
						pathType = networkingv1.PathTypeImplementationSpecific
					}
				}

				// Determine backend service port (prefer HTTPS if TLS enabled, otherwise HTTP)
				backendPort := intstr.FromInt(int(trinoHTTPPort(xtrinode)))
				if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.ServerSecretClass != "" {
					backendPort = intstr.FromInt(config.TrinoPortHTTPS)
				}

				paths = append(paths, networkingv1.HTTPIngressPath{
					Path:     pathSpec.Path,
					PathType: &pathType,
					Backend: networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{
							Name: coordinatorServiceName(xtrinode),
							Port: networkingv1.ServiceBackendPort{
								Number: backendPort.IntVal,
							},
						},
					},
				})
			}
		} else {
			// Default path if no paths specified
			pathType := networkingv1.PathTypePrefix
			backendPort := intstr.FromInt(int(trinoHTTPPort(xtrinode)))
			if xtrinode.Spec.TLS != nil && xtrinode.Spec.TLS.ServerSecretClass != "" {
				backendPort = intstr.FromInt(config.TrinoPortHTTPS)
			}

			paths = append(paths, networkingv1.HTTPIngressPath{
				Path:     "/",
				PathType: &pathType,
				Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{
						Name: coordinatorServiceName(xtrinode),
						Port: networkingv1.ServiceBackendPort{
							Number: backendPort.IntVal,
						},
					},
				},
			})
		}

		rule.IngressRuleValue = networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: paths,
			},
		}

		rules = append(rules, rule)
	}

	// Build TLS configuration
	var tls []networkingv1.IngressTLS
	for _, tlsSpec := range ingressSpec.TLS {
		ingressTLS := networkingv1.IngressTLS{
			SecretName: tlsSpec.SecretName,
		}
		if len(tlsSpec.Hosts) > 0 {
			ingressTLS.Hosts = tlsSpec.Hosts
		}
		tls = append(tls, ingressTLS)
	}

	// Build annotations
	annotations := make(map[string]string)
	if len(ingressSpec.Annotations) > 0 {
		for k, v := range ingressSpec.Annotations {
			annotations[k] = v
		}
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:            ingressName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: networkingv1.IngressSpec{
			Rules: rules,
		},
	}

	// Set ingressClassName if specified
	if ingressSpec.ClassName != "" {
		ingress.Spec.IngressClassName = &ingressSpec.ClassName
	}

	// Set TLS if configured
	if len(tls) > 0 {
		ingress.Spec.TLS = tls
	}

	return ingress
}

// ingressName returns the name for the Ingress resource
func ingressName(xtrinode *analyticsv1.XTrinode) string {
	return config.BuildCoordinatorServiceName(xtrinode.Name)
}
