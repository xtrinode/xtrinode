terraform {
  required_version = ">= 1.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.80"
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

provider "azurerm" {
  features {}
  subscription_id = var.azure_subscription_id
}

provider "kubernetes" {
  host                   = azurerm_kubernetes_cluster.xtrinode.kube_config[0].host
  client_certificate     = base64decode(azurerm_kubernetes_cluster.xtrinode.kube_config[0].client_certificate)
  client_key             = base64decode(azurerm_kubernetes_cluster.xtrinode.kube_config[0].client_key)
  cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.xtrinode.kube_config[0].cluster_ca_certificate)
}

provider "helm" {
  kubernetes {
    host                   = azurerm_kubernetes_cluster.xtrinode.kube_config[0].host
    client_certificate     = base64decode(azurerm_kubernetes_cluster.xtrinode.kube_config[0].client_certificate)
    client_key             = base64decode(azurerm_kubernetes_cluster.xtrinode.kube_config[0].client_key)
    cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.xtrinode.kube_config[0].cluster_ca_certificate)
  }
}

# Resource Group
resource "azurerm_resource_group" "xtrinode" {
  name     = var.resource_group_name
  location = var.azure_region

  tags = {
    Environment = var.environment
    Project     = "xtrinode"
    ManagedBy   = "terraform"
  }
}

# Virtual Network
resource "azurerm_virtual_network" "xtrinode" {
  name                = "xtrinode-vnet"
  address_space       = [var.vnet_cidr]
  location            = azurerm_resource_group.xtrinode.location
  resource_group_name = azurerm_resource_group.xtrinode.name

  tags = {
    Name = "xtrinode-vnet"
  }
}

# Subnet
resource "azurerm_subnet" "xtrinode" {
  name                 = "xtrinode-subnet"
  resource_group_name  = azurerm_resource_group.xtrinode.name
  virtual_network_name = azurerm_virtual_network.xtrinode.name
  address_prefixes     = [var.subnet_cidr]
}

# Network Security Group
resource "azurerm_network_security_group" "xtrinode" {
  name                = "xtrinode-nsg"
  location            = azurerm_resource_group.xtrinode.location
  resource_group_name = azurerm_resource_group.xtrinode.name

  security_rule {
    name                       = "AllowHTTPSFromVNet"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "443"
    source_address_prefix      = "VirtualNetwork"
    destination_address_prefix = "VirtualNetwork"
  }

  security_rule {
    name                       = "AllowOutbound"
    priority                   = 100
    direction                  = "Outbound"
    access                     = "Allow"
    protocol                   = "*"
    source_port_range          = "*"
    destination_port_range     = "*"
    source_address_prefix      = "*"
    destination_address_prefix = "AzureCloud"
  }

  tags = {
    Name = "xtrinode-nsg"
  }
}

# Subnet for PostgreSQL (delegated — cannot share with AKS)
resource "azurerm_subnet" "postgres" {
  name                 = "xtrinode-postgres-subnet"
  resource_group_name  = azurerm_resource_group.xtrinode.name
  virtual_network_name = azurerm_virtual_network.xtrinode.name
  address_prefixes     = [var.postgres_subnet_cidr]

  delegation {
    name = "postgresql-delegation"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

# Subnet Network Security Group Association
resource "azurerm_subnet_network_security_group_association" "xtrinode" {
  subnet_id                 = azurerm_subnet.xtrinode.id
  network_security_group_id = azurerm_network_security_group.xtrinode.id
}

# User Assigned Identity for AKS Kubelet
resource "azurerm_user_assigned_identity" "aks_kubelet" {
  name                = "${var.cluster_name}-kubelet-identity"
  location            = azurerm_resource_group.xtrinode.location
  resource_group_name = azurerm_resource_group.xtrinode.name
}

# AKS Cluster
resource "azurerm_kubernetes_cluster" "xtrinode" {
  name                = var.cluster_name
  location            = azurerm_resource_group.xtrinode.location
  resource_group_name = azurerm_resource_group.xtrinode.name
  dns_prefix          = var.cluster_name
  kubernetes_version  = var.kubernetes_version

  # Fully private cluster - API server accessible only from within VNet
  # Portal/CLI access works via `az aks command invoke` or Azure Portal run-command
  private_cluster_enabled             = true
  private_cluster_public_fqdn_enabled = true # Allows DNS resolution from outside VNet for portal/CLI
  private_dns_zone_id                 = "System"
  role_based_access_control_enabled   = true

  default_node_pool {
    name                = "default"
    node_count          = max(var.node_desired_size, 1)
    vm_size             = var.node_vm_size
    vnet_subnet_id      = azurerm_subnet.xtrinode.id
    enable_auto_scaling = true
    min_count           = max(var.node_min_size, 1) # Default pool requires min >= 1
    max_count           = var.node_max_size
    os_disk_size_gb     = 128
  }

  identity {
    type = "SystemAssigned"
  }

  kubelet_identity {
    client_id                 = azurerm_user_assigned_identity.aks_kubelet.client_id
    object_id                 = azurerm_user_assigned_identity.aks_kubelet.principal_id
    user_assigned_identity_id = azurerm_user_assigned_identity.aks_kubelet.id
  }

  network_profile {
    network_plugin = "azure"
    network_policy = "azure"
    service_cidr   = var.service_cidr
    dns_service_ip = var.dns_service_ip
  }

  tags = {
    Name = var.cluster_name
  }

  depends_on = [
    azurerm_subnet_network_security_group_association.xtrinode,
    azurerm_user_assigned_identity.aks_kubelet
  ]
}

# Role Assignment for AKS Cluster
resource "azurerm_role_assignment" "aks_network_contributor" {
  scope                = azurerm_virtual_network.xtrinode.id
  role_definition_name = "Network Contributor"
  principal_id         = azurerm_kubernetes_cluster.xtrinode.identity[0].principal_id
}

# Single Azure Container Registry for all XTrinode components
# ACR names must be globally unique and alphanumeric only
resource "azurerm_container_registry" "xtrinode" {
  name                = "${replace(var.cluster_name, "-", "")}acr"
  resource_group_name = azurerm_resource_group.xtrinode.name
  location            = azurerm_resource_group.xtrinode.location
  sku                 = "Basic"
  admin_enabled       = false # Use managed identity instead of admin credentials

  tags = {
    Name        = "xtrinode-acr"
    Environment = var.environment
  }
}

# Role Assignment: AKS kubelet can pull from ACR
resource "azurerm_role_assignment" "aks_acr_pull" {
  scope                = azurerm_container_registry.xtrinode.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_kubernetes_cluster.xtrinode.kubelet_identity[0].object_id
}

# Kubernetes Namespace for XTrinode Operator
resource "kubernetes_namespace" "xtrinode_system" {
  metadata {
    name = "xtrinode-system"
    labels = {
      "app.kubernetes.io/name" = "xtrinode-system"
    }
  }

  depends_on = [azurerm_kubernetes_cluster.xtrinode]
}

# Helm Release for XTrinode Operator
# Note: If helm_repository is empty, you'll need to deploy manually via Helm CLI
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
    value = "${azurerm_container_registry.xtrinode.login_server}/xtrinode-operator"
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

# Kubernetes Namespace for Test XTrinode
resource "kubernetes_namespace" "test_team" {
  metadata {
    name = "team-test"
    labels = {
      "app.kubernetes.io/name"                 = "xtrinode-test"
      "xtrinode.analytics.xtrinode.io/managed" = "true"
      "xtrinode.analytics.xtrinode.io/runtime" = "test"
    }
  }

  depends_on = [azurerm_kubernetes_cluster.xtrinode]
}
