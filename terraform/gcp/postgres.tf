# GCP Cloud SQL PostgreSQL Instance
# Provides managed PostgreSQL database for Trino catalogs (testing)
# Set postgres_enabled = true in terraform.tfvars to create

# Cloud SQL PostgreSQL Instance
resource "google_sql_database_instance" "xtrinode" {
  count            = var.postgres_enabled ? 1 : 0
  name             = "${var.cluster_name}-postgres"
  database_version = var.postgres_version
  region           = var.gcp_region

  settings {
    tier              = var.postgres_tier
    availability_type = "ZONAL"  # Single zone - minimal setup
    disk_type         = "PD_HDD" # HDD disk (cheapest - min 10GB)
    disk_size         = max(var.postgres_disk_size_gb, 10)
    disk_autoresize   = false # No autoscaling - omit disk_autoresize_limit when false

    backup_configuration {
      enabled                        = true
      start_time                     = "03:00"
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = 7
        retention_unit   = "COUNT"
      }
    }

    ip_configuration {
      ipv4_enabled                                  = false
      private_network                               = google_compute_network.xtrinode.self_link
      enable_private_path_for_google_cloud_services = true
      ssl_mode                                      = "ENCRYPTED_ONLY"
    }

    database_flags {
      name  = "max_connections"
      value = "100"
    }

    insights_config {
      query_insights_enabled  = true
      query_string_length     = 1024
      record_application_tags = true
      record_client_address   = true
    }

    user_labels = {
      name        = "${var.cluster_name}-postgres"
      environment = var.environment
      project     = "xtrinode"
      managed-by  = "terraform"
    }

    deletion_protection_enabled = false # For testing - set to true for production
  }

  deletion_protection = false # For testing

  depends_on = [
    google_service_networking_connection.private_vpc_connection
  ]
}

# Cloud SQL Database
resource "google_sql_database" "xtrinode" {
  count     = var.postgres_enabled ? 1 : 0
  name      = var.postgres_database_name
  instance  = google_sql_database_instance.xtrinode[0].name
  charset   = "UTF8"
  collation = "en_US.UTF8"
}

resource "google_sql_database" "hive_metastore" {
  count     = var.postgres_enabled ? 1 : 0
  name      = var.hive_metastore_postgres_database_name
  instance  = google_sql_database_instance.xtrinode[0].name
  charset   = "UTF8"
  collation = "en_US.UTF8"
}

# Cloud SQL User
resource "google_sql_user" "xtrinode" {
  count    = var.postgres_enabled ? 1 : 0
  name     = var.postgres_admin_user
  instance = google_sql_database_instance.xtrinode[0].name
  password = var.postgres_admin_password
}

# Kubernetes Secret with PostgreSQL connection details
resource "kubernetes_secret" "postgres_connection" {
  count = var.postgres_enabled ? 1 : 0

  metadata {
    name      = "trino-catalog-postgres-analytics-secret"
    namespace = kubernetes_namespace.xtrinode_system.metadata[0].name
  }

  data = {
    POSTGRES_HOST     = google_sql_database_instance.xtrinode[0].private_ip_address
    POSTGRES_PORT     = "5432"
    POSTGRES_DATABASE = google_sql_database.xtrinode[0].name
    POSTGRES_USER     = google_sql_user.xtrinode[0].name
    POSTGRES_PASSWORD = google_sql_user.xtrinode[0].password
    # JDBC URL for Trino catalog properties
    JDBC_URL = "jdbc:postgresql://${google_sql_database_instance.xtrinode[0].private_ip_address}:5432/${google_sql_database.xtrinode[0].name}"
  }

  type = "Opaque"

  depends_on = [
    google_sql_database.xtrinode,
    google_sql_user.xtrinode,
    kubernetes_namespace.xtrinode_system
  ]
}

# Copy of PostgreSQL secret in team-test for XTrinodeCatalog (same namespace as XTrinode)
resource "kubernetes_secret" "postgres_connection_team_test" {
  count = var.postgres_enabled ? 1 : 0

  metadata {
    name      = "trino-catalog-postgres-analytics-secret"
    namespace = kubernetes_namespace.test_team.metadata[0].name
  }

  data = {
    POSTGRES_HOST     = google_sql_database_instance.xtrinode[0].private_ip_address
    POSTGRES_PORT     = "5432"
    POSTGRES_DATABASE = google_sql_database.xtrinode[0].name
    POSTGRES_USER     = google_sql_user.xtrinode[0].name
    POSTGRES_PASSWORD = google_sql_user.xtrinode[0].password
    JDBC_URL          = "jdbc:postgresql://${google_sql_database_instance.xtrinode[0].private_ip_address}:5432/${google_sql_database.xtrinode[0].name}"
  }

  type = "Opaque"

  depends_on = [
    google_sql_database.xtrinode,
    google_sql_user.xtrinode,
    kubernetes_namespace.test_team
  ]
}

resource "kubernetes_secret" "hive_metastore_postgres" {
  count = var.postgres_enabled ? 1 : 0

  metadata {
    name      = var.hive_metastore_postgres_secret_name
    namespace = kubernetes_namespace.iceberg[0].metadata[0].name
  }

  data = {
    HMS_POSTGRES_HOST     = google_sql_database_instance.xtrinode[0].private_ip_address
    HMS_POSTGRES_PORT     = "5432"
    HMS_POSTGRES_DATABASE = google_sql_database.hive_metastore[0].name
    HMS_POSTGRES_USER     = google_sql_user.xtrinode[0].name
    HMS_POSTGRES_PASSWORD = google_sql_user.xtrinode[0].password
    HMS_JDBC_URL          = "jdbc:postgresql://${google_sql_database_instance.xtrinode[0].private_ip_address}:5432/${google_sql_database.hive_metastore[0].name}"
  }

  type = "Opaque"

  depends_on = [
    google_sql_database.hive_metastore,
    google_sql_user.xtrinode,
    kubernetes_namespace.iceberg
  ]
}

# XTrinodeCatalog for PostgreSQL - uses secret above, same namespace as XTrinode CR
resource "kubernetes_manifest" "xtrinode_catalog_postgres" {
  count = var.postgres_enabled && var.postgres_catalog_cr_enabled ? 1 : 0

  field_manager {
    name            = "terraform"
    force_conflicts = true
  }

  manifest = {
    apiVersion = "analytics.xtrinode.io/v1"
    kind       = "XTrinodeCatalog"
    metadata = {
      name      = "postgres-analytics"
      namespace = kubernetes_namespace.test_team.metadata[0].name
      labels = {
        team         = "team-test"
        catalog-type = "postgres"
      }
    }
    spec = {
      labels = {
        team         = "team-test"
        catalog-type = "postgres"
      }
      connector = {
        postgres = {
          connectionURL  = "jdbc:postgresql://${google_sql_database_instance.xtrinode[0].private_ip_address}:5432/${google_sql_database.xtrinode[0].name}"
          connectionUser = google_sql_user.xtrinode[0].name
          connectionPasswordSecret = {
            name = "trino-catalog-postgres-analytics-secret"
            key  = "POSTGRES_PASSWORD"
          }
        }
      }
    }
  }

  depends_on = [kubernetes_secret.postgres_connection_team_test[0]]
}
