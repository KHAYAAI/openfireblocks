# OpenFireblocks AWS Infrastructure
# Production Multi-Region Deployment with HA

terraform {
  required_version = ">= 1.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # Configure S3 backend for state management
  # Uncomment after creating S3 bucket and DynamoDB table
  # backend "s3" {
  #   bucket         = "openfireblocks-terraform-state"
  #   key            = "prod/terraform.tfstate"
  #   region         = "us-east-1"
  #   encrypt        = true
  #   dynamodb_table = "terraform-lock"
  # }
}

# Primary Region Provider
provider "aws" {
  region = var.primary_region

  default_tags {
    tags = {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
      CreatedAt   = timestamp()
    }
  }
}

# Secondary Region Provider
provider "aws" {
  alias  = "secondary"
  region = var.secondary_region

  default_tags {
    tags = {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
      CreatedAt   = timestamp()
    }
  }
}

# Data source for current AWS account
data "aws_caller_identity" "current" {}
data "aws_availability_zones" "primary" {
  state = "available"
}
data "aws_availability_zones" "secondary" {
  provider = aws.secondary
  state    = "available"
}

# KMS Key for encryption (primary region)
resource "aws_kms_key" "primary" {
  description             = "KMS key for OpenFireblocks encryption in primary region"
  deletion_window_in_days = 10
  enable_key_rotation     = true

  tags = {
    Name = "openfireblocks-primary-key"
  }
}

resource "aws_kms_alias" "primary" {
  name          = "alias/openfireblocks-primary"
  target_key_id = aws_kms_key.primary.key_id
}

# KMS Key for encryption (secondary region)
resource "aws_kms_key" "secondary" {
  provider                = aws.secondary
  description             = "KMS key for OpenFireblocks encryption in secondary region"
  deletion_window_in_days = 10
  enable_key_rotation     = true

  tags = {
    Name = "openfireblocks-secondary-key"
  }
}

resource "aws_kms_alias" "secondary" {
  provider      = aws.secondary
  name          = "alias/openfireblocks-secondary"
  target_key_id = aws_kms_key.secondary.key_id
}

# VPC Modules
module "vpc_primary" {
  source = "./modules/vpc"

  providers = {
    aws = aws
  }

  environment        = var.environment
  region             = var.primary_region
  vpc_cidr           = var.primary_vpc_cidr
  availability_zones = data.aws_availability_zones.primary.names
  enable_nat_gateway = true
  enable_vpn_gateway = var.enable_vpn_gateway
  enable_flow_logs   = true

  tags = {
    Name = "openfireblocks-primary-vpc"
  }
}

module "vpc_secondary" {
  source = "./modules/vpc"

  providers = {
    aws = aws.secondary
  }

  environment        = var.environment
  region             = var.secondary_region
  vpc_cidr           = var.secondary_vpc_cidr
  availability_zones = data.aws_availability_zones.secondary.names
  enable_nat_gateway = true
  enable_vpn_gateway = var.enable_vpn_gateway
  enable_flow_logs   = true

  tags = {
    Name = "openfireblocks-secondary-vpc"
  }
}

# VPC Peering for cross-region communication
resource "aws_vpc_peering_connection" "primary_to_secondary" {
  vpc_id            = module.vpc_primary.vpc_id
  peer_vpc_id       = module.vpc_secondary.vpc_id
  peer_region       = var.secondary_region
  auto_accept       = false

  tags = {
    Name = "openfireblocks-primary-to-secondary"
  }
}

resource "aws_vpc_peering_connection_accepter" "secondary_accept" {
  provider                  = aws.secondary
  vpc_peering_connection_id = aws_vpc_peering_connection.primary_to_secondary.id
  auto_accept               = true

  tags = {
    Name = "openfireblocks-secondary-accept"
  }
}

# RDS PostgreSQL Module (Primary)
module "rds_primary" {
  source = "./modules/rds"

  providers = {
    aws = aws
  }

  environment                   = var.environment
  db_name                      = var.db_name
  db_username                  = var.db_username
  db_password                  = var.db_password
  db_instance_class            = var.db_instance_class
  allocated_storage            = var.db_allocated_storage
  max_allocated_storage        = var.db_max_allocated_storage
  vpc_id                       = module.vpc_primary.vpc_id
  db_subnet_group_name         = module.vpc_primary.db_subnet_group_name
  multi_az                     = true
  enable_backups               = true
  backup_retention_days        = 30
  backup_window               = "00:00-01:00"
  maintenance_window          = "sun:01:00-sun:02:00"
  enable_performance_insights = true
  enable_enhanced_monitoring   = true
  kms_key_id                  = aws_kms_key.primary.arn
  publicly_accessible         = false
  enable_ssl                  = true
  skip_final_snapshot         = false
  final_snapshot_identifier   = "openfireblocks-primary-final-snapshot"

  security_group_ids = [aws_security_group.rds_primary.id]

  tags = {
    Name = "openfireblocks-primary-db"
  }
}

# RDS PostgreSQL Module (Secondary - Read Replica)
module "rds_secondary" {
  source = "./modules/rds-replica"

  providers = {
    aws = aws.secondary
  }

  environment                  = var.environment
  source_db_instance_id        = module.rds_primary.db_instance_id
  replica_instance_class       = var.db_instance_class
  vpc_id                       = module.vpc_secondary.vpc_id
  db_subnet_group_name         = module.vpc_secondary.db_subnet_group_name
  enable_performance_insights  = true
  enable_enhanced_monitoring   = true
  kms_key_id                   = aws_kms_key.secondary.arn
  publicly_accessible          = false

  security_group_ids = [aws_security_group.rds_secondary.id]

  tags = {
    Name = "openfireblocks-secondary-db-replica"
  }
}

# Vault Cluster Module (Primary)
module "vault_primary" {
  source = "./modules/vault"

  providers = {
    aws = aws
  }

  environment            = var.environment
  vault_instance_count   = var.vault_node_count
  vault_instance_type    = var.vault_instance_type
  vpc_id                 = module.vpc_primary.vpc_id
  private_subnet_ids     = module.vpc_primary.private_subnet_ids
  security_group_ids     = [aws_security_group.vault_primary.id]
  kms_key_id            = aws_kms_key.primary.id
  enable_s3_backend     = true
  s3_bucket_name        = aws_s3_bucket.vault_backend_primary.id
  enable_tls            = true
  tls_cert_arn          = aws_acm_certificate.vault_primary.arn
  instance_profile_name = aws_iam_instance_profile.vault_primary.name

  tags = {
    Name = "openfireblocks-primary-vault"
  }

  depends_on = [
    aws_s3_bucket.vault_backend_primary,
    aws_iam_role_policy.vault_primary_policy
  ]
}

# Vault Cluster Module (Secondary)
module "vault_secondary" {
  source = "./modules/vault"

  providers = {
    aws = aws.secondary
  }

  environment            = var.environment
  vault_instance_count   = var.vault_node_count
  vault_instance_type    = var.vault_instance_type
  vpc_id                 = module.vpc_secondary.vpc_id
  private_subnet_ids     = module.vpc_secondary.private_subnet_ids
  security_group_ids     = [aws_security_group.vault_secondary.id]
  kms_key_id            = aws_kms_key.secondary.id
  enable_s3_backend     = true
  s3_bucket_name        = aws_s3_bucket.vault_backend_secondary.id
  enable_tls            = true
  tls_cert_arn          = aws_acm_certificate.vault_secondary.arn
  instance_profile_name = aws_iam_instance_profile.vault_secondary.name

  tags = {
    Name = "openfireblocks-secondary-vault"
  }

  depends_on = [
    aws_s3_bucket.vault_backend_secondary,
    aws_iam_role_policy.vault_secondary_policy
  ]
}

# S3 Buckets for Vault Backend
resource "aws_s3_bucket" "vault_backend_primary" {
  bucket = "openfireblocks-vault-backend-${var.primary_region}-${data.aws_caller_identity.current.account_id}"

  tags = {
    Name = "openfireblocks-vault-primary"
  }
}

resource "aws_s3_bucket_versioning" "vault_backend_primary" {
  bucket = aws_s3_bucket.vault_backend_primary.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "vault_backend_primary" {
  bucket = aws_s3_bucket.vault_backend_primary.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.primary.arn
    }
  }
}

resource "aws_s3_bucket_public_access_block" "vault_backend_primary" {
  bucket = aws_s3_bucket.vault_backend_primary.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Secondary region S3 bucket
resource "aws_s3_bucket" "vault_backend_secondary" {
  provider = aws.secondary
  bucket   = "openfireblocks-vault-backend-${var.secondary_region}-${data.aws_caller_identity.current.account_id}"

  tags = {
    Name = "openfireblocks-vault-secondary"
  }
}

resource "aws_s3_bucket_versioning" "vault_backend_secondary" {
  provider = aws.secondary
  bucket   = aws_s3_bucket.vault_backend_secondary.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "vault_backend_secondary" {
  provider = aws.secondary
  bucket   = aws_s3_bucket.vault_backend_secondary.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.secondary.arn
    }
  }
}

resource "aws_s3_bucket_public_access_block" "vault_backend_secondary" {
  provider = aws.secondary
  bucket   = aws_s3_bucket.vault_backend_secondary.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ECS Cluster for Temporal and OpenFireblocks Services (Primary)
module "ecs_primary" {
  source = "./modules/ecs"

  providers = {
    aws = aws
  }

  environment           = var.environment
  cluster_name          = "openfireblocks-primary"
  vpc_id                = module.vpc_primary.vpc_id
  private_subnet_ids    = module.vpc_primary.private_subnet_ids
  public_subnet_ids     = module.vpc_primary.public_subnet_ids
  security_group_ids    = [aws_security_group.ecs_primary.id]
  enable_container_insights = true
  cloudwatch_log_group_name  = aws_cloudwatch_log_group.ecs_primary.name

  tags = {
    Name = "openfireblocks-primary-ecs"
  }
}

# ECS Cluster for Secondary Region
module "ecs_secondary" {
  source = "./modules/ecs"

  providers = {
    aws = aws.secondary
  }

  environment           = var.environment
  cluster_name          = "openfireblocks-secondary"
  vpc_id                = module.vpc_secondary.vpc_id
  private_subnet_ids    = module.vpc_secondary.private_subnet_ids
  public_subnet_ids     = module.vpc_secondary.public_subnet_ids
  security_group_ids    = [aws_security_group.ecs_secondary.id]
  enable_container_insights = true
  cloudwatch_log_group_name  = aws_cloudwatch_log_group.ecs_secondary.name

  tags = {
    Name = "openfireblocks-secondary-ecs"
  }
}

# CloudWatch Log Groups
resource "aws_cloudwatch_log_group" "ecs_primary" {
  name              = "/ecs/openfireblocks-primary"
  retention_in_days = 30

  kms_key_id = aws_kms_key.primary.arn

  tags = {
    Name = "openfireblocks-primary-logs"
  }
}

resource "aws_cloudwatch_log_group" "ecs_secondary" {
  provider          = aws.secondary
  name              = "/ecs/openfireblocks-secondary"
  retention_in_days = 30

  kms_key_id = aws_kms_key.secondary.arn

  tags = {
    Name = "openfireblocks-secondary-logs"
  }
}

# Backup S3 Buckets
resource "aws_s3_bucket" "backups" {
  bucket = "openfireblocks-backups-${var.primary_region}-${data.aws_caller_identity.current.account_id}"

  tags = {
    Name = "openfireblocks-backups"
  }
}

resource "aws_s3_bucket_versioning" "backups" {
  bucket = aws_s3_bucket.backups.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.primary.arn
    }
  }
}

resource "aws_s3_bucket_public_access_block" "backups" {
  bucket = aws_s3_bucket.backups.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Cross-region replication for backups
resource "aws_s3_bucket" "backups_secondary" {
  provider = aws.secondary
  bucket   = "openfireblocks-backups-${var.secondary_region}-${data.aws_caller_identity.current.account_id}"

  tags = {
    Name = "openfireblocks-backups-secondary"
  }
}

# Outputs
output "primary_vpc_id" {
  value       = module.vpc_primary.vpc_id
  description = "Primary region VPC ID"
}

output "secondary_vpc_id" {
  value       = module.vpc_secondary.vpc_id
  description = "Secondary region VPC ID"
}

output "primary_rds_endpoint" {
  value       = module.rds_primary.db_instance_endpoint
  description = "Primary RDS endpoint"
}

output "secondary_rds_endpoint" {
  value       = module.rds_secondary.db_instance_endpoint
  description = "Secondary RDS replica endpoint"
}

output "vault_primary_security_group_id" {
  value       = aws_security_group.vault_primary.id
  description = "Primary Vault security group"
}

output "ecs_primary_cluster_name" {
  value       = module.ecs_primary.cluster_name
  description = "Primary ECS cluster name"
}

output "ecs_secondary_cluster_name" {
  value       = module.ecs_secondary.cluster_name
  description = "Secondary ECS cluster name"
}

output "backup_bucket_name" {
  value       = aws_s3_bucket.backups.id
  description = "Backup S3 bucket name"
}

output "backup_secondary_bucket_name" {
  value       = aws_s3_bucket.backups_secondary.id
  description = "Secondary backup S3 bucket name"
}
