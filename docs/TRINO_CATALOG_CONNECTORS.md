# Trino Catalog Connectors - Typed Reference

> **Validation status**
>
> This reference lists the typed `XTrinodeCatalog` API shapes and the Trino catalog
> property keys XTrinode renders.
>
> Live connector smoke/query coverage is currently limited to:
>
> - PostgreSQL: local k3d/Tilt Robot coverage verifies generated ConfigMaps,
>   Secret-backed password injection, gateway routing, and real SQL queries.
> - Iceberg: GCP/GCS Hive Metastore smoke material covers live Iceberg catalog
>   configuration and query flow.
>
> All other typed connectors currently have property-rendering/unit coverage only.
> Treat them as implementation coverage, not as proof that the connector has been
> exercised against a live upstream system.
>
> For production use, validate each non-PostgreSQL/non-Iceberg connector against
> the Trino version tracked in [TOOLING.md](TOOLING.md#runtime-and-provider-compatibility).
> When a typed field does not expose the exact setting you need, use the
> `properties` map or the `Custom` connector type with the exact property names
> from the [official Trino connector documentation](https://trino.io/docs/current/connector.html).

This document is a reference for the Trino catalog connector shapes exposed by
the XTrinode operator.

## Overview

The XTrinode operator exposes typed configuration for the 38 connectors listed
in the [Trino connector documentation](https://trino.io/docs/current/connector.html),
plus a `Custom` connector escape hatch for raw catalog properties. Each catalog
is configured with the `XTrinodeCatalog` Custom Resource Definition (CRD).

## Validation Posture

| Coverage level | Current scope | What it means |
| --- | --- | --- |
| Live connector smoke/query coverage | PostgreSQL, Iceberg | These have been exercised with a real Trino runtime and backing service path. |
| Unit-tested property rendering | All typed connectors listed below | Generated `connector.name` and typed property keys are covered by Go tests. |
| Needs connector-specific live validation | Every typed connector except PostgreSQL and Iceberg | Validate with your Trino image, network path, secrets, and backing service before relying on it. |

## Table of Contents

- [BigQuery](#bigquery)
- [Black Hole](#black-hole)
- [Cassandra](#cassandra)
- [ClickHouse](#clickhouse)
- [Delta Lake](#delta-lake)
- [Druid](#druid)
- [DuckDB](#duckdb)
- [Elasticsearch](#elasticsearch)
- [Exasol](#exasol)
- [Faker](#faker)
- [Google Sheets](#google-sheets)
- [Hive](#hive)
- [Hudi](#hudi)
- [Iceberg](#iceberg)
- [Ignite](#ignite)
- [JMX](#jmx)
- [Kafka](#kafka)
- [Lakehouse](#lakehouse)
- [Loki](#loki)
- [MariaDB](#mariadb)
- [Memory](#memory)
- [MongoDB](#mongodb)
- [MySQL](#mysql)
- [OpenSearch](#opensearch)
- [Oracle](#oracle)
- [Pinot](#pinot)
- [PostgreSQL](#postgresql)
- [Prometheus](#prometheus)
- [Redis](#redis)
- [Redshift](#redshift)
- [SingleStore](#singlestore)
- [Snowflake](#snowflake)
- [SQL Server](#sql-server)
- [System](#system)
- [Thrift](#thrift)
- [TPC-DS](#tpc-ds)
- [TPC-H](#tpc-h)
- [Vertica](#vertica)
- [Custom](#custom)

---

## BigQuery

Google BigQuery connector for querying data in BigQuery.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: bigquery-catalog
  namespace: default
spec:
  connector:
    bigQuery:
      projectID: "my-gcp-project"
      parentProjectID: "parent-project"  # optional
      propertySecretRefs:
        bigquery.credentials-key:
          name: bigquery-credentials
          key: credentials-key
      properties:
        bigquery.views-enabled: "true"
```

### Required Fields

- `projectID`: GCP project ID

### Optional Fields

- `parentProjectID`: Parent project ID for billing
- `credentialsFile`: Path to a mounted service account credentials file
- `propertySecretRefs`: Secret-backed properties, for example `bigquery.credentials-key`
- `properties`: Additional BigQuery-specific properties

---

## Black Hole

Black Hole connector discards all data written to it. Useful for testing and benchmarking.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: blackhole-catalog
  namespace: default
spec:
  connector:
    blackHole:
      properties:
        blackhole.page-processing-delay: "1s"
```

### Optional Fields

- `properties`: Additional Black Hole-specific properties

---

## Cassandra

Apache Cassandra connector for querying Cassandra databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: cassandra-catalog
  namespace: default
spec:
  connector:
    cassandra:
      contactPoints: "cassandra-1.example.com,cassandra-2.example.com"
      port: 9042  # optional, default: 9042
      propertySecretRefs:
        cassandra.password:
          name: cassandra-credentials
          key: password
      properties:
        cassandra.username: "cassandra_user"
```

### Required Fields

- `contactPoints`: Comma-separated list of Cassandra contact points

### Optional Fields

- `port`: Cassandra port (default: 9042)
- `propertySecretRefs`: Secret-backed properties, for example `cassandra.password`
- `properties`: Additional Cassandra-specific properties

---

## ClickHouse

ClickHouse connector for querying ClickHouse databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: clickhouse-catalog
  namespace: default
spec:
  connector:
    clickHouse:
      connectionURL: "jdbc:clickhouse://clickhouse.example.com:8123/default"
      connectionUser: "clickhouse_user"
      connectionPasswordSecret:
        name: clickhouse-secret
        key: password
      properties:
        clickhouse.socket-timeout: "30s"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional ClickHouse-specific properties

---

## Delta Lake

Delta Lake connector for querying Delta Lake tables.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: deltalake-catalog
  namespace: default
spec:
  connector:
    deltaLake:
      catalogType: "hive"
      warehouseURI: "thrift://metastore:9083"
      properties:
        fs.native-s3.enabled: "true"
        delta.enable-non-concurrent-writes: "true"
```

### Required Fields

- `catalogType`: Metastore type. `hive` and `hive_metastore` render
  `hive.metastore.uri`; `glue` renders `hive.metastore=glue`.
- `warehouseURI`: Required by the current typed API. For `hive` and
  `hive_metastore`, it renders `hive.metastore.uri`; for `glue`, it is
  currently required by validation but not rendered. The field name is kept for
  API compatibility.

### Optional Fields

- `properties`: Additional Delta Lake-specific properties

---

## Druid

Apache Druid connector for querying Druid databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: druid-catalog
  namespace: default
spec:
  connector:
    druid:
      brokerURL: "http://druid-broker:8082"
```

### Required Fields

- `brokerURL`: Druid broker HTTP URL, or a full Avatica JDBC URL. XTrinode renders
  this as Trino `connection-url`.

### Optional Fields

- `properties`: Additional Druid-specific properties

---

## DuckDB

DuckDB connector for querying DuckDB databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: duckdb-catalog
  namespace: default
spec:
  connector:
    duckDB:
      databasePath: "/data/duckdb.db"  # optional
      properties:
        metadata.cache-ttl: "1m"
```

### Optional Fields

- `databasePath`: Path to the DuckDB database file, or a full `jdbc:duckdb:...`
  URL. XTrinode renders this as Trino `connection-url`.
- `properties`: Additional DuckDB-specific properties

---

## Elasticsearch

Elasticsearch connector for querying Elasticsearch indices.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: elasticsearch-catalog
  namespace: default
spec:
  connector:
    elasticsearch:
      host: "elasticsearch.example.com"
      port: 9200  # optional, default: 9200
      defaultSchema: "default"  # optional
      propertySecretRefs:
        elasticsearch.auth.password:
          name: elasticsearch-credentials
          key: password
      properties:
        elasticsearch.security: "PASSWORD"
        elasticsearch.auth.user: "elastic"
```

### Required Fields

- `host`: Elasticsearch host

### Optional Fields

- `port`: Elasticsearch port (default: 9200)
- `defaultSchema`: Default schema name
- `propertySecretRefs`: Secret-backed properties, for example `elasticsearch.auth.password`
- `properties`: Additional Elasticsearch-specific properties

---

## Exasol

Exasol connector for querying Exasol databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: exasol-catalog
  namespace: default
spec:
  connector:
    exasol:
      connectionURL: "jdbc:exa:exasol.example.com:8563"
      connectionUser: "exasol_user"
      connectionPasswordSecret:
        name: exasol-secret
        key: password
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional Exasol-specific properties

---

## Faker

Faker connector generates fake data for testing purposes.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: faker-catalog
  namespace: default
spec:
  connector:
    faker:
      defaultLimit: 1000  # optional
      nullProbability: 0.1  # optional, 0.0-1.0
      properties:
        faker.locale: "en-US"
```

### Optional Fields

- `defaultLimit`: Default row limit for queries
- `nullProbability`: Probability of generating null values (0.0-1.0)
- `properties`: Additional Faker-specific properties

---

## Google Sheets

Google Sheets connector for querying Google Sheets.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: gsheets-catalog
  namespace: default
spec:
  connector:
    googleSheets:
      credentialsFilePath: "/path/to/credentials.json"
      metadataSheetID: "1ABC123..."  # optional
      properties:
        gsheets.max-data-cache-size: "1000"
```

### Required Fields

- `credentialsFilePath`: Path to service account credentials file

### Optional Fields

- `metadataSheetID`: Google Sheet ID containing metadata
- `properties`: Additional Google Sheets-specific properties

---

## Hive

Apache Hive connector for querying Hive tables.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: hive-catalog
  namespace: default
spec:
  connector:
    hive:
      metastoreURI: "thrift://hive-metastore:9083"
      s3Endpoint: "https://s3.amazonaws.com"  # optional
      s3AccessKeySecret:  # optional, must be paired with s3SecretKeySecret
        name: hive-s3
        key: access-key
      s3SecretKeySecret:
        name: hive-s3
        key: secret-key
      properties:
        hive.allow-drop-table: "true"
        hive.compression-codec: "SNAPPY"
```

### Required Fields

- `metastoreURI`: Hive Metastore URI

### Optional Fields

- `s3Endpoint`: S3 endpoint URL
- `s3AccessKeySecret`: Secret key selector for the S3 access key
- `s3SecretKeySecret`: Secret key selector for the S3 secret key
- `properties`: Additional Hive-specific properties

---

## Hudi

Apache Hudi connector for querying Hudi tables.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: hudi-catalog
  namespace: default
spec:
  connector:
    hudi:
      metastoreURI: "thrift://hive-metastore:9083"
      properties:
        hudi.columns-to-hide: "_hoodie_commit_time,_hoodie_commit_seqno"
```

### Required Fields

- `metastoreURI`: Hive Metastore URI

### Optional Fields

- `properties`: Additional Hudi-specific properties

---

## Iceberg

Apache Iceberg connector for querying Iceberg tables.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: iceberg-catalog
  namespace: default
spec:
  connector:
    iceberg:
      catalogType: "hive"
      warehouseURI: "s3://my-bucket/warehouse"
      properties:
        hive.metastore.uri: "thrift://metastore:9083"
        iceberg.file-format: "PARQUET"
```

### Required Fields

- `catalogType`: Catalog type (e.g., "hive", "jdbc", "rest")
- `warehouseURI`: Required by the current typed API shape. For Hive
  Metastore-backed Iceberg, set the actual Trino metastore and filesystem
  properties under `properties`, such as `hive.metastore.uri` and
  `fs.native-gcs.enabled`. XTrinode does not emit a generic
  `iceberg.catalog.warehouse` property because Trino does not have one property
  that is valid for every Iceberg catalog type.

### Optional Fields

- `properties`: Additional Iceberg-specific properties

---

## Ignite

Apache Ignite connector for querying Ignite databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: ignite-catalog
  namespace: default
spec:
  connector:
    ignite:
      connectionURL: "jdbc:ignite:thin://ignite.example.com:10800"
      connectionUser: "ignite_user"
      connectionPasswordSecret:
        name: ignite-secret
        key: password
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional Ignite-specific properties

---

## JMX

JMX connector exposes JMX MBeans as tables for monitoring.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: jmx-catalog
  namespace: default
spec:
  connector:
    jmx:
      properties:
        jmx.dump-tables: "java.lang:type=Runtime,java.lang:type=Memory"
```

### Optional Fields

- `properties`: Additional JMX-specific properties

---

## Kafka

Apache Kafka connector for querying Kafka topics.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: kafka-catalog
  namespace: default
spec:
  connector:
    kafka:
      kafkaNodes:
        - "kafka-1:9092"
        - "kafka-2:9092"
        - "kafka-3:9092"
      properties:
        kafka.table-names: "topic1,topic2,topic3"
        kafka.hide-internal-columns: "false"
```

### Required Fields

- `kafkaNodes`: List of Kafka broker addresses

### Optional Fields

- `properties`: Additional Kafka-specific properties

---

## Lakehouse

Lakehouse connector for unified lakehouse access.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: lakehouse-catalog
  namespace: default
spec:
  connector:
    lakehouse:
      catalogType: "ICEBERG"
      metastoreURI: "thrift://metastore:9083"  # optional
      properties:
        fs.native-s3.enabled: "true"
```

### Required Fields

- `catalogType`: Default Lakehouse table type. XTrinode renders this as Trino
  `lakehouse.table-type`. Valid Trino values include `HIVE`, `ICEBERG`, and
  `DELTA`.

### Optional Fields

- `metastoreURI`: Hive Thrift metastore URI. XTrinode renders this as Trino
  `hive.metastore=thrift` and `hive.metastore.uri`.
- `properties`: Additional Lakehouse-specific properties

---

## Loki

Grafana Loki connector for querying Loki logs.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: loki-catalog
  namespace: default
spec:
  connector:
    loki:
      uri: "http://loki:3100"
      properties:
        loki.max-query-duration: "30s"
```

### Required Fields

- `uri`: Loki server URI

### Optional Fields

- `properties`: Additional Loki-specific properties

---

## MariaDB

MariaDB connector for querying MariaDB databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: mariadb-catalog
  namespace: default
spec:
  connector:
    mariaDB:
      connectionURL: "jdbc:mariadb://mariadb.example.com:3306/database"
      connectionUser: "mariadb_user"
      connectionPasswordSecret:
        name: mariadb-secret
        key: password
      properties:
        mariadb.auto-reconnect: "true"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional MariaDB-specific properties

---

## Memory

Memory connector stores data in RAM for temporary tables.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: memory-catalog
  namespace: default
spec:
  connector:
    memory:
      maxDataPerNode: "128MB"  # optional
      properties:
        memory.max-data-per-node: "128MB"
```

### Optional Fields

- `maxDataPerNode`: Maximum memory per node
- `properties`: Additional Memory-specific properties

---

## MongoDB

MongoDB connector for querying MongoDB databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: mongodb-catalog
  namespace: default
spec:
  connector:
    mongodb:
      connectionURI: "mongodb://mongodb.example.com:27017/database"
      properties:
        mongodb.read-preference: "PRIMARY"
        mongodb.cursor-batch-size: "1000"
```

### Required Fields

- `connectionURI`: MongoDB connection URI

### Optional Fields

- `properties`: Additional MongoDB-specific properties

---

## MySQL

MySQL connector for querying MySQL databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: mysql-catalog
  namespace: default
spec:
  connector:
    mysql:
      connectionURL: "jdbc:mysql://mysql.example.com:3306/database"
      connectionUser: "mysql_user"
      connectionPasswordSecret:
        name: mysql-secret
        key: password
      properties:
        mysql.auto-reconnect: "true"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional MySQL-specific properties

---

## OpenSearch

OpenSearch connector for querying OpenSearch indices.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: opensearch-catalog
  namespace: default
spec:
  connector:
    openSearch:
      host: "opensearch.example.com"
      port: 9200  # optional, default: 9200
      defaultSchema: "default"  # optional
      propertySecretRefs:
        opensearch.auth.password:
          name: opensearch-credentials
          key: password
      properties:
        opensearch.security: "PASSWORD"
        opensearch.auth.user: "admin"
```

### Required Fields

- `host`: OpenSearch host

### Optional Fields

- `port`: OpenSearch port (default: 9200)
- `defaultSchema`: Default schema name
- `propertySecretRefs`: Secret-backed properties, for example `opensearch.auth.password`
- `properties`: Additional OpenSearch-specific properties

---

## Oracle

Oracle Database connector for querying Oracle databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: oracle-catalog
  namespace: default
spec:
  connector:
    oracle:
      connectionURL: "jdbc:oracle:thin:@oracle.example.com:1521:ORCL"
      connectionUser: "oracle_user"
      connectionPasswordSecret:
        name: oracle-secret
        key: password
      properties:
        oracle.connection-pool.max-size: "30"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional Oracle-specific properties

---

## Pinot

Apache Pinot connector for querying Pinot databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: pinot-catalog
  namespace: default
spec:
  connector:
    pinot:
      controllerURLs: "http://pinot-controller-1:9000,http://pinot-controller-2:9000"
      properties:
        pinot.segments-per-split: "1"
```

### Required Fields

- `controllerURLs`: Comma-separated list of Pinot controller URLs

### Optional Fields

- `properties`: Additional Pinot-specific properties

---

## PostgreSQL

PostgreSQL connector for querying PostgreSQL databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: postgres-catalog
  namespace: default
spec:
  connector:
    postgres:
      connectionURL: "jdbc:postgresql://postgres.example.com:5432/database"
      connectionUser: "postgres_user"
      connectionPasswordSecret:
        name: postgres-secret
        key: password
      properties:
        postgresql.include-system-tables: "false"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional PostgreSQL-specific properties

---

## Prometheus

Prometheus connector for querying Prometheus metrics.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: prometheus-catalog
  namespace: default
spec:
  connector:
    prometheus:
      uri: "http://prometheus:9090"
      properties:
        prometheus.query-chunk-size-duration: "1d"
        prometheus.max-query-range-duration: "21d"
```

### Required Fields

- `uri`: Prometheus server URI

### Optional Fields

- `properties`: Additional Prometheus-specific properties

---

## Redis

Redis connector for querying Redis data structures.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: redis-catalog
  namespace: default
spec:
  connector:
    redis:
      nodes: "redis-1:6379,redis-2:6379,redis-3:6379"
      database: 0  # optional, default: 0
      properties:
        redis.table-names: "users,orders,products"
        redis.key-prefix-schema-table: "true"
```

### Required Fields

- `nodes`: Comma-separated list of Redis nodes

### Optional Fields

- `database`: Redis database number (default: 0)
- `properties`: Additional Redis-specific properties

---

## Redshift

Amazon Redshift connector for querying Redshift databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: redshift-catalog
  namespace: default
spec:
  connector:
    redshift:
      connectionURL: "jdbc:redshift://redshift.example.com:5439/database"
      connectionUser: "redshift_user"
      connectionPasswordSecret:
        name: redshift-secret
        key: password
      properties:
        redshift.max-split-size: "64MB"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional Redshift-specific properties

---

## SingleStore

SingleStore (MemSQL) connector for querying SingleStore databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: singlestore-catalog
  namespace: default
spec:
  connector:
    singleStore:
      connectionURL: "jdbc:singlestore://singlestore.example.com:3306/database"
      connectionUser: "singlestore_user"
      connectionPasswordSecret:
        name: singlestore-secret
        key: password
      properties:
        singlestore.auto-reconnect: "true"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional SingleStore-specific properties

---

## Snowflake

Snowflake connector for querying Snowflake databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: snowflake-catalog
  namespace: default
spec:
  connector:
    snowflake:
      accountURL: "https://account.snowflakecomputing.com"
      user: "snowflake_user"
      database: "MY_DATABASE"  # optional
      role: "MY_ROLE"  # optional
      warehouse: "MY_WAREHOUSE"  # optional
      passwordSecret:
        name: snowflake-secret
        key: password
      properties:
        snowflake.session-properties: "QUERY_TAG=trino"
```

### Required Fields

- `accountURL`: Snowflake account URL, host, account identifier, or
  `jdbc:snowflake://...` URL. XTrinode renders this as Trino `connection-url`
  and `snowflake.account`.
- `user`: Snowflake user. XTrinode renders this as Trino `connection-user`.

### Optional Fields

- `database`: Snowflake database
- `role`: Snowflake role
- `warehouse`: Snowflake warehouse
- `passwordSecret`: Secret reference for password. XTrinode renders this as
  Trino `connection-password`.
- `properties`: Additional Snowflake-specific properties

Snowflake reads use Apache Arrow in Trino. Add the required JVM flags through
`valuesOverlay.coordinator.additionalJVMConfig` and
`valuesOverlay.worker.additionalJVMConfig`:

```yaml
- --add-opens=java.base/java.nio=ALL-UNNAMED
- --sun-misc-unsafe-memory-access=allow
```

---

## SQL Server

Microsoft SQL Server connector for querying SQL Server databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: sqlserver-catalog
  namespace: default
spec:
  connector:
    sqlServer:
      connectionURL: "jdbc:sqlserver://sqlserver.example.com:1433;database=MyDatabase"
      connectionUser: "sqlserver_user"
      connectionPasswordSecret:
        name: sqlserver-secret
        key: password
      properties:
        sqlserver.snapshot-isolation.disabled: "false"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional SQL Server-specific properties

---

## System

System connector provides access to Trino system tables and metadata.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: system-catalog
  namespace: default
spec:
  connector:
    system:
      properties: {}
```

### Optional Fields

- `properties`: Additional System-specific properties

---

## Thrift

Thrift connector for querying Thrift-based data sources.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: thrift-catalog
  namespace: default
spec:
  connector:
    thrift:
      host: "thrift-server.example.com"
      port: 9090
      properties:
        trino.thrift.client.max-retry-time: "30s"
```

### Required Fields

- `host`: Thrift server host
- `port`: Thrift server port

### Optional Fields

- `properties`: Additional Thrift-specific properties

---

## TPC-DS

TPC-DS connector generates TPC-DS benchmark data for testing.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: tpcds-catalog
  namespace: default
spec:
  connector:
    tpcds:
      properties:
        tpcds.splits-per-node: "4"
```

### Optional Fields

- `properties`: Additional TPC-DS-specific properties

---

## TPC-H

TPC-H connector generates TPC-H benchmark data for testing.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: tpch-catalog
  namespace: default
spec:
  connector:
    tpch:
      properties:
        tpch.splits-per-node: "4"
```

### Optional Fields

- `properties`: Additional TPC-H-specific properties

---

## Vertica

Vertica connector for querying Vertica databases.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: vertica-catalog
  namespace: default
spec:
  connector:
    vertica:
      connectionURL: "jdbc:vertica://vertica.example.com:5433/database"
      connectionUser: "vertica_user"
      connectionPasswordSecret:
        name: vertica-secret
        key: password
      properties:
        vertica.connection-pool.max-size: "30"
```

### Required Fields

- `connectionURL`: JDBC connection URL

### Optional Fields

- `connectionUser`: Database user
- `connectionPasswordSecret`: Secret reference for password
- `properties`: Additional Vertica-specific properties

---

## Custom

Custom connector allows you to configure any Trino connector with raw properties.

### Example

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinodeCatalog
metadata:
  name: custom-catalog
  namespace: default
spec:
  connector:
    custom:
      connectorName: "my-custom-connector"
      properties:
        custom.property1: "value1"
        custom.property2: "value2"
```

### Required Fields

- `connectorName`: Connector name (e.g., "postgresql", "mysql")
- `properties`: Raw connector properties

---

## Secret Management

For connectors that require passwords or sensitive credentials, use Kubernetes Secrets:

1. **Create a Secret:**

```bash
kubectl create secret generic my-db-secret \
  --from-literal=password='my-secure-password' \
  --namespace default
```

1. **Reference in XTrinodeCatalog:**

```yaml
spec:
  connector:
    postgres:
      connectionURL: "jdbc:postgresql://postgres:5432/db"
      connectionUser: "user"
      connectionPasswordSecret:
        name: my-db-secret
        key: password
```

The operator automatically injects secrets as environment variables in Trino pods using the format:

```text
CATALOG_<CATALOG_NAME>_<PROPERTY_NAME>
```

---

## Catalog Selection

XTrinodes use label selectors to discover and mount catalogs:

```yaml
apiVersion: analytics.xtrinode.io/v1
kind: XTrinode
metadata:
  name: my-xtrinode
spec:
  catalogSelector:
    matchLabels:
      environment: production
      team: data-engineering
```

All `XTrinodeCatalog` resources with matching `spec.labels` will be automatically mounted.

---

## Additional Resources

- [Official Trino Connector Documentation](https://trino.io/docs/current/connector.html)
- [XTrinodeCatalog API Reference](../xtrinode/api/v1/xtrinodecatalog_types.go)
- [Catalog Examples](../examples/)

---

## Summary

The XTrinode operator provides typed catalog CRD coverage for the Trino
connectors listed in this reference, enabling you to:

- Manage catalogs declaratively via Kubernetes CRDs
- Securely inject credentials via Kubernetes Secrets
- Dynamically mount catalogs to XTrinode runtimes
- Use typed connector fields where they match the required Trino properties
- Use custom connectors or raw `properties` entries for connector settings that
  are not modeled as typed fields

Only PostgreSQL and Iceberg currently have live connector smoke/query coverage.
For all other connectors, treat the typed definitions as property-rendering
coverage and validate them against your target Trino version and backing service.
For connector-specific configuration details, refer to the
[official Trino documentation](https://trino.io/docs/current/connector.html).
