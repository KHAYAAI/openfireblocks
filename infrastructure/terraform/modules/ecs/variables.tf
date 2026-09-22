variable "environment" {
  description = "Environment name"
  type        = string
}

variable "cluster_name" {
  description = "ECS cluster name"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "private_subnet_ids" {
  description = "List of private subnet IDs"
  type        = list(string)
}

variable "public_subnet_ids" {
  description = "List of public subnet IDs"
  type        = list(string)
}

variable "security_group_ids" {
  description = "Security group IDs for ECS instances"
  type        = list(string)
}

variable "instance_type" {
  description = "EC2 instance type for ECS nodes"
  type        = string
  default     = "t3.xlarge"
}

variable "min_capacity" {
  description = "Minimum number of ECS instances"
  type        = number
  default     = 3
}

variable "max_capacity" {
  description = "Maximum number of ECS instances"
  type        = number
  default     = 10
}

variable "desired_capacity" {
  description = "Desired number of ECS instances"
  type        = number
  default     = 3
}

variable "enable_container_insights" {
  description = "Enable CloudWatch Container Insights"
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 30
}

variable "kms_key_id" {
  description = "KMS key ID for log encryption"
  type        = string
}

variable "cloudwatch_log_group_name" {
  description = "CloudWatch log group name"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
