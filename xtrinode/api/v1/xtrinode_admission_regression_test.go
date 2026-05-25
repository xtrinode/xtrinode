package v1

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type stubValuesOverlayAuthorizer struct {
	allowed   bool
	reason    string
	err       error
	called    bool
	namespace string
	username  string
}

func (s *stubValuesOverlayAuthorizer) Allowed(_ context.Context, req *admission.Request, namespace string) (allowed bool, reason string, err error) {
	s.called = true
	s.namespace = namespace
	s.username = req.UserInfo.Username
	return s.allowed, s.reason, s.err
}

type stubCatalogSecretAuthorizer struct {
	allowed    bool
	reason     string
	err        error
	calls      int
	namespace  string
	secretName string
	username   string
}

func (s *stubCatalogSecretAuthorizer) CanGet(_ context.Context, req *admission.Request, namespace, secretName string) (allowed bool, reason string, err error) {
	s.calls++
	s.namespace = namespace
	s.secretName = secretName
	s.username = req.UserInfo.Username
	return s.allowed, s.reason, s.err
}

func TestXTrinodeCatalog_ValidateCreate_ConnectorCardinality(t *testing.T) {
	tests := []struct {
		name    string
		catalog *XTrinodeCatalog
		wantErr bool
	}{
		{
			name: "valid catalog with one connector",
			catalog: &XTrinodeCatalog{
				ObjectMeta: metav1.ObjectMeta{Name: "tpch"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{
						TPCH: &TPCHCatalogSpec{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing connector",
			catalog: &XTrinodeCatalog{
				ObjectMeta: metav1.ObjectMeta{Name: "empty"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{},
				},
			},
			wantErr: true,
		},
		{
			name: "multiple connectors",
			catalog: &XTrinodeCatalog{
				ObjectMeta: metav1.ObjectMeta{Name: "ambiguous"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{
						TPCH:   &TPCHCatalogSpec{},
						System: &SystemCatalogSpec{},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.catalog.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "exactly one connector field must be set")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestXTrinodeCatalog_ValidateCreate_SpecLabels(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-labels"},
		Spec: XTrinodeCatalogSpec{
			Labels: map[string]string{
				"bad/key/prefix": "value",
				"team":           strings.Repeat("x", 64),
			},
			Connector: XTrinodeCatalogConnector{
				TPCH: &TPCHCatalogSpec{},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.labels")
}

func TestXTrinodeCatalog_ValidateCreate_HiveS3CredentialsRequireSecretPair(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "hive"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Hive: &HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	catalog.Spec.Connector.Hive.S3AccessKeySecret = &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "s3-creds"},
		Key:                  "access-key",
	}
	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "s3AccessKeySecret and s3SecretKeySecret must be set together")

	catalog.Spec.Connector.Hive.S3SecretKeySecret = &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "s3-creds"},
		Key:                  "secret-key",
	}
	_, err = catalog.ValidateCreate()
	assert.NoError(t, err)
}

func TestXTrinodeCatalog_ValidateCreate_HiveRejectsPlaintextS3CredentialsInProperties(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "hive"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Hive: &HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
					Properties: map[string]string{
						"hive.allow-drop-table":   "true",
						"s3.aws-secret-key":       "plaintext-secret",
						" HIVE.S3.AWS-ACCESS-KEY": "plaintext-access", //nolint:gocritic
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "s3.aws-secret-key")
	assert.Contains(t, err.Error(), "HIVE.S3.AWS-ACCESS-KEY")
	assert.Contains(t, err.Error(), "use s3SecretKeySecret")
	assert.Contains(t, err.Error(), "use s3AccessKeySecret")
	assert.NotContains(t, err.Error(), "hive.allow-drop-table")
}

func TestXTrinodeCatalog_ValidateCreate_JDBCRejectsPlaintextSensitiveProperties(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://postgres:5432/analytics",
					Properties: map[string]string{ // #nosec G101 -- validates rejection of plaintext credential-like properties.
						"connection-password": "plaintext-password",
						"ssl":                 "true",
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.postgres.properties[connection-password]")
	assert.Contains(t, err.Error(), "use connectionPasswordSecret")
	assert.NotContains(t, err.Error(), "ssl")
}

func TestXTrinodeCatalog_ValidateCreate_BigQueryRejectsPlaintextCredentialProperty(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "bigquery"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				BigQuery: &BigQueryCatalogSpec{ // #nosec G101 -- validates ambiguous credential configuration.
					ProjectID: "analytics-project",
					Properties: map[string]string{ // #nosec G101 -- validates rejection of plaintext credential-like properties.
						"bigquery.credentials-key": `{"private_key":"plaintext"}`,
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.bigQuery.properties[bigquery.credentials-key]")
	assert.Contains(t, err.Error(), "sensitive catalog properties")
}

func TestXTrinodeCatalog_ValidateCreate_BigQueryCredentialsFileMustBeMountedPath(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantMessage string
	}{
		{
			name:        "reject inline json key",
			value:       `{"private_key":"plaintext"}`,
			wantMessage: "inline credential material must use propertySecretRefs instead of credentialsFile",
		},
		{
			name:        "reject relative path",
			value:       "key.json",
			wantMessage: "credentials file path must be an absolute mounted file path",
		},
		{
			name:        "reject newline",
			value:       "/etc/trino/key.json\nanother",
			wantMessage: "credentials file path must not contain control characters or newlines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := &XTrinodeCatalog{
				ObjectMeta: metav1.ObjectMeta{Name: "bigquery"},
				Spec: XTrinodeCatalogSpec{
					Connector: XTrinodeCatalogConnector{
						BigQuery: &BigQueryCatalogSpec{ // #nosec G101 -- validates ambiguous credential configuration.
							ProjectID:       "analytics-project",
							CredentialsFile: tt.value,
						},
					},
				},
			}

			_, err := catalog.ValidateCreate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "spec.connector.bigQuery.credentialsFile")
			assert.Contains(t, err.Error(), tt.wantMessage)
		})
	}
}

func TestXTrinodeCatalog_ValidateCreate_BigQueryRejectsAmbiguousCredentialModes(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "bigquery"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				// #nosec G101 -- validates ambiguous credential configuration.
				BigQuery: &BigQueryCatalogSpec{
					ProjectID:       "analytics-project",
					CredentialsFile: "/etc/trino/secrets/bigquery/key.json",
					CatalogPropertySecretRefs: CatalogPropertySecretRefs{
						PropertySecretRefs: map[string]corev1.SecretKeySelector{
							"bigquery.credentials-key": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "bigquery-credentials"},
								Key:                  "credentials-key",
							},
						},
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.bigQuery.credentialsFile")
	assert.Contains(t, err.Error(), "use either credentialsFile or propertySecretRefs[bigquery.credentials-key], not both")
}

func TestXTrinodeCatalog_ValidateCreate_GenericPropertySecretRefsAllowSensitiveProperties(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "cassandra"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Cassandra: &CassandraCatalogSpec{
					ContactPoints: "cassandra.default.svc.cluster.local",
					CatalogPropertySecretRefs: CatalogPropertySecretRefs{
						PropertySecretRefs: map[string]corev1.SecretKeySelector{
							"cassandra.password": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "cassandra-credentials"},
								Key:                  "password",
							},
							"cassandra.client.read-timeout": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "cassandra-settings"},
								Key:                  "read-timeout",
							},
						},
					},
					Properties: map[string]string{ // #nosec G101 -- validates rejection of plaintext credential-like properties.
						"cassandra.load-policy.use-dc-aware": "true",
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.NoError(t, err)
}

func TestXTrinodeCatalog_ValidateCreate_GenericPropertySecretRefsValidateSelectorShape(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "elasticsearch"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Elasticsearch: &ElasticsearchCatalogSpec{
					Host: "elasticsearch.default.svc.cluster.local",
					CatalogPropertySecretRefs: CatalogPropertySecretRefs{
						PropertySecretRefs: map[string]corev1.SecretKeySelector{
							"elasticsearch.auth.password": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "es-credentials"},
							},
						},
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.elasticsearch.propertySecretRefs[elasticsearch.auth.password].key")
}

func TestXTrinodeCatalog_ValidateCreate_CustomRejectsPlaintextSensitiveProperties(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "custom"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Custom: &CustomCatalogSpec{
					ConnectorName: "custom",
					// #nosec G101 -- validates rejection of plaintext credential-like properties.
					Properties: map[string]string{
						"client.secret":    "plaintext-secret",
						"credentials-json": `{"client_email":"trino@example.com","private_key":"plaintext"}`,
						"custom.property":  "safe",
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.custom.properties[client.secret]")
	assert.Contains(t, err.Error(), "spec.connector.custom.properties[credentials-json]")
	assert.Contains(t, err.Error(), "sensitive catalog properties")
	assert.NotContains(t, err.Error(), "custom.property")
}

func TestXTrinodeCatalog_ValidateCreate_RejectsCredentialJSONInGenericPropertyValue(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "custom"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Custom: &CustomCatalogSpec{
					ConnectorName: "custom",
					Properties: map[string]string{
						"config": `{"type":"service_account","client_secret":"plaintext"}`,
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.custom.properties[config]")
	assert.Contains(t, err.Error(), "sensitive catalog properties")
}

func TestXTrinodeCatalog_ValidateCreate_AllowsSafeCustomProperties(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "custom"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Custom: &CustomCatalogSpec{
					ConnectorName: "custom",
					Properties: map[string]string{
						"safe.property": "enabled",
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.NoError(t, err)
}

func TestXTrinodeCatalog_ValidateCreate_RejectsGeneratedCatalogProperties(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "cassandra"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Cassandra: &CassandraCatalogSpec{
					ContactPoints: "cassandra.default.svc.cluster.local",
					Port:          9042,
					Properties: map[string]string{
						"connector.name":                     "memory",
						"cassandra.contact-points":           "other.default.svc.cluster.local",
						"cassandra.native-protocol-port":     "9142",
						"cassandra.load-policy.use-dc-aware": "true",
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.cassandra.properties[connector.name]")
	assert.Contains(t, err.Error(), "spec.connector.cassandra.properties[cassandra.contact-points]")
	assert.Contains(t, err.Error(), "spec.connector.cassandra.properties[cassandra.native-protocol-port]")
	assert.Contains(t, err.Error(), "generated from typed connector fields")
	assert.NotContains(t, err.Error(), "cassandra.load-policy.use-dc-aware")
}

func TestXTrinodeCatalog_ValidateCreate_RejectsGeneratedCatalogPropertySecretRefs(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				MySQL: &MySQLCatalogSpec{
					ConnectionURL: "jdbc:mysql://mysql:3306/db",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "mysql-password"},
						Key:                  "password",
					},
					CatalogPropertySecretRefs: CatalogPropertySecretRefs{
						PropertySecretRefs: map[string]corev1.SecretKeySelector{
							"connector.name": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "override"},
								Key:                  "connector",
							},
							"connection-password": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "override"},
								Key:                  "password",
							},
						},
					},
				},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.mysql.propertySecretRefs[connector.name]")
	assert.Contains(t, err.Error(), "spec.connector.mysql.propertySecretRefs[connection-password]")
	assert.Contains(t, err.Error(), "generated from")
}

func TestXTrinodeCatalog_ValidateCreate_RequiredConnectorFields(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{},
			},
		},
	}

	_, err := catalog.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connectionURL")
}

func TestXTrinodeCatalog_ValidateUpdate_ConnectorIdentityImmutable(t *testing.T) {
	old := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				TPCH: &TPCHCatalogSpec{},
			},
		},
	}

	updated := old.DeepCopy()
	updated.Spec.Connector = XTrinodeCatalogConnector{System: &SystemCatalogSpec{}}

	_, err := updated.ValidateUpdate(old)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connector identity is immutable")

	updated = old.DeepCopy()
	updated.Spec.Labels = map[string]string{"team": "analytics"}
	_, err = updated.ValidateUpdate(old)
	assert.NoError(t, err)
}

func TestXTrinodeCatalog_ValidateUpdate_CustomConnectorNameImmutable(t *testing.T) {
	old := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Custom: &CustomCatalogSpec{
					ConnectorName: "postgresql",
				},
			},
		},
	}

	updated := old.DeepCopy()
	updated.Spec.Connector.Custom.ConnectorName = "mysql"

	_, err := updated.ValidateUpdate(old)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "custom:postgresql")
	assert.Contains(t, err.Error(), "custom:mysql")
}

func TestXTrinode_ValidateCreate_ValuesOverlayWarns(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "overlay"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"containerSecurityContext": map[string]interface{}{
					"runAsNonRoot": true,
				},
			}),
		},
	}

	warnings, err := xtrinode.ValidateCreate()
	assert.NoError(t, err)
	assert.Contains(t, warnings, buildValuesOverlayChangeWarning())
}

func TestXTrinodeWebhook_ValidateCreate_ValuesOverlayRequiresPrivilegedSubject(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "overlay", Namespace: "team-a"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"securityContext": map[string]interface{}{
					"runAsUser": 0,
				},
			}),
		},
	}
	authorizer := &stubValuesOverlayAuthorizer{
		allowed: false,
		reason:  "tenant role denied",
	}
	hook := &XTrinodeWebhook{valuesOverlayAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Username: "alice",
				Groups:   []string{"tenant"},
			},
		},
	})

	warnings, err := hook.ValidateCreate(ctx, xtrinode)
	assert.Error(t, err)
	assert.Contains(t, warnings, buildValuesOverlayChangeWarning())
	assert.Contains(t, err.Error(), "spec.valuesOverlay")
	assert.Contains(t, err.Error(), "xtrinodes/status")
	assert.Contains(t, err.Error(), "tenant role denied")
	assert.True(t, authorizer.called)
	assert.Equal(t, "team-a", authorizer.namespace)
	assert.Equal(t, "alice", authorizer.username)
}

func TestXTrinodeWebhook_ValidateCreate_ValuesOverlayAllowsPrivilegedSubject(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "overlay", Namespace: "team-a"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"worker": map[string]interface{}{
					"terminationGracePeriodSeconds": 30,
				},
			}),
		},
	}
	authorizer := &stubValuesOverlayAuthorizer{allowed: true}
	hook := &XTrinodeWebhook{valuesOverlayAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "platform-admin"},
		},
	})

	warnings, err := hook.ValidateCreate(ctx, xtrinode)
	assert.NoError(t, err)
	assert.Contains(t, warnings, buildValuesOverlayChangeWarning())
	assert.True(t, authorizer.called)
}

func TestXTrinodeWebhook_ValidateUpdate_ValuesOverlayUnchangedDoesNotRequirePrivilegedSubject(t *testing.T) {
	old := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "overlay", Namespace: "team-a"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"worker": map[string]interface{}{
					"terminationGracePeriodSeconds": 30,
				},
			}),
		},
	}
	updated := old.DeepCopy()
	updated.Spec.Suspended = true
	authorizer := &stubValuesOverlayAuthorizer{allowed: false}
	hook := &XTrinodeWebhook{valuesOverlayAuthorizer: authorizer}

	_, err := hook.ValidateUpdate(context.Background(), old, updated)
	assert.NoError(t, err)
	assert.False(t, authorizer.called)
}

func TestXTrinodeWebhook_ValidateCreate_HelmPodSpecFieldsRequirePrivilegedSubject(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "helm-privileged", Namespace: "team-a"},
		Spec: XTrinodeSpec{
			Size: "s",
			HelmChartConfig: &HelmChartConfigSpec{
				EnvFrom: []corev1.EnvFromSource{
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "tenant-secret"},
						},
					},
				},
			},
		},
	}
	authorizer := &stubValuesOverlayAuthorizer{
		allowed: false,
		reason:  "tenant role denied",
	}
	hook := &XTrinodeWebhook{valuesOverlayAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "alice"},
		},
	})

	_, err := hook.ValidateCreate(ctx, xtrinode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.helmChartConfig")
	assert.Contains(t, err.Error(), "xtrinodes/status")
	assert.True(t, authorizer.called)
}

func TestXTrinodeWebhook_ValidateCreate_HelmPolicyExposureFieldsRequirePrivilegedSubject(t *testing.T) {
	tests := []struct {
		name string
		hcc  *HelmChartConfigSpec
	}{
		{
			name: "access control",
			hcc: &HelmChartConfigSpec{
				AccessControl: &AccessControlSpec{
					Type:       "properties",
					Properties: "access-control.name=allow-all",
				},
			},
		},
		{
			name: "ingress",
			hcc: &HelmChartConfigSpec{
				Ingress: &IngressSpec{
					Enabled: true,
					Hosts: []IngressHostSpec{
						{
							Host: "trino.example.test",
							Paths: []IngressPathSpec{
								{Path: "/", PathType: "Prefix"},
							},
						},
					},
				},
			},
		},
		{
			name: "network policy",
			hcc: &HelmChartConfigSpec{
				NetworkPolicy: &NetworkPolicySpec{Enabled: true},
			},
		},
		{
			name: "service monitor",
			hcc: &HelmChartConfigSpec{
				ServiceMonitor: &ServiceMonitorSpec{
					Enabled:  true,
					Interval: "30s",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "helm-policy", Namespace: "team-a"},
				Spec: XTrinodeSpec{
					Size:            "s",
					HelmChartConfig: tt.hcc,
				},
			}
			authorizer := &stubValuesOverlayAuthorizer{
				allowed: false,
				reason:  "tenant role denied",
			}
			hook := &XTrinodeWebhook{valuesOverlayAuthorizer: authorizer}
			ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UserInfo: authenticationv1.UserInfo{Username: "alice"},
				},
			})

			_, err := hook.ValidateCreate(ctx, xtrinode)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "spec.helmChartConfig")
			assert.Contains(t, err.Error(), "xtrinodes/status")
			assert.True(t, authorizer.called)
		})
	}
}

func TestXTrinodeCatalogWebhook_ValidateCreate_SecretReferenceRequiresGetPermission(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "team-a"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{
					ConnectionURL:  "jdbc:postgresql://postgres:5432/analytics",
					ConnectionUser: "trino",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "postgres-credentials"},
						Key:                  "password",
					},
				},
			},
		},
	}
	authorizer := &stubCatalogSecretAuthorizer{
		allowed: false,
		reason:  "tenant cannot read secrets",
	}
	hook := &XTrinodeCatalogWebhook{secretAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "alice"},
		},
	})

	_, err := hook.ValidateCreate(ctx, catalog)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.postgres.connectionPasswordSecret")
	assert.Contains(t, err.Error(), "postgres-credentials")
	assert.Contains(t, err.Error(), "requires get permission")
	assert.Contains(t, err.Error(), "tenant cannot read secrets")
	assert.Equal(t, 1, authorizer.calls)
	assert.Equal(t, "team-a", authorizer.namespace)
	assert.Equal(t, "postgres-credentials", authorizer.secretName)
	assert.Equal(t, "alice", authorizer.username)
}

func TestXTrinodeCatalogWebhook_ValidateCreate_PropertySecretRefRequiresGetPermission(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "cassandra", Namespace: "team-a"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Cassandra: &CassandraCatalogSpec{
					ContactPoints: "cassandra.default.svc.cluster.local",
					CatalogPropertySecretRefs: CatalogPropertySecretRefs{
						PropertySecretRefs: map[string]corev1.SecretKeySelector{
							"cassandra.password": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "cassandra-credentials"},
								Key:                  "password",
							},
						},
					},
				},
			},
		},
	}
	authorizer := &stubCatalogSecretAuthorizer{
		allowed: false,
		reason:  "tenant cannot read secrets",
	}
	hook := &XTrinodeCatalogWebhook{secretAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "alice"},
		},
	})

	_, err := hook.ValidateCreate(ctx, catalog)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.cassandra.propertySecretRefs[cassandra.password]")
	assert.Contains(t, err.Error(), "cassandra-credentials")
	assert.Contains(t, err.Error(), "requires get permission")
	assert.Equal(t, 1, authorizer.calls)
	assert.Equal(t, "team-a", authorizer.namespace)
	assert.Equal(t, "cassandra-credentials", authorizer.secretName)
}

func TestXTrinodeCatalogWebhook_ValidateCreate_SecretReferenceAllowsSecretReader(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "team-a"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://postgres:5432/analytics",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "postgres-credentials"},
						Key:                  "password",
					},
				},
			},
		},
	}
	authorizer := &stubCatalogSecretAuthorizer{allowed: true}
	hook := &XTrinodeCatalogWebhook{secretAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "platform-admin"},
		},
	})

	_, err := hook.ValidateCreate(ctx, catalog)
	assert.NoError(t, err)
	assert.Equal(t, 1, authorizer.calls)
}

func TestXTrinodeCatalogWebhook_ValidateCreate_SecretReferenceRequiresAdmissionRequest(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "team-a"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://postgres:5432/analytics",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "postgres-credentials"},
						Key:                  "password",
					},
				},
			},
		},
	}
	hook := &XTrinodeCatalogWebhook{secretAuthorizer: &stubCatalogSecretAuthorizer{allowed: true}}

	_, err := hook.ValidateCreate(context.Background(), catalog)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "catalog Secret references require admission request user info")
}

func TestXTrinodeCatalogWebhook_ValidateUpdate_SecretReferenceRequiresGetPermission(t *testing.T) {
	oldCatalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "team-a"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				Postgres: &PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://postgres:5432/analytics",
				},
			},
		},
	}
	updated := oldCatalog.DeepCopy()
	updated.Spec.Connector.Postgres.ConnectionPasswordSecret = &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "postgres-credentials"},
		Key:                  "password",
	}
	authorizer := &stubCatalogSecretAuthorizer{
		allowed: false,
		reason:  "tenant cannot read secrets",
	}
	hook := &XTrinodeCatalogWebhook{secretAuthorizer: authorizer}
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "alice"},
		},
	})

	_, err := hook.ValidateUpdate(ctx, oldCatalog, updated)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.connector.postgres.connectionPasswordSecret")
	assert.Equal(t, 1, authorizer.calls)
}

func TestXTrinode_ValidateCreate_TrinoControlAuth(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "control-auth"},
		Spec: XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &TrinoControlAuthSpec{
				Username: "bad:user",
			},
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.trinoControlAuth.username")
	assert.Contains(t, err.Error(), "spec.trinoControlAuth.passwordSecret")

	xtrinode.Spec.TrinoControlAuth = &TrinoControlAuthSpec{
		Username: "xtrinode-operator",
		PasswordSecret: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
			Key:                  "password",
		},
	}
	_, err = xtrinode.ValidateCreate()
	assert.NoError(t, err)
}

func TestXTrinode_ValidateCreate_TrinoAuthRequiresControlCredential(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "trino-auth"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"server": map[string]interface{}{
					"config": map[string]interface{}{
						"authenticationType": "PASSWORD",
					},
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.trinoControlAuth")
	assert.Contains(t, err.Error(), "internal-communication.shared-secret")

	xtrinode.Spec.TrinoControlAuth = &TrinoControlAuthSpec{
		Username: "xtrinode-operator",
		PasswordSecret: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
			Key:                  "password",
		},
	}
	xtrinode.Spec.ValuesOverlay = valuesOverlayFromMap(map[string]interface{}{
		"server": map[string]interface{}{
			"config": map[string]interface{}{
				"authenticationType": "PASSWORD",
			},
		},
		"additionalConfigProperties": []interface{}{
			"internal-communication.shared-secret=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	})
	_, err = xtrinode.ValidateCreate()
	assert.NoError(t, err)
}

func TestXTrinode_ValidateCreate_TrinoAuthRejectsUnsupportedLifecycleAuthType(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "trino-auth"},
		Spec: XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &TrinoControlAuthSpec{
				Username: "xtrinode-operator",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"server": map[string]interface{}{
					"config": map[string]interface{}{
						"authenticationType": "JWT",
					},
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unsupported value: \"JWT\"")
	assert.Contains(t, err.Error(), "supported values: \"PASSWORD\"")
}

func TestXTrinode_ValidateCreate_TrinoAuthRejectsUnsupportedRawConfigAuthType(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "trino-auth"},
		Spec: XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &TrinoControlAuthSpec{
				Username: "xtrinode-operator",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"additionalConfigProperties": []interface{}{
					"internal-communication.shared-secret=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					"http-server.authentication.type=JWT",
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.valuesOverlay.additionalConfigProperties")
	assert.Contains(t, err.Error(), "Unsupported value: \"JWT\"")
	assert.Contains(t, err.Error(), "supported values: \"PASSWORD\"")
}

func TestXTrinode_ValidateCreate_TrinoAuthRejectsUnsupportedExtraConfigAuthType(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "trino-auth"},
		Spec: XTrinodeSpec{
			Size: "s",
			TrinoControlAuth: &TrinoControlAuthSpec{
				Username: "xtrinode-operator",
				PasswordSecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "trino-control"},
					Key:                  "password",
				},
			},
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"additionalConfigProperties": []interface{}{
					"internal-communication.shared-secret=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				},
				"server": map[string]interface{}{
					"coordinatorExtraConfig": "http-server.authentication.type=JWT\n",
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.valuesOverlay.server.coordinatorExtraConfig")
	assert.Contains(t, err.Error(), "Unsupported value: \"JWT\"")
}

func TestXTrinode_ValidateCreate_RawPasswordAuthRequiresControlCredential(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "trino-auth"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"additionalConfigProperties": []interface{}{
					"http-server.authentication.type=PASSWORD",
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.trinoControlAuth")
	assert.Contains(t, err.Error(), "internal-communication.shared-secret")
}

func TestXTrinode_ValidateCreate_TLSServerRejectedUntilHTTPSControlSupported(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-control"},
		Spec: XTrinodeSpec{
			Size: "s",
			TLS: &TLSSpec{
				ServerSecretClass:   "server-tls",
				InternalSecretClass: "internal-tls",
			},
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.tls.serverSecretClass")
	assert.Contains(t, err.Error(), "HTTP-only coordinator URLs")
}

func TestXTrinode_ValidateCreate_HTTPListenerCannotBeDisabled(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "http-disabled"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"additionalConfigProperties": []interface{}{
					"http-server.http.enabled=false",
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.valuesOverlay.additionalConfigProperties")
	assert.Contains(t, err.Error(), "HTTP listener must stay enabled")
}

func TestXTrinode_ValidateCreate_HTTPPortRawOverrideRejected(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "http-port"},
		Spec: XTrinodeSpec{
			Size: "s",
			ValuesOverlay: valuesOverlayFromMap(map[string]interface{}{
				"server": map[string]interface{}{
					"coordinatorExtraConfig": "http-server.http.port=8181\n",
				},
			}),
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec.valuesOverlay.server.coordinatorExtraConfig")
	assert.Contains(t, err.Error(), "valuesOverlay.service.port")
}

func TestXTrinode_ValidateCreate_SelfManagedNodeTaintsRejected(t *testing.T) {
	xtrinode := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "self-managed-taints"},
		Spec: XTrinodeSpec{
			Size: "s",
			NodePool: &NodePoolSpec{
				Provider:          "aws",
				ProviderMode:      "self-managed",
				KubernetesVersion: "v1.28.0",
				AWS: &AWSNodePoolSpec{
					InstanceType: "m5.xlarge",
				},
				BootstrapConfigRef: &corev1.ObjectReference{
					APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
					Kind:       "KubeadmConfigTemplate",
					Name:       "worker-bootstrap",
				},
				NodeTaints: []corev1.Taint{
					{Key: "dedicated", Value: "trino", Effect: corev1.TaintEffectNoSchedule},
				},
			},
		},
	}

	_, err := xtrinode.ValidateCreate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nodeRegistration.taints")
}

func TestXTrinodeCatalog_ValidateUpdate_InvalidOldObject(t *testing.T) {
	catalog := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "tpch"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				TPCH: &TPCHCatalogSpec{},
			},
		},
	}

	_, err := catalog.ValidateUpdate(&XTrinode{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected old object to be of type XTrinodeCatalog")
}

func TestXTrinodeCatalogWebhook_Adapters(t *testing.T) {
	hook := &XTrinodeCatalogWebhook{}
	valid := &XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "tpch"},
		Spec: XTrinodeCatalogSpec{
			Connector: XTrinodeCatalogConnector{
				TPCH: &TPCHCatalogSpec{},
			},
		},
	}

	_, err := hook.ValidateCreate(context.Background(), valid)
	assert.NoError(t, err)

	_, err = hook.ValidateCreate(context.Background(), &XTrinode{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected XTrinodeCatalog")

	_, err = hook.ValidateUpdate(context.Background(), valid, valid.DeepCopy())
	assert.NoError(t, err)

	_, err = hook.ValidateUpdate(context.Background(), valid, &XTrinode{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected new XTrinodeCatalog")

	_, err = hook.ValidateDelete(context.Background(), valid)
	assert.NoError(t, err)

	_, err = hook.ValidateDelete(context.Background(), &XTrinode{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected XTrinodeCatalog")
}

func TestXTrinodeWebhook_Adapters(t *testing.T) {
	hook := &XTrinodeWebhook{}
	valid := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime"},
		Spec: XTrinodeSpec{
			Size: "xs",
		},
	}

	err := hook.Default(context.Background(), valid)
	assert.NoError(t, err)
	assert.NotNil(t, valid.Spec.MinWorkers)

	err = hook.Default(context.Background(), &XTrinodeCatalog{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected XTrinode")

	_, err = hook.ValidateCreate(context.Background(), valid)
	assert.NoError(t, err)

	_, err = hook.ValidateCreate(context.Background(), &XTrinodeCatalog{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected XTrinode")

	_, err = hook.ValidateUpdate(context.Background(), valid, valid.DeepCopy())
	assert.NoError(t, err)

	_, err = hook.ValidateUpdate(context.Background(), valid, &XTrinodeCatalog{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected new XTrinode")

	_, err = hook.ValidateDelete(context.Background(), valid)
	assert.NoError(t, err)

	_, err = hook.ValidateDelete(context.Background(), &XTrinodeCatalog{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected XTrinode")
}

func TestValidateUpdate_NodePoolTaintChangeWarns(t *testing.T) {
	old := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime"},
		Spec: XTrinodeSpec{
			Size: "s",
			NodePool: &NodePoolSpec{
				Provider:          "azure",
				ProviderMode:      "managed",
				KubernetesVersion: "v1.28.0",
				Azure:             &AzureNodePoolSpec{VMSize: "Standard_D8as_v5"},
			},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.NodePool.NodeTaints = []corev1.Taint{
		{Key: "xtrinode", Value: "workers", Effect: corev1.TaintEffectNoSchedule},
	}

	warnings, err := updated.ValidateUpdate(old)
	assert.NoError(t, err)
	assert.Contains(t, warnings, buildNodePoolSchedulingChangeWarning("node taints"))
}

func TestValidateUpdate_KEDADeepChangesWarn(t *testing.T) {
	old := &XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "runtime"},
		Spec: XTrinodeSpec{
			Size: "s",
			KEDA: &KEDASpec{
				ScalerType:    "http",
				ScalingMetric: "query",
				HTTPEndpoint:  stringPtr("coordinator"),
			},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.KEDA.Threshold = stringPtr("5")
	updated.Spec.KEDA.HTTPValueLocation = stringPtr("#(state==\"RUNNING\")#")

	warnings, err := updated.ValidateUpdate(old)
	assert.NoError(t, err)
	assert.Contains(t, warnings, buildKEDAConfigChangeWarning("threshold"))
	assert.Contains(t, warnings, buildKEDAConfigChangeWarning("httpValueLocation"))
}

func TestValidateRolloutPolicy(t *testing.T) {
	tests := []struct {
		name          string
		rolloutPolicy *RolloutPolicySpec
		wantErr       bool
	}{
		{
			name: "valid percentage rollout policy",
			rolloutPolicy: &RolloutPolicySpec{
				RevisionHistoryLimit: int32Ptr(10),
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxSurge:       intstrPtr(intstr.FromString("25%")),
					MaxUnavailable: intstrPtr(intstr.FromInt(0)),
				},
			},
			wantErr: false,
		},
		{
			name: "valid quoted integer rollout policy",
			rolloutPolicy: &RolloutPolicySpec{
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxSurge:       intstrPtr(intstr.FromString("1")),
					MaxUnavailable: intstrPtr(intstr.FromString("0")),
				},
			},
			wantErr: false,
		},
		{
			name: "negative maxSurge integer",
			rolloutPolicy: &RolloutPolicySpec{
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxSurge: intstrPtr(intstr.FromInt(-1)),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid maxUnavailable string",
			rolloutPolicy: &RolloutPolicySpec{
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxUnavailable: intstrPtr(intstr.FromString("many")),
				},
			},
			wantErr: true,
		},
		{
			name: "percentage too high",
			rolloutPolicy: &RolloutPolicySpec{
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxUnavailable: intstrPtr(intstr.FromString("101%")),
				},
			},
			wantErr: true,
		},
		{
			name: "both maxSurge and maxUnavailable are zero",
			rolloutPolicy: &RolloutPolicySpec{
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxSurge:       intstrPtr(intstr.FromInt(0)),
					MaxUnavailable: intstrPtr(intstr.FromString("0%")),
				},
			},
			wantErr: true,
		},
		{
			name: "both quoted maxSurge and maxUnavailable are zero",
			rolloutPolicy: &RolloutPolicySpec{
				RollingUpdateStrategy: &RollingUpdateStrategySpec{
					MaxSurge:       intstrPtr(intstr.FromString("0")),
					MaxUnavailable: intstrPtr(intstr.FromString("0")),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xtrinode := &XTrinode{
				ObjectMeta: metav1.ObjectMeta{Name: "runtime"},
				Spec: XTrinodeSpec{
					Size:          "s",
					RolloutPolicy: tt.rolloutPolicy,
				},
			}

			_, err := xtrinode.ValidateCreate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
