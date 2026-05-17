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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CatalogSecretReference represents a secret reference from a catalog
type CatalogSecretReference struct {
	CatalogName       string
	PropertyName      string
	SecretKeySelector *corev1.SecretKeySelector
	EnvVarName        string
}

// ExtractCatalogSecretReferences extracts all secret references from XTrinodeCatalogs
// matching the given selector. Returns a list of environment variables that should
// be injected into Trino pods.
func ExtractCatalogSecretReferences(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) ([]corev1.EnvVar, error) {
	if xtrinode.Spec.CatalogSelector == nil {
		log.V(1).Info("No CatalogSelector specified - no catalog secrets to inject")
		return []corev1.EnvVar{}, nil
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

	var envVars []corev1.EnvVar
	var secretRefs []CatalogSecretReference

	for i := range selectedCatalogs {
		catalog := &selectedCatalogs[i]
		catalogName := strings.TrimPrefix(catalog.Name, config.CatalogConfigMapPrefix)

		// Extract secret references from connector
		refs := extractSecretReferencesFromConnector(catalog, catalogName)
		secretRefs = append(secretRefs, refs...)
	}

	// Convert secret references to environment variables
	for _, ref := range secretRefs {
		envVar := corev1.EnvVar{
			Name: ref.EnvVarName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: ref.SecretKeySelector,
			},
		}
		envVars = append(envVars, envVar)
		log.V(1).Info("Adding catalog secret env var",
			"catalog", ref.CatalogName,
			"property", ref.PropertyName,
			"envVar", ref.EnvVarName)
	}

	log.Info("Extracted catalog secret references",
		"count", len(envVars),
		"namespace", xtrinode.Namespace)

	return envVars, nil
}

// extractSecretReferencesFromConnector extracts secret references from a catalog's connector
func extractSecretReferencesFromConnector(catalog *analyticsv1.XTrinodeCatalog, catalogName string) []CatalogSecretReference {
	var refs []CatalogSecretReference

	connector := &catalog.Spec.Connector
	refs = append(refs, genericPropertySecretReferences(catalog, catalogName)...)

	// Hive S3 connector credentials
	if connector.Hive != nil && connector.Hive.S3AccessKeySecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "s3.aws-access-key",
			SecretKeySelector: connector.Hive.S3AccessKeySecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "s3.aws-access-key"),
		})
	}
	if connector.Hive != nil && connector.Hive.S3SecretKeySecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "s3.aws-secret-key",
			SecretKeySelector: connector.Hive.S3SecretKeySecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "s3.aws-secret-key"),
		})
	}

	// PostgreSQL connector
	if connector.Postgres != nil && connector.Postgres.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Postgres.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// MySQL connector
	if connector.MySQL != nil && connector.MySQL.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.MySQL.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// ClickHouse connector
	if connector.ClickHouse != nil && connector.ClickHouse.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.ClickHouse.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// Exasol connector
	if connector.Exasol != nil && connector.Exasol.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Exasol.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// Ignite connector
	if connector.Ignite != nil && connector.Ignite.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Ignite.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// MariaDB connector
	if connector.MariaDB != nil && connector.MariaDB.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.MariaDB.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// Oracle connector
	if connector.Oracle != nil && connector.Oracle.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Oracle.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// Redshift connector
	if connector.Redshift != nil && connector.Redshift.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Redshift.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// SingleStore connector
	if connector.SingleStore != nil && connector.SingleStore.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.SingleStore.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// SQLServer connector
	if connector.SQLServer != nil && connector.SQLServer.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.SQLServer.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// Vertica connector
	if connector.Vertica != nil && connector.Vertica.ConnectionPasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Vertica.ConnectionPasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	// Snowflake connector (uses PasswordSecret, not ConnectionPasswordSecret)
	if connector.Snowflake != nil && connector.Snowflake.PasswordSecret != nil {
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      "connection-password",
			SecretKeySelector: connector.Snowflake.PasswordSecret,
			EnvVarName:        calculateEnvVarName(catalog.Name, "connection-password"),
		})
	}

	return refs
}

func genericPropertySecretReferences(catalog *analyticsv1.XTrinodeCatalog, catalogName string) []CatalogSecretReference {
	secretRefs := catalog.Spec.Connector.GenericPropertySecretRefs()
	if len(secretRefs) == 0 {
		return nil
	}

	propertyNames := make([]string, 0, len(secretRefs))
	for propertyName := range secretRefs {
		if strings.TrimSpace(propertyName) == "" {
			continue
		}
		propertyNames = append(propertyNames, propertyName)
	}
	sort.Strings(propertyNames)

	refs := make([]CatalogSecretReference, 0, len(propertyNames))
	for _, propertyName := range propertyNames {
		trimmedProperty := strings.TrimSpace(propertyName)
		ref := secretRefs[propertyName]
		refs = append(refs, CatalogSecretReference{
			CatalogName:       catalogName,
			PropertyName:      trimmedProperty,
			SecretKeySelector: &ref,
			EnvVarName:        calculateEnvVarName(catalog.Name, trimmedProperty),
		})
	}
	return refs
}

// calculateEnvVarName generates a consistent environment variable name for catalog properties
// Format: CATALOG_<CATALOG_NAME>_<PROPERTY_NAME> (uppercase, dashes/dots replaced with underscores)
// This must match the function in xtrinodecatalog_controller.go
func calculateEnvVarName(catalogName, propertyName string) string {
	// Remove catalog prefix if present
	catalogName = strings.TrimPrefix(catalogName, config.CatalogConfigMapPrefix)
	// Replace special characters with underscores
	catalogName = strings.NewReplacer(".", "_", "-", "_").Replace(catalogName)
	propertyName = strings.NewReplacer(".", "_", "-", "_").Replace(propertyName)
	return fmt.Sprintf("CATALOG_%s_%s", strings.ToUpper(catalogName), strings.ToUpper(propertyName))
}
