output "eks_cluster_name" {
  description = "EKS cluster name"
  value       = aws_eks_cluster.xtrinode.name
}

output "eks_cluster_endpoint" {
  description = "EKS cluster endpoint"
  value       = aws_eks_cluster.xtrinode.endpoint
}

output "eks_cluster_version" {
  description = "EKS cluster Kubernetes version"
  value       = aws_eks_cluster.xtrinode.version
}

output "eks_cluster_arn" {
  description = "EKS cluster ARN"
  value       = aws_eks_cluster.xtrinode.arn
}

output "eks_cluster_certificate_authority" {
  description = "EKS cluster certificate authority"
  value       = aws_eks_cluster.xtrinode.certificate_authority[0].data
  sensitive   = true
}

output "configure_kubectl" {
  description = "Command to configure kubectl"
  value       = "aws eks update-kubeconfig --region ${var.aws_region} --name ${aws_eks_cluster.xtrinode.name}"
}

output "vpc_id" {
  description = "VPC ID"
  value       = aws_vpc.xtrinode.id
}

output "public_subnets" {
  description = "Public subnet IDs"
  value       = aws_subnet.public[*].id
}

output "private_subnets" {
  description = "Private subnet IDs"
  value       = aws_subnet.private[*].id
}

output "node_group_id" {
  description = "EKS node group ID"
  value       = aws_eks_node_group.xtrinode.id
}

output "node_group_status" {
  description = "EKS node group status"
  value       = aws_eks_node_group.xtrinode.status
}

output "xtrinode_system_namespace" {
  description = "Kubernetes namespace for the XTrinode control plane"
  value       = kubernetes_namespace.xtrinode_system.metadata[0].name
}

output "test_namespace" {
  description = "Kubernetes namespace for testing"
  value       = kubernetes_namespace.test_team.metadata[0].name
}

output "postgres_endpoint" {
  description = "PostgreSQL RDS endpoint"
  value       = var.postgres_enabled ? aws_db_instance.xtrinode[0].address : null
}

output "postgres_port" {
  description = "PostgreSQL RDS port"
  value       = var.postgres_enabled ? aws_db_instance.xtrinode[0].port : null
}

output "postgres_database_name" {
  description = "PostgreSQL database name"
  value       = var.postgres_enabled ? aws_db_instance.xtrinode[0].db_name : null
}

output "postgres_connection_secret" {
  description = "Kubernetes Secret name with PostgreSQL connection details"
  value       = var.postgres_enabled ? kubernetes_secret.postgres_connection[0].metadata[0].name : null
}

output "xtrinode_operator_release" {
  description = "Helm release name for XTrinode operator"
  value       = var.helm_repository != "" ? helm_release.xtrinode_operator[0].name : null
}

output "prometheus_enabled" {
  description = "Whether Prometheus is enabled"
  value       = var.prometheus_enabled
}

output "prometheus_namespace" {
  description = "Kubernetes namespace for Prometheus"
  value       = local.observability_enabled ? local.prometheus_namespace : null
}

output "prometheus_service_url" {
  description = "Prometheus service URL"
  value       = var.prometheus_enabled ? local.prometheus_service_url : null
}

output "vector_enabled" {
  description = "Whether Vector log collection is enabled"
  value       = var.vector_enabled
}

output "cluster_autoscaler_enabled" {
  description = "Whether Cluster Autoscaler is enabled"
  value       = var.cluster_autoscaler_enabled
}

output "cluster_autoscaler_service_account" {
  description = "Cluster Autoscaler service account name"
  value       = var.cluster_autoscaler_enabled ? kubernetes_service_account.cluster_autoscaler[0].metadata[0].name : null
}

output "ecr_operator_repository_url" {
  description = "ECR repository URL for XTrinode operator"
  value       = aws_ecr_repository.xtrinode_operator.repository_url
}

output "ecr_gateway_repository_url" {
  description = "ECR repository URL for XTrinode gateway"
  value       = aws_ecr_repository.xtrinode_gateway.repository_url
}

output "ecr_api_server_repository_url" {
  description = "ECR repository URL for XTrinode API server"
  value       = aws_ecr_repository.xtrinode_api_server.repository_url
}

output "docker_login_command" {
  description = "Command to login to ECR"
  value       = "aws ecr get-login-password --region ${var.aws_region} | docker login --username AWS --password-stdin ${split("/", aws_ecr_repository.xtrinode_operator.repository_url)[0]}"
}
