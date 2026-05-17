output "gke_cluster_name" {
  description = "GKE cluster name"
  value       = google_container_cluster.xtrinode.name
}

output "cluster_name" {
  description = "Alias for the GKE cluster name"
  value       = google_container_cluster.xtrinode.name
}

output "gcp_project_id" {
  description = "GCP project ID"
  value       = var.gcp_project_id
}

output "gcp_region" {
  description = "GCP region"
  value       = var.gcp_region
}

output "gke_cluster_endpoint" {
  description = "GKE cluster endpoint"
  value       = google_container_cluster.xtrinode.endpoint
}

output "gke_cluster_location" {
  description = "GKE cluster location"
  value       = google_container_cluster.xtrinode.location
}

output "gke_cluster_ca_certificate" {
  description = "GKE cluster CA certificate"
  value       = google_container_cluster.xtrinode.master_auth[0].cluster_ca_certificate
  sensitive   = true
}

output "configure_kubectl" {
  description = "Command to configure kubectl"
  value       = "gcloud container clusters get-credentials ${google_container_cluster.xtrinode.name} --zone ${var.gcp_zone} --project ${var.gcp_project_id}"
}

output "network_name" {
  description = "VPC network name"
  value       = google_compute_network.xtrinode.name
}

output "subnet_name" {
  description = "Subnet name"
  value       = google_compute_subnetwork.xtrinode.name
}

output "node_pool_name" {
  description = "GKE node pool name"
  value       = google_container_node_pool.xtrinode.name
}

output "xtrinode_system_namespace" {
  description = "Kubernetes namespace for the XTrinode control plane"
  value       = kubernetes_namespace.xtrinode_system.metadata[0].name
}

output "test_namespace" {
  description = "Kubernetes namespace for testing"
  value       = kubernetes_namespace.test_team.metadata[0].name
}

output "postgres_private_ip" {
  description = "PostgreSQL Cloud SQL private IP address"
  value       = var.postgres_enabled ? google_sql_database_instance.xtrinode[0].private_ip_address : null
}

output "postgres_connection_name" {
  description = "PostgreSQL Cloud SQL connection name"
  value       = var.postgres_enabled ? google_sql_database_instance.xtrinode[0].connection_name : null
}

output "postgres_database_name" {
  description = "PostgreSQL database name"
  value       = var.postgres_enabled ? google_sql_database.xtrinode[0].name : null
}

output "hive_metastore_postgres_database_name" {
  description = "Cloud SQL PostgreSQL database name used by the Hive Metastore"
  value       = var.postgres_enabled ? google_sql_database.hive_metastore[0].name : null
}

output "hive_metastore_postgres_secret" {
  description = "Kubernetes Secret name with Hive Metastore PostgreSQL connection details"
  value       = var.postgres_enabled ? kubernetes_secret.hive_metastore_postgres[0].metadata[0].name : null
}

output "postgres_connection_secret" {
  description = "Kubernetes Secret name with PostgreSQL connection details"
  value       = var.postgres_enabled ? kubernetes_secret.postgres_connection[0].metadata[0].name : null
}

output "xtrinode_operator_release" {
  description = "Helm release name for XTrinode operator"
  value       = var.helm_repository != "" ? helm_release.xtrinode_operator[0].name : null
}

output "xtrinode_observability_release" {
  description = "Helm release name for the XTrinode observability stack"
  value       = local.observability_enabled ? helm_release.xtrinode_observability[0].name : null
}

output "prometheus_enabled" {
  description = "Whether Prometheus is enabled"
  value       = var.prometheus_enabled
}

output "prometheus_namespace" {
  description = "Kubernetes namespace for Prometheus"
  value       = var.prometheus_enabled ? local.prometheus_namespace : null
}

output "prometheus_service_url" {
  description = "Prometheus service URL"
  value       = var.prometheus_enabled ? local.prometheus_service_url : null
}

output "vector_enabled" {
  description = "Whether Vector log collection is enabled"
  value       = var.vector_enabled
}

output "vector_namespace" {
  description = "Kubernetes namespace for Vector"
  value       = var.vector_enabled ? "observability" : null
}

output "artifact_registry_operator_repository_url" {
  description = "Artifact Registry repository URL for XTrinode operator"
  value       = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.xtrinode_operator.repository_id}/xtrinode-operator"
}

output "artifact_registry_gateway_repository_url" {
  description = "Artifact Registry repository URL for XTrinode gateway"
  value       = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.xtrinode_gateway.repository_id}/xtrinode-gateway"
}

output "artifact_registry_api_server_repository_url" {
  description = "Artifact Registry repository URL for XTrinode API server"
  value       = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.xtrinode_api_server.repository_id}/xtrinode-api-server"
}

output "artifact_registry_location" {
  description = "Artifact Registry location"
  value       = var.gcp_region
}

output "docker_login_command" {
  description = "Command to configure Docker for Artifact Registry"
  value       = "gcloud auth configure-docker ${var.gcp_region}-docker.pkg.dev"
}

output "iceberg_gcs_bucket_name" {
  description = "GCS bucket name for Iceberg warehouse data"
  value       = var.iceberg_gcs_enabled ? google_storage_bucket.iceberg[0].name : null
}

output "iceberg_gcs_bucket_url" {
  description = "GCS bucket URL for Iceberg warehouse data"
  value       = var.iceberg_gcs_enabled ? "gs://${google_storage_bucket.iceberg[0].name}" : null
}

output "iceberg_gcp_service_account_email" {
  description = "Google service account email used by Trino pods for Iceberg GCS access"
  value       = var.iceberg_gcs_enabled ? google_service_account.iceberg_gcs[0].email : null
}

output "capg_enabled" {
  description = "Whether CAPG management-plane wiring is enabled"
  value       = var.capg_enabled
}

output "capg_operator_namespace" {
  description = "Namespace for Cluster API Operator"
  value       = var.capg_enabled ? kubernetes_namespace.capg_operator[0].metadata[0].name : null
}

output "capg_namespace" {
  description = "Namespace for Cluster API Provider GCP"
  value       = var.capg_enabled ? kubernetes_namespace.capg[0].metadata[0].name : null
}

output "capg_gcp_credentials_secret" {
  description = "Kubernetes Secret name used by CAPG"
  value       = var.capg_enabled ? kubernetes_secret.capg_gcp_credentials[0].metadata[0].name : null
}

output "capg_gcp_service_account_email" {
  description = "Terraform-managed CAPG Google service account email"
  value       = var.capg_enabled && var.capg_manage_gcp_credentials ? google_service_account.capg[0].email : null
}
