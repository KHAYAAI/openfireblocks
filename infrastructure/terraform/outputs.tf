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
  description = "Primary RDS endpoint (hostname:port)"
}

output "primary_rds_address" {
  value       = module.rds_primary.db_instance_address
  description = "Primary RDS hostname"
}

output "secondary_rds_endpoint" {
  value       = module.rds_secondary.db_instance_endpoint
  description = "Secondary RDS replica endpoint (hostname:port)"
}

output "secondary_rds_address" {
  value       = module.rds_secondary.db_instance_address
  description = "Secondary RDS replica hostname"
}

output "primary_vault_endpoint" {
  value       = module.vault_primary.vault_api_endpoint
  description = "Primary Vault API endpoint"
}

output "secondary_vault_endpoint" {
  value       = module.vault_secondary.vault_api_endpoint
  description = "Secondary Vault API endpoint"
}

output "primary_ecs_cluster_name" {
  value       = module.ecs_primary.cluster_name
  description = "Primary ECS cluster name"
}

output "secondary_ecs_cluster_name" {
  value       = module.ecs_secondary.cluster_name
  description = "Secondary ECS cluster name"
}

output "primary_vault_security_group_id" {
  value       = aws_security_group.vault_primary.id
  description = "Primary Vault security group ID"
}

output "secondary_vault_security_group_id" {
  value       = aws_security_group.vault_secondary.id
  description = "Secondary Vault security group ID"
}

output "backup_bucket_name" {
  value       = aws_s3_bucket.backups.id
  description = "Backup S3 bucket name (primary region)"
}

output "backup_secondary_bucket_name" {
  value       = aws_s3_bucket.backups_secondary.id
  description = "Backup S3 bucket name (secondary region)"
}

output "vault_primary_backend_bucket" {
  value       = aws_s3_bucket.vault_backend_primary.id
  description = "Primary Vault S3 backend bucket"
}

output "vault_secondary_backend_bucket" {
  value       = aws_s3_bucket.vault_backend_secondary.id
  description = "Secondary Vault S3 backend bucket"
}

output "primary_kms_key_id" {
  value       = aws_kms_key.primary.id
  description = "Primary region KMS key ID"
}

output "secondary_kms_key_id" {
  value       = aws_kms_key.secondary.id
  description = "Secondary region KMS key ID"
}

output "vpc_peering_connection_id" {
  value       = aws_vpc_peering_connection.primary_to_secondary.id
  description = "VPC peering connection ID"
}

output "deployment_summary" {
  value = {
    primary_region        = var.primary_region
    secondary_region      = var.secondary_region
    environment           = var.environment
    database_name         = var.db_name
    backup_retention_days = var.backup_retention_days
    vault_node_count      = var.vault_node_count
    description           = "OpenFireblocks multi-region HA infrastructure deployed successfully"
  }
  description = "Deployment summary"
}
