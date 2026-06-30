resource "aws_db_instance" "main" {
  identifier          = "openfireblocks-${var.environment}-db"
  engine              = "postgres"
  engine_version      = "16.1"
  instance_class      = var.db_instance_class
  allocated_storage   = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage

  db_name  = var.db_name
  username = var.db_username
  password = var.db_password

  vpc_security_group_ids = var.security_group_ids
  db_subnet_group_name   = var.db_subnet_group_name
  parameter_group_name   = aws_db_parameter_group.main.name

  multi_az              = var.multi_az
  publicly_accessible   = var.publicly_accessible
  skip_final_snapshot   = var.skip_final_snapshot
  final_snapshot_identifier = var.final_snapshot_identifier

  backup_retention_period = var.backup_retention_days
  backup_window          = var.backup_window
  maintenance_window     = var.maintenance_window
  copy_tags_to_snapshot  = true

  storage_encrypted       = true
  kms_key_id             = var.kms_key_id
  storage_type           = "gp3"
  iops                   = 3000
  storage_throughput     = 125

  enabled_cloudwatch_logs_exports = ["postgresql"]

  performance_insights_enabled    = var.enable_performance_insights
  performance_insights_retention_period = 7
  performance_insights_kms_key_id = var.kms_key_id

  monitoring_interval           = var.enable_enhanced_monitoring ? 60 : 0
  monitoring_role_arn          = var.enable_enhanced_monitoring ? aws_iam_role.rds_monitoring[0].arn : null
  enable_iam_database_authentication = true

  enable_http_endpoint = false

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-db"
    }
  )
}

# IAM Role for Enhanced Monitoring
resource "aws_iam_role" "rds_monitoring" {
  count = var.enable_enhanced_monitoring ? 1 : 0
  name  = "openfireblocks-${var.environment}-rds-monitoring-role"

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
  name              = "/aws/rds/instance/openfireblocks-${var.environment}-db/postgresql"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-rds-logs"
    }
  )
}

# Parameter Group for PostgreSQL
resource "aws_db_parameter_group" "main" {
  name        = "openfireblocks-${var.environment}-pg16"
  family      = "postgres16"
  description = "Custom parameter group for OpenFireblocks PostgreSQL"

  parameter {
    name  = "log_connections"
    value = "1"
  }

  parameter {
    name  = "log_disconnections"
    value = "1"
  }

  parameter {
    name  = "log_duration"
    value = "1"
  }

  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }

  parameter {
    name  = "log_statement"
    value = "all"
  }

  parameter {
    name  = "log_lock_waits"
    value = "1"
  }

  parameter {
    name  = "max_connections"
    value = "500"
  }

  parameter {
    name  = "shared_buffers"
    value = "{DBInstanceClassMemory/4}"
  }

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-pg16-params"
    }
  )
}
