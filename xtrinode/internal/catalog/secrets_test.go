package catalog

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func secretRef(name, key string) *corev1.SecretKeySelector {
	return &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: name},
		Key:                  key,
	}
}

// ---------------------------------------------------------------------------
// Regression: extractSecretReferencesFromConnector must cover all JDBC connectors
// ---------------------------------------------------------------------------

func TestRegression_ExtractSecrets_AllJDBCConnectors(t *testing.T) {
	tests := []struct {
		name        string
		connector   analyticsv1.XTrinodeCatalogConnector
		expectCount int
		expectProp  string
	}{
		{
			name: "Postgres",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:            "jdbc:postgresql://pg:5432/db",
					ConnectionPasswordSecret: secretRef("pg-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "MySQL",
			connector: analyticsv1.XTrinodeCatalogConnector{
				MySQL: &analyticsv1.MySQLCatalogSpec{
					ConnectionURL:            "jdbc:mysql://mysql:3306/db",
					ConnectionPasswordSecret: secretRef("mysql-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "ClickHouse",
			connector: analyticsv1.XTrinodeCatalogConnector{
				ClickHouse: &analyticsv1.ClickHouseCatalogSpec{
					ConnectionURL:            "jdbc:clickhouse://ch:8123/db",
					ConnectionPasswordSecret: secretRef("ch-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "Exasol",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Exasol: &analyticsv1.ExasolCatalogSpec{
					ConnectionURL:            "jdbc:exa://exa:8563",
					ConnectionPasswordSecret: secretRef("exa-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "Ignite",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Ignite: &analyticsv1.IgniteCatalogSpec{
					ConnectionURL:            "jdbc:ignite://ignite:10800",
					ConnectionPasswordSecret: secretRef("ignite-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "MariaDB",
			connector: analyticsv1.XTrinodeCatalogConnector{
				MariaDB: &analyticsv1.MariaDBCatalogSpec{
					ConnectionURL:            "jdbc:mariadb://maria:3306/db",
					ConnectionPasswordSecret: secretRef("maria-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "Oracle",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Oracle: &analyticsv1.OracleCatalogSpec{
					ConnectionURL:            "jdbc:oracle:thin:@oracle:1521:orcl",
					ConnectionPasswordSecret: secretRef("oracle-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "Redshift",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Redshift: &analyticsv1.RedshiftCatalogSpec{
					ConnectionURL:            "jdbc:redshift://rs:5439/db",
					ConnectionPasswordSecret: secretRef("rs-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "SingleStore",
			connector: analyticsv1.XTrinodeCatalogConnector{
				SingleStore: &analyticsv1.SingleStoreCatalogSpec{
					ConnectionURL:            "jdbc:singlestore://ss:3306/db",
					ConnectionPasswordSecret: secretRef("ss-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "SQLServer",
			connector: analyticsv1.XTrinodeCatalogConnector{
				SQLServer: &analyticsv1.SQLServerCatalogSpec{
					ConnectionURL:            "jdbc:sqlserver://mssql:1433;database=db",
					ConnectionPasswordSecret: secretRef("mssql-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "Vertica",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Vertica: &analyticsv1.VerticaCatalogSpec{
					ConnectionURL:            "jdbc:vertica://vertica:5433/db",
					ConnectionPasswordSecret: secretRef("vertica-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
		{
			name: "Snowflake",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Snowflake: &analyticsv1.SnowflakeCatalogSpec{
					AccountURL:     "https://acme.snowflakecomputing.com",
					User:           "trino",
					PasswordSecret: secretRef("sf-secret", "password"),
				},
			},
			expectCount: 1,
			expectProp:  "connection-password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := &analyticsv1.XTrinodeCatalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-catalog",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeCatalogSpec{
					Connector: tt.connector,
				},
			}

			refs := extractSecretReferencesFromConnector(catalog, "test-catalog")
			require.Len(t, refs, tt.expectCount, "expected %d secret ref for %s", tt.expectCount, tt.name)
			assert.Equal(t, tt.expectProp, refs[0].PropertyName)
			assert.NotEmpty(t, refs[0].EnvVarName)
			assert.NotNil(t, refs[0].SecretKeySelector)
		})
	}
}

func TestRegression_ExtractSecrets_NoSecretReturnsEmpty(t *testing.T) {
	// Connector with no secret reference should return empty
	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "hive-catalog", Namespace: "default"},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	refs := extractSecretReferencesFromConnector(catalog, "hive-catalog")
	assert.Empty(t, refs)
}

func TestExtractSecrets_HiveS3SecretReferences(t *testing.T) {
	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "hive-catalog", Namespace: "default"},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
					S3AccessKeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "hive-s3"},
						Key:                  "access-key",
					},
					S3SecretKeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "hive-s3"},
						Key:                  "secret-key",
					},
				},
			},
		},
	}

	refs := extractSecretReferencesFromConnector(catalog, "hive-catalog")
	require.Len(t, refs, 2)
	assert.Equal(t, "s3.aws-access-key", refs[0].PropertyName)
	assert.Equal(t, "CATALOG_HIVE_CATALOG_S3_AWS_ACCESS_KEY", refs[0].EnvVarName)
	assert.Equal(t, "hive-s3", refs[0].SecretKeySelector.Name)
	assert.Equal(t, "access-key", refs[0].SecretKeySelector.Key)
	assert.Equal(t, "s3.aws-secret-key", refs[1].PropertyName)
	assert.Equal(t, "CATALOG_HIVE_CATALOG_S3_AWS_SECRET_KEY", refs[1].EnvVarName)
	assert.Equal(t, "hive-s3", refs[1].SecretKeySelector.Name)
	assert.Equal(t, "secret-key", refs[1].SecretKeySelector.Key)
}

func TestExtractSecrets_GenericPropertySecretReferences(t *testing.T) {
	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "cassandra-analytics", Namespace: "default"},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Cassandra: &analyticsv1.CassandraCatalogSpec{
					ContactPoints: "cassandra.default.svc.cluster.local",
					CatalogPropertySecretRefs: analyticsv1.CatalogPropertySecretRefs{
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

	refs := extractSecretReferencesFromConnector(catalog, "cassandra-analytics")
	require.Len(t, refs, 1)
	assert.Equal(t, "cassandra.password", refs[0].PropertyName)
	assert.Equal(t, "CATALOG_CASSANDRA_ANALYTICS_CASSANDRA_PASSWORD", refs[0].EnvVarName)
	assert.Equal(t, "cassandra-credentials", refs[0].SecretKeySelector.Name)
	assert.Equal(t, "password", refs[0].SecretKeySelector.Key)
}

func TestRegression_ExtractSecrets_EndToEnd_ClickHouse(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ch-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				ClickHouse: &analyticsv1.ClickHouseCatalogSpec{
					ConnectionURL: "jdbc:clickhouse://ch:8123/db",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "ch-secret"},
						Key:                  "password",
					},
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "data"},
			},
		},
	}

	envVars, err := ExtractCatalogSecretReferences(ctx, cli, xtrinode, log)
	require.NoError(t, err)
	require.Len(t, envVars, 1)
	assert.Equal(t, "CATALOG_CH_ANALYTICS_CONNECTION_PASSWORD", envVars[0].Name)
	assert.Equal(t, "ch-secret", envVars[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", envVars[0].ValueFrom.SecretKeyRef.Key)
}

func TestExtractCatalogSecretReferences_EndToEnd_GenericPropertySecretRefs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "es-logs",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "logs"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Elasticsearch: &analyticsv1.ElasticsearchCatalogSpec{
					Host: "elasticsearch.default.svc.cluster.local",
					CatalogPropertySecretRefs: analyticsv1.CatalogPropertySecretRefs{
						PropertySecretRefs: map[string]corev1.SecretKeySelector{
							"elasticsearch.auth.password": {
								LocalObjectReference: corev1.LocalObjectReference{Name: "es-credentials"},
								Key:                  "password",
							},
						},
					},
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog).Build()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "logs"},
			},
		},
	}

	envVars, err := ExtractCatalogSecretReferences(ctx, cli, xtrinode, logr.Discard())
	require.NoError(t, err)
	require.Len(t, envVars, 1)
	assert.Equal(t, "CATALOG_ES_LOGS_ELASTICSEARCH_AUTH_PASSWORD", envVars[0].Name)
	assert.Equal(t, "es-credentials", envVars[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", envVars[0].ValueFrom.SecretKeyRef.Key)
}

func TestExtractCatalogSecretReferences_UsesSpecLabelsOverMetadataLabels(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-analytics",
			Namespace: "default",
			Labels:    map[string]string{"team": "metadata-only"},
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "spec-label"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:            "jdbc:postgresql://pg:5432/db",
					ConnectionPasswordSecret: secretRef("pg-secret", "password"),
				},
			},
		},
	}
	metadataOnlyCatalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metadata-only-pg",
			Namespace: "default",
			Labels:    map[string]string{"team": "spec-label"},
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:            "jdbc:postgresql://pg:5432/db",
					ConnectionPasswordSecret: secretRef("metadata-secret", "password"),
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog, metadataOnlyCatalog).Build()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "spec-label"},
			},
		},
	}

	envVars, err := ExtractCatalogSecretReferences(ctx, cli, xtrinode, logr.Discard())
	require.NoError(t, err)
	require.Len(t, envVars, 1)
	assert.Equal(t, "CATALOG_PG_ANALYTICS_CONNECTION_PASSWORD", envVars[0].Name)

	xtrinode.Spec.CatalogSelector.MatchLabels["team"] = "metadata-only"
	envVars, err = ExtractCatalogSecretReferences(ctx, cli, xtrinode, logr.Discard())
	require.NoError(t, err)
	assert.Empty(t, envVars)
}

func TestRegression_ExtractSecrets_EndToEnd_Snowflake(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sf-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "bi"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Snowflake: &analyticsv1.SnowflakeCatalogSpec{
					AccountURL: "https://acme.snowflakecomputing.com",
					User:       "trino",
					PasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "sf-secret"},
						Key:                  "password",
					},
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "bi"},
			},
		},
	}

	envVars, err := ExtractCatalogSecretReferences(ctx, cli, xtrinode, log)
	require.NoError(t, err)
	require.Len(t, envVars, 1)
	assert.Equal(t, "CATALOG_SF_ANALYTICS_CONNECTION_PASSWORD", envVars[0].Name)
	assert.Equal(t, "sf-secret", envVars[0].ValueFrom.SecretKeyRef.Name)
}
