# OpenFireblocks AWS Infrastructure
# Production Multi-Region Deployment with HA

terraform {
  required_version = ">= 1.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    vault = {
      source  = "hashicorp/vault"
      version = "~> 4.0"
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

# Vault provider, for modules/vault-pki (only instantiated when
# var.vault_pki_enabled is true -- see that variable's description).
# Address always points at the primary Vault cluster's real LB endpoint,
# not a placeholder: module.vault_primary is unconditional, so this is
# always a real value, it just goes unused while vault_pki_enabled is
# false since nothing references the vault provider in that case.
provider "vault" {
  address = module.vault_primary.vault_api_endpoint
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
  vpc_id      = module.vpc_primary.vpc_id
  peer_vpc_id = module.vpc_secondary.vpc_id
  peer_region = var.secondary_region
  auto_accept = false

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

  environment                 = var.environment
  db_name                     = var.db_name
  db_username                 = var.db_username
  db_password                 = var.db_password
  db_instance_class           = var.db_instance_class
  allocated_storage           = var.db_allocated_storage
  max_allocated_storage       = var.db_max_allocated_storage
  vpc_id                      = module.vpc_primary.vpc_id
  db_subnet_group_name        = module.vpc_primary.db_subnet_group_name
  multi_az                    = true
  backup_retention_days       = var.backup_retention_days
  backup_window               = var.backup_window
  maintenance_window          = var.maintenance_window
  enable_performance_insights = var.enable_performance_insights
  enable_enhanced_monitoring  = var.enable_enhanced_monitoring
  kms_key_id                  = aws_kms_key.primary.arn
  publicly_accessible         = false
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

  environment                 = var.environment
  source_db_instance_id       = module.rds_primary.db_instance_id
  replica_instance_class      = var.db_instance_class
  vpc_id                      = module.vpc_secondary.vpc_id
  db_subnet_group_name        = module.vpc_secondary.db_subnet_group_name
  enable_performance_insights = true
  enable_enhanced_monitoring  = true
  kms_key_id                  = aws_kms_key.secondary.arn
  publicly_accessible         = false

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

  environment           = var.environment
  region                = var.primary_region
  vault_instance_count  = var.vault_node_count
  vault_instance_type   = var.vault_instance_type
  vpc_id                = module.vpc_primary.vpc_id
  private_subnet_ids    = module.vpc_primary.private_subnet_ids
  security_group_ids    = [aws_security_group.vault_primary.id]
  kms_key_id            = aws_kms_key.primary.id
  enable_s3_backend     = true
  s3_bucket_name        = aws_s3_bucket.vault_backend_primary.id
  instance_profile_name = aws_iam_instance_profile.vault_primary.name

  tags = {
    Name = "openfireblocks-primary-vault"
  }

  depends_on = [
    aws_s3_bucket.vault_backend_primary,
    aws_iam_role_policy.vault_primary_policy
  ]
}

# Service-to-service mTLS CA (services/mpc-party <-> services/temporal-worker's
# DKG round-relay transport). Off by default -- see vault_pki_enabled's
# description: Vault has to be manually initialized and unsealed first,
# which is true of every module.vault_primary deployment until someone does
# that by hand, so this can't safely default to on.
module "vault_pki" {
  source = "./modules/vault-pki"
  count  = var.vault_pki_enabled ? 1 : 0

  providers = {
    vault = vault
  }

  environment     = var.environment
  allowed_domains = var.mtls_allowed_domains

  kubernetes_auth_enabled = var.vault_kubernetes_auth_enabled
  kubernetes_host         = var.vault_kubernetes_host
  kubernetes_ca_cert      = var.vault_kubernetes_ca_cert
}

# Vault Cluster Module (Secondary)
module "vault_secondary" {
  source = "./modules/vault"

  providers = {
    aws = aws.secondary
  }

  environment           = var.environment
  region                = var.secondary_region
  vault_instance_count  = var.vault_node_count
  vault_instance_type   = var.vault_instance_type
  vpc_id                = module.vpc_secondary.vpc_id
  private_subnet_ids    = module.vpc_secondary.private_subnet_ids
  security_group_ids    = [aws_security_group.vault_secondary.id]
  kms_key_id            = aws_kms_key.secondary.id
  enable_s3_backend     = true
  s3_bucket_name        = aws_s3_bucket.vault_backend_secondary.id
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

  environment               = var.environment
  cluster_name              = "openfireblocks-primary"
  vpc_id                    = module.vpc_primary.vpc_id
  private_subnet_ids        = module.vpc_primary.private_subnet_ids
  public_subnet_ids         = module.vpc_primary.public_subnet_ids
  security_group_ids        = [aws_security_group.ecs_primary.id]
  kms_key_id                = aws_kms_key.primary.arn
  enable_container_insights = true
  cloudwatch_log_group_name = aws_cloudwatch_log_group.ecs_primary.name

  tags = {
    Name = "openfireblocks-primary-ecs"
  }
}

# Application secrets (DB credentials, JWT signing key, admin API key) in
# Secrets Manager, generated by Terraform rather than hand-typed -- see
# modules/secrets. Wired to the primary ECS cluster's task execution role
# only; ecs_secondary would need its own instance of this module (its own
# random credentials, or a cross-region secret replica) before any service
# actually runs there -- not yet wired, since no ECS service/task
# definition exists for either region yet (see the audit checklist).
module "app_secrets_primary" {
  source = "./modules/secrets"

  environment                  = var.environment
  db_address                   = module.rds_primary.db_instance_address
  db_port                      = module.rds_primary.db_instance_port
  db_name                      = var.db_name
  kms_key_id                   = aws_kms_key.primary.arn
  ecs_task_execution_role_name = module.ecs_primary.task_execution_role_name

  tags = {
    Name = "openfireblocks-primary-app-secrets"
  }
}

# ECS Cluster for Secondary Region
module "ecs_secondary" {
  source = "./modules/ecs"

  providers = {
    aws = aws.secondary
  }

  environment               = var.environment
  cluster_name              = "openfireblocks-secondary"
  vpc_id                    = module.vpc_secondary.vpc_id
  private_subnet_ids        = module.vpc_secondary.private_subnet_ids
  public_subnet_ids         = module.vpc_secondary.public_subnet_ids
  security_group_ids        = [aws_security_group.ecs_secondary.id]
  kms_key_id                = aws_kms_key.secondary.arn
  enable_container_insights = true
  cloudwatch_log_group_name = aws_cloudwatch_log_group.ecs_secondary.name

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

# Outputs live in outputs.tf (kept as the single source of truth to avoid
# duplicate-output errors between the two files).
