package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// BuildServiceAccount builds the ServiceAccount
// Returns nil if serviceAccount.create is explicitly set to false
func BuildServiceAccount(xtrinode *analyticsv1.XTrinode) *corev1.ServiceAccount {
	// Check if ServiceAccount creation is explicitly disabled (default: true)
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.ServiceAccount != nil {
		if xtrinode.Spec.HelmChartConfig.ServiceAccount.Create != nil && !*xtrinode.Spec.HelmChartConfig.ServiceAccount.Create {
			return nil
		}
	}

	annotations := make(map[string]string)

	// Add annotations from HelmChartConfig if specified
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.ServiceAccount != nil {
		for k, v := range xtrinode.Spec.HelmChartConfig.ServiceAccount.Annotations {
			annotations[k] = v
		}
	}

	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            serviceAccountName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			Annotations:     annotations,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
	}
}

// serviceAccountName returns the ServiceAccount name, respecting name overrides
func serviceAccountName(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.ServiceAccount != nil {
		if xtrinode.Spec.HelmChartConfig.ServiceAccount.Name != "" {
			return xtrinode.Spec.HelmChartConfig.ServiceAccount.Name
		}
	}
	return fmt.Sprintf("trino-%s", xtrinode.Name)
}

func automountServiceAccountToken(xtrinode *analyticsv1.XTrinode) *bool {
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if serviceAccount, ok := xtrinode.Spec.GetValuesOverlayMap()["serviceAccount"].(map[string]interface{}); ok {
			if automount, ok := serviceAccount["automountServiceAccountToken"].(bool); ok {
				return &automount
			}
		}
	}

	automount := false
	if xtrinode.Spec.HelmChartConfig != nil && xtrinode.Spec.HelmChartConfig.ServiceAccount != nil {
		for annotation := range xtrinode.Spec.HelmChartConfig.ServiceAccount.Annotations {
			if isWorkloadIdentityAnnotation(annotation) {
				automount = true
				break
			}
		}
	}
	return &automount
}

func isWorkloadIdentityAnnotation(annotation string) bool {
	switch annotation {
	case "iam.gke.io/gcp-service-account",
		"eks.amazonaws.com/role-arn",
		"azure.workload.identity/client-id":
		return true
	default:
		return false
	}
}
