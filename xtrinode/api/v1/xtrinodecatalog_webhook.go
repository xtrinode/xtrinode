package v1

import (
	"context"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var xtrinodecataloglog = logf.Log.WithName("xtrinodecatalog-resource")

// SetupWebhookWithManager sets up the catalog webhook with the manager.
func (c *XTrinodeCatalog) SetupWebhookWithManager(mgr ctrl.Manager) error {
	hook := &XTrinodeCatalogWebhook{
		secretAuthorizer: subjectAccessReviewCatalogSecretAuthorizer{client: mgr.GetClient()},
	}
	return ctrl.NewWebhookManagedBy(mgr).
		For(c).
		WithValidator(hook).
		Complete()
}

// XTrinodeCatalogWebhook adapts XTrinodeCatalog validation to controller-runtime admission.
// +kubebuilder:object:generate=false
type XTrinodeCatalogWebhook struct {
	secretAuthorizer catalogSecretAuthorizer
}

// +kubebuilder:object:generate=false
type catalogSecretAuthorizer interface {
	CanGet(ctx context.Context, req *admission.Request, namespace, secretName string) (allowed bool, reason string, err error)
}

// +kubebuilder:object:generate=false
type subjectAccessReviewCatalogSecretAuthorizer struct {
	client client.Client
}

func (a subjectAccessReviewCatalogSecretAuthorizer) CanGet(ctx context.Context, req *admission.Request, namespace, secretName string) (allowed bool, reason string, err error) {
	if a.client == nil {
		return false, "catalog Secret admission authorizer is not configured", nil
	}

	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   req.UserInfo.Username,
			Groups: req.UserInfo.Groups,
			UID:    req.UserInfo.UID,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      "get",
				Group:     "",
				Resource:  "secrets",
				Name:      secretName,
			},
		},
	}
	if len(req.UserInfo.Extra) > 0 {
		sar.Spec.Extra = make(map[string]authorizationv1.ExtraValue, len(req.UserInfo.Extra))
		for key, values := range req.UserInfo.Extra {
			sar.Spec.Extra[key] = authorizationv1.ExtraValue(values)
		}
	}

	if err := a.client.Create(ctx, sar); err != nil {
		return false, "", fmt.Errorf("failed to evaluate catalog Secret authorization: %w", err)
	}
	if sar.Status.Allowed {
		return true, "", nil
	}
	if sar.Status.Reason != "" {
		return false, sar.Status.Reason, nil
	}
	if sar.Status.EvaluationError != "" {
		return false, sar.Status.EvaluationError, nil
	}
	return false, fmt.Sprintf("user lacks get permission on Secret %q", secretName), nil
}

func (w *XTrinodeCatalogWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	catalog, ok := obj.(*XTrinodeCatalog)
	if !ok {
		return nil, fmt.Errorf("expected XTrinodeCatalog, got %T", obj)
	}
	warnings, err := catalog.ValidateCreate()
	if err != nil {
		return warnings, err
	}
	if err := w.validateSecretReferenceAdmission(ctx, catalog); err != nil {
		return warnings, err
	}
	return warnings, nil
}

func (w *XTrinodeCatalogWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	catalog, ok := newObj.(*XTrinodeCatalog)
	if !ok {
		return nil, fmt.Errorf("expected new XTrinodeCatalog, got %T", newObj)
	}
	warnings, err := catalog.ValidateUpdate(oldObj)
	if err != nil {
		return warnings, err
	}
	if err := w.validateSecretReferenceAdmission(ctx, catalog); err != nil {
		return warnings, err
	}
	return warnings, nil
}

func (w *XTrinodeCatalogWebhook) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	catalog, ok := obj.(*XTrinodeCatalog)
	if !ok {
		return nil, fmt.Errorf("expected XTrinodeCatalog, got %T", obj)
	}
	return catalog.ValidateDelete()
}

func (w *XTrinodeCatalogWebhook) validateSecretReferenceAdmission(ctx context.Context, catalog *XTrinodeCatalog) error {
	refs := catalogSecretReferenceFields(catalog)
	if len(refs) == 0 {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return apierrors.NewInvalid(
			schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinodeCatalog"},
			catalog.Name,
			field.ErrorList{
				field.Forbidden(field.NewPath("spec", "connector"), "catalog Secret references require admission request user info"),
			},
		)
	}
	if w.secretAuthorizer == nil {
		return apierrors.NewInvalid(
			schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinodeCatalog"},
			catalog.Name,
			field.ErrorList{
				field.Forbidden(field.NewPath("spec", "connector"), "catalog Secret references require admission authorization, but it is not configured"),
			},
		)
	}

	var allErrs field.ErrorList
	for _, ref := range refs {
		allowed, reason, err := w.secretAuthorizer.CanGet(ctx, &req, catalog.Namespace, ref.secretName)
		if err != nil {
			allErrs = append(allErrs, field.InternalError(ref.path, err))
			continue
		}
		if allowed {
			continue
		}
		message := fmt.Sprintf("referenced Secret %q requires get permission on secrets in namespace %q", ref.secretName, catalog.Namespace)
		if strings.TrimSpace(reason) != "" {
			message = fmt.Sprintf("%s: %s", message, reason)
		}
		allErrs = append(allErrs, field.Forbidden(ref.path, message))
	}

	if len(allErrs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinodeCatalog"},
		catalog.Name,
		allErrs,
	)
}

// +kubebuilder:webhook:path=/validate-analytics-xtrinode-io-v1-xtrinodecatalog,mutating=false,failurePolicy=fail,sideEffects=None,groups=analytics.xtrinode.io,resources=xtrinodecatalogs,verbs=create;update,versions=v1,name=vxtrinodecatalog.kb.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (c *XTrinodeCatalog) ValidateCreate() (admission.Warnings, error) {
	xtrinodecataloglog.Info("validate create", "name", c.Name)
	return nil, c.validateXTrinodeCatalog()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (c *XTrinodeCatalog) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	xtrinodecataloglog.Info("validate update", "name", c.Name)
	oldCatalog, ok := old.(*XTrinodeCatalog)
	if !ok {
		return nil, fmt.Errorf("expected old object to be of type XTrinodeCatalog")
	}
	if err := c.validateXTrinodeCatalog(); err != nil {
		return nil, err
	}
	if err := c.validateXTrinodeCatalogUpdate(oldCatalog); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (c *XTrinodeCatalog) ValidateDelete() (admission.Warnings, error) {
	xtrinodecataloglog.Info("validate delete", "name", c.Name)
	return nil, nil
}

func (c *XTrinodeCatalog) validateXTrinodeCatalog() error {
	var allErrs field.ErrorList
	connectorPath := field.NewPath("spec", "connector")
	setConnectors := catalogConnectorFields(&c.Spec.Connector)
	allErrs = append(allErrs, validateCatalogSpecLabels(field.NewPath("spec", "labels"), c.Spec.Labels)...)

	switch len(setConnectors) {
	case 0:
		allErrs = append(allErrs, field.Required(connectorPath, "exactly one connector field must be set"))
	case 1:
		allErrs = append(allErrs, validateCatalogConnector(connectorPath, &c.Spec.Connector)...)
	default:
		allErrs = append(allErrs, field.Invalid(connectorPath, setConnectors, "exactly one connector field must be set"))
	}
	allErrs = append(allErrs, validateCatalogPlaintextSensitiveProperties(connectorPath, &c.Spec.Connector)...)
	allErrs = append(allErrs, validateCatalogGeneratedPropertyCollisions(connectorPath, &c.Spec.Connector)...)
	allErrs = append(allErrs, validateCatalogPropertySecretRefs(connectorPath, &c.Spec.Connector)...)

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinodeCatalog"},
		c.Name,
		allErrs,
	)
}

func (c *XTrinodeCatalog) validateXTrinodeCatalogUpdate(old *XTrinodeCatalog) error {
	oldIdentity := catalogConnectorIdentity(&old.Spec.Connector)
	newIdentity := catalogConnectorIdentity(&c.Spec.Connector)
	if oldIdentity == newIdentity {
		return nil
	}

	allErrs := field.ErrorList{
		field.Forbidden(
			field.NewPath("spec", "connector"),
			fmt.Sprintf("connector identity is immutable (old: %s, new: %s)", oldIdentity, newIdentity),
		),
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinodeCatalog"},
		c.Name,
		allErrs,
	)
}

func catalogConnectorIdentity(connector *XTrinodeCatalogConnector) string {
	fields := catalogConnectorFields(connector)
	if len(fields) != 1 {
		return strings.Join(fields, ",")
	}
	if fields[0] == "custom" && connector.Custom != nil {
		return fmt.Sprintf("custom:%s", connector.Custom.ConnectorName)
	}
	return fields[0]
}

func validateCatalogSpecLabels(path *field.Path, values map[string]string) field.ErrorList {
	var allErrs field.ErrorList
	for key, value := range values {
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(path.Key(key), key, strings.Join(errs, "; ")))
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(path.Key(key), value, strings.Join(errs, "; ")))
		}
	}
	return allErrs
}

func validateCatalogConnector(path *field.Path, connector *XTrinodeCatalogConnector) field.ErrorList {
	if allErrs, handled := validateCommonCatalogConnector(path, connector); handled {
		return allErrs
	}
	if allErrs, handled := validateAdditionalCatalogConnector(path, connector); handled {
		return allErrs
	}
	return nil
}

func validateCommonCatalogConnector(path *field.Path, connector *XTrinodeCatalogConnector) (field.ErrorList, bool) {
	var allErrs field.ErrorList

	switch {
	case connector.BigQuery != nil:
		allErrs = append(allErrs, validateBigQueryCatalog(path.Child("bigQuery"), connector.BigQuery)...)
	case connector.Cassandra != nil:
		allErrs = appendRequiredString(allErrs, path.Child("cassandra", "contactPoints"), connector.Cassandra.ContactPoints)
	case connector.ClickHouse != nil:
		allErrs = appendRequiredString(allErrs, path.Child("clickHouse", "connectionURL"), connector.ClickHouse.ConnectionURL)
	case connector.Custom != nil:
		allErrs = appendRequiredString(allErrs, path.Child("custom", "connectorName"), connector.Custom.ConnectorName)
	case connector.DeltaLake != nil:
		allErrs = appendRequiredString(allErrs, path.Child("deltaLake", "catalogType"), connector.DeltaLake.CatalogType)
		allErrs = appendRequiredString(allErrs, path.Child("deltaLake", "warehouseURI"), connector.DeltaLake.WarehouseURI)
	case connector.Druid != nil:
		allErrs = appendRequiredString(allErrs, path.Child("druid", "brokerURL"), connector.Druid.BrokerURL)
	case connector.Elasticsearch != nil:
		allErrs = appendRequiredString(allErrs, path.Child("elasticsearch", "host"), connector.Elasticsearch.Host)
	case connector.Exasol != nil:
		allErrs = appendRequiredString(allErrs, path.Child("exasol", "connectionURL"), connector.Exasol.ConnectionURL)
	case connector.GoogleSheets != nil:
		allErrs = appendRequiredString(allErrs, path.Child("googleSheets", "credentialsFilePath"), connector.GoogleSheets.CredentialsFilePath)
	case connector.Hive != nil:
		allErrs = append(allErrs, validateHiveCatalog(path.Child("hive"), connector.Hive)...)
	case connector.Hudi != nil:
		allErrs = appendRequiredString(allErrs, path.Child("hudi", "metastoreURI"), connector.Hudi.MetastoreURI)
	case connector.Iceberg != nil:
		allErrs = appendRequiredString(allErrs, path.Child("iceberg", "catalogType"), connector.Iceberg.CatalogType)
		allErrs = appendRequiredString(allErrs, path.Child("iceberg", "warehouseURI"), connector.Iceberg.WarehouseURI)
	case connector.Ignite != nil:
		allErrs = appendRequiredString(allErrs, path.Child("ignite", "connectionURL"), connector.Ignite.ConnectionURL)
	case connector.Kafka != nil:
		if len(connector.Kafka.KafkaNodes) == 0 {
			allErrs = append(allErrs, field.Required(path.Child("kafka", "kafkaNodes"), "at least one Kafka node is required"))
		}
	case connector.Lakehouse != nil:
		allErrs = appendRequiredString(allErrs, path.Child("lakehouse", "catalogType"), connector.Lakehouse.CatalogType)
	default:
		return nil, false
	}

	return allErrs, true
}

func validateAdditionalCatalogConnector(path *field.Path, connector *XTrinodeCatalogConnector) (field.ErrorList, bool) {
	var allErrs field.ErrorList

	switch {
	case connector.Loki != nil:
		allErrs = appendRequiredString(allErrs, path.Child("loki", "uri"), connector.Loki.URI)
	case connector.MariaDB != nil:
		allErrs = appendRequiredString(allErrs, path.Child("mariaDB", "connectionURL"), connector.MariaDB.ConnectionURL)
	case connector.MongoDB != nil:
		allErrs = appendRequiredString(allErrs, path.Child("mongodb", "connectionURI"), connector.MongoDB.ConnectionURI)
	case connector.MySQL != nil:
		allErrs = appendRequiredString(allErrs, path.Child("mysql", "connectionURL"), connector.MySQL.ConnectionURL)
	case connector.OpenSearch != nil:
		allErrs = appendRequiredString(allErrs, path.Child("openSearch", "host"), connector.OpenSearch.Host)
	case connector.Oracle != nil:
		allErrs = appendRequiredString(allErrs, path.Child("oracle", "connectionURL"), connector.Oracle.ConnectionURL)
	case connector.Pinot != nil:
		allErrs = appendRequiredString(allErrs, path.Child("pinot", "controllerURLs"), connector.Pinot.ControllerURLs)
	case connector.Postgres != nil:
		allErrs = appendRequiredString(allErrs, path.Child("postgres", "connectionURL"), connector.Postgres.ConnectionURL)
	case connector.Prometheus != nil:
		allErrs = appendRequiredString(allErrs, path.Child("prometheus", "uri"), connector.Prometheus.URI)
	case connector.Redis != nil:
		allErrs = appendRequiredString(allErrs, path.Child("redis", "nodes"), connector.Redis.Nodes)
	case connector.Redshift != nil:
		allErrs = appendRequiredString(allErrs, path.Child("redshift", "connectionURL"), connector.Redshift.ConnectionURL)
	case connector.SingleStore != nil:
		allErrs = appendRequiredString(allErrs, path.Child("singleStore", "connectionURL"), connector.SingleStore.ConnectionURL)
	case connector.Snowflake != nil:
		allErrs = appendRequiredString(allErrs, path.Child("snowflake", "accountURL"), connector.Snowflake.AccountURL)
		allErrs = appendRequiredString(allErrs, path.Child("snowflake", "user"), connector.Snowflake.User)
	case connector.SQLServer != nil:
		allErrs = appendRequiredString(allErrs, path.Child("sqlServer", "connectionURL"), connector.SQLServer.ConnectionURL)
	case connector.Thrift != nil:
		allErrs = appendRequiredString(allErrs, path.Child("thrift", "host"), connector.Thrift.Host)
		if connector.Thrift.Port <= 0 {
			allErrs = append(allErrs, field.Invalid(path.Child("thrift", "port"), connector.Thrift.Port, "must be greater than zero"))
		}
	case connector.Vertica != nil:
		allErrs = appendRequiredString(allErrs, path.Child("vertica", "connectionURL"), connector.Vertica.ConnectionURL)
	default:
		return nil, false
	}

	return allErrs, true
}

func validateHiveCatalog(path *field.Path, hive *HiveCatalogSpec) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = appendRequiredString(allErrs, path.Child("metastoreURI"), hive.MetastoreURI)
	if (hive.S3AccessKeySecret == nil) != (hive.S3SecretKeySecret == nil) {
		allErrs = append(allErrs, field.Invalid(path, "partial S3 credential secret configuration", "s3AccessKeySecret and s3SecretKeySecret must be set together"))
	}
	allErrs = append(allErrs, validateSecretKeySelector(path.Child("s3AccessKeySecret"), hive.S3AccessKeySecret)...)
	allErrs = append(allErrs, validateSecretKeySelector(path.Child("s3SecretKeySecret"), hive.S3SecretKeySecret)...)
	return allErrs
}

func validateBigQueryCatalog(path *field.Path, bigQuery *BigQueryCatalogSpec) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = appendRequiredString(allErrs, path.Child("projectID"), bigQuery.ProjectID)
	allErrs = append(allErrs, validateMountedCredentialFilePath(path.Child("credentialsFile"), bigQuery.CredentialsFile)...)
	if bigQuery.CredentialsFile != "" && hasCatalogPropertySecretRef(bigQuery.PropertySecretRefs, "bigquery.credentials-key") {
		allErrs = append(allErrs, field.Forbidden(
			path.Child("credentialsFile"),
			"use either credentialsFile or propertySecretRefs[bigquery.credentials-key], not both",
		))
	}
	return allErrs
}

func validateCatalogPlaintextSensitiveProperties(path *field.Path, connector *XTrinodeCatalogConnector) field.ErrorList {
	var allErrs field.ErrorList
	for _, propMap := range catalogPropertyMaps(path, connector) {
		allErrs = append(allErrs, validateNoPlaintextSensitiveCatalogProperties(propMap.path, propMap.properties)...)
	}
	return allErrs
}

func validateNoPlaintextSensitiveCatalogProperties(path *field.Path, properties map[string]string) field.ErrorList {
	var allErrs field.ErrorList
	for key, value := range properties {
		normalizedKey := normalizeCatalogPropertyKey(key)
		replacement, hasReplacement := sensitiveCatalogPropertyReplacement(normalizedKey)
		if hasReplacement {
			allErrs = append(allErrs, field.Forbidden(
				path.Key(key),
				fmt.Sprintf("use %s instead of plaintext catalog properties", replacement),
			))
			continue
		}
		if isSensitiveCatalogPropertyKey(normalizedKey) || looksLikeCredentialJSON(value) {
			allErrs = append(allErrs, field.Forbidden(
				path.Key(key),
				"sensitive catalog properties must use typed Secret references or an operator-managed Secret injection path instead of plaintext ConfigMap properties",
			))
		}
	}
	return allErrs
}

func validateCatalogGeneratedPropertyCollisions(path *field.Path, connector *XTrinodeCatalogConnector) field.ErrorList {
	reserved := generatedCatalogPropertyKeys(connector)
	if len(reserved) == 0 {
		return nil
	}

	var allErrs field.ErrorList
	for _, propMap := range catalogPropertyMaps(path, connector) {
		allErrs = append(allErrs, validateNoReservedCatalogPropertyNames(propMap.path, mapKeys(propMap.properties), reserved)...)
	}
	for _, propMap := range catalogPropertySecretRefMaps(path, connector) {
		allErrs = append(allErrs, validateNoReservedCatalogPropertyNames(propMap.path, mapKeys(propMap.refs), reserved)...)
	}
	return allErrs
}

func validateNoReservedCatalogPropertyNames(path *field.Path, names []string, reserved map[string]string) field.ErrorList {
	var allErrs field.ErrorList
	for _, name := range names {
		normalizedKey := normalizeCatalogPropertyKey(name)
		if message, ok := reserved[normalizedKey]; ok {
			allErrs = append(allErrs, field.Forbidden(path.Key(name), message))
		}
	}
	return allErrs
}

//nolint:gocyclo // connector union requires explicit generated-key coverage.
func generatedCatalogPropertyKeys(connector *XTrinodeCatalogConnector) map[string]string {
	reserved := map[string]string{
		"connector.name": "connector.name is generated from the selected connector type",
	}
	add := func(key string) {
		reserved[normalizeCatalogPropertyKey(key)] = "property is generated from typed connector fields; use the typed XTrinodeCatalog field instead"
	}
	addIf := func(condition bool, key string) {
		if condition {
			add(key)
		}
	}
	addJDBC := func(connectionUser string, passwordSecret *corev1.SecretKeySelector) {
		add("connection-url")
		addIf(connectionUser != "", "connection-user")
		addIf(passwordSecret != nil, "connection-password")
	}

	if connector == nil {
		return reserved
	}
	switch {
	case connector.BigQuery != nil:
		add("bigquery.project-id")
		addIf(connector.BigQuery.ParentProjectID != "", "bigquery.parent-project-id")
		addIf(connector.BigQuery.CredentialsFile != "", "bigquery.credentials-file")
	case connector.Cassandra != nil:
		add("cassandra.contact-points")
		addIf(connector.Cassandra.Port > 0, "cassandra.native-protocol-port")
	case connector.ClickHouse != nil:
		addJDBC(connector.ClickHouse.ConnectionUser, connector.ClickHouse.ConnectionPasswordSecret)
	case connector.DeltaLake != nil:
		switch normalizeGeneratedCatalogType(connector.DeltaLake.CatalogType) {
		case "glue":
			add("hive.metastore")
		case "hive", "hive_metastore":
			addIf(connector.DeltaLake.WarehouseURI != "", "hive.metastore.uri")
		}
	case connector.Druid != nil:
		add("connection-url")
	case connector.DuckDB != nil:
		addIf(connector.DuckDB.DatabasePath != "", "connection-url")
	case connector.Elasticsearch != nil:
		add("elasticsearch.host")
		addIf(connector.Elasticsearch.Port > 0, "elasticsearch.port")
		addIf(connector.Elasticsearch.DefaultSchema != "", "elasticsearch.default-schema-name")
	case connector.Exasol != nil:
		addJDBC(connector.Exasol.ConnectionUser, connector.Exasol.ConnectionPasswordSecret)
	case connector.Faker != nil:
		addIf(connector.Faker.DefaultLimit > 0, "faker.default-limit")
		addIf(connector.Faker.NullProbability > 0, "faker.null-probability")
	case connector.GoogleSheets != nil:
		add("gsheets.credentials-path")
		addIf(connector.GoogleSheets.MetadataSheetID != "", "gsheets.metadata-sheet-id")
	case connector.Hive != nil:
		add("hive.metastore.uri")
		addIf(connector.Hive.S3Endpoint != "", "s3.endpoint")
		addIf(connector.Hive.S3Endpoint != "" || connector.Hive.S3AccessKeySecret != nil || connector.Hive.S3SecretKeySecret != nil, "fs.native-s3.enabled")
		addIf(connector.Hive.S3AccessKeySecret != nil, "s3.aws-access-key")
		addIf(connector.Hive.S3SecretKeySecret != nil, "s3.aws-secret-key")
	case connector.Hudi != nil:
		add("hive.metastore.uri")
	case connector.Iceberg != nil:
		add("iceberg.catalog.type")
	case connector.Ignite != nil:
		addJDBC(connector.Ignite.ConnectionUser, connector.Ignite.ConnectionPasswordSecret)
	case connector.Kafka != nil:
		add("kafka.nodes")
	case connector.Lakehouse != nil:
		add("lakehouse.table-type")
		addIf(connector.Lakehouse.MetastoreURI != "", "hive.metastore")
		addIf(connector.Lakehouse.MetastoreURI != "", "hive.metastore.uri")
	case connector.Loki != nil:
		add("loki.uri")
	case connector.MariaDB != nil:
		addJDBC(connector.MariaDB.ConnectionUser, connector.MariaDB.ConnectionPasswordSecret)
	case connector.Memory != nil:
		addIf(connector.Memory.MaxDataPerNode != "", "memory.max-data-per-node")
	case connector.MongoDB != nil:
		add("mongodb.connection-url")
	case connector.MySQL != nil:
		addJDBC(connector.MySQL.ConnectionUser, connector.MySQL.ConnectionPasswordSecret)
	case connector.OpenSearch != nil:
		add("opensearch.host")
		addIf(connector.OpenSearch.Port > 0, "opensearch.port")
		addIf(connector.OpenSearch.DefaultSchema != "", "opensearch.default-schema-name")
	case connector.Oracle != nil:
		addJDBC(connector.Oracle.ConnectionUser, connector.Oracle.ConnectionPasswordSecret)
	case connector.Pinot != nil:
		add("pinot.controller-urls")
	case connector.Postgres != nil:
		addJDBC(connector.Postgres.ConnectionUser, connector.Postgres.ConnectionPasswordSecret)
	case connector.Prometheus != nil:
		add("prometheus.uri")
	case connector.Redis != nil:
		add("redis.nodes")
		addIf(connector.Redis.Database > 0, "redis.database-index")
	case connector.Redshift != nil:
		addJDBC(connector.Redshift.ConnectionUser, connector.Redshift.ConnectionPasswordSecret)
	case connector.SingleStore != nil:
		addJDBC(connector.SingleStore.ConnectionUser, connector.SingleStore.ConnectionPasswordSecret)
	case connector.Snowflake != nil:
		add("connection-url")
		add("connection-user")
		add("snowflake.account")
		addIf(connector.Snowflake.Database != "", "snowflake.database")
		addIf(connector.Snowflake.Role != "", "snowflake.role")
		addIf(connector.Snowflake.Warehouse != "", "snowflake.warehouse")
		addIf(connector.Snowflake.PasswordSecret != nil, "connection-password")
	case connector.SQLServer != nil:
		addJDBC(connector.SQLServer.ConnectionUser, connector.SQLServer.ConnectionPasswordSecret)
	case connector.Thrift != nil:
		add("trino.thrift.client.addresses")
	case connector.Vertica != nil:
		addJDBC(connector.Vertica.ConnectionUser, connector.Vertica.ConnectionPasswordSecret)
	}
	return reserved
}

func normalizeGeneratedCatalogType(catalogType string) string {
	return strings.ToLower(strings.TrimSpace(catalogType))
}

func mapKeys[V any](values map[string]V) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func validateCatalogPropertySecretRefs(path *field.Path, connector *XTrinodeCatalogConnector) field.ErrorList {
	var allErrs field.ErrorList
	for _, propMap := range catalogPropertySecretRefMaps(path, connector) {
		for propertyName, ref := range propMap.refs {
			propertyPath := propMap.path.Key(propertyName)
			trimmedProperty := strings.TrimSpace(propertyName)
			if trimmedProperty == "" {
				allErrs = append(allErrs, field.Required(propertyPath, "property name must be set"))
			} else if trimmedProperty != propertyName {
				allErrs = append(allErrs, field.Invalid(propertyPath, propertyName, "property name must not have leading or trailing whitespace"))
			} else if strings.ContainsAny(propertyName, "\r\n=") {
				allErrs = append(allErrs, field.Invalid(propertyPath, propertyName, "property name must not contain newlines or '='"))
			}
			refCopy := ref
			allErrs = append(allErrs, validateSecretKeySelector(propertyPath, &refCopy)...)
		}
	}
	return allErrs
}

func sensitiveCatalogPropertyReplacement(normalizedKey string) (string, bool) {
	// #nosec G101 -- map values are replacement field names, not credential values.
	replacements := map[string]string{
		"connection-password":    "connectionPasswordSecret",
		"snowflake.password":     "passwordSecret",
		"s3.aws-access-key":      "s3AccessKeySecret",
		"s3.aws-secret-key":      "s3SecretKeySecret",
		"hive.s3.aws-access-key": "s3AccessKeySecret",
		"hive.s3.aws-secret-key": "s3SecretKeySecret",
		"s3.access-key":          "s3AccessKeySecret",
		"s3.secret-key":          "s3SecretKeySecret",
		"hive.s3.access-key":     "s3AccessKeySecret",
		"hive.s3.secret-key":     "s3SecretKeySecret",
		"hive.s3.aws.access-key": "s3AccessKeySecret",
		"hive.s3.aws.secret-key": "s3SecretKeySecret",
		"s3.aws.access-key":      "s3AccessKeySecret",
		"s3.aws.secret-key":      "s3SecretKeySecret",
	}
	replacement, ok := replacements[normalizedKey]
	return replacement, ok
}

func normalizeCatalogPropertyKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	return normalized
}

func isSensitiveCatalogPropertyKey(normalizedKey string) bool {
	compactKey := strings.NewReplacer("-", "", ".", "", "_", "").Replace(normalizedKey)
	switch {
	case strings.Contains(compactKey, "password"):
		return true
	case strings.Contains(compactKey, "secret"):
		return true
	case strings.Contains(compactKey, "accesskey"):
		return true
	case strings.Contains(compactKey, "privatekey"):
		return true
	case strings.Contains(compactKey, "apikey"):
		return true
	case strings.Contains(compactKey, "token"):
		return true
	case strings.Contains(compactKey, "keytab"):
		return true
	case strings.Contains(compactKey, "credential"):
		return true
	default:
		return false
	}
}

func looksLikeCredentialJSON(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(normalized, "private_key") ||
		strings.Contains(normalized, "client_secret") ||
		strings.Contains(normalized, "-----begin private key-----")
}

func validateMountedCredentialFilePath(path *field.Path, value string) field.ErrorList {
	if value == "" {
		return nil
	}

	var allErrs field.ErrorList
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed != value:
		allErrs = append(allErrs, field.Invalid(path, value, "credentials file path must not have leading or trailing whitespace"))
	case strings.ContainsAny(value, "\x00\r\n"):
		allErrs = append(allErrs, field.Invalid(path, value, "credentials file path must not contain control characters or newlines"))
	case looksLikeCredentialJSON(value):
		allErrs = append(allErrs, field.Forbidden(path, "inline credential material must use propertySecretRefs instead of credentialsFile"))
	case !strings.HasPrefix(value, "/"):
		allErrs = append(allErrs, field.Invalid(path, value, "credentials file path must be an absolute mounted file path"))
	}
	return allErrs
}

func hasCatalogPropertySecretRef(refs map[string]corev1.SecretKeySelector, normalizedKey string) bool {
	for propertyName := range refs {
		if normalizeCatalogPropertyKey(propertyName) == normalizedKey {
			return true
		}
	}
	return false
}

func validateSecretKeySelector(path *field.Path, ref *corev1.SecretKeySelector) field.ErrorList {
	if ref == nil {
		return nil
	}

	var allErrs field.ErrorList
	if strings.TrimSpace(ref.Name) == "" {
		allErrs = append(allErrs, field.Required(path.Child("name"), "Secret name must be set"))
	}
	if strings.TrimSpace(ref.Key) == "" {
		allErrs = append(allErrs, field.Required(path.Child("key"), "Secret key must be set"))
	}
	return allErrs
}

type catalogPropertyMap struct {
	path       *field.Path
	properties map[string]string
}

type catalogPropertySecretRefMap struct {
	path *field.Path
	refs map[string]corev1.SecretKeySelector
}

//nolint:gocyclo // connector union requires explicit property-map coverage.
func catalogPropertyMaps(path *field.Path, connector *XTrinodeCatalogConnector) []catalogPropertyMap {
	if connector == nil {
		return nil
	}
	var maps []catalogPropertyMap
	appendMap := func(path *field.Path, properties map[string]string) {
		if len(properties) == 0 {
			return
		}
		maps = append(maps, catalogPropertyMap{path: path, properties: properties})
	}

	if connector.BigQuery != nil {
		appendMap(path.Child("bigQuery", "properties"), connector.BigQuery.Properties)
	}
	if connector.BlackHole != nil {
		appendMap(path.Child("blackHole", "properties"), connector.BlackHole.Properties)
	}
	if connector.Cassandra != nil {
		appendMap(path.Child("cassandra", "properties"), connector.Cassandra.Properties)
	}
	if connector.ClickHouse != nil {
		appendMap(path.Child("clickHouse", "properties"), connector.ClickHouse.Properties)
	}
	if connector.Custom != nil {
		appendMap(path.Child("custom", "properties"), connector.Custom.Properties)
	}
	if connector.DeltaLake != nil {
		appendMap(path.Child("deltaLake", "properties"), connector.DeltaLake.Properties)
	}
	if connector.Druid != nil {
		appendMap(path.Child("druid", "properties"), connector.Druid.Properties)
	}
	if connector.DuckDB != nil {
		appendMap(path.Child("duckDB", "properties"), connector.DuckDB.Properties)
	}
	if connector.Elasticsearch != nil {
		appendMap(path.Child("elasticsearch", "properties"), connector.Elasticsearch.Properties)
	}
	if connector.Exasol != nil {
		appendMap(path.Child("exasol", "properties"), connector.Exasol.Properties)
	}
	if connector.Faker != nil {
		appendMap(path.Child("faker", "properties"), connector.Faker.Properties)
	}
	if connector.GoogleSheets != nil {
		appendMap(path.Child("googleSheets", "properties"), connector.GoogleSheets.Properties)
	}
	if connector.Hive != nil {
		appendMap(path.Child("hive", "properties"), connector.Hive.Properties)
	}
	if connector.Hudi != nil {
		appendMap(path.Child("hudi", "properties"), connector.Hudi.Properties)
	}
	if connector.Iceberg != nil {
		appendMap(path.Child("iceberg", "properties"), connector.Iceberg.Properties)
	}
	if connector.Ignite != nil {
		appendMap(path.Child("ignite", "properties"), connector.Ignite.Properties)
	}
	if connector.JMX != nil {
		appendMap(path.Child("jmx", "properties"), connector.JMX.Properties)
	}
	if connector.Kafka != nil {
		appendMap(path.Child("kafka", "properties"), connector.Kafka.Properties)
	}
	if connector.Lakehouse != nil {
		appendMap(path.Child("lakehouse", "properties"), connector.Lakehouse.Properties)
	}
	if connector.Loki != nil {
		appendMap(path.Child("loki", "properties"), connector.Loki.Properties)
	}
	if connector.MariaDB != nil {
		appendMap(path.Child("mariaDB", "properties"), connector.MariaDB.Properties)
	}
	if connector.Memory != nil {
		appendMap(path.Child("memory", "properties"), connector.Memory.Properties)
	}
	if connector.MongoDB != nil {
		appendMap(path.Child("mongodb", "properties"), connector.MongoDB.Properties)
	}
	if connector.MySQL != nil {
		appendMap(path.Child("mysql", "properties"), connector.MySQL.Properties)
	}
	if connector.OpenSearch != nil {
		appendMap(path.Child("openSearch", "properties"), connector.OpenSearch.Properties)
	}
	if connector.Oracle != nil {
		appendMap(path.Child("oracle", "properties"), connector.Oracle.Properties)
	}
	if connector.Pinot != nil {
		appendMap(path.Child("pinot", "properties"), connector.Pinot.Properties)
	}
	if connector.Postgres != nil {
		appendMap(path.Child("postgres", "properties"), connector.Postgres.Properties)
	}
	if connector.Prometheus != nil {
		appendMap(path.Child("prometheus", "properties"), connector.Prometheus.Properties)
	}
	if connector.Redis != nil {
		appendMap(path.Child("redis", "properties"), connector.Redis.Properties)
	}
	if connector.Redshift != nil {
		appendMap(path.Child("redshift", "properties"), connector.Redshift.Properties)
	}
	if connector.SingleStore != nil {
		appendMap(path.Child("singleStore", "properties"), connector.SingleStore.Properties)
	}
	if connector.Snowflake != nil {
		appendMap(path.Child("snowflake", "properties"), connector.Snowflake.Properties)
	}
	if connector.SQLServer != nil {
		appendMap(path.Child("sqlServer", "properties"), connector.SQLServer.Properties)
	}
	if connector.System != nil {
		appendMap(path.Child("system", "properties"), connector.System.Properties)
	}
	if connector.Thrift != nil {
		appendMap(path.Child("thrift", "properties"), connector.Thrift.Properties)
	}
	if connector.TPCDS != nil {
		appendMap(path.Child("tpcds", "properties"), connector.TPCDS.Properties)
	}
	if connector.TPCH != nil {
		appendMap(path.Child("tpch", "properties"), connector.TPCH.Properties)
	}
	if connector.Vertica != nil {
		appendMap(path.Child("vertica", "properties"), connector.Vertica.Properties)
	}

	return maps
}

//nolint:gocyclo // connector union requires explicit Secret-ref coverage.
func catalogPropertySecretRefMaps(path *field.Path, connector *XTrinodeCatalogConnector) []catalogPropertySecretRefMap {
	if connector == nil {
		return nil
	}
	var maps []catalogPropertySecretRefMap
	appendMap := func(path *field.Path, refs map[string]corev1.SecretKeySelector) {
		if len(refs) == 0 {
			return
		}
		maps = append(maps, catalogPropertySecretRefMap{path: path, refs: refs})
	}

	if connector.BigQuery != nil {
		appendMap(path.Child("bigQuery", "propertySecretRefs"), connector.BigQuery.PropertySecretRefs)
	}
	if connector.BlackHole != nil {
		appendMap(path.Child("blackHole", "propertySecretRefs"), connector.BlackHole.PropertySecretRefs)
	}
	if connector.Cassandra != nil {
		appendMap(path.Child("cassandra", "propertySecretRefs"), connector.Cassandra.PropertySecretRefs)
	}
	if connector.ClickHouse != nil {
		appendMap(path.Child("clickHouse", "propertySecretRefs"), connector.ClickHouse.PropertySecretRefs)
	}
	if connector.Custom != nil {
		appendMap(path.Child("custom", "propertySecretRefs"), connector.Custom.PropertySecretRefs)
	}
	if connector.DeltaLake != nil {
		appendMap(path.Child("deltaLake", "propertySecretRefs"), connector.DeltaLake.PropertySecretRefs)
	}
	if connector.Druid != nil {
		appendMap(path.Child("druid", "propertySecretRefs"), connector.Druid.PropertySecretRefs)
	}
	if connector.DuckDB != nil {
		appendMap(path.Child("duckDB", "propertySecretRefs"), connector.DuckDB.PropertySecretRefs)
	}
	if connector.Elasticsearch != nil {
		appendMap(path.Child("elasticsearch", "propertySecretRefs"), connector.Elasticsearch.PropertySecretRefs)
	}
	if connector.Exasol != nil {
		appendMap(path.Child("exasol", "propertySecretRefs"), connector.Exasol.PropertySecretRefs)
	}
	if connector.Faker != nil {
		appendMap(path.Child("faker", "propertySecretRefs"), connector.Faker.PropertySecretRefs)
	}
	if connector.GoogleSheets != nil {
		appendMap(path.Child("googleSheets", "propertySecretRefs"), connector.GoogleSheets.PropertySecretRefs)
	}
	if connector.Hive != nil {
		appendMap(path.Child("hive", "propertySecretRefs"), connector.Hive.PropertySecretRefs)
	}
	if connector.Hudi != nil {
		appendMap(path.Child("hudi", "propertySecretRefs"), connector.Hudi.PropertySecretRefs)
	}
	if connector.Iceberg != nil {
		appendMap(path.Child("iceberg", "propertySecretRefs"), connector.Iceberg.PropertySecretRefs)
	}
	if connector.Ignite != nil {
		appendMap(path.Child("ignite", "propertySecretRefs"), connector.Ignite.PropertySecretRefs)
	}
	if connector.JMX != nil {
		appendMap(path.Child("jmx", "propertySecretRefs"), connector.JMX.PropertySecretRefs)
	}
	if connector.Kafka != nil {
		appendMap(path.Child("kafka", "propertySecretRefs"), connector.Kafka.PropertySecretRefs)
	}
	if connector.Lakehouse != nil {
		appendMap(path.Child("lakehouse", "propertySecretRefs"), connector.Lakehouse.PropertySecretRefs)
	}
	if connector.Loki != nil {
		appendMap(path.Child("loki", "propertySecretRefs"), connector.Loki.PropertySecretRefs)
	}
	if connector.MariaDB != nil {
		appendMap(path.Child("mariaDB", "propertySecretRefs"), connector.MariaDB.PropertySecretRefs)
	}
	if connector.Memory != nil {
		appendMap(path.Child("memory", "propertySecretRefs"), connector.Memory.PropertySecretRefs)
	}
	if connector.MongoDB != nil {
		appendMap(path.Child("mongodb", "propertySecretRefs"), connector.MongoDB.PropertySecretRefs)
	}
	if connector.MySQL != nil {
		appendMap(path.Child("mysql", "propertySecretRefs"), connector.MySQL.PropertySecretRefs)
	}
	if connector.OpenSearch != nil {
		appendMap(path.Child("openSearch", "propertySecretRefs"), connector.OpenSearch.PropertySecretRefs)
	}
	if connector.Oracle != nil {
		appendMap(path.Child("oracle", "propertySecretRefs"), connector.Oracle.PropertySecretRefs)
	}
	if connector.Pinot != nil {
		appendMap(path.Child("pinot", "propertySecretRefs"), connector.Pinot.PropertySecretRefs)
	}
	if connector.Postgres != nil {
		appendMap(path.Child("postgres", "propertySecretRefs"), connector.Postgres.PropertySecretRefs)
	}
	if connector.Prometheus != nil {
		appendMap(path.Child("prometheus", "propertySecretRefs"), connector.Prometheus.PropertySecretRefs)
	}
	if connector.Redis != nil {
		appendMap(path.Child("redis", "propertySecretRefs"), connector.Redis.PropertySecretRefs)
	}
	if connector.Redshift != nil {
		appendMap(path.Child("redshift", "propertySecretRefs"), connector.Redshift.PropertySecretRefs)
	}
	if connector.SingleStore != nil {
		appendMap(path.Child("singleStore", "propertySecretRefs"), connector.SingleStore.PropertySecretRefs)
	}
	if connector.Snowflake != nil {
		appendMap(path.Child("snowflake", "propertySecretRefs"), connector.Snowflake.PropertySecretRefs)
	}
	if connector.SQLServer != nil {
		appendMap(path.Child("sqlServer", "propertySecretRefs"), connector.SQLServer.PropertySecretRefs)
	}
	if connector.System != nil {
		appendMap(path.Child("system", "propertySecretRefs"), connector.System.PropertySecretRefs)
	}
	if connector.Thrift != nil {
		appendMap(path.Child("thrift", "propertySecretRefs"), connector.Thrift.PropertySecretRefs)
	}
	if connector.TPCDS != nil {
		appendMap(path.Child("tpcds", "propertySecretRefs"), connector.TPCDS.PropertySecretRefs)
	}
	if connector.TPCH != nil {
		appendMap(path.Child("tpch", "propertySecretRefs"), connector.TPCH.PropertySecretRefs)
	}
	if connector.Vertica != nil {
		appendMap(path.Child("vertica", "propertySecretRefs"), connector.Vertica.PropertySecretRefs)
	}

	return maps
}

type catalogSecretReferenceField struct {
	path       *field.Path
	secretName string
}

func catalogSecretReferenceFields(catalog *XTrinodeCatalog) []catalogSecretReferenceField {
	if catalog == nil {
		return nil
	}
	connector := &catalog.Spec.Connector
	base := field.NewPath("spec", "connector")
	var refs []catalogSecretReferenceField
	appendRef := func(path *field.Path, ref *corev1.SecretKeySelector) {
		if ref == nil || strings.TrimSpace(ref.Name) == "" {
			return
		}
		refs = append(refs, catalogSecretReferenceField{
			path:       path,
			secretName: ref.Name,
		})
	}

	for _, propMap := range catalogPropertySecretRefMaps(base, connector) {
		for propertyName, ref := range propMap.refs {
			refCopy := ref
			appendRef(propMap.path.Key(propertyName), &refCopy)
		}
	}

	if connector.Hive != nil {
		path := base.Child("hive")
		appendRef(path.Child("s3AccessKeySecret"), connector.Hive.S3AccessKeySecret)
		appendRef(path.Child("s3SecretKeySecret"), connector.Hive.S3SecretKeySecret)
	}
	if connector.Postgres != nil {
		appendRef(base.Child("postgres", "connectionPasswordSecret"), connector.Postgres.ConnectionPasswordSecret)
	}
	if connector.MySQL != nil {
		appendRef(base.Child("mysql", "connectionPasswordSecret"), connector.MySQL.ConnectionPasswordSecret)
	}
	if connector.ClickHouse != nil {
		appendRef(base.Child("clickHouse", "connectionPasswordSecret"), connector.ClickHouse.ConnectionPasswordSecret)
	}
	if connector.Exasol != nil {
		appendRef(base.Child("exasol", "connectionPasswordSecret"), connector.Exasol.ConnectionPasswordSecret)
	}
	if connector.Ignite != nil {
		appendRef(base.Child("ignite", "connectionPasswordSecret"), connector.Ignite.ConnectionPasswordSecret)
	}
	if connector.MariaDB != nil {
		appendRef(base.Child("mariaDB", "connectionPasswordSecret"), connector.MariaDB.ConnectionPasswordSecret)
	}
	if connector.Oracle != nil {
		appendRef(base.Child("oracle", "connectionPasswordSecret"), connector.Oracle.ConnectionPasswordSecret)
	}
	if connector.Redshift != nil {
		appendRef(base.Child("redshift", "connectionPasswordSecret"), connector.Redshift.ConnectionPasswordSecret)
	}
	if connector.SingleStore != nil {
		appendRef(base.Child("singleStore", "connectionPasswordSecret"), connector.SingleStore.ConnectionPasswordSecret)
	}
	if connector.Snowflake != nil {
		appendRef(base.Child("snowflake", "passwordSecret"), connector.Snowflake.PasswordSecret)
	}
	if connector.SQLServer != nil {
		appendRef(base.Child("sqlServer", "connectionPasswordSecret"), connector.SQLServer.ConnectionPasswordSecret)
	}
	if connector.Vertica != nil {
		appendRef(base.Child("vertica", "connectionPasswordSecret"), connector.Vertica.ConnectionPasswordSecret)
	}

	return refs
}

func appendRequiredString(allErrs field.ErrorList, path *field.Path, value string) field.ErrorList {
	if strings.TrimSpace(value) == "" {
		return append(allErrs, field.Required(path, "must be set"))
	}
	return allErrs
}

func catalogConnectorFields(connector *XTrinodeCatalogConnector) []string {
	fields := []struct {
		name string
		set  bool
	}{
		{"bigQuery", connector.BigQuery != nil},
		{"blackHole", connector.BlackHole != nil},
		{"cassandra", connector.Cassandra != nil},
		{"clickHouse", connector.ClickHouse != nil},
		{"deltaLake", connector.DeltaLake != nil},
		{"druid", connector.Druid != nil},
		{"duckDB", connector.DuckDB != nil},
		{"elasticsearch", connector.Elasticsearch != nil},
		{"exasol", connector.Exasol != nil},
		{"faker", connector.Faker != nil},
		{"googleSheets", connector.GoogleSheets != nil},
		{"hive", connector.Hive != nil},
		{"hudi", connector.Hudi != nil},
		{"iceberg", connector.Iceberg != nil},
		{"ignite", connector.Ignite != nil},
		{"jmx", connector.JMX != nil},
		{"kafka", connector.Kafka != nil},
		{"lakehouse", connector.Lakehouse != nil},
		{"loki", connector.Loki != nil},
		{"mariaDB", connector.MariaDB != nil},
		{"memory", connector.Memory != nil},
		{"mongodb", connector.MongoDB != nil},
		{"mysql", connector.MySQL != nil},
		{"openSearch", connector.OpenSearch != nil},
		{"oracle", connector.Oracle != nil},
		{"pinot", connector.Pinot != nil},
		{"postgres", connector.Postgres != nil},
		{"prometheus", connector.Prometheus != nil},
		{"redis", connector.Redis != nil},
		{"redshift", connector.Redshift != nil},
		{"singleStore", connector.SingleStore != nil},
		{"snowflake", connector.Snowflake != nil},
		{"sqlServer", connector.SQLServer != nil},
		{"system", connector.System != nil},
		{"thrift", connector.Thrift != nil},
		{"tpcds", connector.TPCDS != nil},
		{"tpch", connector.TPCH != nil},
		{"vertica", connector.Vertica != nil},
		{"custom", connector.Custom != nil},
	}

	setConnectors := make([]string, 0, 1)
	for _, f := range fields {
		if f.set {
			setConnectors = append(setConnectors, f.name)
		}
	}
	return setConnectors
}
