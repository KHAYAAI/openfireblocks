variable "environment" {
  description = "Environment name"
  type        = string
}

variable "region" {
  description = "AWS region this Vault cluster runs in (used in vault.hcl for the S3 storage backend and awskms seal, not just the provider block)"
  type        = string
}

variable "vault_instance_count" {
  description = "Number of Vault nodes in HA cluster"
  type        = number
  default     = 3
}

variable "vault_instance_type" {
  description = "EC2 instance type for Vault"
  type        = string
  default     = "t3.large"
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "private_subnet_ids" {
  description = "List of private subnet IDs for Vault nodes"
  type        = list(string)
}

variable "security_group_ids" {
  description = "Security group IDs for Vault instances"
  type        = list(string)
}

variable "kms_key_id" {
  description = "KMS key ID for Vault auto-unseal"
  type        = string
}

variable "s3_bucket_name" {
  description = "S3 bucket name for Vault storage"
  type        = string
}

variable "instance_profile_name" {
  description = "IAM instance profile name for Vault instances"
  type        = string
}

variable "enable_s3_backend" {
  description = "Enable S3 backend for Vault storage"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
