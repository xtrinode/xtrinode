# GCS bucket and Workload Identity plumbing for Iceberg smoke tests.

locals {
  iceberg_gcs_bucket_name     = coalesce(var.iceberg_gcs_bucket_name, "${var.gcp_project_id}-${var.cluster_name}-iceberg")
  iceberg_gcs_bucket_location = coalesce(var.iceberg_gcs_bucket_location, var.gcp_region)
}

resource "google_storage_bucket" "iceberg" {
  count = var.iceberg_gcs_enabled ? 1 : 0

  name                        = local.iceberg_gcs_bucket_name
  location                    = local.iceberg_gcs_bucket_location
  force_destroy               = var.iceberg_gcs_force_destroy
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  labels = {
    environment = var.environment
    project     = "xtrinode"
    purpose     = "iceberg-smoke-test"
    managed-by  = "terraform"
  }
}

resource "google_service_account" "iceberg_gcs" {
  count = var.iceberg_gcs_enabled ? 1 : 0

  account_id   = var.iceberg_gcp_service_account_id
  display_name = "XTrinode Iceberg GCS"
  description  = "Workload Identity service account for Trino Iceberg GCS smoke tests"
}

resource "kubernetes_namespace" "iceberg" {
  count = var.iceberg_gcs_enabled || var.postgres_enabled ? 1 : 0

  metadata {
    name = var.iceberg_kubernetes_namespace
    labels = {
      "app.kubernetes.io/name"                         = "xtrinode-iceberg"
      "xtrinode.analytics.xtrinode.io/guardrail-scope" = "namespace"
      "xtrinode.analytics.xtrinode.io/managed"         = "true"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "google_storage_bucket_iam_member" "iceberg_object_user" {
  count = var.iceberg_gcs_enabled ? 1 : 0

  bucket = google_storage_bucket.iceberg[0].name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.iceberg_gcs[0].email}"
}

resource "google_storage_bucket_iam_member" "iceberg_bucket_reader" {
  count = var.iceberg_gcs_enabled ? 1 : 0

  bucket = google_storage_bucket.iceberg[0].name
  role   = "roles/storage.legacyBucketReader"
  member = "serviceAccount:${google_service_account.iceberg_gcs[0].email}"
}

resource "google_service_account_iam_member" "iceberg_workload_identity" {
  count = var.iceberg_gcs_enabled ? 1 : 0

  service_account_id = google_service_account.iceberg_gcs[0].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.gcp_project_id}.svc.id.goog[${var.iceberg_kubernetes_namespace}/${var.iceberg_kubernetes_service_account}]"
}

resource "google_service_account_iam_member" "iceberg_hive_metastore_workload_identity" {
  count = var.iceberg_gcs_enabled ? 1 : 0

  service_account_id = google_service_account.iceberg_gcs[0].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.gcp_project_id}.svc.id.goog[${var.iceberg_kubernetes_namespace}/${var.iceberg_hive_metastore_kubernetes_service_account}]"
}
