variable "primary_region" {
  description = "Primary AWS region for OpenFireblocks deployment"
  type        = string
  default     = "us-east-1"
}

variable "secondary_region" {
  description = "Secondary AWS region for disaster recovery and failover"
  type        = string
  default     = "eu-west-1"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "prod"
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "Environment must be dev, staging, or prod."
  }
}

# VPC Configuration
variable "primary_vpc_cidr" {
  description = "CIDR block for primary VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "secondary_vpc_cidr" {
  description = "CIDR block for secondary VPC"
  type        = string
  default     = "10.1.0.0/16"
}

variable "enable_vpn_gateway" {
  description = "Enable VPN gateway for site-to-site connectivity"
  type        = bool
  default     = false
}

# RDS Configuration
variable "db_name" {
  description = "PostgreSQL database name"
  type        = string
  default     = "openfireblocks"
  sensitive   = false
}

variable "db_username" {
  description = "PostgreSQL master username"
  type        = string
  default     = "postgres"
  sensitive   = true
}

variable "db_password" {
  description = "PostgreSQL master password (should be at least 16 characters)"
  type        = string
  sensitive   = true
  validation {
    condition     = length(var.db_password) >= 16
    error_message = "Database password must be at least 16 characters long."
  }
}

variable "db_instance_class" {
  description = "RDS instance type"
  type        = string
  default     = "db.r6i.xlarge"
}

variable "db_allocated_storage" {
  description = "Initial allocated storage in GB"
  type        = number
  default     = 100
}

variable "db_max_allocated_storage" {
  description = "Maximum allocated storage for autoscaling in GB"
  type        = number
  default     = 500
}

# Vault Configuration
variable "vault_node_count" {
  description = "Number of Vault nodes in HA cluster (should be odd: 1, 3, 5, 7)"
  type        = number
  default     = 3
  validation {
    condition     = contains([1, 3, 5, 7], var.vault_node_count)
    error_message = "Vault node count must be odd (1, 3, 5, or 7)."
  }
}

variable "vault_instance_type" {
  description = "EC2 instance type for Vault nodes"
  type        = string
  default     = "t3.large"
}

# Temporal Configuration
variable "temporal_worker_count" {
  description = "Number of Temporal worker nodes"
  type        = number
  default     = 3
}

variable "temporal_instance_type" {
  description = "EC2 instance type for Temporal workers"
  type        = string
  default     = "t3.large"
}

# Application Configuration
variable "api_gateway_instance_count" {
  description = "Number of API Gateway instances"
  type        = number
  default     = 3
}

variable "api_gateway_instance_type" {
  description = "EC2 instance type for API Gateway"
  type        = string
  default     = "t3.xlarge"
}

# Backup Configuration
variable "backup_retention_days" {
  description = "Number of days to retain automated backups"
  type        = number
  default     = 30
}

variable "backup_window" {
  description = "Time window for daily backups (UTC)"
  type        = string
  default     = "00:00-01:00"
}

variable "maintenance_window" {
  description = "Time window for maintenance operations (UTC)"
  type        = string
  default     = "sun:01:00-sun:02:00"
}

# Monitoring Configuration
variable "cloudwatch_log_retention_days" {
  description = "CloudWatch log retention period in days"
  type        = number
  default     = 30
}

variable "enable_performance_insights" {
  description = "Enable RDS Performance Insights"
  type        = bool
  default     = true
}

variable "enable_enhanced_monitoring" {
  description = "Enable enhanced RDS monitoring"
  type        = bool
  default     = true
}

# Tagging
variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default = {
    Project     = "OpenFireblocks"
    ManagedBy   = "Terraform"
  }
}
