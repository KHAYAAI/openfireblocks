terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

resource "aws_db_instance" "replica" {
  identifier                 = "openfireblocks-${var.environment}-db-replica"
  replicate_source_db        = var.source_db_instance_id
  instance_class             = var.replica_instance_class
  publicly_accessible        = var.publicly_accessible
  auto_minor_version_upgrade = false
  multi_az                   = false
  skip_final_snapshot        = var.skip_final_snapshot

  vpc_security_group_ids = var.security_group_ids
  db_subnet_group_name   = var.db_subnet_group_name

  performance_insights_enabled          = var.enable_performance_insights
  performance_insights_retention_period = 7
  performance_insights_kms_key_id       = var.kms_key_id

  monitoring_interval                 = var.enable_enhanced_monitoring ? 60 : 0
  monitoring_role_arn                 = var.enable_enhanced_monitoring ? aws_iam_role.rds_monitoring[0].arn : null
  iam_database_authentication_enabled = true

  enabled_cloudwatch_logs_exports = ["postgresql"]

  storage_type       = "gp3"
  iops               = 3000
  storage_throughput = 125

  backup_retention_period = 7
  backup_window           = "00:00-01:00"

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-db-replica"
    }
  )
}

# IAM Role for Enhanced Monitoring
resource "aws_iam_role" "rds_monitoring" {
  count = var.enable_enhanced_monitoring ? 1 : 0
  name  = "openfireblocks-${var.environment}-rds-replica-monitoring-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "monitoring.rds.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  count      = var.enable_enhanced_monitoring ? 1 : 0
  role       = aws_iam_role.rds_monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

# CloudWatch Log Group for PostgreSQL Logs
resource "aws_cloudwatch_log_group" "postgres" {
  name              = "/aws/rds/instance/openfireblocks-${var.environment}-db-replica/postgresql"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-rds-replica-logs"
    }
  )
}
