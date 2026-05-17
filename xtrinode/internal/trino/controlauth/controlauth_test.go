package controlauth

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCredentialFromXTrinode_LoadsPasswordSecret(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: "team-a"},
		Spec: analyticsv1.XTrinodeSpec{
			TrinoControlAuth: &analyticsv1.TrinoControlAuthSpec{
				Username: "xtrinode-operator",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "trino-control", Namespace: "team-a"},
		Data: map[string][]byte{
			"password": []byte("s3cret"),
		},
	}
	cli := fake.NewClientBuilder().WithObjects(secret).Build()

	credential, err := CredentialFromXTrinode(context.Background(), cli, xtrinode)
	require.NoError(t, err)
	assert.Equal(t, "xtrinode-operator", credential.Username)
	assert.True(t, credential.HasPassword)
	assert.Equal(t, "s3cret", credential.Password)
}

func TestCredentialFromXTrinode_DefaultsToHeaderOnlyCredential(t *testing.T) {
	credential, err := CredentialFromXTrinode(context.Background(), fake.NewClientBuilder().Build(), &analyticsv1.XTrinode{})
	require.NoError(t, err)
	assert.Equal(t, config.TrinoOperatorUser, credential.Username)
	assert.False(t, credential.HasPassword)
}

func TestApplyRequestAuth(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://trino.example/v1/query", http.NoBody)
	require.NoError(t, err)

	ApplyRequestAuth(req, Credential{Username: "control", Password: "secret", HasPassword: true})

	assert.Equal(t, "control", req.Header.Get(config.TrinoUserHeader))
	assert.Equal(t, ForwardedProtoHTTPS, req.Header.Get(ForwardedProtoHeader))
	username, password, ok := req.BasicAuth()
	assert.True(t, ok)
	assert.Equal(t, "control", username)
	assert.Equal(t, "secret", password)
}

func TestHTTPAuthenticationConfigured(t *testing.T) {
	tests := []struct {
		name          string
		valuesOverlay string
		want          bool
	}{
		{
			name: "server config authentication type",
			valuesOverlay: `{
				"server": {
					"config": {
						"authenticationType": "PASSWORD"
					}
				}
			}`,
			want: true,
		},
		{
			name: "additional config property authentication type",
			valuesOverlay: `{
				"additionalConfigProperties": [
					"internal-communication.shared-secret=test-secret",
					"http-server.authentication.type=PASSWORD"
				]
			}`,
			want: true,
		},
		{
			name: "coordinator extra config authentication type",
			valuesOverlay: `{
				"server": {
					"coordinatorExtraConfig": "query.max-memory=512MB\nhttp-server.authentication.type=PASSWORD\n"
				}
			}`,
			want: true,
		},
		{
			name: "worker extra config authentication type",
			valuesOverlay: `{
				"server": {
					"workerExtraConfig": "http-server.authentication.type=PASSWORD\n"
				}
			}`,
			want: true,
		},
		{
			name: "empty additional config property authentication value",
			valuesOverlay: `{
				"additionalConfigProperties": [
					"http-server.authentication.type="
				]
			}`,
			want: false,
		},
		{
			name:          "no auth configuration",
			valuesOverlay: `{"additionalConfigProperties":["query.max-memory=512MB"]}`,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &analyticsv1.XTrinode{
				Spec: analyticsv1.XTrinodeSpec{
					ValuesOverlay: &apiextensionsv1.JSON{Raw: []byte(tt.valuesOverlay)},
				},
			}
			assert.Equal(t, tt.want, HTTPAuthenticationConfigured(xtrinode))
		})
	}

	assert.False(t, HTTPAuthenticationConfigured(nil))
	assert.False(t, HTTPAuthenticationConfigured(&analyticsv1.XTrinode{}))
}
