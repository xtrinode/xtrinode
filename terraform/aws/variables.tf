variable "aws_region" {
  description = "AWS region for resources"
  type        = string
  default     = "us-east-1"
}

variable "aws_profile" {
  description = "AWS CLI profile name for credentials"
  type        = string
  default     = "default"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "testing"
}

variable "cluster_name" {
  description = "EKS cluster name"
  type        = string
  default     = "xtrinode-eks-test"
}

variable "vpc_cidr" {
  description = "CIDR block for VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "kubernetes_version" {
  description = "Kubernetes version for EKS (1.35 = latest, 1.34, 1.33, 1.32)"
  type        = string
  default     = "1.35"
}

variable "node_instance_types" {
  description = "EC2 instance types for worker nodes (spot). Diversify for better capacity - use 4+ types from different families."
  type        = list(string)
  default     = ["t3.small", "t3.medium", "t3a.small", "t3a.medium", "m6i.large", "m5.large"]
}

variable "node_desired_size" {
  description = "Desired number of worker nodes for the baseline EKS node group"
  type        = number
  default     = 1
}

variable "node_min_size" {
  description = "Minimum number of worker nodes for the baseline EKS node group"
  type        = number
  default     = 1
}

variable "node_max_size" {
  description = "Maximum number of worker nodes"
  type        = number
  default     = 10
}

variable "eks_public_access" {
  description = "Enable public access to the EKS API endpoint (set true for dev/testing from local machine)"
  type        = bool
  default     = false
}

variable "eks_public_access_cidrs" {
  description = "CIDR blocks allowed to access the EKS public endpoint (only applies when eks_public_access is true)"
  type        = list(string)
  default     = ["0.0.0.0/0"] # Restrict to your IP in production
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
  description = "Enable RDS PostgreSQL for catalog smoke tests"
  type        = bool
  default     = false
}

variable "postgres_version" {
  description = "PostgreSQL version (18 = latest, 17, 16 also supported)"
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

variable "postgres_instance_class" {
  description = "RDS instance class (e.g., db.t3.micro, db.t3.small)"
  type        = string
  default     = "db.t3.micro" # Small instance for testing
}

variable "postgres_allocated_storage" {
  description = "PostgreSQL allocated storage in GB (gp3 minimum is 20)"
  type        = number
  default     = 20
}

variable "postgres_max_allocated_storage" {
  description = "PostgreSQL maximum allocated storage in GB (autoscaling)"
  type        = number
  default     = 20
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
  default     = "gp3" # AWS EBS gp3 storage class
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

# Cluster Autoscaler Variables
variable "cluster_autoscaler_enabled" {
  description = "Enable Cluster Autoscaler deployment"
  type        = bool
  default     = true
}

variable "cluster_autoscaler_version" {
  description = "Cluster Autoscaler Helm chart version"
  type        = string
  default     = "9.54.1"
}
