# Azure Database for PostgreSQL Flexible Server
# Provides managed PostgreSQL database for Trino catalogs

resource "azurerm_postgresql_flexible_server" "xtrinode" {
  count = var.postgres_enabled ? 1 : 0

  name                   = "${var.cluster_name}-postgres"
  resource_group_name    = azurerm_resource_group.xtrinode.name
  location               = azurerm_resource_group.xtrinode.location
  version                = var.postgres_version
  delegated_subnet_id    = azurerm_subnet.postgres.id
  private_dns_zone_id    = azurerm_private_dns_zone.postgres[0].id
  administrator_login    = var.postgres_admin_user
  administrator_password = var.postgres_admin_password
  zone                   = "1"

  # Explicitly disable public network access - fully private networking
  public_network_access_enabled = false

  storage_mb = var.postgres_storage_mb
  sku_name   = var.postgres_sku_name

  backup_retention_days        = 7
  geo_redundant_backup_enabled = false

  maintenance_window {
    day_of_week  = 0
    start_hour   = 2
    start_minute = 0
  }

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]

  tags = {
    Name        = "${var.cluster_name}-postgres"
    Environment = var.environment
    Project     = "xtrinode"
    ManagedBy   = "terraform"
  }
}

# Private DNS Zone for PostgreSQL
resource "azurerm_private_dns_zone" "postgres" {
  count = var.postgres_enabled ? 1 : 0

  name                = "${var.cluster_name}.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.xtrinode.name

  tags = {
    Name = "${var.cluster_name}-postgres-dns"
  }
}

# Link Private DNS Zone to VNet
resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  count = var.postgres_enabled ? 1 : 0

  name                  = "${var.cluster_name}-postgres-dns-link"
  resource_group_name   = azurerm_resource_group.xtrinode.name
  private_dns_zone_name = azurerm_private_dns_zone.postgres[0].name
  virtual_network_id    = azurerm_virtual_network.xtrinode.id

  tags = {
    Name = "${var.cluster_name}-postgres-dns-link"
  }
}

# PostgreSQL Database
resource "azurerm_postgresql_flexible_server_database" "xtrinode" {
  count = var.postgres_enabled ? 1 : 0

  name      = var.postgres_database_name
  server_id = azurerm_postgresql_flexible_server.xtrinode[0].id
  collation = "en_US.utf8"
  charset   = "utf8"
}

# Note: Firewall rules are NOT needed when using delegated_subnet_id with public_network_access_enabled = false
# The server is fully private and accessible only from within the VNet via private DNS
# Firewall rules are only applicable for public network access scenarios

# Kubernetes Secret with PostgreSQL connection details
resource "kubernetes_secret" "postgres_connection" {
  count = var.postgres_enabled ? 1 : 0

  metadata {
    name      = "trino-catalog-postgres-analytics-secret"
    namespace = kubernetes_namespace.xtrinode_system.metadata[0].name
  }

  data = {
    POSTGRES_HOST     = azurerm_postgresql_flexible_server.xtrinode[0].fqdn
    POSTGRES_PORT     = "5432"
    POSTGRES_DATABASE = azurerm_postgresql_flexible_server_database.xtrinode[0].name
    POSTGRES_USER     = var.postgres_admin_user
    POSTGRES_PASSWORD = var.postgres_admin_password
    # JDBC URL for Trino catalog properties
    JDBC_URL = "jdbc:postgresql://${azurerm_postgresql_flexible_server.xtrinode[0].fqdn}:5432/${azurerm_postgresql_flexible_server_database.xtrinode[0].name}"
  }

  type = "Opaque"

  depends_on = [
    azurerm_postgresql_flexible_server_database.xtrinode,
    kubernetes_namespace.xtrinode_system
  ]
}
