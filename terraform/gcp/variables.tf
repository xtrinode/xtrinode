variable "gcp_project_id" {
  description = "GCP project ID"
  type        = string
}

variable "gcp_region" {
  description = "GCP region for resources"
  type        = string
  default     = "us-central1"
}

variable "gcp_zone" {
  description = "GCP zone for GKE (use single zone to reduce SSD quota - regional = 300GB, zonal = 100GB)"
  type        = string
  default     = "us-central1-a"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "testing"
}

variable "cluster_name" {
  description = "GKE cluster name"
  type        = string
  default     = "xtrinode-gke-test"
}

variable "subnet_cidr" {
  description = "CIDR block for subnet"
  type        = string
  default     = "10.0.1.0/24"
}

variable "master_authorized_cidrs" {
  description = "Additional CIDRs allowed to access GKE control plane (e.g. your home IP for kubectl from laptop). Use /32 for single IP."
  type        = list(object({ cidr_block = string, display_name = string }))
  default     = []

  validation {
    condition = alltrue([
      for cidr in var.master_authorized_cidrs :
      cidr.cidr_block != "0.0.0.0/0" && cidr.cidr_block != "::/0"
    ])
    error_message = "Do not expose the GKE control plane to the internet. Use specific admin, VPN, or bastion CIDRs."
  }
}

variable "node_machine_type" {
  description = "GCE machine type for worker nodes"
  type        = string
  default     = "e2-standard-2"
}

variable "node_desired_size" {
  description = "Desired number of worker nodes (0 for scale-to-zero)"
  type        = number
  default     = 3
}

variable "node_min_size" {
  description = "Minimum number of worker nodes"
  type        = number
  default     = 1
}

variable "node_max_size" {
  description = "Maximum number of worker nodes"
  type        = number
  default     = 10
}

variable "node_disk_size_gb" {
  description = "Boot disk size per node in GB (min 20 for GKE, default 100 uses more SSD quota)"
  type        = number
  default     = 30
}

variable "node_preemptible" {
  description = "Use preemptible/spot nodes. Keep false for production control-plane and baseline runtime capacity."
  type        = bool
  default     = false
}

variable "node_service_account_id" {
  description = "Dedicated GCP service account ID for GKE nodes."
  type        = string
  default     = "xtrinode-gke-nodes"
}

variable "node_oauth_scopes" {
  description = "OAuth scopes for GKE nodes. Workload Identity should carry workload permissions."
  type        = list(string)
  default = [
    "https://www.googleapis.com/auth/devstorage.read_only",
    "https://www.googleapis.com/auth/logging.write",
    "https://www.googleapis.com/auth/monitoring",
  ]
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
  description = "Enable Cloud SQL PostgreSQL (set false for dev to avoid teardown blockers)"
  type        = bool
  default     = false
}

variable "postgres_catalog_cr_enabled" {
  description = "Create the example XTrinodeCatalog CR from Terraform. Enable only after XTrinode CRDs are installed."
  type        = bool
  default     = false
}

variable "postgres_version" {
  description = "PostgreSQL version (POSTGRES_18 = latest, POSTGRES_17, POSTGRES_16)"
  type        = string
  default     = "POSTGRES_18"
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

variable "hive_metastore_postgres_database_name" {
  description = "Cloud SQL PostgreSQL database name used by the Hive Metastore"
  type        = string
  default     = "hive_metastore"
}

variable "hive_metastore_postgres_secret_name" {
  description = "Kubernetes Secret name containing Hive Metastore PostgreSQL connection details"
  type        = string
  default     = "hive-metastore-postgres-secret"
}

variable "postgres_tier" {
  description = "Cloud SQL instance tier (e.g., db-f1-micro, db-g1-small)"
  type        = string
  default     = "db-f1-micro" # Small instance for testing
}

variable "postgres_disk_size_gb" {
  description = "PostgreSQL disk size in GB"
  type        = number
  default     = 5 # 5 GB max - minimal setup
}

# Prometheus Operator Variables
variable "prometheus_enabled" {
  description = "Enable Prometheus Operator deployment (optional; required for ServiceMonitor scraping and Prometheus-based KEDA scaling)"
  type        = bool
  default     = false
}

variable "prometheus_storage_class" {
  description = "Storage class for Prometheus PVC"
  type        = string
  default     = "standard" # GKE pd-standard storage class
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

# Iceberg GCS Warehouse Variables
variable "iceberg_gcs_enabled" {
  description = "Enable GCS bucket and Workload Identity for Iceberg smoke tests"
  type        = bool
  default     = false
}

variable "iceberg_gcs_bucket_name" {
  description = "GCS bucket name for Iceberg warehouse data. Defaults to <project>-<cluster>-iceberg."
  type        = string
  default     = null
  nullable    = true
}

variable "iceberg_gcs_bucket_location" {
  description = "GCS bucket location for Iceberg warehouse data"
  type        = string
  default     = null
  nullable    = true
}

variable "iceberg_gcs_force_destroy" {
  description = "Allow Terraform to delete the Iceberg test bucket even when it contains objects"
  type        = bool
  default     = true
}

variable "iceberg_gcp_service_account_id" {
  description = "Google service account ID used by Trino pods for Iceberg GCS access"
  type        = string
  default     = "trino-iceberg-gcs"
}

variable "iceberg_kubernetes_namespace" {
  description = "Kubernetes namespace containing the Iceberg test XTrinode"
  type        = string
  default     = "team-iceberg"
}

variable "iceberg_kubernetes_service_account" {
  description = "Kubernetes service account used by the Iceberg test XTrinode"
  type        = string
  default     = "trino-iceberg-gcs"
}

variable "iceberg_hive_metastore_kubernetes_service_account" {
  description = "Kubernetes service account used by the Iceberg smoke Hive Metastore"
  type        = string
  default     = "hive-metastore-gcs"
}

# Cluster API Provider GCP (CAPG) Variables
variable "capg_enabled" {
  description = "Install cert-manager, Cluster API Operator, CAPI providers, and CAPG into the Terraform-created GKE cluster"
  type        = bool
  default     = false
}

variable "capg_install_cert_manager" {
  description = "Install cert-manager for Cluster API Operator webhooks. Set false if cert-manager is already installed."
  type        = bool
  default     = true
}

variable "capg_cert_manager_version" {
  description = "cert-manager Helm chart version used when capg_install_cert_manager is true"
  type        = string
  default     = "v1.20.2"
}

variable "capg_operator_chart_version" {
  description = "Cluster API Operator Helm chart version"
  type        = string
  default     = "0.24.0"
}

variable "capg_cluster_api_version" {
  description = "Cluster API core/kubeadm provider version installed by Cluster API Operator"
  type        = string
  default     = "v1.11.3"
}

variable "capg_provider_version" {
  description = "Cluster API Provider GCP version installed by Cluster API Operator"
  type        = string
  default     = "v1.11.1"
}

variable "capg_operator_namespace" {
  description = "Namespace for Cluster API Operator"
  type        = string
  default     = "capi-operator-system"
}

variable "capg_core_namespace" {
  description = "Namespace for the Cluster API core provider"
  type        = string
  default     = "capi-system"
}

variable "capg_bootstrap_namespace" {
  description = "Namespace for the kubeadm bootstrap provider"
  type        = string
  default     = "capi-kubeadm-bootstrap-system"
}

variable "capg_control_plane_namespace" {
  description = "Namespace for the kubeadm control plane provider"
  type        = string
  default     = "capi-kubeadm-control-plane-system"
}

variable "capg_namespace" {
  description = "Namespace for Cluster API Provider GCP"
  type        = string
  default     = "capg-system"
}

variable "capg_enable_gke" {
  description = "Enable the CAPG GKE feature gate for GCPManagedCluster/GCPManagedMachinePool support"
  type        = bool
  default     = true
}

variable "capg_enable_machine_pool" {
  description = "Enable the CAPI MachinePool feature gate explicitly for node pool reconciliation"
  type        = bool
  default     = true
}

variable "capg_manage_gcp_credentials" {
  description = "Create a CAPG Google service account, key, IAM bindings, and Kubernetes credentials Secret. The key is stored in Terraform state."
  type        = bool
  default     = true
}

variable "capg_gcp_service_account_id" {
  description = "Google service account ID created for CAPG when capg_manage_gcp_credentials is true"
  type        = string
  default     = "xtrinode-capg"
}

variable "capg_gcp_service_account_roles" {
  description = "Project IAM roles granted to the Terraform-managed CAPG service account"
  type        = list(string)
  default = [
    "roles/editor",
    "roles/iam.serviceAccountTokenCreator",
  ]
}

variable "capg_gcp_credentials_b64" {
  description = "Base64-encoded GCP service account JSON credentials for CAPG when capg_manage_gcp_credentials is false"
  type        = string
  default     = null
  nullable    = true
  sensitive   = true
}

variable "capg_gcp_credentials_secret_name" {
  description = "Kubernetes Secret name used by Cluster API Operator for CAPG credentials"
  type        = string
  default     = "gcp-credentials"
}
