# Optional Cluster API Provider GCP (CAPG) management-plane wiring.
#
# This installs cert-manager, Cluster API Operator, CAPI core/kubeadm providers,
# and CAPG into the Terraform-created GKE cluster. It is intentionally gated so
# regular XTrinode smoke-test applies do not install CAPI controllers.

locals {
  capg_provider_values = {
    core = {
      "cluster-api" = {
        namespace       = var.capg_core_namespace
        version         = var.capg_cluster_api_version
        createNamespace = false
      }
    }
    bootstrap = {
      kubeadm = {
        namespace       = var.capg_bootstrap_namespace
        version         = var.capg_cluster_api_version
        createNamespace = false
      }
    }
    controlPlane = {
      kubeadm = {
        namespace       = var.capg_control_plane_namespace
        version         = var.capg_cluster_api_version
        createNamespace = false
      }
    }
    infrastructure = {
      gcp = {
        namespace       = var.capg_namespace
        version         = var.capg_provider_version
        createNamespace = false
        configSecret = {
          name      = var.capg_gcp_credentials_secret_name
          namespace = var.capg_namespace
        }
      }
    }
    manager = {
      featureGates = merge(
        var.capg_enable_machine_pool ? {
          core = {
            MachinePool = true
          }
        } : {},
        var.capg_enable_gke ? {
          gcp = {
            GKE = true
          }
        } : {}
      )
    }
  }
}

resource "kubernetes_namespace" "capg_operator" {
  count = var.capg_enabled ? 1 : 0

  metadata {
    name = var.capg_operator_namespace
    labels = {
      "app.kubernetes.io/name"       = "cluster-api-operator"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "kubernetes_namespace" "capi_core" {
  count = var.capg_enabled ? 1 : 0

  metadata {
    name = var.capg_core_namespace
    labels = {
      "cluster.x-k8s.io/provider"    = "cluster-api"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "kubernetes_namespace" "capi_bootstrap" {
  count = var.capg_enabled ? 1 : 0

  metadata {
    name = var.capg_bootstrap_namespace
    labels = {
      "cluster.x-k8s.io/provider"    = "bootstrap-kubeadm"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "kubernetes_namespace" "capi_control_plane" {
  count = var.capg_enabled ? 1 : 0

  metadata {
    name = var.capg_control_plane_namespace
    labels = {
      "cluster.x-k8s.io/provider"    = "control-plane-kubeadm"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "kubernetes_namespace" "capg" {
  count = var.capg_enabled ? 1 : 0

  metadata {
    name = var.capg_namespace
    labels = {
      "cluster.x-k8s.io/provider"    = "infrastructure-gcp"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "google_service_account" "capg" {
  count = var.capg_enabled && var.capg_manage_gcp_credentials ? 1 : 0

  account_id   = var.capg_gcp_service_account_id
  display_name = "XTrinode CAPG controller"
  description  = "Service account used by Cluster API Provider GCP for test management clusters"
}

resource "google_project_iam_member" "capg" {
  for_each = var.capg_enabled && var.capg_manage_gcp_credentials ? toset(var.capg_gcp_service_account_roles) : toset([])

  project = var.gcp_project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.capg[0].email}"
}

resource "google_service_account_key" "capg" {
  count = var.capg_enabled && var.capg_manage_gcp_credentials ? 1 : 0

  service_account_id = google_service_account.capg[0].name
  private_key_type   = "TYPE_GOOGLE_CREDENTIALS_FILE"
}

resource "kubernetes_secret" "capg_gcp_credentials" {
  count = var.capg_enabled ? 1 : 0

  metadata {
    name      = var.capg_gcp_credentials_secret_name
    namespace = kubernetes_namespace.capg[0].metadata[0].name
    labels = {
      "cluster.x-k8s.io/provider"    = "infrastructure-gcp"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  data = {
    GCP_B64ENCODED_CREDENTIALS = var.capg_manage_gcp_credentials ? google_service_account_key.capg[0].private_key : coalesce(var.capg_gcp_credentials_b64, "")
    EXP_CAPG_GKE               = tostring(var.capg_enable_gke)
    EXP_MACHINE_POOL           = tostring(var.capg_enable_machine_pool)
  }

  type = "Opaque"

  lifecycle {
    precondition {
      condition     = var.capg_manage_gcp_credentials || (var.capg_gcp_credentials_b64 != null && length(var.capg_gcp_credentials_b64) > 0)
      error_message = "Set capg_manage_gcp_credentials=true or provide capg_gcp_credentials_b64 when capg_enabled=true."
    }
  }

  depends_on = [google_project_iam_member.capg]
}

resource "helm_release" "cert_manager" {
  count = var.capg_enabled && var.capg_install_cert_manager ? 1 : 0

  name             = "cert-manager"
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  namespace        = "cert-manager"
  create_namespace = true
  version          = var.capg_cert_manager_version
  wait             = true
  timeout          = 300

  set {
    name  = "crds.enabled"
    value = "true"
  }

  depends_on = [google_container_node_pool.xtrinode]
}

resource "helm_release" "capi_operator" {
  count = var.capg_enabled ? 1 : 0

  name             = "capi-operator"
  repository       = "https://kubernetes-sigs.github.io/cluster-api-operator"
  chart            = "cluster-api-operator"
  namespace        = kubernetes_namespace.capg_operator[0].metadata[0].name
  create_namespace = false
  version          = var.capg_operator_chart_version
  wait             = true
  timeout          = 600
  values           = [yamlencode(local.capg_provider_values)]

  depends_on = [
    helm_release.cert_manager,
    kubernetes_namespace.capi_core,
    kubernetes_namespace.capi_bootstrap,
    kubernetes_namespace.capi_control_plane,
    kubernetes_secret.capg_gcp_credentials,
  ]
}
