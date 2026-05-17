package controlauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	EnvUsername          = "XTRINODE_TRINO_CONTROL_USER"
	EnvPassword          = "XTRINODE_TRINO_CONTROL_PASSWORD" // #nosec G101 -- environment variable name, not a credential value.
	ForwardedProtoHeader = "X-Forwarded-Proto"
	ForwardedProtoHTTPS  = "https"

	httpAuthenticationTypeProperty = "http-server.authentication.type"
)

type Credential struct {
	Username    string
	Password    string
	HasPassword bool
}

func Username(xtrinode *analyticsv1.XTrinode) string {
	if xtrinode != nil && xtrinode.Spec.TrinoControlAuth != nil && xtrinode.Spec.TrinoControlAuth.Username != "" {
		return xtrinode.Spec.TrinoControlAuth.Username
	}
	return config.TrinoOperatorUser
}

func HasPasswordSecret(xtrinode *analyticsv1.XTrinode) bool {
	return xtrinode != nil &&
		xtrinode.Spec.TrinoControlAuth != nil &&
		xtrinode.Spec.TrinoControlAuth.PasswordSecret != nil
}

func HTTPAuthenticationConfigured(xtrinode *analyticsv1.XTrinode) bool {
	if xtrinode == nil {
		return false
	}
	values := xtrinode.Spec.GetValuesOverlayMap()
	if values == nil {
		return false
	}

	if server, ok := values["server"].(map[string]interface{}); ok {
		if cfg, ok := server["config"].(map[string]interface{}); ok {
			if authType, ok := cfg["authenticationType"].(string); ok && strings.TrimSpace(authType) != "" {
				return true
			}
		}
		for _, extraConfigField := range []string{"coordinatorExtraConfig", "workerExtraConfig"} {
			extraConfig, ok := server[extraConfigField].(string)
			if !ok {
				continue
			}
			for _, line := range strings.Split(extraConfig, "\n") {
				if value, found := configPropertyValue(line, httpAuthenticationTypeProperty); found && value != "" {
					return true
				}
			}
		}
	}

	props, ok := values["additionalConfigProperties"].([]interface{})
	if !ok {
		return false
	}
	for _, prop := range props {
		propStr, ok := prop.(string)
		if !ok {
			continue
		}
		if value, found := configPropertyValue(propStr, httpAuthenticationTypeProperty); found && value != "" {
			return true
		}
	}
	return false
}

func configPropertyValue(line, key string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	name, value, found := strings.Cut(line, "=")
	if !found {
		return "", false
	}
	if strings.TrimSpace(name) != key {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func CredentialFromXTrinode(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) (Credential, error) {
	credential := Credential{Username: Username(xtrinode)}
	if !HasPasswordSecret(xtrinode) {
		return credential, nil
	}
	if cli == nil {
		return credential, fmt.Errorf("kubernetes client is required to load trinoControlAuth password Secret")
	}

	selector := xtrinode.Spec.TrinoControlAuth.PasswordSecret
	secret := &corev1.Secret{}
	if err := cli.Get(ctx, types.NamespacedName{Name: selector.Name, Namespace: xtrinode.Namespace}, secret); err != nil {
		return credential, fmt.Errorf("failed to get trinoControlAuth password Secret %s/%s: %w", xtrinode.Namespace, selector.Name, err)
	}

	password, ok := secret.Data[selector.Key]
	if !ok {
		return credential, fmt.Errorf("trinoControlAuth password Secret %s/%s is missing key %q", xtrinode.Namespace, selector.Name, selector.Key)
	}
	credential.Password = string(password)
	credential.HasPassword = true
	return credential, nil
}

func ApplyRequestAuth(req *http.Request, credential Credential) {
	username := credential.Username
	if username == "" {
		username = config.TrinoOperatorUser
	}
	req.Header.Set(config.TrinoUserHeader, username)
	if credential.HasPassword {
		req.SetBasicAuth(username, credential.Password)
		if req.Header.Get(ForwardedProtoHeader) == "" {
			req.Header.Set(ForwardedProtoHeader, ForwardedProtoHTTPS)
		}
	}
}
