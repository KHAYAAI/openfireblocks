# Variable contract for main.tf / security.tf / waf.tf / outputs.tf. Matches
# terraform.tfvars.example — see that file for a filled-in starting point.

variable "primary_region" {
  description = "Primary AWS region"
  type        = string
  default     = "us-east-1"
}

variable "secondary_region" {
  description = "Secondary (DR/failover) AWS region"
  type        = string
  default     = "eu-west-1"
}

variable "environment" {
  description = "Environment name (prod, staging, dev)"
  type        = string
  default     = "prod"
}

variable "primary_vpc_cidr" {
  description = "CIDR block for the primary region VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "secondary_vpc_cidr" {
  description = "CIDR block for the secondary region VPC"
  type        = string
  default     = "10.1.0.0/16"
}

variable "enable_vpn_gateway" {
  description = "Attach a VPN gateway to each VPC for site-to-site access"
  type        = bool
  default     = false
}

variable "db_name" {
  description = "Initial database name"
  type        = string
  default     = "openfireblocks"
}

variable "db_username" {
  description = "Master database username"
  type        = string
  default     = "postgres"
}

variable "db_password" {
  description = "Master database password (min 16 chars). No default: must be supplied via terraform.tfvars or TF_VAR_db_password, never committed."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.db_password) >= 16
    error_message = "db_password must be at least 16 characters."
  }
}

variable "db_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.r6i.xlarge"
}

variable "db_allocated_storage" {
  description = "Initial RDS allocated storage, in GB"
  type        = number
  default     = 100
}

variable "db_max_allocated_storage" {
  description = "Ceiling for RDS storage autoscaling, in GB"
  type        = number
  default     = 500
}

variable "vault_node_count" {
  description = "Number of Vault instances per region"
  type        = number
  default     = 3
}

variable "vault_instance_type" {
  description = "EC2 instance type for Vault nodes"
  type        = string
  default     = "t3.large"
}

variable "backup_retention_days" {
  description = "RDS automated backup retention, in days"
  type        = number
  default     = 30
}

variable "backup_window" {
  description = "Preferred RDS backup window (UTC)"
  type        = string
  default     = "00:00-01:00"
}

variable "maintenance_window" {
  description = "Preferred RDS maintenance window (UTC)"
  type        = string
  default     = "sun:01:00-sun:02:00"
}

variable "cloudwatch_log_retention_days" {
  description = "CloudWatch Logs retention, in days"
  type        = number
  default     = 30
}

variable "enable_performance_insights" {
  description = "Enable RDS Performance Insights"
  type        = bool
  default     = true
}

variable "enable_enhanced_monitoring" {
  description = "Enable RDS Enhanced Monitoring"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional resource tags merged into every resource's tag set"
  type        = map(string)
  default = {
    Project   = "OpenFireblocks"
    ManagedBy = "Terraform"
  }
}
