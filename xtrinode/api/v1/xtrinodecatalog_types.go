// +kubebuilder:object:generate=true
package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// XTrinodeCatalogSpec defines the desired state of a XTrinodeCatalog
type XTrinodeCatalogSpec struct {
	// Connector defines the connector type and configuration
	Connector XTrinodeCatalogConnector `json:"connector"`

	// Labels for selecting this catalog in XTrinodes
	// XTrinodes use label selectors to find catalogs
	// Example: xtrinode.Spec.CatalogSelector: {matchLabels: {team: "data-eng"}}
	Labels map[string]string `json:"labels,omitempty"`
}

// XTrinodeCatalogConnector defines connector configuration
// Only one connector type should be specified
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
type XTrinodeCatalogConnector struct {
	// BigQuery connector configuration
	BigQuery *BigQueryCatalogSpec `json:"bigQuery,omitempty"`

	// BlackHole connector configuration
	BlackHole *BlackHoleCatalogSpec `json:"blackHole,omitempty"`

	// Cassandra connector configuration
	Cassandra *CassandraCatalogSpec `json:"cassandra,omitempty"`

	// ClickHouse connector configuration
	ClickHouse *ClickHouseCatalogSpec `json:"clickHouse,omitempty"`

	// DeltaLake connector configuration
	DeltaLake *DeltaLakeCatalogSpec `json:"deltaLake,omitempty"`

	// Druid connector configuration
	Druid *DruidCatalogSpec `json:"druid,omitempty"`

	// DuckDB connector configuration
	DuckDB *DuckDBCatalogSpec `json:"duckDB,omitempty"`

	// Elasticsearch connector configuration
	Elasticsearch *ElasticsearchCatalogSpec `json:"elasticsearch,omitempty"`

	// Exasol connector configuration
	Exasol *ExasolCatalogSpec `json:"exasol,omitempty"`

	// Faker connector configuration (generates fake data for testing)
	Faker *FakerCatalogSpec `json:"faker,omitempty"`

	// GoogleSheets connector configuration
	GoogleSheets *GoogleSheetsCatalogSpec `json:"googleSheets,omitempty"`

	// Hive connector configuration
	Hive *HiveCatalogSpec `json:"hive,omitempty"`

	// Hudi connector configuration
	Hudi *HudiCatalogSpec `json:"hudi,omitempty"`

	// Iceberg connector configuration
	Iceberg *IcebergCatalogSpec `json:"iceberg,omitempty"`

	// Ignite connector configuration
	Ignite *IgniteCatalogSpec `json:"ignite,omitempty"`

	// JMX connector configuration
	JMX *JMXCatalogSpec `json:"jmx,omitempty"`

	// Kafka connector configuration
	Kafka *KafkaCatalogSpec `json:"kafka,omitempty"`

	// Lakehouse connector configuration
	Lakehouse *LakehouseCatalogSpec `json:"lakehouse,omitempty"`

	// Loki connector configuration
	Loki *LokiCatalogSpec `json:"loki,omitempty"`

	// MariaDB connector configuration
	MariaDB *MariaDBCatalogSpec `json:"mariaDB,omitempty"`

	// Memory connector configuration
	Memory *MemoryCatalogSpec `json:"memory,omitempty"`

	// MongoDB connector configuration
	MongoDB *MongoDBCatalogSpec `json:"mongodb,omitempty"`

	// MySQL connector configuration
	MySQL *MySQLCatalogSpec `json:"mysql,omitempty"`

	// OpenSearch connector configuration
	OpenSearch *OpenSearchCatalogSpec `json:"openSearch,omitempty"`

	// Oracle connector configuration
	Oracle *OracleCatalogSpec `json:"oracle,omitempty"`

	// Pinot connector configuration
	Pinot *PinotCatalogSpec `json:"pinot,omitempty"`

	// Postgres connector configuration
	Postgres *PostgresCatalogSpec `json:"postgres,omitempty"`

	// Prometheus connector configuration
	Prometheus *PrometheusCatalogSpec `json:"prometheus,omitempty"`

	// Redis connector configuration
	Redis *RedisCatalogSpec `json:"redis,omitempty"`

	// Redshift connector configuration
	Redshift *RedshiftCatalogSpec `json:"redshift,omitempty"`

	// SingleStore connector configuration
	SingleStore *SingleStoreCatalogSpec `json:"singleStore,omitempty"`

	// Snowflake connector configuration
	Snowflake *SnowflakeCatalogSpec `json:"snowflake,omitempty"`

	// SQLServer connector configuration
	SQLServer *SQLServerCatalogSpec `json:"sqlServer,omitempty"`

	// System connector configuration
	System *SystemCatalogSpec `json:"system,omitempty"`

	// Thrift connector configuration
	Thrift *ThriftCatalogSpec `json:"thrift,omitempty"`

	// TPCDS connector configuration (TPC-DS benchmark)
	TPCDS *TPCDSCatalogSpec `json:"tpcds,omitempty"`

	// TPCH connector configuration (TPC-H benchmark)
	TPCH *TPCHCatalogSpec `json:"tpch,omitempty"`

	// Vertica connector configuration
	Vertica *VerticaCatalogSpec `json:"vertica,omitempty"`

	// Custom connector (raw properties)
	// Allows teams to provide custom connector configuration
	Custom *CustomCatalogSpec `json:"custom,omitempty"`
}

// CatalogPropertySecretRefs defines generic Secret-backed Trino catalog properties.
type CatalogPropertySecretRefs struct {
	// PropertySecretRefs maps catalog property names to Secret keys.
	// Each entry renders the property as ${ENV:CATALOG_<CATALOG>_<PROPERTY>}
	// in the generated catalog ConfigMap and injects the Secret key into
	// selected Trino pods as that environment variable.
	// The Secret must exist in the same namespace as the XTrinodeCatalog.
	PropertySecretRefs map[string]corev1.SecretKeySelector `json:"propertySecretRefs,omitempty"`
}

// HiveCatalogSpec defines Hive connector configuration
type HiveCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// MetastoreURI is the Hive Metastore URI
	MetastoreURI string `json:"metastoreURI"`

	// S3Endpoint is the S3 endpoint for native S3 file system access (optional)
	S3Endpoint string `json:"s3Endpoint,omitempty"`

	// S3AccessKeySecret references a Secret containing the S3 access key.
	// The Secret must exist in the same namespace as the XTrinodeCatalog.
	S3AccessKeySecret *corev1.SecretKeySelector `json:"s3AccessKeySecret,omitempty"`

	// S3SecretKeySecret references a Secret containing the S3 secret key.
	// The Secret must exist in the same namespace as the XTrinodeCatalog.
	S3SecretKeySecret *corev1.SecretKeySelector `json:"s3SecretKeySecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// IcebergCatalogSpec defines Iceberg connector configuration
type IcebergCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// CatalogType is the Iceberg catalog type (hive_metastore, jdbc, rest, etc.)
	CatalogType string `json:"catalogType"`

	// WarehouseURI is the warehouse URI
	WarehouseURI string `json:"warehouseURI"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// PostgresCatalogSpec defines PostgreSQL connector configuration
type PostgresCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the PostgreSQL connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the PostgreSQL user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	// The Secret must exist in the same namespace as the XTrinodeCatalog
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// DeltaLakeCatalogSpec defines Delta Lake connector configuration
type DeltaLakeCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// CatalogType is the Delta Lake metastore type (hive_metastore or glue)
	CatalogType string `json:"catalogType"`

	// WarehouseURI is the Hive Metastore URI when catalogType is hive/hive_metastore.
	WarehouseURI string `json:"warehouseURI"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// MySQLCatalogSpec defines MySQL connector configuration
type MySQLCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the MySQL connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the MySQL user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	// The Secret must exist in the same namespace as the XTrinodeCatalog
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// MongoDBCatalogSpec defines MongoDB connector configuration
type MongoDBCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURI is the MongoDB connection URI
	ConnectionURI string `json:"connectionURI"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// KafkaCatalogSpec defines Kafka connector configuration
type KafkaCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// KafkaNodes is the list of Kafka broker addresses
	KafkaNodes []string `json:"kafkaNodes"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// CustomCatalogSpec defines custom connector configuration
// Allows teams to provide raw connector properties
type CustomCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectorName is the connector name (e.g., "postgresql", "mysql", etc.)
	ConnectorName string `json:"connectorName"`

	// Properties are the raw connector properties
	Properties map[string]string `json:"properties"`
}

// BigQueryCatalogSpec defines BigQuery connector configuration
type BigQueryCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ProjectID is the GCP project ID
	ProjectID string `json:"projectID"`

	// ParentProjectID is the parent project ID (optional)
	ParentProjectID string `json:"parentProjectID,omitempty"`

	// CredentialsFile is the path to a mounted service account credentials file (optional).
	// Use propertySecretRefs["bigquery.credentials-key"] for raw service account key material.
	CredentialsFile string `json:"credentialsFile,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// BlackHoleCatalogSpec defines Black Hole connector configuration
// Black Hole connector discards all data written to it
type BlackHoleCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// CassandraCatalogSpec defines Cassandra connector configuration
type CassandraCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ContactPoints is the comma-separated list of Cassandra contact points
	ContactPoints string `json:"contactPoints"`

	// Port is the Cassandra port (default: 9042)
	Port int `json:"port,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// ClickHouseCatalogSpec defines ClickHouse connector configuration
type ClickHouseCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the ClickHouse connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the ClickHouse user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// DruidCatalogSpec defines Druid connector configuration
type DruidCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// BrokerURL is the Druid broker URL
	BrokerURL string `json:"brokerURL"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// DuckDBCatalogSpec defines DuckDB connector configuration
type DuckDBCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// DatabasePath is the path to the DuckDB database file
	DatabasePath string `json:"databasePath,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// ElasticsearchCatalogSpec defines Elasticsearch connector configuration
type ElasticsearchCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Host is the Elasticsearch host
	Host string `json:"host"`

	// Port is the Elasticsearch port (default: 9200)
	Port int `json:"port,omitempty"`

	// DefaultSchema is the default schema name
	DefaultSchema string `json:"defaultSchema,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// ExasolCatalogSpec defines Exasol connector configuration
type ExasolCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the Exasol connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the Exasol user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// FakerCatalogSpec defines Faker connector configuration
// Faker connector generates fake data for testing
type FakerCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// DefaultLimit is the default row limit (optional)
	DefaultLimit int64 `json:"defaultLimit,omitempty"`

	// NullProbability is the probability of generating null values (0.0-1.0)
	NullProbability float64 `json:"nullProbability,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// GoogleSheetsCatalogSpec defines Google Sheets connector configuration
type GoogleSheetsCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// CredentialsFilePath is the path to the service account credentials file
	CredentialsFilePath string `json:"credentialsFilePath"`

	// MetadataSheetID is the Google Sheet ID containing metadata
	MetadataSheetID string `json:"metadataSheetID,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// HudiCatalogSpec defines Hudi connector configuration
type HudiCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// MetastoreURI is the Hive Metastore URI
	MetastoreURI string `json:"metastoreURI"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// IgniteCatalogSpec defines Ignite connector configuration
type IgniteCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the Ignite connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the Ignite user (optional)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// JMXCatalogSpec defines JMX connector configuration
// JMX connector exposes JMX MBeans as tables
type JMXCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// LakehouseCatalogSpec defines Lakehouse connector configuration
type LakehouseCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// CatalogType is the lakehouse catalog type
	CatalogType string `json:"catalogType"`

	// MetastoreURI is the metastore URI
	MetastoreURI string `json:"metastoreURI,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// LokiCatalogSpec defines Loki connector configuration
type LokiCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// URI is the Loki server URI
	URI string `json:"uri"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// MariaDBCatalogSpec defines MariaDB connector configuration
type MariaDBCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the MariaDB connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the MariaDB user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// MemoryCatalogSpec defines Memory connector configuration
// Memory connector stores data in RAM
type MemoryCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// MaxDataPerNode is the maximum amount of memory per node
	MaxDataPerNode string `json:"maxDataPerNode,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// OpenSearchCatalogSpec defines OpenSearch connector configuration
type OpenSearchCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Host is the OpenSearch host
	Host string `json:"host"`

	// Port is the OpenSearch port (default: 9200)
	Port int `json:"port,omitempty"`

	// DefaultSchema is the default schema name
	DefaultSchema string `json:"defaultSchema,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// OracleCatalogSpec defines Oracle connector configuration
type OracleCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the Oracle connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the Oracle user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// PinotCatalogSpec defines Pinot connector configuration
type PinotCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ControllerURLs is the comma-separated list of Pinot controller URLs
	ControllerURLs string `json:"controllerURLs"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// PrometheusCatalogSpec defines Prometheus connector configuration
type PrometheusCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// URI is the Prometheus server URI
	URI string `json:"uri"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// RedisCatalogSpec defines Redis connector configuration
type RedisCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Nodes is the comma-separated list of Redis nodes
	Nodes string `json:"nodes"`

	// Database is the Redis database number (default: 0)
	Database int `json:"database,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// RedshiftCatalogSpec defines Redshift connector configuration
type RedshiftCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the Redshift connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the Redshift user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// SingleStoreCatalogSpec defines SingleStore connector configuration
type SingleStoreCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the SingleStore connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the SingleStore user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// SnowflakeCatalogSpec defines Snowflake connector configuration
type SnowflakeCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// AccountURL is the Snowflake account URL
	AccountURL string `json:"accountURL"`

	// User is the Snowflake user
	User string `json:"user"`

	// Database is the Snowflake database
	Database string `json:"database,omitempty"`

	// Role is the Snowflake role
	Role string `json:"role,omitempty"`

	// Warehouse is the Snowflake warehouse
	Warehouse string `json:"warehouse,omitempty"`

	// PasswordSecret references a Secret containing the password
	PasswordSecret *corev1.SecretKeySelector `json:"passwordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// SQLServerCatalogSpec defines SQL Server connector configuration
type SQLServerCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the SQL Server connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the SQL Server user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// SystemCatalogSpec defines System connector configuration
// System connector provides access to Trino system tables
type SystemCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// ThriftCatalogSpec defines Thrift connector configuration
type ThriftCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Host is the Thrift server host
	Host string `json:"host"`

	// Port is the Thrift server port
	Port int `json:"port"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// TPCDSCatalogSpec defines TPC-DS connector configuration
// TPC-DS connector generates TPC-DS benchmark data
type TPCDSCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// TPCHCatalogSpec defines TPC-H connector configuration
// TPC-H connector generates TPC-H benchmark data
type TPCHCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// VerticaCatalogSpec defines Vertica connector configuration
type VerticaCatalogSpec struct {
	CatalogPropertySecretRefs `json:",inline"`

	// ConnectionURL is the Vertica connection URL
	ConnectionURL string `json:"connectionURL"`

	// ConnectionUser is the Vertica user (optional if in URL)
	ConnectionUser string `json:"connectionUser,omitempty"`

	// ConnectionPasswordSecret references a Secret containing the password
	ConnectionPasswordSecret *corev1.SecretKeySelector `json:"connectionPasswordSecret,omitempty"`

	// Additional properties as key-value pairs
	Properties map[string]string `json:"properties,omitempty"`
}

// XTrinodeCatalogStatus defines the observed state of XTrinodeCatalog
type XTrinodeCatalogStatus struct {
	// Phase indicates the catalog phase
	// Values: Pending, Ready, Error
	Phase string `json:"phase,omitempty"`

	// Message provides additional information about the catalog status
	Message string `json:"message,omitempty"`

	// ConfigMapName is the name of the generated ConfigMap
	ConfigMapName string `json:"configMapName,omitempty"`

	// LastUpdated is the timestamp of the last update
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Connector",type="string",JSONPath=".spec.connector.*",description="Connector type"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Catalog phase"
// +kubebuilder:printcolumn:name="ConfigMap",type="string",JSONPath=".status.configMapName",description="Generated ConfigMap name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// XTrinodeCatalog is the Schema for the xtrinodecatalogs API
type XTrinodeCatalog struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   XTrinodeCatalogSpec   `json:"spec,omitempty"`
	Status XTrinodeCatalogStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// XTrinodeCatalogList contains a list of XTrinodeCatalog
type XTrinodeCatalogList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []XTrinodeCatalog `json:"items"`
}

func init() {
	SchemeBuilder.Register(&XTrinodeCatalog{}, &XTrinodeCatalogList{})
}
