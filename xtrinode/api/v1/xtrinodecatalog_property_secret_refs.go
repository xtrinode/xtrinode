package v1

import corev1 "k8s.io/api/core/v1"

// GenericPropertySecretRefs returns the connector's generic Secret-backed catalog properties.
//
//nolint:gocyclo // connector union requires explicit Secret-ref coverage.
func (c *XTrinodeCatalogConnector) GenericPropertySecretRefs() map[string]corev1.SecretKeySelector {
	if c == nil {
		return nil
	}
	switch {
	case c.BigQuery != nil:
		return c.BigQuery.PropertySecretRefs
	case c.BlackHole != nil:
		return c.BlackHole.PropertySecretRefs
	case c.Cassandra != nil:
		return c.Cassandra.PropertySecretRefs
	case c.ClickHouse != nil:
		return c.ClickHouse.PropertySecretRefs
	case c.Custom != nil:
		return c.Custom.PropertySecretRefs
	case c.DeltaLake != nil:
		return c.DeltaLake.PropertySecretRefs
	case c.Druid != nil:
		return c.Druid.PropertySecretRefs
	case c.DuckDB != nil:
		return c.DuckDB.PropertySecretRefs
	case c.Elasticsearch != nil:
		return c.Elasticsearch.PropertySecretRefs
	case c.Exasol != nil:
		return c.Exasol.PropertySecretRefs
	case c.Faker != nil:
		return c.Faker.PropertySecretRefs
	case c.GoogleSheets != nil:
		return c.GoogleSheets.PropertySecretRefs
	case c.Hive != nil:
		return c.Hive.PropertySecretRefs
	case c.Hudi != nil:
		return c.Hudi.PropertySecretRefs
	case c.Iceberg != nil:
		return c.Iceberg.PropertySecretRefs
	case c.Ignite != nil:
		return c.Ignite.PropertySecretRefs
	case c.JMX != nil:
		return c.JMX.PropertySecretRefs
	case c.Kafka != nil:
		return c.Kafka.PropertySecretRefs
	case c.Lakehouse != nil:
		return c.Lakehouse.PropertySecretRefs
	case c.Loki != nil:
		return c.Loki.PropertySecretRefs
	case c.MariaDB != nil:
		return c.MariaDB.PropertySecretRefs
	case c.Memory != nil:
		return c.Memory.PropertySecretRefs
	case c.MongoDB != nil:
		return c.MongoDB.PropertySecretRefs
	case c.MySQL != nil:
		return c.MySQL.PropertySecretRefs
	case c.OpenSearch != nil:
		return c.OpenSearch.PropertySecretRefs
	case c.Oracle != nil:
		return c.Oracle.PropertySecretRefs
	case c.Pinot != nil:
		return c.Pinot.PropertySecretRefs
	case c.Postgres != nil:
		return c.Postgres.PropertySecretRefs
	case c.Prometheus != nil:
		return c.Prometheus.PropertySecretRefs
	case c.Redis != nil:
		return c.Redis.PropertySecretRefs
	case c.Redshift != nil:
		return c.Redshift.PropertySecretRefs
	case c.SingleStore != nil:
		return c.SingleStore.PropertySecretRefs
	case c.Snowflake != nil:
		return c.Snowflake.PropertySecretRefs
	case c.SQLServer != nil:
		return c.SQLServer.PropertySecretRefs
	case c.System != nil:
		return c.System.PropertySecretRefs
	case c.Thrift != nil:
		return c.Thrift.PropertySecretRefs
	case c.TPCDS != nil:
		return c.TPCDS.PropertySecretRefs
	case c.TPCH != nil:
		return c.TPCH.PropertySecretRefs
	case c.Vertica != nil:
		return c.Vertica.PropertySecretRefs
	default:
		return nil
	}
}
