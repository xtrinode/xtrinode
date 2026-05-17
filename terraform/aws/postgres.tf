# AWS RDS PostgreSQL Instance
# Provides managed PostgreSQL database for Trino catalogs (testing)

# DB Subnet Group
# RDS requires subnets in at least 2 AZs
resource "aws_db_subnet_group" "xtrinode" {
  count = var.postgres_enabled ? 1 : 0

  name       = "${var.cluster_name}-postgres-subnet-group"
  subnet_ids = aws_subnet.private[*].id # Requires subnets in at least 2 AZs

  tags = {
    Name        = "${var.cluster_name}-postgres-subnet-group"
    Environment = var.environment
    Project     = "xtrinode"
    ManagedBy   = "terraform"
  }
}

# Security Group for RDS PostgreSQL
resource "aws_security_group" "postgres" {
  count = var.postgres_enabled ? 1 : 0

  name        = "${var.cluster_name}-postgres-sg"
  description = "Security group for PostgreSQL RDS instance"
  vpc_id      = aws_vpc.xtrinode.id

  ingress {
    description = "PostgreSQL from EKS nodes"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    # Allow from private subnet (where EKS nodes run)
    cidr_blocks = [for s in aws_subnet.private : s.cidr_block]
  }

  egress {
    description = "Allow outbound within the VPC"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = {
    Name        = "${var.cluster_name}-postgres-sg"
    Environment = var.environment
    Project     = "xtrinode"
    ManagedBy   = "terraform"
  }
}

# RDS PostgreSQL Instance
resource "aws_db_instance" "xtrinode" {
  count = var.postgres_enabled ? 1 : 0

  identifier = "${var.cluster_name}-postgres"

  engine         = "postgres"
  engine_version = var.postgres_version
  instance_class = var.postgres_instance_class

  allocated_storage     = var.postgres_allocated_storage
  max_allocated_storage = var.postgres_max_allocated_storage
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = var.postgres_database_name
  username = var.postgres_admin_user
  password = var.postgres_admin_password

  db_subnet_group_name   = aws_db_subnet_group.xtrinode[0].name
  vpc_security_group_ids = [aws_security_group.postgres[0].id]
  publicly_accessible    = false

  # Single AZ deployment - minimal setup (no multi-AZ)
  multi_az = false

  backup_retention_period = 7
  backup_window           = "03:00-04:00"
  maintenance_window      = "mon:04:00-mon:05:00"

  skip_final_snapshot       = true # For testing - set to false for production
  final_snapshot_identifier = "${var.cluster_name}-postgres-final-snapshot"

  enabled_cloudwatch_logs_exports = ["postgresql", "upgrade"]

  tags = {
    Name        = "${var.cluster_name}-postgres"
    Environment = var.environment
    Project     = "xtrinode"
    ManagedBy   = "terraform"
  }
}

# Kubernetes Secret with PostgreSQL connection details
resource "kubernetes_secret" "postgres_connection" {
  count = var.postgres_enabled ? 1 : 0

  metadata {
    name      = "trino-catalog-postgres-analytics-secret"
    namespace = kubernetes_namespace.xtrinode_system.metadata[0].name
  }

  data = {
    POSTGRES_HOST     = aws_db_instance.xtrinode[0].address
    POSTGRES_PORT     = tostring(aws_db_instance.xtrinode[0].port)
    POSTGRES_DATABASE = aws_db_instance.xtrinode[0].db_name
    POSTGRES_USER     = aws_db_instance.xtrinode[0].username
    POSTGRES_PASSWORD = aws_db_instance.xtrinode[0].password
    # JDBC URL for Trino catalog properties
    JDBC_URL = "jdbc:postgresql://${aws_db_instance.xtrinode[0].address}:${aws_db_instance.xtrinode[0].port}/${aws_db_instance.xtrinode[0].db_name}"
  }

  type = "Opaque"

  depends_on = [
    aws_db_instance.xtrinode,
    kubernetes_namespace.xtrinode_system
  ]
}
