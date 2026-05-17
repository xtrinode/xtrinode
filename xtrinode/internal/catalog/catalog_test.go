package catalog

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateCatalogConfigMaps_EmptyCatalogs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	err := ValidateCatalogConfigMaps(ctx, cli, xtrinode, []string{}, log)
	assert.NoError(t, err)
}

func TestValidateCatalogConfigMaps_ConfigMapExists(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	catalogName := "iceberg"
	configMapName := config.CatalogConfigMapPrefix + catalogName
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
		},
		Data: map[string]string{
			catalogName + ".properties": "connector.name=iceberg",
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	err := ValidateCatalogConfigMaps(ctx, cli, xtrinode, []string{catalogName}, log)
	assert.NoError(t, err)
}

func TestValidateCatalogConfigMaps_ConfigMapNotFound(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	// NotFound errors are logged but don't fail validation
	err := ValidateCatalogConfigMaps(ctx, cli, xtrinode, []string{"missing"}, log)
	assert.NoError(t, err)
}

func TestValidateCatalogConfigMaps_OtherError(t *testing.T) {
	// Note: Testing actual API errors requires integration tests
	// Unit tests with fake client can't easily simulate non-NotFound errors
	// This test verifies NotFound errors are handled gracefully (tested above)
	t.Skip("API error simulation requires integration tests or custom client wrapper")
}

func TestValidateCatalogConfigMaps_MultipleCatalogs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	configMaps := []*corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.CatalogConfigMapPrefix + "iceberg",
				Namespace: "default",
			},
			Data: map[string]string{"iceberg.properties": "connector.name=iceberg"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.CatalogConfigMapPrefix + "hive",
				Namespace: "default",
			},
			Data: map[string]string{"hive.properties": "connector.name=hive"},
		},
	}

	objects := make([]client.Object, len(configMaps))
	for i, cm := range configMaps {
		objects[i] = cm
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	err := ValidateCatalogConfigMaps(ctx, cli, xtrinode, []string{"iceberg", "hive"}, log)
	assert.NoError(t, err)
}

func TestDiscoverCatalogsFromConfigMaps_NoConfigMaps(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	catalogs, err := DiscoverCatalogsFromConfigMaps(ctx, cli, "default", log)
	assert.NoError(t, err)
	assert.Empty(t, catalogs)
}

func TestDiscoverCatalogsFromConfigMaps_ValidCatalogs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	configMaps := []*corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.CatalogConfigMapPrefix + "iceberg",
				Namespace: "default",
			},
			Data: map[string]string{"iceberg.properties": "connector.name=iceberg"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.CatalogConfigMapPrefix + "hive",
				Namespace: "default",
			},
			Data: map[string]string{"hive.properties": "connector.name=hive"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-configmap",
				Namespace: "default",
			},
			Data: map[string]string{"data": "value"},
		},
	}

	objects := make([]client.Object, len(configMaps))
	for i, cm := range configMaps {
		objects[i] = cm
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	log := logr.Discard()

	catalogs, err := DiscoverCatalogsFromConfigMaps(ctx, cli, "default", log)
	assert.NoError(t, err)
	assert.Len(t, catalogs, 2)
	assert.Contains(t, catalogs, "iceberg")
	assert.Contains(t, catalogs, "hive")
	// Should be sorted alphabetically
	assert.Equal(t, []string{"hive", "iceberg"}, catalogs)
}

func TestDiscoverCatalogsFromConfigMaps_MissingPropertiesFile(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.CatalogConfigMapPrefix + "iceberg",
			Namespace: "default",
		},
		Data: map[string]string{
			"other.properties": "value",
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()
	log := logr.Discard()

	catalogs, err := DiscoverCatalogsFromConfigMaps(ctx, cli, "default", log)
	assert.NoError(t, err)
	assert.Empty(t, catalogs)
}

func TestDiscoverCatalogsFromConfigMaps_ListError(t *testing.T) {
	// Note: Testing actual API errors requires integration tests
	// Unit tests with fake client can't easily simulate List errors
	t.Skip("API error simulation requires integration tests or custom client wrapper")
}

func TestGetEffectiveCatalogs_NoSelector(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	catalogs, err := GetEffectiveCatalogs(ctx, cli, xtrinode, log)
	assert.NoError(t, err)
	assert.Empty(t, catalogs)
}

func TestGetEffectiveCatalogs_WithMatchingCatalogs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	catalog1 := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iceberg",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Iceberg: &analyticsv1.IcebergCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "s3://lakehouse/",
				},
			},
		},
	}

	catalog2 := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hive",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Hive: &analyticsv1.HiveCatalogSpec{
					MetastoreURI: "thrift://metastore:9083",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog1, catalog2).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "data"},
			},
		},
	}

	catalogs, err := GetEffectiveCatalogs(ctx, cli, xtrinode, log)
	assert.NoError(t, err)
	assert.Len(t, catalogs, 2)
	assert.Contains(t, catalogs, "iceberg")
	assert.Contains(t, catalogs, "hive")
	// Should be sorted alphabetically
	assert.Equal(t, []string{"hive", "iceberg"}, catalogs)
}

func TestGetEffectiveCatalogs_UsesSpecLabelsOverMetadataLabels(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "analytics",
			Namespace: "default",
			Labels:    map[string]string{"team": "metadata-only"},
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "spec-label"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				TPCH: &analyticsv1.TPCHCatalogSpec{},
			},
		},
	}
	metadataOnlyCatalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metadata-only",
			Namespace: "default",
			Labels:    map[string]string{"team": "spec-label"},
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Connector: analyticsv1.XTrinodeCatalogConnector{
				TPCH: &analyticsv1.TPCHCatalogSpec{},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog, metadataOnlyCatalog).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "spec-label"},
			},
		},
	}

	catalogs, err := GetEffectiveCatalogs(ctx, cli, xtrinode, log)
	require.NoError(t, err)
	assert.Equal(t, []string{"analytics"}, catalogs)

	xtrinode.Spec.CatalogSelector.MatchLabels["team"] = "metadata-only"
	catalogs, err = GetEffectiveCatalogs(ctx, cli, xtrinode, log)
	require.NoError(t, err)
	assert.Empty(t, catalogs)
}

func TestGetEffectiveCatalogs_InvalidSelector(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "team",
						Operator: "InvalidOperator",
						Values:   []string{"data"},
					},
				},
			},
		},
	}

	catalogs, err := GetEffectiveCatalogs(ctx, cli, xtrinode, log)
	assert.Error(t, err)
	assert.Nil(t, catalogs)
	assert.Contains(t, err.Error(), "invalid CatalogSelector")
}

func TestGetEffectiveCatalogs_ListError(t *testing.T) {
	// Note: Testing actual API errors requires integration tests
	// Unit tests with fake client can't easily simulate List errors
	t.Skip("API error simulation requires integration tests or custom client wrapper")
}

func TestGetEffectiveCatalogs_CatalogNameWithPrefix(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))

	// Test case where catalog name already includes prefix
	catalog := &analyticsv1.XTrinodeCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.CatalogConfigMapPrefix + "iceberg",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeCatalogSpec{
			Labels: map[string]string{"team": "data"},
			Connector: analyticsv1.XTrinodeCatalogConnector{
				Iceberg: &analyticsv1.IcebergCatalogSpec{
					CatalogType:  "hive",
					WarehouseURI: "s3://lakehouse/",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(catalog).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			CatalogSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "data"},
			},
		},
	}

	catalogs, err := GetEffectiveCatalogs(ctx, cli, xtrinode, log)
	assert.NoError(t, err)
	assert.Len(t, catalogs, 1)
	// Should strip prefix
	assert.Equal(t, []string{"iceberg"}, catalogs)
}

func TestDeleteCatalogConfigMaps_NoOp(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := logr.Discard()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	err := DeleteCatalogConfigMaps(ctx, cli, xtrinode, log)
	assert.NoError(t, err)
}
