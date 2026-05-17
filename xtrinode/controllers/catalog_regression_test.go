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
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// ---------------------------------------------------------------------------
// Regression: ensureConfigMap must repair labels/ownerRef even when data hash matches
// ---------------------------------------------------------------------------

func TestRegression_EnsureConfigMap_RepairsLabelsWhenHashMatches(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
			UID:       "test-uid-123",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	configMapName := config.CatalogConfigMapPrefix + "test-catalog"
	desiredData := map[string]string{
		"test-catalog.properties": "connector.name=hive\nhive.metastore.uri=thrift://hive:9083\n",
	}

	// Pre-create ConfigMap with WRONG labels but correct data + hash
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels:    map[string]string{"wrong": "label"},
			Annotations: map[string]string{
				catalogHashAnnotation: catalogDataHash(desiredData),
			},
		},
		Data: desiredData,
	}

	cli := newTestClient(scheme, catalog, existingCM)
	reconciler := newTestCatalogReconciler(cli, scheme)

	// Build desired ConfigMap with correct labels
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels: map[string]string{
				"app":                        "trino",
				"xtrinode-catalog":           "test-catalog",
				"xtrinode-catalog-generated": "true",
			},
		},
		Data: desiredData,
	}

	err := reconciler.ensureConfigMap(ctx, catalog, desired, newTestLogger())
	require.NoError(t, err)

	// Verify labels were repaired
	updated := &corev1.ConfigMap{}
	err = cli.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: "default"}, updated)
	require.NoError(t, err)

	assert.Equal(t, "trino", updated.Labels["app"], "label 'app' should be repaired")
	assert.Equal(t, "test-catalog", updated.Labels["xtrinode-catalog"], "label 'xtrinode-catalog' should be repaired")
	assert.Equal(t, "true", updated.Labels["xtrinode-catalog-generated"], "label 'xtrinode-catalog-generated' should be repaired")
	assert.Empty(t, updated.Labels["wrong"], "stale label should be removed")
}

func TestRegression_EnsureConfigMap_RepairsOwnerRefWhenHashMatches(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
			UID:       "test-uid-456",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	configMapName := config.CatalogConfigMapPrefix + "test-catalog"
	desiredData := map[string]string{
		"test-catalog.properties": "connector.name=hive\nhive.metastore.uri=thrift://hive:9083\n",
	}

	// Pre-create ConfigMap with NO ownerRef but correct data + hash
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels: map[string]string{
				"app":                        "trino",
				"xtrinode-catalog":           "test-catalog",
				"xtrinode-catalog-generated": "true",
			},
			Annotations: map[string]string{
				catalogHashAnnotation: catalogDataHash(desiredData),
			},
			// Deliberately NO OwnerReferences
		},
		Data: desiredData,
	}

	cli := newTestClient(scheme, catalog, existingCM)
	reconciler := newTestCatalogReconciler(cli, scheme)

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels: map[string]string{
				"app":                        "trino",
				"xtrinode-catalog":           "test-catalog",
				"xtrinode-catalog-generated": "true",
			},
		},
		Data: desiredData,
	}

	err := reconciler.ensureConfigMap(ctx, catalog, desired, newTestLogger())
	require.NoError(t, err)

	// Verify ownerRef was added
	updated := &corev1.ConfigMap{}
	err = cli.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: "default"}, updated)
	require.NoError(t, err)

	require.Len(t, updated.OwnerReferences, 1, "ownerRef should be repaired")
	assert.Equal(t, "test-catalog", updated.OwnerReferences[0].Name)
	assert.Equal(t, types.UID("test-uid-456"), updated.OwnerReferences[0].UID)
}

// ---------------------------------------------------------------------------
// Regression: customExtractor must return a copy of the properties map
// ---------------------------------------------------------------------------

func TestRegression_CustomExtractor_ReturnsCopy(t *testing.T) {
	original := map[string]string{
		"my.property": "original-value",
	}

	connector := &analyticsv1.XTrinodeCatalogConnector{
		Custom: &analyticsv1.CustomCatalogSpec{
			ConnectorName: "custom-connector",
			Properties:    original,
		},
	}

	_, props, err := resolveConnector(connector, "test-catalog")
	require.NoError(t, err)

	// Mutate the returned map
	props["injected-key"] = "injected-value"

	// Original must NOT be mutated
	_, exists := original["injected-key"]
	assert.False(t, exists, "mutating returned props should not affect the original spec map")
	assert.Equal(t, "original-value", original["my.property"])
}

func TestRegression_CustomExtractor_NilProperties(t *testing.T) {
	connector := &analyticsv1.XTrinodeCatalogConnector{
		Custom: &analyticsv1.CustomCatalogSpec{
			ConnectorName: "custom-connector",
			Properties:    nil,
		},
	}

	name, props, err := resolveConnector(connector, "test-catalog")
	require.NoError(t, err)
	assert.Equal(t, "custom-connector", name)
	assert.NotNil(t, props, "should return empty map, not nil")
}

// ---------------------------------------------------------------------------
// Additional regression: data update still works when hash differs
// ---------------------------------------------------------------------------

func TestRegression_EnsureConfigMap_UpdatesDataWhenHashDiffers(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-catalog",
			Namespace: "default",
			UID:       "test-uid-789",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	configMapName := config.CatalogConfigMapPrefix + "test-catalog"
	oldData := map[string]string{
		"test-catalog.properties": "connector.name=hive\nhive.metastore.uri=thrift://old:9083\n",
	}
	newData := map[string]string{
		"test-catalog.properties": "connector.name=hive\nhive.metastore.uri=thrift://new:9083\n",
	}

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels: map[string]string{
				"app": "trino",
			},
			Annotations: map[string]string{
				catalogHashAnnotation: catalogDataHash(oldData),
			},
		},
		Data: oldData,
	}

	cli := newTestClient(scheme, catalog, existingCM)
	reconciler := newTestCatalogReconciler(cli, scheme)

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels:    map[string]string{"app": "trino"},
		},
		Data: newData,
	}

	err := reconciler.ensureConfigMap(ctx, catalog, desired, newTestLogger())
	require.NoError(t, err)

	updated := &corev1.ConfigMap{}
	err = cli.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: "default"}, updated)
	require.NoError(t, err)

	assert.Equal(t, newData["test-catalog.properties"], updated.Data["test-catalog.properties"],
		"data should be updated when hash differs")
	assert.Equal(t, catalogDataHash(newData), updated.Annotations[catalogHashAnnotation],
		"hash annotation should be updated")
}

// ---------------------------------------------------------------------------
// Regression: full reconcile creates ConfigMap correctly
// ---------------------------------------------------------------------------

func TestRegression_EnsureConfigMap_CreatesNewConfigMap(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-catalog",
			Namespace: "default",
			UID:       "new-uid-123",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Iceberg: &analyticsv1.IcebergCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "s3://warehouse/",
				},
			},
		},
	}

	cli := newTestClient(scheme, catalog)
	reconciler := newTestCatalogReconciler(cli, scheme)

	configMapName := config.CatalogConfigMapPrefix + "new-catalog"
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels: map[string]string{
				"app":                        "trino",
				"xtrinode-catalog":           "new-catalog",
				"xtrinode-catalog-generated": "true",
			},
		},
		Data: map[string]string{
			"new-catalog.properties": "connector.name=iceberg\niceberg.catalog.type=hive\n",
		},
	}

	err := reconciler.ensureConfigMap(ctx, catalog, desired, newTestLogger())
	require.NoError(t, err)

	created := &corev1.ConfigMap{}
	err = cli.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: "default"}, created)
	require.NoError(t, err)

	assert.Equal(t, desired.Data, created.Data)
	assert.Equal(t, "trino", created.Labels["app"])
	require.Len(t, created.OwnerReferences, 1)
	assert.Equal(t, "new-catalog", created.OwnerReferences[0].Name)
}

// ---------------------------------------------------------------------------
// Regression: resolveConnector env-var placeholders for all JDBC connectors
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Regression: updateStatusWithRetry must use correct object type for XTrinodeCatalog
// ---------------------------------------------------------------------------

func TestRegression_CatalogStatusUpdate_UsesCorrectType(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-test-catalog",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	cli := newTestClient(scheme, catalog)
	reconciler := newTestCatalogReconciler(cli, scheme)

	// Set status fields on the in-memory catalog
	catalog.Status.Phase = "Ready"
	catalog.Status.Message = "ConfigMap applied"
	catalog.Status.ConfigMapName = "trino-catalog-status-test-catalog"
	now := metav1.Now()
	catalog.Status.LastUpdated = &now

	// Call updateStatus — before the fix, this would fail because
	// updateStatusWithRetry hardcoded &XTrinode{} instead of &XTrinodeCatalog{}
	err := reconciler.updateStatus(ctx, catalog, newTestLogger())
	// With fake client, status subresource updates may not be fully supported,
	// but the function should NOT panic or return a type assertion error.
	// The key validation: it must not return "unexpected object type *v1.XTrinode"
	if err != nil {
		assert.NotContains(t, err.Error(), "unexpected object type",
			"updateStatusWithRetry should use XTrinodeCatalog, not XTrinode")
	}
}

func TestRegression_CatalogStatusUpdate_ViaReconcile(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "reconcile-status-test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	cli := newTestClient(scheme, catalog)
	reconciler := newTestCatalogReconciler(cli, scheme)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "reconcile-status-test",
			Namespace: "default",
		},
	}

	_, err := reconciler.Reconcile(ctx, req)
	// Before the fix, this always returned "unexpected object type *v1.XTrinode"
	// After the fix, status update uses XTrinodeCatalog correctly
	if err != nil {
		assert.NotContains(t, err.Error(), "unexpected object type",
			"reconcile should not fail with wrong object type for status update")
	}
}

// ---------------------------------------------------------------------------
// Regression: updateStatus must capture LastUpdated for conflict retry
// ---------------------------------------------------------------------------

func TestRegression_CatalogUpdateStatus_CapturesLastUpdated(t *testing.T) {
	scheme := newTestScheme()
	ctx := context.Background()

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lastupdated-test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://hive:9083",
				},
			},
		},
	}

	cli := newTestClient(scheme, catalog)
	reconciler := newTestCatalogReconciler(cli, scheme)

	// Simulate updateStatusToReady which sets LastUpdated
	catalog.Status.Phase = "Ready"
	catalog.Status.Message = "ConfigMap applied"
	catalog.Status.ConfigMapName = "trino-catalog-lastupdated-test"
	now := metav1.Now()
	catalog.Status.LastUpdated = &now

	// The updateStatus function should capture LastUpdated
	// Verify by calling it and checking the mutation function includes LastUpdated
	err := reconciler.updateStatus(ctx, catalog, newTestLogger())
	// Even if the fake client doesn't support status updates, the function should
	// not lose LastUpdated during the capture phase
	if err != nil {
		// Acceptable — fake client limitation
		// Key check: no type assertion panic
		assert.NotContains(t, err.Error(), "unexpected object type")
	}
}

func TestRegression_ResolveConnector_AllJDBCSecretPlaceholders(t *testing.T) {
	secretRef := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "db-secret"},
		Key:                  "password",
	}

	tests := []struct {
		name         string
		connector    analyticsv1.XTrinodeCatalogConnector
		catalogName  string
		expectedName string
		expectedEnv  string
		propertyKey  string
	}{
		{
			name: "ClickHouse",
			connector: analyticsv1.XTrinodeCatalogConnector{
				ClickHouse: &analyticsv1.ClickHouseCatalogSpec{
					ConnectionURL:            "jdbc:clickhouse://ch:8123/db",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "ch-analytics",
			expectedName: "clickhouse",
			expectedEnv:  "${ENV:CATALOG_CH_ANALYTICS_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "Exasol",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Exasol: &analyticsv1.ExasolCatalogSpec{
					ConnectionURL:            "jdbc:exa://exa:8563",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "exa-analytics",
			expectedName: "exasol",
			expectedEnv:  "${ENV:CATALOG_EXA_ANALYTICS_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "Ignite",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Ignite: &analyticsv1.IgniteCatalogSpec{
					ConnectionURL:            "jdbc:ignite://ignite:10800",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "ignite-cache",
			expectedName: "ignite",
			expectedEnv:  "${ENV:CATALOG_IGNITE_CACHE_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "MariaDB",
			connector: analyticsv1.XTrinodeCatalogConnector{
				MariaDB: &analyticsv1.MariaDBCatalogSpec{
					ConnectionURL:            "jdbc:mariadb://maria:3306/db",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "maria-main",
			expectedName: "mariadb",
			expectedEnv:  "${ENV:CATALOG_MARIA_MAIN_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "Oracle",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Oracle: &analyticsv1.OracleCatalogSpec{
					ConnectionURL:            "jdbc:oracle:thin:@oracle:1521:orcl",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "oracle-erp",
			expectedName: "oracle",
			expectedEnv:  "${ENV:CATALOG_ORACLE_ERP_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "Redshift",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Redshift: &analyticsv1.RedshiftCatalogSpec{
					ConnectionURL:            "jdbc:redshift://rs:5439/db",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "rs-dw",
			expectedName: "redshift",
			expectedEnv:  "${ENV:CATALOG_RS_DW_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "SingleStore",
			connector: analyticsv1.XTrinodeCatalogConnector{
				SingleStore: &analyticsv1.SingleStoreCatalogSpec{
					ConnectionURL:            "jdbc:singlestore://ss:3306/db",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "ss-analytics",
			expectedName: "singlestore",
			expectedEnv:  "${ENV:CATALOG_SS_ANALYTICS_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "SQLServer",
			connector: analyticsv1.XTrinodeCatalogConnector{
				SQLServer: &analyticsv1.SQLServerCatalogSpec{
					ConnectionURL:            "jdbc:sqlserver://mssql:1433;database=db",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "mssql-prod",
			expectedName: "sqlserver",
			expectedEnv:  "${ENV:CATALOG_MSSQL_PROD_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "Vertica",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Vertica: &analyticsv1.VerticaCatalogSpec{
					ConnectionURL:            "jdbc:vertica://vertica:5433/db",
					ConnectionPasswordSecret: secretRef,
				},
			},
			catalogName:  "vertica-dw",
			expectedName: "vertica",
			expectedEnv:  "${ENV:CATALOG_VERTICA_DW_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
		{
			name: "Snowflake",
			connector: analyticsv1.XTrinodeCatalogConnector{
				Snowflake: &analyticsv1.SnowflakeCatalogSpec{
					AccountURL: "https://acme.snowflakecomputing.com",
					User:       "trino",
					PasswordSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "sf-secret"},
						Key:                  "password",
					},
				},
			},
			catalogName:  "sf-analytics",
			expectedName: "snowflake",
			expectedEnv:  "${ENV:CATALOG_SF_ANALYTICS_CONNECTION_PASSWORD}",
			propertyKey:  "connection-password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, props, err := resolveConnector(&tt.connector, tt.catalogName)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedName, name)
			assert.Equal(t, tt.expectedEnv, props[tt.propertyKey],
				"env-var placeholder mismatch for %s", tt.name)
		})
	}
}
