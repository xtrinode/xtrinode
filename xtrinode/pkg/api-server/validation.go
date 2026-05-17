package apiserver

import (
	"fmt"
	"regexp"

	"github.com/xtrinode/xtrinode/internal/config"
)

// k8sNameRegex validates Kubernetes resource names (RFC 1123 DNS subdomain)
var k8sNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// validateK8sName validates a Kubernetes resource name
func validateK8sName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > 253 {
		return fmt.Errorf("name cannot be longer than 253 characters")
	}
	if !k8sNameRegex.MatchString(name) {
		return fmt.Errorf("name must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character")
	}
	return nil
}

// validateNamespace validates a Kubernetes namespace name
func validateNamespace(namespace string) error {
	return validateK8sName(namespace)
}

// validateSize validates a runtime size
func validateSize(size string) error {
	if !config.ValidSizes[size] {
		return fmt.Errorf("invalid size: %s (must be one of: %v)", size, config.SizeList)
	}
	return nil
}

// k8sLabelKeyRegex validates Kubernetes label keys (simplified: allows dns prefix/name)
var k8sLabelKeyRegex = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*\/)?[a-zA-Z0-9]([-a-zA-Z0-9_.]*[a-zA-Z0-9])?$`)

// k8sLabelValueRegex validates Kubernetes label values
var k8sLabelValueRegex = regexp.MustCompile(`^([a-zA-Z0-9]([-a-zA-Z0-9_.]*[a-zA-Z0-9])?)?$`)

// validateLabels validates Kubernetes label key/value pairs
func validateLabels(labels map[string]string) error {
	for key, value := range labels {
		if len(key) > 253+1+63 { // prefix(253) + / + name(63)
			return fmt.Errorf("label key %q exceeds maximum length", key)
		}
		if !k8sLabelKeyRegex.MatchString(key) {
			return fmt.Errorf("label key %q is not a valid Kubernetes label key", key)
		}
		if len(value) > 63 {
			return fmt.Errorf("label value for key %q exceeds 63 characters", key)
		}
		if value != "" && !k8sLabelValueRegex.MatchString(value) {
			return fmt.Errorf("label value %q for key %q is not a valid Kubernetes label value", value, key)
		}
	}
	return nil
}
