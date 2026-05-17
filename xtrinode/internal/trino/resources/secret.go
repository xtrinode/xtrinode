package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// BuildPasswordAuthSecret builds a Secret for password authentication if passwordAuth is provided as a string
func BuildPasswordAuthSecret(xtrinode *analyticsv1.XTrinode) *corev1.Secret {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return nil
	}

	auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Only create secret if passwordAuth is provided as string AND passwordAuthSecret is NOT provided
	passwordAuth, hasPasswordAuth := auth["passwordAuth"].(string)
	_, hasPasswordAuthSecret := auth["passwordAuthSecret"].(string)

	if !hasPasswordAuth || hasPasswordAuthSecret || passwordAuth == "" {
		return nil
	}

	// Generate secret name (matching Helm chart pattern)
	secretName := passwordSecretName(xtrinode)

	// Secret Data field automatically base64 encodes, pass raw bytes
	// Helm chart uses b64enc but that's for the YAML template, not the actual Secret data
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            secretName,
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"password.db": []byte(passwordAuth),
		},
	}
}

// BuildGroupsAuthSecret builds a Secret for groups authentication if groups is provided as a string
func BuildGroupsAuthSecret(xtrinode *analyticsv1.XTrinode) *corev1.Secret {
	if xtrinode.Spec.GetValuesOverlayMap() == nil {
		return nil
	}

	auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Only create secret if groups is provided as string AND groupsAuthSecret is NOT provided
	groups, hasGroups := auth["groups"].(string)
	_, hasGroupsAuthSecret := auth["groupsAuthSecret"].(string)

	if !hasGroups || hasGroupsAuthSecret || groups == "" {
		return nil
	}

	// Generate secret name (matching Helm chart pattern)
	secretName := groupsSecretName(xtrinode)

	// Secret Data field automatically base64 encodes, pass raw bytes
	// Helm chart uses b64enc but that's for the YAML template, not the actual Secret data
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            secretName,
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"group.db": []byte(groups),
		},
	}
}

// passwordSecretName generates the password secret name (matching Helm chart template)
// Template: {{ template "trino.passwordSecretName" . }}
func passwordSecretName(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{}); ok {
			if passwordAuthSecret, ok := auth["passwordAuthSecret"].(string); ok && passwordAuthSecret != "" {
				// Truncate to 63 chars and trim trailing dash (Helm chart does this)
				name := passwordAuthSecret
				if len(name) > 63 {
					name = name[:63]
				}
				for name != "" && name[len(name)-1] == '-' {
					name = name[:len(name)-1]
				}
				return name
			}
		}
	}

	// Default: trino-{name}-password-file (truncated to 63 chars)
	name := fmt.Sprintf("trino-%s-password-file", xtrinode.Name)
	// Trim to 63 characters if needed
	if len(name) > 63 {
		name = name[:63]
	}
	for name != "" && name[len(name)-1] == '-' {
		name = name[:len(name)-1]
	}
	return name
}

// groupsSecretName generates the groups secret name (matching Helm chart template)
// Template: {{ template "trino.groupsSecretName" . }}
func groupsSecretName(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{}); ok {
			if groupsAuthSecret, ok := auth["groupsAuthSecret"].(string); ok && groupsAuthSecret != "" {
				return groupsAuthSecret
			}
		}
	}

	// Default: trino-{name}-groups-file (truncated to 63 chars)
	name := fmt.Sprintf("trino-%s-groups-file", xtrinode.Name)
	if len(name) > 63 {
		name = name[:63]
	}
	for name != "" && name[len(name)-1] == '-' {
		name = name[:len(name)-1]
	}
	return name
}

// GetPasswordAuthSecretName returns the secret name to use for password authentication
// This handles both direct passwordAuth strings (creates secret) and passwordAuthSecret references
func GetPasswordAuthSecretName(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{}); ok {
			// If passwordAuthSecret is provided, use it
			if passwordAuthSecret, ok := auth["passwordAuthSecret"].(string); ok && passwordAuthSecret != "" {
				return passwordAuthSecret
			}
			// If passwordAuth is provided as string, use generated secret name
			if passwordAuth, ok := auth["passwordAuth"].(string); ok && passwordAuth != "" {
				return passwordSecretName(xtrinode)
			}
		}
	}
	return ""
}

// GetGroupsAuthSecretName returns the secret name to use for groups authentication
// This handles both direct groups strings (creates secret) and groupsAuthSecret references
func GetGroupsAuthSecretName(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if auth, ok := xtrinode.Spec.GetValuesOverlayMap()["auth"].(map[string]interface{}); ok {
			// If groupsAuthSecret is provided, use it
			if groupsAuthSecret, ok := auth["groupsAuthSecret"].(string); ok && groupsAuthSecret != "" {
				return groupsAuthSecret
			}
			// If groups is provided as string, use generated secret name
			if groups, ok := auth["groups"].(string); ok && groups != "" {
				return groupsSecretName(xtrinode)
			}
		}
	}
	return ""
}
