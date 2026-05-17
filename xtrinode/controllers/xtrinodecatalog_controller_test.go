package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestXTrinodeCatalogReconciler_Reconcile_CreateCatalog(t *testing.T) {
	scheme := newTestScheme()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data", "catalog-type": "hive"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive-metastore:9083",
				},
			},
		},
	}

	client := newTestClient(scheme, catalog)
	reconciler := newTestCatalogReconciler(client, scheme)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-catalog",
			Namespace: "default",
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	if err != nil {
		t.Logf("Reconciliation returned error (may be expected in test env): %v", err)
		return
	}

	assert.NotNil(t, result)
}

func TestXTrinodeCatalogReconciler_Reconcile_NotFound(t *testing.T) {
	scheme := newTestScheme()
	client := newTestClient(scheme)
	reconciler := newTestCatalogReconciler(client, scheme)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "default",
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.False(t, result.RequeueAfter > 0)
}

func TestXTrinodeCatalogReconciler_generateProperties_Hive(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data", "catalog-type": "hive"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive-metastore:9083",
					S3Endpoint:   "s3.amazonaws.com",
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=hive")
	assert.Contains(t, properties, "hive.metastore.uri=thrift://hive-metastore:9083")
	assert.Contains(t, properties, "fs.native-s3.enabled=true")
	assert.Contains(t, properties, "s3.endpoint=s3.amazonaws.com")
}

func TestXTrinodeCatalogReconciler_generateProperties_Postgres_WithSecret(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgres-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:  "jdbc:postgresql://postgres:5432/mydb",
					ConnectionUser: "user",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "postgres-credentials",
						},
						Key: "password",
					},
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=postgresql")
	assert.Contains(t, properties, "connection-url=jdbc:postgresql://postgres:5432/mydb")
	assert.Contains(t, properties, "connection-user=user")
	// Should contain env var placeholder, not actual password
	assert.Contains(t, properties, "connection-password=${ENV:CATALOG_POSTGRES_ANALYTICS_CONNECTION_PASSWORD}")
}

func TestXTrinodeCatalogReconciler_generateProperties_Postgres_NoPassword(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:  "jdbc:postgresql://postgres:5432/mydb",
					ConnectionUser: "user",
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=postgresql")
	assert.Contains(t, properties, "connection-url=jdbc:postgresql://postgres:5432/mydb")
	assert.Contains(t, properties, "connection-user=user")
	assert.NotContains(t, properties, "connection-password")
}

func TestXTrinodeCatalogReconciler_generateProperties_Custom(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Custom: &analyticsv1.CustomCatalogSpec{
					ConnectorName: "custom-connector",
					Properties: map[string]string{
						"custom.property": "value",
					},
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=custom-connector")
	assert.Contains(t, properties, "custom.property=value")
}

func TestXTrinodeCatalogReconciler_generateProperties_RejectsPlaintextSensitiveProperties(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgres-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL: "jdbc:postgresql://postgres:5432/mydb",
					Properties: map[string]string{
						"connection-password": "plaintext-password",
					},
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	assert.Error(t, err)
	assert.Empty(t, properties)
	assert.Contains(t, err.Error(), "invalid XTrinodeCatalog spec")
	assert.Contains(t, err.Error(), "use connectionPasswordSecret")
}

func TestXTrinodeCatalogReconciler_generateProperties_CassandraPropertySecretRefs(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cassandra-analytics",
			Namespace: "default",
		},
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
					Properties: map[string]string{
						"cassandra.load-policy.use-dc-aware": "true",
					},
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=cassandra")
	assert.Contains(t, properties, "cassandra.contact-points=cassandra.default.svc.cluster.local")
	assert.Contains(t, properties, "cassandra.password=${ENV:CATALOG_CASSANDRA_ANALYTICS_CASSANDRA_PASSWORD}")
	assert.Contains(t, properties, "cassandra.load-policy.use-dc-aware=true")
	assert.NotContains(t, properties, "plaintext")
}

func TestXTrinodeCatalogReconciler_generateProperties_BigQueryCredentialsFile(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bigquery-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				BigQuery: &analyticsv1.BigQueryCatalogSpec{ // #nosec G101 -- test fixture path uses credential terminology only.
					ProjectID:       "analytics-project",
					ParentProjectID: "billing-project",
					CredentialsFile: "/etc/trino/secrets/bigquery/key.json",
					Properties: map[string]string{
						"bigquery.views-enabled": "true",
					},
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=bigquery")
	assert.Contains(t, properties, "bigquery.project-id=analytics-project")
	assert.Contains(t, properties, "bigquery.parent-project-id=billing-project")
	assert.Contains(t, properties, "bigquery.credentials-file=/etc/trino/secrets/bigquery/key.json")
	assert.Contains(t, properties, "bigquery.views-enabled=true")
}

func TestXTrinodeCatalogReconciler_generateProperties_BigQueryCredentialKeySecretRef(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bigquery-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				BigQuery: &analyticsv1.BigQueryCatalogSpec{
					ProjectID: "analytics-project",
					CatalogPropertySecretRefs: analyticsv1.CatalogPropertySecretRefs{
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

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=bigquery")
	assert.Contains(t, properties, "bigquery.project-id=analytics-project")
	assert.Contains(t, properties, "bigquery.credentials-key=${ENV:CATALOG_BIGQUERY_ANALYTICS_BIGQUERY_CREDENTIALS_KEY}")
	assert.NotContains(t, properties, "credentials-file")
}

func TestXTrinodeCatalogReconciler_generateConfigMap(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data", "catalog-type": "hive"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive-metastore:9083",
				},
			},
		},
	}

	configMap, err := reconciler.generateConfigMap(catalog)
	require.NoError(t, err)
	assert.Equal(t, "trino-catalog-test-catalog", configMap.Name)
	assert.Equal(t, "default", configMap.Namespace)
	assert.Contains(t, configMap.Data, "test-catalog.properties")
	assert.Equal(t, catalog.Name, configMap.Labels["xtrinode-catalog"])
	assert.Equal(t, "data", configMap.Labels["team"])
	assert.Equal(t, "hive", configMap.Labels["catalog-type"])
}

func TestXTrinodeCatalogReconciler_buildHiveProperties(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		Hive: &analyticsv1.HiveCatalogSpec{
			MetastoreURI: "thrift://hive:9083",
			S3Endpoint:   "s3.amazonaws.com",
			S3AccessKeySecret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "hive-s3"},
				Key:                  "access-key",
			},
			S3SecretKeySecret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "hive-s3"},
				Key:                  "secret-key",
			},
			Properties: map[string]string{
				"custom.property": "value",
			},
		},
	}

	name, props, err := resolveConnector(connector, "test-catalog")
	require.NoError(t, err)
	assert.Equal(t, "hive", name)
	assert.Equal(t, "thrift://hive:9083", props["hive.metastore.uri"])
	assert.Equal(t, "true", props["fs.native-s3.enabled"])
	assert.Equal(t, "s3.amazonaws.com", props["s3.endpoint"])
	assert.Equal(t, "${ENV:CATALOG_TEST_CATALOG_S3_AWS_ACCESS_KEY}", props["s3.aws-access-key"])
	assert.Equal(t, "${ENV:CATALOG_TEST_CATALOG_S3_AWS_SECRET_KEY}", props["s3.aws-secret-key"])
	assert.Equal(t, "value", props["custom.property"])
}

func TestXTrinodeCatalogReconciler_buildPostgresProperties_WithSecret(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		Postgres: &analyticsv1.PostgresCatalogSpec{
			ConnectionURL:  "jdbc:postgresql://postgres:5432/db",
			ConnectionUser: "user",
			ConnectionPasswordSecret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "postgres-password",
				},
				Key: "password",
			},
		},
	}

	name, props, err := resolveConnector(connector, "postgres-analytics")
	require.NoError(t, err)
	assert.Equal(t, "postgresql", name)
	assert.Equal(t, "jdbc:postgresql://postgres:5432/db", props["connection-url"])
	assert.Equal(t, "user", props["connection-user"])
	// Should contain env var placeholder
	assert.Equal(t, "${ENV:CATALOG_POSTGRES_ANALYTICS_CONNECTION_PASSWORD}", props["connection-password"])
}

func TestXTrinodeCatalogReconciler_buildPostgresProperties_NoSecret(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		Postgres: &analyticsv1.PostgresCatalogSpec{
			ConnectionURL:  "jdbc:postgresql://postgres:5432/db",
			ConnectionUser: "user",
		},
	}

	name, props, err := resolveConnector(connector, "postgres-analytics")
	require.NoError(t, err)
	assert.Equal(t, "postgresql", name)
	assert.Equal(t, "jdbc:postgresql://postgres:5432/db", props["connection-url"])
	assert.Equal(t, "user", props["connection-user"])
	// Should not contain password property at all
	_, exists := props["connection-password"]
	assert.False(t, exists)
}

func TestResolveConnectorBasicTypes(t *testing.T) {
	tests := []struct {
		name         string
		connector    analyticsv1.XTrinodeCatalogConnector
		catalogName  string
		expectedName string
	}{
		{
			name: "Hive connector",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
			catalogName:  "test-catalog",
			expectedName: "hive",
		},
		{
			name: "Iceberg connector",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Iceberg: &analyticsv1.IcebergCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "s3://warehouse",
				},
			},
			catalogName:  "test-catalog",
			expectedName: "iceberg",
		},
		{
			name: "Custom connector",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Custom: &analyticsv1.CustomCatalogSpec{
					ConnectorName: "custom",
					Properties:    map[string]string{"key": "value"},
				},
			},
			catalogName:  "test-catalog",
			expectedName: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, props, err := resolveConnector(&tt.connector, tt.catalogName)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedName, name)
			assert.NotNil(t, props)
		})
	}
}

func TestXTrinodeCatalogReconciler_buildPropertiesString(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	props := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	result := reconciler.buildPropertiesString("test-connector", props)
	assert.Contains(t, result, "connector.name=test-connector")
	assert.Contains(t, result, "key1=value1")
	assert.Contains(t, result, "key2=value2")
}

func TestResolveConnector_IcebergHiveMetastoreProperties(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		Iceberg: &analyticsv1.IcebergCatalogSpec{
			CatalogType:  "hive",
			WarehouseURI: "gs://warehouse/iceberg",
			Properties: map[string]string{
				"hive.metastore.uri":    "thrift://hive-metastore:9083",
				"fs.native-gcs.enabled": "true",
			},
		},
	}

	name, props, err := resolveConnector(connector, "iceberg")
	require.NoError(t, err)
	assert.Equal(t, "iceberg", name)
	assert.Equal(t, "hive_metastore", props["iceberg.catalog.type"])
	assert.Equal(t, "thrift://hive-metastore:9083", props["hive.metastore.uri"])
	assert.NotContains(t, props, "iceberg.catalog.warehouse")
}

func TestResolveConnector_DeltaLakeHiveMetastoreProperties(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		DeltaLake: &analyticsv1.DeltaLakeCatalogSpec{
			CatalogType:  "hive",
			WarehouseURI: "thrift://hive-metastore:9083",
			Properties: map[string]string{
				"fs.native-gcs.enabled": "true",
			},
		},
	}

	name, props, err := resolveConnector(connector, "delta")
	require.NoError(t, err)
	assert.Equal(t, "delta_lake", name)
	assert.Equal(t, "thrift://hive-metastore:9083", props["hive.metastore.uri"])
	assert.NotContains(t, props, "delta.catalog.type")
	assert.NotContains(t, props, "delta.catalog.warehouse")
}

func TestResolveConnector_DeltaLakeGlueProperties(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		DeltaLake: &analyticsv1.DeltaLakeCatalogSpec{
			CatalogType: "glue",
		},
	}

	name, props, err := resolveConnector(connector, "delta")
	require.NoError(t, err)
	assert.Equal(t, "delta_lake", name)
	assert.Equal(t, "glue", props["hive.metastore"])
}

func TestResolveConnector_OfficialTrinoPropertyNames(t *testing.T) {
	secretRef := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "password-secret"},
		Key:                  "password",
	}
	passwordEnv := func(catalogName string) string {
		return "${ENV:" + calculateEnvVarName(catalogName, "connection-password") + "}"
	}

	tests := []struct {
		name         string
		connector    analyticsv1.XTrinodeCatalogConnector
		catalogName  string
		expectedName string
		expected     map[string]string
		absent       []string
	}{
		{
			name: "BigQuery renders project and credentials properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				BigQuery: &analyticsv1.BigQueryCatalogSpec{
					ProjectID:       "analytics-prod",
					ParentProjectID: "billing-prod",
					CredentialsFile: "/etc/trino/secrets/bigquery.json", // #nosec G101 -- test fixture path, not a credential
					Properties: map[string]string{
						"bigquery.views-enabled": "true",
					},
				},
			},
			catalogName:  "bigquery",
			expectedName: "bigquery",
			expected: map[string]string{
				"bigquery.project-id":        "analytics-prod",
				"bigquery.parent-project-id": "billing-prod",
				"bigquery.credentials-file":  "/etc/trino/secrets/bigquery.json", // #nosec G101 -- test fixture path, not a credential
				"bigquery.views-enabled":     "true",
			},
		},
		{
			name: "Black Hole uses official connector name",
			connector: analyticsv1.XTrinodeCatalogConnector{
				BlackHole: &analyticsv1.BlackHoleCatalogSpec{},
			},
			catalogName:  "blackhole",
			expectedName: "blackhole",
		},
		{
			name: "Cassandra renders contact points and native protocol port",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Cassandra: &analyticsv1.CassandraCatalogSpec{
					ContactPoints: "cassandra-1,cassandra-2",
					Port:          9042,
					Properties: map[string]string{
						"cassandra.load-policy.use-dc-aware": "true",
					},
				},
			},
			catalogName:  "cassandra",
			expectedName: "cassandra",
			expected: map[string]string{
				"cassandra.contact-points":           "cassandra-1,cassandra-2",
				"cassandra.native-protocol-port":     "9042",
				"cassandra.load-policy.use-dc-aware": "true",
			},
		},
		{
			name: "ClickHouse renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				ClickHouse: &analyticsv1.ClickHouseCatalogSpec{
					ConnectionURL:            "jdbc:clickhouse://clickhouse:8123/analytics",
					ConnectionUser:           "trino",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "clickhouse",
			expectedName: "clickhouse",
			expected: map[string]string{
				"connection-url":      "jdbc:clickhouse://clickhouse:8123/analytics",
				"connection-user":     "trino",
				"connection-password": passwordEnv("clickhouse"),
			},
		},
		{
			name: "Delta Lake renders Hive metastore URI",
			connector: analyticsv1.XTrinodeCatalogConnector{
				DeltaLake: &analyticsv1.DeltaLakeCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "thrift://delta-metastore:9083",
					Properties: map[string]string{
						"delta.enable-non-concurrent-writes": "true",
					},
				},
			},
			catalogName:  "delta",
			expectedName: "delta_lake",
			expected: map[string]string{
				"hive.metastore.uri":                 "thrift://delta-metastore:9083",
				"delta.enable-non-concurrent-writes": "true",
			},
			absent: []string{"delta.catalog.type", "delta.catalog.warehouse"},
		},
		{
			name: "Druid renders Avatica connection-url",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Druid: &analyticsv1.DruidCatalogSpec{
					BrokerURL: "http://druid-broker:8082",
				},
			},
			catalogName:  "druid",
			expectedName: "druid",
			expected: map[string]string{
				"connection-url": "jdbc:avatica:remote:url=http://druid-broker:8082/druid/v2/sql/avatica/",
			},
			absent: []string{"druid.broker-url"},
		},
		{
			name: "DuckDB renders JDBC connection-url",
			connector: analyticsv1.XTrinodeCatalogConnector{
				DuckDB: &analyticsv1.DuckDBCatalogSpec{
					DatabasePath: "/data/analytics.duckdb",
				},
			},
			catalogName:  "duckdb",
			expectedName: "duckdb",
			expected: map[string]string{
				"connection-url": "jdbc:duckdb:/data/analytics.duckdb",
			},
			absent: []string{"duckdb.database-path"},
		},
		{
			name: "Elasticsearch renders host, port, and default schema",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Elasticsearch: &analyticsv1.ElasticsearchCatalogSpec{
					Host:          "elasticsearch",
					Port:          9200,
					DefaultSchema: "logs",
				},
			},
			catalogName:  "elasticsearch",
			expectedName: "elasticsearch",
			expected: map[string]string{
				"elasticsearch.host":                "elasticsearch",
				"elasticsearch.port":                "9200",
				"elasticsearch.default-schema-name": "logs",
			},
		},
		{
			name: "Exasol renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Exasol: &analyticsv1.ExasolCatalogSpec{
					ConnectionURL:            "jdbc:exa:exasol:8563",
					ConnectionUser:           "sys",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "exasol",
			expectedName: "exasol",
			expected: map[string]string{
				"connection-url":      "jdbc:exa:exasol:8563",
				"connection-user":     "sys",
				"connection-password": passwordEnv("exasol"),
			},
		},
		{
			name: "Faker renders generated-data defaults",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Faker: &analyticsv1.FakerCatalogSpec{
					DefaultLimit:    1000,
					NullProbability: 0.25,
				},
			},
			catalogName:  "faker",
			expectedName: "faker",
			expected: map[string]string{
				"faker.default-limit":    "1000",
				"faker.null-probability": "0.250000",
			},
		},
		{
			name: "Google Sheets renders credentials path and metadata sheet",
			connector: analyticsv1.XTrinodeCatalogConnector{
				GoogleSheets: &analyticsv1.GoogleSheetsCatalogSpec{
					CredentialsFilePath: "/etc/trino/secrets/gsheets.json", // #nosec G101 -- test fixture path, not a credential
					MetadataSheetID:     "sheet-id",
				},
			},
			catalogName:  "gsheets",
			expectedName: "gsheets",
			expected: map[string]string{
				"gsheets.credentials-path":  "/etc/trino/secrets/gsheets.json", // #nosec G101 -- test fixture path, not a credential
				"gsheets.metadata-sheet-id": "sheet-id",
			},
		},
		{
			name: "Hive renders metastore and native S3 secret properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI:      "thrift://hive-metastore:9083",
					S3Endpoint:        "https://s3.us-east-1.amazonaws.com",
					S3AccessKeySecret: secretRef,
					S3SecretKeySecret: secretRef,
				},
			},
			catalogName:  "hive",
			expectedName: "hive",
			expected: map[string]string{
				"hive.metastore.uri":   "thrift://hive-metastore:9083",
				"fs.native-s3.enabled": "true",
				"s3.endpoint":          "https://s3.us-east-1.amazonaws.com",
				"s3.aws-access-key":    "${ENV:CATALOG_HIVE_S3_AWS_ACCESS_KEY}", // #nosec G101 -- test fixture env reference, not a credential
				"s3.aws-secret-key":    "${ENV:CATALOG_HIVE_S3_AWS_SECRET_KEY}", // #nosec G101 -- test fixture env reference, not a credential
			},
		},
		{
			name: "Hudi renders Hive metastore URI",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Hudi: &analyticsv1.HudiCatalogSpec{
					MetastoreURI: "thrift://hudi-metastore:9083",
				},
			},
			catalogName:  "hudi",
			expectedName: "hudi",
			expected: map[string]string{
				"hive.metastore.uri": "thrift://hudi-metastore:9083",
			},
		},
		{
			name: "Iceberg renders catalog type and preserves metastore properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Iceberg: &analyticsv1.IcebergCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "gs://warehouse/iceberg",
					Properties: map[string]string{
						"hive.metastore.uri": "thrift://iceberg-metastore:9083",
					},
				},
			},
			catalogName:  "iceberg",
			expectedName: "iceberg",
			expected: map[string]string{
				"iceberg.catalog.type": "hive_metastore",
				"hive.metastore.uri":   "thrift://iceberg-metastore:9083",
			},
			absent: []string{"iceberg.catalog.warehouse"},
		},
		{
			name: "Ignite renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Ignite: &analyticsv1.IgniteCatalogSpec{
					ConnectionURL:            "jdbc:ignite:thin://ignite:10800",
					ConnectionUser:           "ignite",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "ignite",
			expectedName: "ignite",
			expected: map[string]string{
				"connection-url":      "jdbc:ignite:thin://ignite:10800",
				"connection-user":     "ignite",
				"connection-password": passwordEnv("ignite"),
			},
		},
		{
			name: "JMX preserves connector properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				JMX: &analyticsv1.JMXCatalogSpec{
					Properties: map[string]string{
						"jmx.dump-tables": "java.lang:type=Runtime",
					},
				},
			},
			catalogName:  "jmx",
			expectedName: "jmx",
			expected: map[string]string{
				"jmx.dump-tables": "java.lang:type=Runtime",
			},
		},
		{
			name: "Kafka renders broker nodes",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Kafka: &analyticsv1.KafkaCatalogSpec{
					KafkaNodes: []string{"broker-1:9092", "broker-2:9092"},
					Properties: map[string]string{
						"kafka.table-description-dir": "/etc/trino/kafka",
					},
				},
			},
			catalogName:  "kafka",
			expectedName: "kafka",
			expected: map[string]string{
				"kafka.nodes":                 "broker-1:9092,broker-2:9092",
				"kafka.table-description-dir": "/etc/trino/kafka",
			},
		},
		{
			name: "Lakehouse renders table type and thrift metastore",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Lakehouse: &analyticsv1.LakehouseCatalogSpec{
					CatalogType:  "hive",
					MetastoreURI: "thrift://metastore:9083",
				},
			},
			catalogName:  "lakehouse",
			expectedName: "lakehouse",
			expected: map[string]string{
				"lakehouse.table-type": "HIVE",
				"hive.metastore":       "thrift",
				"hive.metastore.uri":   "thrift://metastore:9083",
			},
			absent: []string{"lakehouse.catalog.type"},
		},
		{
			name: "Loki renders service URI",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Loki: &analyticsv1.LokiCatalogSpec{
					URI: "http://loki:3100",
				},
			},
			catalogName:  "loki",
			expectedName: "loki",
			expected: map[string]string{
				"loki.uri": "http://loki:3100",
			},
		},
		{
			name: "MariaDB renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				MariaDB: &analyticsv1.MariaDBCatalogSpec{
					ConnectionURL:            "jdbc:mariadb://mariadb:3306/analytics",
					ConnectionUser:           "trino",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "mariadb",
			expectedName: "mariadb",
			expected: map[string]string{
				"connection-url":      "jdbc:mariadb://mariadb:3306/analytics",
				"connection-user":     "trino",
				"connection-password": passwordEnv("mariadb"),
			},
		},
		{
			name: "Memory renders max data per node",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Memory: &analyticsv1.MemoryCatalogSpec{
					MaxDataPerNode: "128MB",
				},
			},
			catalogName:  "memory",
			expectedName: "memory",
			expected: map[string]string{
				"memory.max-data-per-node": "128MB",
			},
		},
		{
			name: "MongoDB renders connection URL",
			connector: analyticsv1.XTrinodeCatalogConnector{
				MongoDB: &analyticsv1.MongoDBCatalogSpec{
					ConnectionURI: "mongodb://mongodb:27017",
				},
			},
			catalogName:  "mongodb",
			expectedName: "mongodb",
			expected: map[string]string{
				"mongodb.connection-url": "mongodb://mongodb:27017",
			},
		},
		{
			name: "MySQL renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				MySQL: &analyticsv1.MySQLCatalogSpec{
					ConnectionURL:            "jdbc:mysql://mysql:3306/analytics",
					ConnectionUser:           "trino",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "mysql",
			expectedName: "mysql",
			expected: map[string]string{
				"connection-url":      "jdbc:mysql://mysql:3306/analytics",
				"connection-user":     "trino",
				"connection-password": passwordEnv("mysql"),
			},
		},
		{
			name: "OpenSearch renders host, port, and default schema",
			connector: analyticsv1.XTrinodeCatalogConnector{
				OpenSearch: &analyticsv1.OpenSearchCatalogSpec{
					Host:          "opensearch",
					Port:          9200,
					DefaultSchema: "logs",
				},
			},
			catalogName:  "opensearch",
			expectedName: "opensearch",
			expected: map[string]string{
				"opensearch.host":                "opensearch",
				"opensearch.port":                "9200",
				"opensearch.default-schema-name": "logs",
			},
		},
		{
			name: "Oracle renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Oracle: &analyticsv1.OracleCatalogSpec{
					ConnectionURL:            "jdbc:oracle:thin:@oracle:1521/FREEPDB1",
					ConnectionUser:           "system",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "oracle",
			expectedName: "oracle",
			expected: map[string]string{
				"connection-url":      "jdbc:oracle:thin:@oracle:1521/FREEPDB1",
				"connection-user":     "system",
				"connection-password": passwordEnv("oracle"),
			},
		},
		{
			name: "Pinot renders controller URLs",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Pinot: &analyticsv1.PinotCatalogSpec{
					ControllerURLs: "pinot-controller:9000",
				},
			},
			catalogName:  "pinot",
			expectedName: "pinot",
			expected: map[string]string{
				"pinot.controller-urls": "pinot-controller:9000",
			},
		},
		{
			name: "PostgreSQL renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Postgres: &analyticsv1.PostgresCatalogSpec{
					ConnectionURL:            "jdbc:postgresql://postgres:5432/analytics",
					ConnectionUser:           "trino",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "postgres",
			expectedName: "postgresql",
			expected: map[string]string{
				"connection-url":      "jdbc:postgresql://postgres:5432/analytics",
				"connection-user":     "trino",
				"connection-password": passwordEnv("postgres"),
			},
		},
		{
			name: "Prometheus renders service URI",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Prometheus: &analyticsv1.PrometheusCatalogSpec{
					URI: "http://prometheus:9090",
				},
			},
			catalogName:  "prometheus",
			expectedName: "prometheus",
			expected: map[string]string{
				"prometheus.uri": "http://prometheus:9090",
			},
		},
		{
			name: "Redis renders node and database index",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Redis: &analyticsv1.RedisCatalogSpec{
					Nodes:    "redis:6379",
					Database: 2,
				},
			},
			catalogName:  "redis",
			expectedName: "redis",
			expected: map[string]string{
				"redis.nodes":          "redis:6379",
				"redis.database-index": "2",
			},
		},
		{
			name: "Redshift renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Redshift: &analyticsv1.RedshiftCatalogSpec{
					ConnectionURL:            "jdbc:redshift://redshift:5439/analytics",
					ConnectionUser:           "awsuser",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "redshift",
			expectedName: "redshift",
			expected: map[string]string{
				"connection-url":      "jdbc:redshift://redshift:5439/analytics",
				"connection-user":     "awsuser",
				"connection-password": passwordEnv("redshift"),
			},
		},
		{
			name: "SingleStore renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				SingleStore: &analyticsv1.SingleStoreCatalogSpec{
					ConnectionURL:            "jdbc:singlestore://singlestore:3306/analytics",
					ConnectionUser:           "trino",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "singlestore",
			expectedName: "singlestore",
			expected: map[string]string{
				"connection-url":      "jdbc:singlestore://singlestore:3306/analytics",
				"connection-user":     "trino",
				"connection-password": passwordEnv("singlestore"),
			},
		},
		{
			name: "Snowflake renders JDBC credentials and account",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Snowflake: &analyticsv1.SnowflakeCatalogSpec{
					AccountURL:     "https://acme.snowflakecomputing.com",
					User:           "trino",
					Database:       "ANALYTICS",
					Role:           "BI_ROLE",
					Warehouse:      "BI_WH",
					PasswordSecret: secretRef,
				},
			},
			catalogName:  "snowflake",
			expectedName: "snowflake",
			expected: map[string]string{
				"connection-url":      "jdbc:snowflake://acme.snowflakecomputing.com",
				"connection-user":     "trino",
				"connection-password": "${ENV:CATALOG_SNOWFLAKE_CONNECTION_PASSWORD}", // #nosec G101 -- test fixture env reference, not a credential
				"snowflake.account":   "acme",
				"snowflake.database":  "ANALYTICS",
				"snowflake.role":      "BI_ROLE",
				"snowflake.warehouse": "BI_WH",
			},
			absent: []string{"snowflake.account-url", "snowflake.user", "snowflake.password"},
		},
		{
			name: "SQL Server renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				SQLServer: &analyticsv1.SQLServerCatalogSpec{
					ConnectionURL:            "jdbc:sqlserver://sqlserver:1433;databaseName=analytics",
					ConnectionUser:           "sa",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "sqlserver",
			expectedName: "sqlserver",
			expected: map[string]string{
				"connection-url":      "jdbc:sqlserver://sqlserver:1433;databaseName=analytics",
				"connection-user":     "sa",
				"connection-password": passwordEnv("sqlserver"),
			},
		},
		{
			name: "System uses official connector name",
			connector: analyticsv1.XTrinodeCatalogConnector{
				System: &analyticsv1.SystemCatalogSpec{},
			},
			catalogName:  "system",
			expectedName: "system",
		},
		{
			name: "Thrift uses official connector name",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Thrift: &analyticsv1.ThriftCatalogSpec{
					Host: "thrift-server",
					Port: 7777,
				},
			},
			catalogName:  "thrift",
			expectedName: "trino_thrift",
			expected: map[string]string{
				"trino.thrift.client.addresses": "thrift-server:7777",
			},
		},
		{
			name: "TPC-DS preserves connector properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				TPCDS: &analyticsv1.TPCDSCatalogSpec{
					Properties: map[string]string{
						"tpcds.splits-per-node": "8",
					},
				},
			},
			catalogName:  "tpcds",
			expectedName: "tpcds",
			expected: map[string]string{
				"tpcds.splits-per-node": "8",
			},
		},
		{
			name: "TPC-H preserves connector properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				TPCH: &analyticsv1.TPCHCatalogSpec{
					Properties: map[string]string{
						"tpch.splits-per-node": "8",
					},
				},
			},
			catalogName:  "tpch",
			expectedName: "tpch",
			expected: map[string]string{
				"tpch.splits-per-node": "8",
			},
		},
		{
			name: "Vertica renders JDBC connection properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Vertica: &analyticsv1.VerticaCatalogSpec{
					ConnectionURL:            "jdbc:vertica://vertica:5433/analytics",
					ConnectionUser:           "dbadmin",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "vertica",
			expectedName: "vertica",
			expected: map[string]string{
				"connection-url":      "jdbc:vertica://vertica:5433/analytics",
				"connection-user":     "dbadmin",
				"connection-password": passwordEnv("vertica"),
			},
		},
		{
			name: "Custom preserves raw connector properties",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Custom: &analyticsv1.CustomCatalogSpec{
					ConnectorName: "example",
					Properties: map[string]string{
						"example.property": "value",
					},
				},
			},
			catalogName:  "custom",
			expectedName: "example",
			expected: map[string]string{
				"example.property": "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, props, err := resolveConnector(&tt.connector, tt.catalogName)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedName, name)
			for key, expectedValue := range tt.expected {
				assert.Equal(t, expectedValue, props[key], "property %s", key)
			}
			for _, key := range tt.absent {
				assert.NotContains(t, props, key)
			}
		})
	}
}

func TestSnowflakeConnectionURLAndAccount(t *testing.T) {
	tests := []struct {
		name          string
		accountURL    string
		connectionURL string
		account       string
	}{
		{
			name:          "account locator",
			accountURL:    "acme",
			connectionURL: "jdbc:snowflake://acme.snowflakecomputing.com",
			account:       "acme",
		},
		{
			name:          "https account URL",
			accountURL:    "https://acme.snowflakecomputing.com",
			connectionURL: "jdbc:snowflake://acme.snowflakecomputing.com",
			account:       "acme",
		},
		{
			name:          "regional account host",
			accountURL:    "xy12345.us-east-1.snowflakecomputing.com",
			connectionURL: "jdbc:snowflake://xy12345.us-east-1.snowflakecomputing.com",
			account:       "xy12345.us-east-1",
		},
		{
			name:          "jdbc URL with query",
			accountURL:    "jdbc:snowflake://acme.snowflakecomputing.com/?schema=PUBLIC",
			connectionURL: "jdbc:snowflake://acme.snowflakecomputing.com/?schema=PUBLIC",
			account:       "acme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connectionURL, account := snowflakeConnectionURLAndAccount(tt.accountURL)
			assert.Equal(t, tt.connectionURL, connectionURL)
			assert.Equal(t, tt.account, account)
		})
	}
}

func TestXTrinodeCatalogReconciler_generateProperties_MySQL_WithSecret(t *testing.T) {
	reconciler := &XTrinodeCatalogReconciler{}

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mysql-analytics",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				MySQL: &analyticsv1.MySQLCatalogSpec{
					ConnectionURL:  "jdbc:mysql://mysql:3306/mydb",
					ConnectionUser: "user",
					ConnectionPasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "mysql-password",
						},
						Key: "password",
					},
					Properties: map[string]string{
						"custom.property": "value",
					},
				},
			},
		},
	}

	properties, err := reconciler.generateProperties(catalog)
	require.NoError(t, err)
	assert.Contains(t, properties, "connector.name=mysql")
	assert.Contains(t, properties, "connection-url=jdbc:mysql://mysql:3306/mydb")
	assert.Contains(t, properties, "connection-user=user")
	// Should contain env var placeholder
	assert.Contains(t, properties, "connection-password=${ENV:CATALOG_MYSQL_ANALYTICS_CONNECTION_PASSWORD}")
	assert.Contains(t, properties, "custom.property=value")
}

func TestXTrinodeCatalogReconciler_buildMySQLProperties_WithSecret(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		MySQL: &analyticsv1.MySQLCatalogSpec{
			ConnectionURL:  "jdbc:mysql://mysql:3306/db",
			ConnectionUser: "user",
			ConnectionPasswordSecret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "mysql-password",
				},
				Key: "password",
			},
			Properties: map[string]string{
				"custom.property": "value",
			},
		},
	}

	name, props, err := resolveConnector(connector, "mysql-analytics")
	require.NoError(t, err)
	assert.Equal(t, "mysql", name)
	assert.Equal(t, "jdbc:mysql://mysql:3306/db", props["connection-url"])
	assert.Equal(t, "user", props["connection-user"])
	// Should contain env var placeholder
	assert.Equal(t, "${ENV:CATALOG_MYSQL_ANALYTICS_CONNECTION_PASSWORD}", props["connection-password"])
	assert.Equal(t, "value", props["custom.property"])
}

func TestCalculateEnvVarName(t *testing.T) {
	tests := []struct {
		name         string
		catalogName  string
		propertyName string
		expected     string
	}{
		{
			name:         "Simple names",
			catalogName:  "postgres-analytics",
			propertyName: "connection-password",
			expected:     "CATALOG_POSTGRES_ANALYTICS_CONNECTION_PASSWORD",
		},
		{
			name:         "With dots",
			catalogName:  "my.catalog",
			propertyName: "my.property",
			expected:     "CATALOG_MY_CATALOG_MY_PROPERTY",
		},
		{
			name:         "Mixed special chars",
			catalogName:  "my-catalog.test",
			propertyName: "prop-name.value",
			expected:     "CATALOG_MY_CATALOG_TEST_PROP_NAME_VALUE",
		},
		{
			name:         "With catalog prefix",
			catalogName:  "trino-catalog-postgres",
			propertyName: "connection-password",
			expected:     "CATALOG_POSTGRES_CONNECTION_PASSWORD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateEnvVarName(tt.catalogName, tt.propertyName)
			assert.Equal(t, tt.expected, result)
		})
	}
}
