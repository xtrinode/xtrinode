package controllers

import (
	"fmt"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

// ConnectorExtractor knows how to extract the connector name and properties
// from a XTrinodeCatalogConnector for a single connector type.
type ConnectorExtractor interface {
	// Match returns true if this extractor handles the given connector.
	Match(connector *analyticsv1.XTrinodeCatalogConnector) bool
	// ConnectorName returns the Trino connector.name value.
	ConnectorName(connector *analyticsv1.XTrinodeCatalogConnector) string
	// BuildProps builds the properties map for the connector.
	BuildProps(connector *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string
}

// connectorRegistry is the ordered list of all connector extractors.
// Registered once at init time; iterated at reconcile time.
var connectorRegistry []ConnectorExtractor

func init() {
	connectorRegistry = []ConnectorExtractor{
		&bigQueryExtractor{},
		&blackHoleExtractor{},
		&cassandraExtractor{},
		&clickHouseExtractor{},
		&deltaLakeExtractor{},
		&druidExtractor{},
		&duckDBExtractor{},
		&elasticsearchExtractor{},
		&exasolExtractor{},
		&fakerExtractor{},
		&googleSheetsExtractor{},
		&hiveExtractor{},
		&hudiExtractor{},
		&icebergExtractor{},
		&igniteExtractor{},
		&jmxExtractor{},
		&kafkaExtractor{},
		&lakehouseExtractor{},
		&lokiExtractor{},
		&mariaDBExtractor{},
		&memoryExtractor{},
		&mongoDBExtractor{},
		&mysqlExtractor{},
		&openSearchExtractor{},
		&oracleExtractor{},
		&pinotExtractor{},
		&postgresExtractor{},
		&prometheusExtractor{},
		&redisExtractor{},
		&redshiftExtractor{},
		&singleStoreExtractor{},
		&snowflakeExtractor{},
		&sqlServerExtractor{},
		&systemExtractor{},
		&thriftExtractor{},
		&tpcdsExtractor{},
		&tpchExtractor{},
		&verticaExtractor{},
		&customExtractor{}, // must be last — catch-all for user-defined connectors
	}
}

// resolveConnector iterates the registry and returns the connector name + properties.
// Returns an error if zero or more than one extractor matches (misconfigured CR).
func resolveConnector(connector *analyticsv1.XTrinodeCatalogConnector, catalogName string) (connectorName string, properties map[string]string, err error) {
	var matched []ConnectorExtractor
	for _, ext := range connectorRegistry {
		if ext.Match(connector) {
			matched = append(matched, ext)
		}
	}
	switch len(matched) {
	case 0:
		return "", nil, fmt.Errorf("no connector specified in XTrinodeCatalog %q: exactly one connector field must be set", catalogName)
	case 1:
		name := matched[0].ConnectorName(connector)
		props := matched[0].BuildProps(connector, catalogName)
		applyPropertySecretRefs(props, connector.GenericPropertySecretRefs(), catalogName)
		return name, props, nil
	default:
		names := make([]string, 0, len(matched))
		for _, m := range matched {
			names = append(names, m.ConnectorName(connector))
		}
		return "", nil, fmt.Errorf("multiple connectors specified in XTrinodeCatalog %q (%s): exactly one connector field must be set",
			catalogName, strings.Join(names, ", "))
	}
}

// ---------------------------------------------------------------------------
// helpers shared by JDBC-style extractors
// ---------------------------------------------------------------------------

// buildJDBCProps is a helper for connectors that follow the connection-url / user / password pattern.
func buildJDBCProps(base map[string]string, connectionURL, connectionUser, catalogName string, passwordSecret *corev1.SecretKeySelector) map[string]string {
	props := ensurePropertiesMap(base)
	props["connection-url"] = connectionURL
	if connectionUser != "" {
		props["connection-user"] = connectionUser
	}
	if passwordSecret != nil {
		envVarName := calculateEnvVarName(catalogName, "connection-password")
		props["connection-password"] = fmt.Sprintf("${ENV:%s}", envVarName)
	}
	return props
}

func applyPropertySecretRefs(props map[string]string, refs map[string]corev1.SecretKeySelector, catalogName string) {
	for propertyName := range refs {
		trimmedProperty := strings.TrimSpace(propertyName)
		if trimmedProperty == "" {
			continue
		}
		envVarName := calculateEnvVarName(catalogName, trimmedProperty)
		props[trimmedProperty] = fmt.Sprintf("${ENV:%s}", envVarName)
	}
}

// ---------------------------------------------------------------------------
// Extractor implementations — one per connector type
// ---------------------------------------------------------------------------

// --- BigQuery ---
type bigQueryExtractor struct{}

func (e *bigQueryExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.BigQuery != nil
}
func (e *bigQueryExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "bigquery"
}
func (e *bigQueryExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	bq := c.BigQuery
	props := ensurePropertiesMap(bq.Properties)
	props["bigquery.project-id"] = bq.ProjectID
	if bq.ParentProjectID != "" {
		props["bigquery.parent-project-id"] = bq.ParentProjectID
	}
	if bq.CredentialsFile != "" {
		props["bigquery.credentials-file"] = bq.CredentialsFile
	}
	return props
}

// --- BlackHole ---
type blackHoleExtractor struct{}

func (e *blackHoleExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.BlackHole != nil
}
func (e *blackHoleExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "blackhole"
}
func (e *blackHoleExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	return ensurePropertiesMap(c.BlackHole.Properties)
}

// --- Cassandra ---
type cassandraExtractor struct{}

func (e *cassandraExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Cassandra != nil
}
func (e *cassandraExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "cassandra"
}
func (e *cassandraExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	cass := c.Cassandra
	props := ensurePropertiesMap(cass.Properties)
	props["cassandra.contact-points"] = cass.ContactPoints
	if cass.Port > 0 {
		props["cassandra.native-protocol-port"] = fmt.Sprintf("%d", cass.Port)
	}
	return props
}

// --- ClickHouse ---
type clickHouseExtractor struct{}

func (e *clickHouseExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.ClickHouse != nil
}
func (e *clickHouseExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "clickhouse"
}
func (e *clickHouseExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	ch := c.ClickHouse
	return buildJDBCProps(ch.Properties, ch.ConnectionURL, ch.ConnectionUser, catalogName, ch.ConnectionPasswordSecret)
}

// --- DeltaLake ---
type deltaLakeExtractor struct{}

func (e *deltaLakeExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.DeltaLake != nil
}
func (e *deltaLakeExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "delta_lake"
}
func (e *deltaLakeExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	dl := c.DeltaLake
	props := ensurePropertiesMap(dl.Properties)
	switch normalizeMetastoreCatalogType(dl.CatalogType) {
	case "glue":
		props["hive.metastore"] = "glue"
	case "hive", "hive_metastore":
		if dl.WarehouseURI != "" {
			props["hive.metastore.uri"] = dl.WarehouseURI
		}
	}
	return props
}

// --- Druid ---
type druidExtractor struct{}

func (e *druidExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Druid != nil }
func (e *druidExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "druid"
}
func (e *druidExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Druid.Properties)
	props["connection-url"] = druidConnectionURL(c.Druid.BrokerURL)
	return props
}

func druidConnectionURL(brokerURL string) string {
	trimmed := strings.TrimSpace(brokerURL)
	if strings.HasPrefix(strings.ToLower(trimmed), "jdbc:") {
		return trimmed
	}

	base := strings.TrimRight(trimmed, "/")
	if !strings.HasSuffix(base, "/druid/v2/sql/avatica") {
		base += "/druid/v2/sql/avatica"
	}
	return "jdbc:avatica:remote:url=" + base + "/"
}

// --- DuckDB ---
type duckDBExtractor struct{}

func (e *duckDBExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.DuckDB != nil }
func (e *duckDBExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "duckdb"
}
func (e *duckDBExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.DuckDB.Properties)
	if c.DuckDB.DatabasePath != "" {
		props["connection-url"] = duckDBConnectionURL(c.DuckDB.DatabasePath)
	}
	return props
}

func duckDBConnectionURL(databasePath string) string {
	trimmed := strings.TrimSpace(databasePath)
	if strings.HasPrefix(strings.ToLower(trimmed), "jdbc:duckdb:") {
		return trimmed
	}
	return "jdbc:duckdb:" + trimmed
}

// --- Elasticsearch ---
type elasticsearchExtractor struct{}

func (e *elasticsearchExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Elasticsearch != nil
}
func (e *elasticsearchExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "elasticsearch"
}
func (e *elasticsearchExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	es := c.Elasticsearch
	props := ensurePropertiesMap(es.Properties)
	props["elasticsearch.host"] = es.Host
	if es.Port > 0 {
		props["elasticsearch.port"] = fmt.Sprintf("%d", es.Port)
	}
	if es.DefaultSchema != "" {
		props["elasticsearch.default-schema-name"] = es.DefaultSchema
	}
	return props
}

// --- Exasol ---
type exasolExtractor struct{}

func (e *exasolExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Exasol != nil }
func (e *exasolExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "exasol"
}
func (e *exasolExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	ex := c.Exasol
	return buildJDBCProps(ex.Properties, ex.ConnectionURL, ex.ConnectionUser, catalogName, ex.ConnectionPasswordSecret)
}

// --- Faker ---
type fakerExtractor struct{}

func (e *fakerExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Faker != nil }
func (e *fakerExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "faker"
}
func (e *fakerExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	f := c.Faker
	props := ensurePropertiesMap(f.Properties)
	if f.DefaultLimit > 0 {
		props["faker.default-limit"] = fmt.Sprintf("%d", f.DefaultLimit)
	}
	if f.NullProbability > 0 {
		props["faker.null-probability"] = fmt.Sprintf("%f", f.NullProbability)
	}
	return props
}

// --- GoogleSheets ---
type googleSheetsExtractor struct{}

func (e *googleSheetsExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.GoogleSheets != nil
}
func (e *googleSheetsExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "gsheets"
}
func (e *googleSheetsExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	gs := c.GoogleSheets
	props := ensurePropertiesMap(gs.Properties)
	props["gsheets.credentials-path"] = gs.CredentialsFilePath
	if gs.MetadataSheetID != "" {
		props["gsheets.metadata-sheet-id"] = gs.MetadataSheetID
	}
	return props
}

// --- Hive ---
type hiveExtractor struct{}

func (e *hiveExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool           { return c.Hive != nil }
func (e *hiveExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string { return "hive" }
func (e *hiveExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	h := c.Hive
	props := ensurePropertiesMap(h.Properties)
	props["hive.metastore.uri"] = h.MetastoreURI
	if h.S3Endpoint != "" {
		props["fs.native-s3.enabled"] = "true"
		props["s3.endpoint"] = h.S3Endpoint
	}
	if h.S3AccessKeySecret != nil {
		props["fs.native-s3.enabled"] = "true"
		props["s3.aws-access-key"] = fmt.Sprintf("${ENV:%s}", calculateEnvVarName(catalogName, "s3.aws-access-key"))
	}
	if h.S3SecretKeySecret != nil {
		props["fs.native-s3.enabled"] = "true"
		props["s3.aws-secret-key"] = fmt.Sprintf("${ENV:%s}", calculateEnvVarName(catalogName, "s3.aws-secret-key"))
	}
	return props
}

// --- Hudi ---
type hudiExtractor struct{}

func (e *hudiExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool           { return c.Hudi != nil }
func (e *hudiExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string { return "hudi" }
func (e *hudiExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Hudi.Properties)
	props["hive.metastore.uri"] = c.Hudi.MetastoreURI
	return props
}

// --- Iceberg ---
type icebergExtractor struct{}

func (e *icebergExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Iceberg != nil
}
func (e *icebergExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "iceberg"
}
func (e *icebergExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	ic := c.Iceberg
	props := ensurePropertiesMap(ic.Properties)
	props["iceberg.catalog.type"] = normalizeIcebergCatalogType(ic.CatalogType)
	return props
}

func normalizeIcebergCatalogType(catalogType string) string {
	normalized := normalizeMetastoreCatalogType(catalogType)
	if normalized == "" || normalized == "hive" {
		return "hive_metastore"
	}
	return normalized
}

func normalizeMetastoreCatalogType(catalogType string) string {
	return strings.ToLower(strings.TrimSpace(catalogType))
}

// --- Ignite ---
type igniteExtractor struct{}

func (e *igniteExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Ignite != nil }
func (e *igniteExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "ignite"
}
func (e *igniteExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	ig := c.Ignite
	return buildJDBCProps(ig.Properties, ig.ConnectionURL, ig.ConnectionUser, catalogName, ig.ConnectionPasswordSecret)
}

// --- JMX ---
type jmxExtractor struct{}

func (e *jmxExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool           { return c.JMX != nil }
func (e *jmxExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string { return "jmx" }
func (e *jmxExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	return ensurePropertiesMap(c.JMX.Properties)
}

// --- Kafka ---
type kafkaExtractor struct{}

func (e *kafkaExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Kafka != nil }
func (e *kafkaExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "kafka"
}
func (e *kafkaExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Kafka.Properties)
	props["kafka.nodes"] = strings.Join(c.Kafka.KafkaNodes, ",")
	return props
}

// --- Lakehouse ---
type lakehouseExtractor struct{}

func (e *lakehouseExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Lakehouse != nil
}
func (e *lakehouseExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "lakehouse"
}
func (e *lakehouseExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	lh := c.Lakehouse
	props := ensurePropertiesMap(lh.Properties)
	props["lakehouse.table-type"] = normalizeLakehouseTableType(lh.CatalogType)
	if lh.MetastoreURI != "" {
		props["hive.metastore"] = "thrift"
		props["hive.metastore.uri"] = lh.MetastoreURI
	}
	return props
}

func normalizeLakehouseTableType(catalogType string) string {
	normalized := strings.ToUpper(strings.TrimSpace(catalogType))
	if normalized == "DELTA_LAKE" {
		return "DELTA"
	}
	return normalized
}

// --- Loki ---
type lokiExtractor struct{}

func (e *lokiExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool           { return c.Loki != nil }
func (e *lokiExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string { return "loki" }
func (e *lokiExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Loki.Properties)
	props["loki.uri"] = c.Loki.URI
	return props
}

// --- MariaDB ---
type mariaDBExtractor struct{}

func (e *mariaDBExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.MariaDB != nil
}
func (e *mariaDBExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "mariadb"
}
func (e *mariaDBExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	m := c.MariaDB
	return buildJDBCProps(m.Properties, m.ConnectionURL, m.ConnectionUser, catalogName, m.ConnectionPasswordSecret)
}

// --- Memory ---
type memoryExtractor struct{}

func (e *memoryExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Memory != nil }
func (e *memoryExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "memory"
}
func (e *memoryExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Memory.Properties)
	if c.Memory.MaxDataPerNode != "" {
		props["memory.max-data-per-node"] = c.Memory.MaxDataPerNode
	}
	return props
}

// --- MongoDB ---
type mongoDBExtractor struct{}

func (e *mongoDBExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.MongoDB != nil
}
func (e *mongoDBExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "mongodb"
}
func (e *mongoDBExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.MongoDB.Properties)
	props["mongodb.connection-url"] = c.MongoDB.ConnectionURI
	return props
}

// --- MySQL ---
type mysqlExtractor struct{}

func (e *mysqlExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.MySQL != nil }
func (e *mysqlExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "mysql"
}
func (e *mysqlExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	my := c.MySQL
	return buildJDBCProps(my.Properties, my.ConnectionURL, my.ConnectionUser, catalogName, my.ConnectionPasswordSecret)
}

// --- OpenSearch ---
type openSearchExtractor struct{}

func (e *openSearchExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.OpenSearch != nil
}
func (e *openSearchExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "opensearch"
}
func (e *openSearchExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	os := c.OpenSearch
	props := ensurePropertiesMap(os.Properties)
	props["opensearch.host"] = os.Host
	if os.Port > 0 {
		props["opensearch.port"] = fmt.Sprintf("%d", os.Port)
	}
	if os.DefaultSchema != "" {
		props["opensearch.default-schema-name"] = os.DefaultSchema
	}
	return props
}

// --- Oracle ---
type oracleExtractor struct{}

func (e *oracleExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Oracle != nil }
func (e *oracleExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "oracle"
}
func (e *oracleExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	o := c.Oracle
	return buildJDBCProps(o.Properties, o.ConnectionURL, o.ConnectionUser, catalogName, o.ConnectionPasswordSecret)
}

// --- Pinot ---
type pinotExtractor struct{}

func (e *pinotExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Pinot != nil }
func (e *pinotExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "pinot"
}
func (e *pinotExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Pinot.Properties)
	props["pinot.controller-urls"] = c.Pinot.ControllerURLs
	return props
}

// --- Postgres ---
type postgresExtractor struct{}

func (e *postgresExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Postgres != nil
}
func (e *postgresExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "postgresql"
}
func (e *postgresExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	pg := c.Postgres
	return buildJDBCProps(pg.Properties, pg.ConnectionURL, pg.ConnectionUser, catalogName, pg.ConnectionPasswordSecret)
}

// --- Prometheus ---
type prometheusExtractor struct{}

func (e *prometheusExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Prometheus != nil
}
func (e *prometheusExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "prometheus"
}
func (e *prometheusExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	props := ensurePropertiesMap(c.Prometheus.Properties)
	props["prometheus.uri"] = c.Prometheus.URI
	return props
}

// --- Redis ---
type redisExtractor struct{}

func (e *redisExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Redis != nil }
func (e *redisExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "redis"
}
func (e *redisExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	r := c.Redis
	props := ensurePropertiesMap(r.Properties)
	props["redis.nodes"] = r.Nodes
	if r.Database > 0 {
		props["redis.database-index"] = fmt.Sprintf("%d", r.Database)
	}
	return props
}

// --- Redshift ---
type redshiftExtractor struct{}

func (e *redshiftExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Redshift != nil
}
func (e *redshiftExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "redshift"
}
func (e *redshiftExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	rs := c.Redshift
	return buildJDBCProps(rs.Properties, rs.ConnectionURL, rs.ConnectionUser, catalogName, rs.ConnectionPasswordSecret)
}

// --- SingleStore ---
type singleStoreExtractor struct{}

func (e *singleStoreExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.SingleStore != nil
}
func (e *singleStoreExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "singlestore"
}
func (e *singleStoreExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	ss := c.SingleStore
	return buildJDBCProps(ss.Properties, ss.ConnectionURL, ss.ConnectionUser, catalogName, ss.ConnectionPasswordSecret)
}

// --- Snowflake ---
type snowflakeExtractor struct{}

func (e *snowflakeExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Snowflake != nil
}
func (e *snowflakeExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "snowflake"
}
func (e *snowflakeExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	sf := c.Snowflake
	props := ensurePropertiesMap(sf.Properties)
	connectionURL, account := snowflakeConnectionURLAndAccount(sf.AccountURL)
	props["connection-url"] = connectionURL
	props["connection-user"] = sf.User
	if account != "" {
		props["snowflake.account"] = account
	}
	if sf.Database != "" {
		props["snowflake.database"] = sf.Database
	}
	if sf.Role != "" {
		props["snowflake.role"] = sf.Role
	}
	if sf.Warehouse != "" {
		props["snowflake.warehouse"] = sf.Warehouse
	}
	if sf.PasswordSecret != nil {
		envVarName := calculateEnvVarName(catalogName, "connection-password")
		props["connection-password"] = fmt.Sprintf("${ENV:%s}", envVarName)
	}
	return props
}

func snowflakeConnectionURLAndAccount(accountURL string) (connectionURL, account string) {
	trimmed := strings.TrimSpace(accountURL)
	if trimmed == "" {
		return "", ""
	}

	if value, ok := stripCaseInsensitivePrefix(trimmed, "jdbc:snowflake://"); ok {
		return trimmed, snowflakeAccountFromHost(hostFromSnowflakeURLRemainder(value))
	}

	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		host := parsed.Host
		pathAndQuery := parsed.EscapedPath()
		if parsed.RawQuery != "" {
			pathAndQuery += "?" + parsed.RawQuery
		}
		return "jdbc:snowflake://" + host + pathAndQuery, snowflakeAccountFromHost(host)
	}

	host := trimmed
	if !strings.Contains(host, ".") {
		host += ".snowflakecomputing.com"
	}
	return "jdbc:snowflake://" + host, snowflakeAccountFromHost(host)
}

func stripCaseInsensitivePrefix(value, prefix string) (string, bool) {
	if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
		return value[len(prefix):], true
	}
	return value, false
}

func hostFromSnowflakeURLRemainder(remainder string) string {
	host := remainder
	for _, separator := range []string{"/", "?"} {
		if index := strings.Index(host, separator); index >= 0 {
			host = host[:index]
		}
	}
	return host
}

func snowflakeAccountFromHost(host string) string {
	host = strings.TrimSpace(host)
	if parsedHost, _, found := strings.Cut(host, ":"); found {
		host = parsedHost
	}
	host = strings.TrimSuffix(host, ".")
	lowerHost := strings.ToLower(host)
	suffix := ".snowflakecomputing.com"
	if index := strings.Index(lowerHost, suffix); index > 0 {
		return host[:index]
	}
	return host
}

// --- SQLServer ---
type sqlServerExtractor struct{}

func (e *sqlServerExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.SQLServer != nil
}
func (e *sqlServerExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "sqlserver"
}
func (e *sqlServerExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	ss := c.SQLServer
	return buildJDBCProps(ss.Properties, ss.ConnectionURL, ss.ConnectionUser, catalogName, ss.ConnectionPasswordSecret)
}

// --- System ---
type systemExtractor struct{}

func (e *systemExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.System != nil }
func (e *systemExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "system"
}
func (e *systemExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	return ensurePropertiesMap(c.System.Properties)
}

// --- Thrift ---
type thriftExtractor struct{}

func (e *thriftExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Thrift != nil }
func (e *thriftExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "trino_thrift"
}
func (e *thriftExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	t := c.Thrift
	props := ensurePropertiesMap(t.Properties)
	props["trino.thrift.client.addresses"] = fmt.Sprintf("%s:%d", t.Host, t.Port)
	return props
}

// --- TPCDS ---
type tpcdsExtractor struct{}

func (e *tpcdsExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.TPCDS != nil }
func (e *tpcdsExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "tpcds"
}
func (e *tpcdsExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	return ensurePropertiesMap(c.TPCDS.Properties)
}

// --- TPCH ---
type tpchExtractor struct{}

func (e *tpchExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool           { return c.TPCH != nil }
func (e *tpchExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string { return "tpch" }
func (e *tpchExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	return ensurePropertiesMap(c.TPCH.Properties)
}

// --- Vertica ---
type verticaExtractor struct{}

func (e *verticaExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool {
	return c.Vertica != nil
}
func (e *verticaExtractor) ConnectorName(_ *analyticsv1.XTrinodeCatalogConnector) string {
	return "vertica"
}
func (e *verticaExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, catalogName string) map[string]string {
	v := c.Vertica
	return buildJDBCProps(v.Properties, v.ConnectionURL, v.ConnectionUser, catalogName, v.ConnectionPasswordSecret)
}

// --- Custom ---
type customExtractor struct{}

func (e *customExtractor) Match(c *analyticsv1.XTrinodeCatalogConnector) bool { return c.Custom != nil }
func (e *customExtractor) ConnectorName(c *analyticsv1.XTrinodeCatalogConnector) string {
	return c.Custom.ConnectorName
}
func (e *customExtractor) BuildProps(c *analyticsv1.XTrinodeCatalogConnector, _ string) map[string]string {
	return ensurePropertiesMap(c.Custom.Properties)
}
