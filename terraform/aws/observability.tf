# XTrinode observability stack.
# Terraform installs the Helm chart and passes AWS-specific values; Prometheus
# and Vector resources are owned by Helm, not hand-written Terraform resources.

resource "kubernetes_namespace" "monitoring" {
  count = local.observability_enabled ? 1 : 0

  metadata {
    name = "monitoring"
    labels = {
      "app.kubernetes.io/name"    = "monitoring"
      "app.kubernetes.io/part-of" = "xtrinode"
    }
  }

  depends_on = [aws_eks_node_group.xtrinode]
}

locals {
  observability_enabled  = var.prometheus_enabled || var.vector_enabled
  prometheus_namespace   = local.observability_enabled ? kubernetes_namespace.monitoring[0].metadata[0].name : "monitoring"
  prometheus_service_url = "http://prometheus-operated.${local.prometheus_namespace}.svc.cluster.local:9090"
}

resource "helm_release" "xtrinode_observability" {
  count     = local.observability_enabled ? 1 : 0
  name      = "xtrinode-observability"
  chart     = "${path.module}/../../helm/xtrinode-observability"
  namespace = kubernetes_namespace.monitoring[0].metadata[0].name

  dependency_update = true
  create_namespace  = false

  values = [
    yamlencode({
      "prometheus-stack" = {
        enabled = var.prometheus_enabled
        defaultRules = {
          create = false
        }
        alertmanager = {
          enabled = false
        }
        nodeExporter = {
          enabled = false
        }
        kubeStateMetrics = {
          enabled = false
        }
        kubeApiServer = {
          enabled = false
        }
        kubelet = {
          enabled = false
        }
        kubeControllerManager = {
          enabled = false
        }
        coreDns = {
          enabled = false
        }
        kubeDns = {
          enabled = false
        }
        kubeEtcd = {
          enabled = false
        }
        kubeScheduler = {
          enabled = false
        }
        kubeProxy = {
          enabled = false
        }
        grafana = {
          enabled = var.grafana_enabled
        }
        prometheus = {
          enabled = true
          prometheusSpec = {
            retention                               = "30d"
            serviceMonitorSelectorNilUsesHelmValues = false
            podMonitorSelectorNilUsesHelmValues     = false
            probeSelectorNilUsesHelmValues          = false
            ruleSelectorNilUsesHelmValues           = false
            storageSpec = {
              volumeClaimTemplate = {
                spec = {
                  storageClassName = var.prometheus_storage_class
                  accessModes      = ["ReadWriteOnce"]
                  resources = {
                    requests = {
                      storage = var.prometheus_storage_size
                    }
                  }
                }
              }
            }
            resources = {
              requests = {
                cpu    = "500m"
                memory = "2Gi"
              }
              limits = {
                cpu    = "2"
                memory = "4Gi"
              }
            }
          }
        }
      }
      vector = {
        enabled     = var.vector_enabled
        clusterName = var.cluster_name
        environment = var.environment
        region      = var.aws_region
        logLevel    = var.vector_log_level
        serviceMonitor = {
          enabled = var.prometheus_enabled
        }
      }
    })
  ]

  depends_on = [
    kubernetes_namespace.monitoring[0],
    aws_eks_node_group.xtrinode
  ]
}
