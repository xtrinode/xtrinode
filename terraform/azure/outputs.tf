output "aks_cluster_name" {
  description = "AKS cluster name"
  value       = azurerm_kubernetes_cluster.xtrinode.name
}

output "aks_cluster_id" {
  description = "AKS cluster ID"
  value       = azurerm_kubernetes_cluster.xtrinode.id
}

output "aks_fqdn" {
  description = "AKS cluster FQDN"
  value       = azurerm_kubernetes_cluster.xtrinode.fqdn
}

output "kube_config" {
  description = "Kubernetes config"
  value       = azurerm_kubernetes_cluster.xtrinode.kube_config_raw
  sensitive   = true
}

output "configure_kubectl" {
  description = "Command to configure kubectl"
  value       = "az aks get-credentials --resource-group ${azurerm_resource_group.xtrinode.name} --name ${azurerm_kubernetes_cluster.xtrinode.name}"
}

output "resource_group_name" {
  description = "Azure resource group name"
  value       = azurerm_resource_group.xtrinode.name
}

output "resource_group_id" {
  description = "Azure resource group ID"
  value       = azurerm_resource_group.xtrinode.id
}

output "vnet_id" {
  description = "Virtual network ID"
  value       = azurerm_virtual_network.xtrinode.id
}

output "subnet_id" {
  description = "Subnet ID"
  value       = azurerm_subnet.xtrinode.id
}

output "xtrinode_system_namespace" {
  description = "Kubernetes namespace for the XTrinode control plane"
  value       = kubernetes_namespace.xtrinode_system.metadata[0].name
}

output "test_namespace" {
  description = "Kubernetes namespace for testing"
  value       = kubernetes_namespace.test_team.metadata[0].name
}

output "xtrinode_operator_release" {
  description = "Helm release name for XTrinode operator"
  value       = var.helm_repository != "" ? helm_release.xtrinode_operator[0].name : null
}

output "postgres_fqdn" {
  description = "PostgreSQL server FQDN"
  value       = var.postgres_enabled ? azurerm_postgresql_flexible_server.xtrinode[0].fqdn : null
}

output "postgres_database_name" {
  description = "PostgreSQL database name"
  value       = var.postgres_enabled ? azurerm_postgresql_flexible_server_database.xtrinode[0].name : null
}

output "postgres_connection_secret" {
  description = "Kubernetes Secret name with PostgreSQL connection details"
  value       = var.postgres_enabled ? kubernetes_secret.postgres_connection[0].metadata[0].name : null
}

output "prometheus_enabled" {
  description = "Whether Prometheus is enabled"
  value       = var.prometheus_enabled
}

output "prometheus_namespace" {
  description = "Kubernetes namespace for Prometheus"
  value       = local.observability_enabled ? local.prometheus_namespace : null
}

output "prometheus_service_url" {
  description = "Prometheus service URL"
  value       = var.prometheus_enabled ? local.prometheus_service_url : null
}

output "vector_enabled" {
  description = "Whether Vector log collection is enabled"
  value       = var.vector_enabled
}

output "acr_login_server" {
  description = "ACR login server"
  value       = azurerm_container_registry.xtrinode.login_server
}

output "acr_operator_repository_url" {
  description = "ACR repository URL for XTrinode operator"
  value       = "${azurerm_container_registry.xtrinode.login_server}/xtrinode-operator"
}

output "acr_gateway_repository_url" {
  description = "ACR repository URL for XTrinode gateway"
  value       = "${azurerm_container_registry.xtrinode.login_server}/xtrinode-gateway"
}

output "acr_api_server_repository_url" {
  description = "ACR repository URL for XTrinode API server"
  value       = "${azurerm_container_registry.xtrinode.login_server}/xtrinode-api-server"
}

output "docker_login_command" {
  description = "Command to login to ACR"
  value       = "az acr login --name ${azurerm_container_registry.xtrinode.name}"
}
