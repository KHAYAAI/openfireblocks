variable "environment" {
  description = "Environment name"
  type        = string
}

variable "source_db_instance_id" {
  description = "Source DB instance identifier"
  type        = string
}

variable "replica_instance_class" {
  description = "Replica instance type"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "db_subnet_group_name" {
  description = "DB subnet group name"
  type        = string
}

variable "security_group_ids" {
  description = "Security group IDs for RDS replica"
  type        = list(string)
}

variable "publicly_accessible" {
  description = "Make RDS replica publicly accessible"
  type        = bool
  default     = false
}

variable "skip_final_snapshot" {
  description = "Skip final snapshot on deletion"
  type        = bool
  default     = true
}

variable "kms_key_id" {
  description = "KMS key ARN for encryption"
  type        = string
}

variable "enable_performance_insights" {
  description = "Enable Performance Insights"
  type        = bool
  default     = true
}

variable "enable_enhanced_monitoring" {
  description = "Enable enhanced monitoring"
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 30
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
