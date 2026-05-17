package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateCatalogConfigMaps validates that catalog ConfigMaps exist
// XTrinodeCatalog controller generates ConfigMaps from XTrinodeCatalog CRDs
// This function validates the generated ConfigMaps exist
func ValidateCatalogConfigMaps(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, catalogs []string, log logr.Logger) error {
	if len(catalogs) == 0 {
		return nil // No catalogs to validate
	}

	log.Info("Validating catalog ConfigMaps", "catalogs", catalogs, "namespace", xtrinode.Namespace)

	for _, catalogName := range catalogs {
		// XTrinodeCatalog controller generates ConfigMaps named: trino-catalog-{catalogName}
		configMapName := fmt.Sprintf("%s%s", config.CatalogConfigMapPrefix, catalogName)

		// Check if ConfigMap exists
		configMap := &corev1.ConfigMap{}
		err := cli.Get(ctx, client.ObjectKey{
			Name:      configMapName,
			Namespace: xtrinode.Namespace,
		}, configMap)

		if err != nil {
			if k8serrors.IsNotFound(err) {
				log.Info("Catalog ConfigMap not found - XTrinodeCatalog may not be ready",
					"catalog", catalogName,
					"expectedConfigMap", configMapName,
					"namespace", xtrinode.Namespace)
				// Don't fail - XTrinodeCatalog controller may still be processing
				// Trino will just not have this catalog until ConfigMap exists
			} else {
				log.Error(err, "failed to check catalog ConfigMap", "catalog", catalogName)
				return fmt.Errorf("failed to check catalog ConfigMap %s: %w", configMapName, err)
			}
		} else {
			log.V(1).Info("Catalog ConfigMap found", "catalog", catalogName, "configMap", configMapName)
		}
	}

	return nil
}

// DiscoverCatalogsFromConfigMaps automatically discovers catalogs from ConfigMaps in the namespace
// Looks for ConfigMaps matching pattern: trino-catalog-{catalogName}
// Returns list of catalog names found
func DiscoverCatalogsFromConfigMaps(ctx context.Context, cli client.Client, namespace string, log logr.Logger) ([]string, error) {
	// List all ConfigMaps in the namespace
	configMapList := &corev1.ConfigMapList{}
	err := cli.List(ctx, configMapList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list ConfigMaps in namespace %s: %w", namespace, err)
	}

	var discoveredCatalogs []string

	for i := range configMapList.Items {
		// Check if ConfigMap name starts with catalog prefix
		if strings.HasPrefix(configMapList.Items[i].Name, config.CatalogConfigMapPrefix) {
			// Extract catalog name (everything after prefix)
			catalogName := strings.TrimPrefix(configMapList.Items[i].Name, config.CatalogConfigMapPrefix)

			// Validate that ConfigMap contains {catalogName}.properties file
			expectedKey := fmt.Sprintf("%s.properties", catalogName)
			if _, exists := configMapList.Items[i].Data[expectedKey]; exists {
				discoveredCatalogs = append(discoveredCatalogs, catalogName)
				log.V(1).Info("Auto-discovered catalog", "catalog", catalogName, "configMap", configMapList.Items[i].Name)
			} else {
				log.V(1).Info("ConfigMap matches catalog pattern but missing properties file",
					"configMap", configMapList.Items[i].Name,
					"expectedKey", expectedKey,
					"namespace", namespace)
			}
		}
	}

	// Sort catalogs alphabetically for consistent ordering
	sort.Strings(discoveredCatalogs)

	log.Info("Auto-discovered catalogs from ConfigMaps",
		"catalogs", discoveredCatalogs,
		"namespace", namespace,
		"count", len(discoveredCatalogs))

	return discoveredCatalogs, nil
}

// GetEffectiveCatalogs returns the list of catalogs to use for a XTrinode
// Uses CatalogSelector to find matching XTrinodeCatalog CRDs
// Returns catalogs sorted alphabetically for consistent ordering
func GetEffectiveCatalogs(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) ([]string, error) {
	// If CatalogSelector is not specified, no catalogs
	if xtrinode.Spec.CatalogSelector == nil {
		log.V(1).Info("No CatalogSelector specified - no catalogs will be mounted")
		return []string{}, nil
	}

	// Convert LabelSelector to labels.Selector
	selector, err := metav1.LabelSelectorAsSelector(xtrinode.Spec.CatalogSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid CatalogSelector: %w", err)
	}

	selectedCatalogs, err := listSelectedCatalogs(ctx, cli, xtrinode.Namespace, selector)
	if err != nil {
		return nil, err
	}

	var catalogs []string
	for i := range selectedCatalogs {
		// Extract catalog name from XTrinodeCatalog name
		// XTrinodeCatalog controller generates ConfigMap named with CatalogConfigMapPrefix
		// TrimPrefix handles the case where prefix doesn't exist (returns original string)
		catalogName := strings.TrimPrefix(selectedCatalogs[i].Name, config.CatalogConfigMapPrefix)
		catalogs = append(catalogs, catalogName)
		log.V(1).Info("Found matching XTrinodeCatalog", "catalog", selectedCatalogs[i].Name, "catalogName", catalogName)
	}

	// Sort catalogs alphabetically for consistent ordering
	sort.Strings(catalogs)

	log.Info("Found catalogs via CatalogSelector", "catalogs", catalogs, "count", len(catalogs))
	return catalogs, nil
}

func listSelectedCatalogs(ctx context.Context, cli client.Client, namespace string, selector labels.Selector) ([]analyticsv1.XTrinodeCatalog, error) {
	catalogList := &analyticsv1.XTrinodeCatalogList{}
	if err := cli.List(ctx, catalogList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list XTrinodeCatalogs: %w", err)
	}

	selected := make([]analyticsv1.XTrinodeCatalog, 0, len(catalogList.Items))
	for i := range catalogList.Items {
		if selector.Matches(catalogSelectionLabels(&catalogList.Items[i])) {
			selected = append(selected, catalogList.Items[i])
		}
	}
	return selected, nil
}

func catalogSelectionLabels(catalog *analyticsv1.XTrinodeCatalog) labels.Set {
	if catalog == nil {
		return labels.Set{}
	}
	return labels.Set(catalog.Spec.Labels)
}

// DeleteCatalogConfigMaps is a no-op since XTrinodeCatalog controller manages ConfigMaps
// XTrinodeCatalog CRDs own their ConfigMaps via owner references
// This function is kept for API compatibility but does nothing
func DeleteCatalogConfigMaps(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	// XTrinodeCatalog controller manages ConfigMaps via owner references
	// When XTrinodeCatalog is deleted, ConfigMap is automatically deleted
	log.V(1).Info("Catalog ConfigMaps are managed by XTrinodeCatalog controller - skipping deletion")
	return nil
}
