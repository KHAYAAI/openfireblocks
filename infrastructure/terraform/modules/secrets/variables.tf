variable "environment" {
  description = "Environment name (e.g. production, staging)"
  type        = string
}

variable "db_address" {
  description = "RDS instance address (hostname only), e.g. module.rds_primary.db_instance_address"
  type        = string
}

variable "db_port" {
  description = "RDS instance port, e.g. module.rds_primary.db_instance_port"
  type        = number
}

variable "db_name" {
  description = "Database name"
  type        = string
}

variable "kms_key_id" {
  description = "KMS key ARN used to encrypt the secret at rest"
  type        = string
}

variable "ecs_task_execution_role_name" {
  description = "Name (not ARN) of the ECS task execution role to grant read access to this secret -- see modules/ecs's task_execution_role_name output. This must be the execution role, not the task role: ECS uses the execution role to resolve a container definition's `secrets` block at container start."
  type        = string
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
