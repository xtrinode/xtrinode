terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.11"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
}

provider "kubernetes" {
  host                   = "https://${google_container_cluster.xtrinode.endpoint}"
  token                  = data.google_client_config.default.access_token
  cluster_ca_certificate = base64decode(google_container_cluster.xtrinode.master_auth[0].cluster_ca_certificate)
}

provider "helm" {
  kubernetes {
    host                   = "https://${google_container_cluster.xtrinode.endpoint}"
    token                  = data.google_client_config.default.access_token
    cluster_ca_certificate = base64decode(google_container_cluster.xtrinode.master_auth[0].cluster_ca_certificate)
  }
}

# Data source for GCP client config
data "google_client_config" "default" {}

# VPC Network
resource "google_compute_network" "xtrinode" {
  name                    = "xtrinode-network"
  auto_create_subnetworks = false
  routing_mode            = "REGIONAL"
}

# Subnet
resource "google_compute_subnetwork" "xtrinode" {
  name          = "xtrinode-subnet"
  ip_cidr_range = var.subnet_cidr
  region        = var.gcp_region
  network       = google_compute_network.xtrinode.id

  private_ip_google_access = true
}

# Firewall rule for internal communication
resource "google_compute_firewall" "xtrinode_internal" {
  name    = "xtrinode-internal"
  network = google_compute_network.xtrinode.name

  allow {
    protocol = "tcp"
    ports    = ["0-65535"]
  }

  allow {
    protocol = "udp"
    ports    = ["0-65535"]
  }

  source_ranges = [var.subnet_cidr]
}

# Cloud NAT - allows private GKE nodes to pull images from ghcr.io (KEDA, etc.)
resource "google_compute_router" "xtrinode" {
  name    = "xtrinode-router"
  region  = var.gcp_region
  network = google_compute_network.xtrinode.id
}

resource "google_compute_router_nat" "xtrinode" {
  name                               = "xtrinode-nat"
  router                             = google_compute_router.xtrinode.name
  region                             = var.gcp_region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
}

# Private Service Access for Cloud SQL (only when postgres_enabled)
resource "google_compute_global_address" "private_ip_range" {
  count         = var.postgres_enabled ? 1 : 0
  name          = "xtrinode-private-ip-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.xtrinode.id
}

resource "google_service_networking_connection" "private_vpc_connection" {
  count                   = var.postgres_enabled ? 1 : 0
  network                 = google_compute_network.xtrinode.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_range[0].name]
}

# GKE Cluster (zonal = single zone, ~100GB SSD vs regional ~300GB - fits free tier quota)
resource "google_container_cluster" "xtrinode" {
  name     = var.cluster_name
  location = var.gcp_zone

  deletion_protection = false

  remove_default_node_pool = true
  initial_node_count       = 1

  network    = google_compute_network.xtrinode.name
  subnetwork = google_compute_subnetwork.xtrinode.name

  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }

  master_authorized_networks_config {
    cidr_blocks {
      cidr_block   = var.subnet_cidr
      display_name = "VPC subnet"
    }
    dynamic "cidr_blocks" {
      for_each = var.master_authorized_cidrs
      content {
        cidr_block   = cidr_blocks.value.cidr_block
        display_name = cidr_blocks.value.display_name
      }
    }
  }

  cluster_autoscaling {
    enabled = true
    resource_limits {
      resource_type = "cpu"
      minimum       = 1
      maximum       = 100
    }
    resource_limits {
      resource_type = "memory"
      minimum       = 1
      maximum       = 1000
    }
    auto_provisioning_defaults {
      service_account = google_service_account.gke_nodes.email
      oauth_scopes    = var.node_oauth_scopes
      disk_size       = var.node_disk_size_gb
      disk_type       = "pd-standard"
    }
  }

  workload_identity_config {
    workload_pool = "${var.gcp_project_id}.svc.id.goog"
  }

  logging_service    = "logging.googleapis.com/kubernetes"
  monitoring_service = "monitoring.googleapis.com/kubernetes"

  network_policy {
    enabled = true
  }

  addons_config {
    network_policy_config {
      disabled = false
    }
  }
}

resource "google_container_node_pool" "xtrinode" {
  name       = "xtrinode-node-pool"
  location   = var.gcp_zone
  cluster    = google_container_cluster.xtrinode.name
  node_count = var.node_desired_size

  autoscaling {
    min_node_count = var.node_min_size
    max_node_count = var.node_max_size
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }

  node_config {
    preemptible     = var.node_preemptible
    machine_type    = var.node_machine_type
    disk_size_gb    = var.node_disk_size_gb
    disk_type       = "pd-standard"
    service_account = google_service_account.gke_nodes.email
    oauth_scopes    = var.node_oauth_scopes

    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    metadata = {
      disable-legacy-endpoints = "true"
    }

    labels = {
      environment = var.environment
      project     = "xtrinode"
      preemptible = tostring(var.node_preemptible)
    }

    tags = ["xtrinode", "gke"]
  }

  lifecycle {
    ignore_changes = [node_count]
  }
}

resource "google_service_account" "gke_nodes" {
  account_id   = var.node_service_account_id
  display_name = "XTrinode GKE node service account"
}

resource "google_project_iam_member" "gke_nodes_log_writer" {
  project = var.gcp_project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.gke_nodes.email}"
}

resource "google_project_iam_member" "gke_nodes_metric_writer" {
  project = var.gcp_project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.gke_nodes.email}"
}

resource "google_project_iam_member" "gke_nodes_resource_metadata_writer" {
  project = var.gcp_project_id
  role    = "roles/stackdriver.resourceMetadata.writer"
  member  = "serviceAccount:${google_service_account.gke_nodes.email}"
}

# Artifact Registry Repository for XTrinode Operator
resource "google_artifact_registry_repository" "xtrinode_operator" {
  location      = var.gcp_region
  repository_id = "xtrinode-operator"
  description   = "Container registry for XTrinode operator"
  format        = "DOCKER"
}

# Artifact Registry Repository for XTrinode Gateway
resource "google_artifact_registry_repository" "xtrinode_gateway" {
  location      = var.gcp_region
  repository_id = "xtrinode-gateway"
  description   = "Container registry for XTrinode gateway"
  format        = "DOCKER"
}

# Artifact Registry Repository for XTrinode API Server
resource "google_artifact_registry_repository" "xtrinode_api_server" {
  location      = var.gcp_region
  repository_id = "xtrinode-api-server"
  description   = "Container registry for XTrinode API server"
  format        = "DOCKER"
}

# IAM binding: GKE service account can pull from Artifact Registry
resource "google_artifact_registry_repository_iam_member" "gke_operator_puller" {
  location   = var.gcp_region
  repository = google_artifact_registry_repository.xtrinode_operator.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.gke_nodes.email}"
}

resource "google_artifact_registry_repository_iam_member" "gke_gateway_puller" {
  location   = var.gcp_region
  repository = google_artifact_registry_repository.xtrinode_gateway.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.gke_nodes.email}"
}

resource "google_artifact_registry_repository_iam_member" "gke_api_server_puller" {
  location   = var.gcp_region
  repository = google_artifact_registry_repository.xtrinode_api_server.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.gke_nodes.email}"
}

resource "kubernetes_namespace" "xtrinode_system" {
  metadata {
    name = "xtrinode-system"
    labels = {
      "app.kubernetes.io/name" = "xtrinode-system"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "helm_release" "xtrinode_operator" {
  count = var.helm_repository != "" ? 1 : 0

  name             = "xtrinode-operator"
  repository       = var.helm_repository
  chart            = "xtrinode-operator"
  namespace        = kubernetes_namespace.xtrinode_system.metadata[0].name
  version          = var.xtrinode_operator_version
  create_namespace = false

  set {
    name  = "image.repository"
    value = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${google_artifact_registry_repository.xtrinode_operator.repository_id}/xtrinode-operator"
  }

  set {
    name  = "image.tag"
    value = var.xtrinode_operator_version
  }

  set {
    name  = "replicaCount"
    value = 1
  }

  set {
    name  = "webhook.enabled"
    value = true
  }

  set {
    name  = "operator.prometheus.address"
    value = local.prometheus_service_url
  }

  depends_on = [
    kubernetes_namespace.xtrinode_system,
    helm_release.xtrinode_observability
  ]
}

resource "kubernetes_namespace" "test_team" {
  metadata {
    name = "team-test"
    labels = {
      "app.kubernetes.io/name"                         = "xtrinode-test"
      "xtrinode.analytics.xtrinode.io/guardrail-scope" = "namespace"
      "xtrinode.analytics.xtrinode.io/managed"         = "true"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}
