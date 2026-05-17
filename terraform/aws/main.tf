terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
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
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

provider "aws" {
  region  = var.aws_region
  profile = var.aws_profile

  default_tags {
    tags = {
      Environment = var.environment
      Project     = "xtrinode"
      ManagedBy   = "terraform"
    }
  }
}

provider "kubernetes" {
  host                   = aws_eks_cluster.xtrinode.endpoint
  cluster_ca_certificate = base64decode(aws_eks_cluster.xtrinode.certificate_authority[0].data)
  token                  = data.aws_eks_cluster_auth.xtrinode.token
}

provider "helm" {
  kubernetes {
    host                   = aws_eks_cluster.xtrinode.endpoint
    cluster_ca_certificate = base64decode(aws_eks_cluster.xtrinode.certificate_authority[0].data)
    token                  = data.aws_eks_cluster_auth.xtrinode.token
  }
}

# Data source for EKS cluster authentication
data "aws_eks_cluster_auth" "xtrinode" {
  name = aws_eks_cluster.xtrinode.name
}

# VPC for EKS cluster
resource "aws_vpc" "xtrinode" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "xtrinode-vpc"
  }
}

# Internet Gateway
resource "aws_internet_gateway" "xtrinode" {
  vpc_id = aws_vpc.xtrinode.id

  tags = {
    Name = "xtrinode-igw"
  }
}

# Public Subnets (EKS requires at least 2 AZs)
resource "aws_subnet" "public" {
  count                   = 2
  vpc_id                  = aws_vpc.xtrinode.id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = false

  tags = {
    Name                                        = "xtrinode-public-${count.index}"
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
    "kubernetes.io/role/elb"                    = "1"
  }
}

# Private Subnets (EKS requires at least 2 AZs)
resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.xtrinode.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index + 2)
  availability_zone = data.aws_availability_zones.available.names[count.index]

  tags = {
    Name                                        = "xtrinode-private-${count.index}"
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
    "kubernetes.io/role/internal-elb"           = "1"
  }
}

# Elastic IP for NAT Gateway
resource "aws_eip" "nat" {
  domain = "vpc"

  tags = {
    Name = "xtrinode-eip"
  }

  depends_on = [aws_internet_gateway.xtrinode]
}

# NAT Gateway (in first public subnet)
resource "aws_nat_gateway" "xtrinode" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id

  tags = {
    Name = "xtrinode-nat"
  }

  depends_on = [aws_internet_gateway.xtrinode]
}

# Route Table for Public Subnets
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.xtrinode.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.xtrinode.id
  }

  tags = {
    Name = "xtrinode-public-rt"
  }
}

# Route Table Association for Public Subnets
resource "aws_route_table_association" "public" {
  count          = 2
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Route Table for Private Subnet
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.xtrinode.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.xtrinode.id
  }

  tags = {
    Name = "xtrinode-private-rt"
  }
}

# Route Table Association for Private Subnets
resource "aws_route_table_association" "private" {
  count          = 2
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

# Availability Zones
data "aws_availability_zones" "available" {
  state = "available"
}

# IAM Role for EKS Cluster
resource "aws_iam_role" "eks_cluster_role" {
  name = "xtrinode-eks-cluster-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "eks.amazonaws.com"
        }
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "eks_cluster_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
  role       = aws_iam_role.eks_cluster_role.name
}

# IAM Role for EKS Node Group
resource "aws_iam_role" "eks_node_role" {
  name = "xtrinode-eks-node-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "eks_worker_node_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"
  role       = aws_iam_role.eks_node_role.name
}

resource "aws_iam_role_policy_attachment" "eks_cni_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
  role       = aws_iam_role.eks_node_role.name
}

resource "aws_iam_role_policy_attachment" "eks_container_registry_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
  role       = aws_iam_role.eks_node_role.name
}

# ECR Repository for XTrinode Operator
resource "aws_ecr_repository" "xtrinode_operator" {
  name                 = "xtrinode-operator"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = {
    Name        = "xtrinode-operator"
    Environment = var.environment
  }
}

# ECR Repository for XTrinode Gateway
resource "aws_ecr_repository" "xtrinode_gateway" {
  name                 = "xtrinode-gateway"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = {
    Name        = "xtrinode-gateway"
    Environment = var.environment
  }
}

# ECR Repository for XTrinode API Server
resource "aws_ecr_repository" "xtrinode_api_server" {
  name                 = "xtrinode-api-server"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = {
    Name        = "xtrinode-api-server"
    Environment = var.environment
  }
}

# ECR Lifecycle Policy - keep last 10 images
resource "aws_ecr_lifecycle_policy" "xtrinode_operator" {
  repository = aws_ecr_repository.xtrinode_operator.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 10 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

resource "aws_ecr_lifecycle_policy" "xtrinode_gateway" {
  repository = aws_ecr_repository.xtrinode_gateway.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 10 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

resource "aws_ecr_lifecycle_policy" "xtrinode_api_server" {
  repository = aws_ecr_repository.xtrinode_api_server.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 10 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

# Security Group for EKS Cluster
resource "aws_security_group" "eks_cluster" {
  name        = "xtrinode-eks-cluster-sg"
  description = "Security group for XTrinode EKS cluster"
  vpc_id      = aws_vpc.xtrinode.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = {
    Name = "xtrinode-eks-cluster-sg"
  }
}

resource "aws_security_group_rule" "eks_cluster_ingress" {
  type              = "ingress"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = [var.vpc_cidr]
  security_group_id = aws_security_group.eks_cluster.id
}

# KMS key for Kubernetes Secret encryption at rest in EKS
resource "aws_kms_key" "eks_secrets" {
  description             = "KMS key for ${var.cluster_name} EKS Kubernetes Secret encryption"
  deletion_window_in_days = 7
  enable_key_rotation     = true

  tags = {
    Name        = "${var.cluster_name}-eks-secrets"
    Environment = var.environment
  }
}

resource "aws_kms_alias" "eks_secrets" {
  name          = "alias/${var.cluster_name}-eks-secrets"
  target_key_id = aws_kms_key.eks_secrets.key_id
}

# EKS Cluster
resource "aws_eks_cluster" "xtrinode" {
  name                      = var.cluster_name
  role_arn                  = aws_iam_role.eks_cluster_role.arn
  version                   = var.kubernetes_version
  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  vpc_config {
    subnet_ids              = concat(aws_subnet.public[*].id, aws_subnet.private[*].id)
    security_group_ids      = [aws_security_group.eks_cluster.id]
    endpoint_private_access = true
    endpoint_public_access  = var.eks_public_access
    public_access_cidrs     = var.eks_public_access_cidrs
  }

  encryption_config {
    resources = ["secrets"]

    provider {
      key_arn = aws_kms_key.eks_secrets.arn
    }
  }

  depends_on = [
    aws_iam_role_policy_attachment.eks_cluster_policy,
    aws_cloudwatch_log_group.eks_cluster
  ]

  tags = {
    Name = var.cluster_name
  }
}

# CloudWatch Log Group for EKS
resource "aws_cloudwatch_log_group" "eks_cluster" {
  name              = "/aws/eks/${var.cluster_name}/cluster"
  retention_in_days = 7

  tags = {
    Name = "xtrinode-eks-logs"
  }
}

# EKS Node Group (Spot Instances)
resource "aws_eks_node_group" "xtrinode" {
  cluster_name    = aws_eks_cluster.xtrinode.name
  node_group_name = "xtrinode-node-group"
  node_role_arn   = aws_iam_role.eks_node_role.arn
  subnet_ids      = aws_subnet.private[*].id
  version         = var.kubernetes_version

  # Use spot instances for cost savings
  capacity_type = "SPOT"

  scaling_config {
    desired_size = var.node_desired_size
    max_size     = var.node_max_size
    min_size     = var.node_min_size
  }

  instance_types = var.node_instance_types

  tags = {
    Name = "xtrinode-node-group"
  }

  depends_on = [
    aws_iam_role_policy_attachment.eks_worker_node_policy,
    aws_iam_role_policy_attachment.eks_cni_policy,
    aws_iam_role_policy_attachment.eks_container_registry_policy,
  ]
}

# Kubernetes Namespace for XTrinode Operator
resource "kubernetes_namespace" "xtrinode_system" {
  metadata {
    name = "xtrinode-system"
    labels = {
      "app.kubernetes.io/name" = "xtrinode-system"
    }
  }

  depends_on = [aws_eks_node_group.xtrinode]
}

# Helm Release for XTrinode Operator
# Note: If helm_repository is empty, you'll need to deploy manually via Helm CLI
# This allows flexibility for local development vs. production deployments
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
    value = aws_ecr_repository.xtrinode_operator.repository_url
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

  depends_on = [aws_eks_node_group.xtrinode]
}
