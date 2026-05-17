variable "azure_subscription_id" {
  description = "Azure subscription ID"
  type        = string
}

variable "azure_region" {
  description = "Azure region for resources"
  type        = string
  default     = "eastus"
}

variable "resource_group_name" {
  description = "Azure resource group name"
  type        = string
  default     = "xtrinode-rg"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "testing"
}

variable "cluster_name" {
  description = "AKS cluster name"
  type        = string
  default     = "xtrinode-aks-test"
}

variable "vnet_cidr" {
  description = "CIDR block for vnet"
  type        = string
  default     = "10.0.0.0/16"
}

variable "subnet_cidr" {
  description = "CIDR block for AKS subnet"
  type        = string
  default     = "10.0.1.0/24"
}

variable "postgres_subnet_cidr" {
  description = "CIDR block for PostgreSQL delegated subnet (must not overlap with AKS subnet)"
  type        = string
  default     = "10.0.2.0/24"
}

variable "kubernetes_version" {
  description = "Kubernetes version for AKS (1.34 = latest, 1.33, 1.32)"
  type        = string
  default     = "1.34"
}

variable "node_vm_size" {
  description = "Azure VM size for worker nodes (minimal)"
  type        = string
  default     = "Standard_B2s" # Minimal - 2 vCPU, 4GB RAM
}

variable "node_desired_size" {
  description = "Desired number of worker nodes (0 for scale-to-zero)"
  type        = number
  default     = 0
}

variable "node_min_size" {
  description = "Minimum number of worker nodes (0 for scale-to-zero)"
  type        = number
  default     = 0
}

variable "node_max_size" {
  description = "Maximum number of worker nodes"
  type        = number
  default     = 10
}

variable "service_cidr" {
  description = "CIDR block for Kubernetes service"
  type        = string
  default     = "10.1.0.0/16"
}

variable "dns_service_ip" {
  description = "IP address for Kubernetes DNS service"
  type        = string
  default     = "10.1.0.10"
}

variable "helm_repository" {
  description = "Helm repository URL for XTrinode operator (leave empty to skip Helm deployment)"
  type        = string
  default     = ""
}

variable "xtrinode_operator_version" {
  description = "Version of XTrinode operator"
  type        = string
  default     = "0.1.0"
}

# PostgreSQL Database Variables
variable "postgres_enabled" {
  description = "Enable Azure Database for PostgreSQL Flexible Server for catalog smoke tests"
  type        = bool
  default     = false
}

variable "postgres_version" {
  description = "PostgreSQL version (18 = latest, 17, 16)"
  type        = string
  default     = "18"
}

variable "postgres_admin_user" {
  description = "PostgreSQL administrator username"
  type        = string
  default     = "xtrinode_admin"
  sensitive   = false
}

variable "postgres_admin_password" {
  description = "PostgreSQL administrator password"
  type        = string
  sensitive   = true
  default     = null
  nullable    = true
  # Required only when postgres_enabled = true. Provide via TF_VAR_postgres_admin_password or terraform.tfvars.
}

variable "postgres_database_name" {
  description = "PostgreSQL database name"
  type        = string
  default     = "xtrinode_analytics"
}

variable "postgres_storage_mb" {
  description = "PostgreSQL storage size in MB"
  type        = number
  default     = 5120 # 5 GB max - minimal setup
}

variable "postgres_sku_name" {
  description = "PostgreSQL SKU name (e.g., B_Standard_B1ms, GP_Standard_D2s_v3)"
  type        = string
  default     = "B_Standard_B1ms" # Burstable, 1 vCore, 2GB RAM
}

# Prometheus Operator Variables
variable "prometheus_enabled" {
  description = "Enable Prometheus Operator deployment for ServiceMonitor scraping and Prometheus-backed KEDA scaling"
  type        = bool
  default     = false
}

variable "prometheus_storage_class" {
  description = "Storage class for Prometheus PVC"
  type        = string
  default     = "managed-premium" # Azure Premium SSD
}

variable "prometheus_storage_size" {
  description = "Storage size for Prometheus PVC"
  type        = string
  default     = "50Gi"
}

variable "grafana_enabled" {
  description = "Enable Grafana deployment"
  type        = bool
  default     = false # Disable by default, enable if needed
}

variable "vector_enabled" {
  description = "Enable Vector log collection through the XTrinode observability Helm chart"
  type        = bool
  default     = false
}

variable "vector_log_level" {
  description = "Vector log filtering level"
  type        = string
  default     = "info"
}
