variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "prod"
}

variable "vpc_cidr" {
  description = "CIDR block for VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "Availability zones"
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b", "us-east-1c"]
}

variable "db_username" {
  description = "Database username"
  type        = string
  default     = "postgres"
}

variable "db_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.large"
}

variable "api_gateway_image" {
  description = "Docker image for API gateway"
  type        = string
}

variable "ethereum_rpc_url" {
  description = "Ethereum RPC URL"
  type        = string
  sensitive   = true
}

variable "bitcoin_rpc_url" {
  description = "Bitcoin RPC URL"
  type        = string
  sensitive   = true
}

variable "onfido_api_key" {
  description = "Onfido API key"
  type        = string
  sensitive   = true
}

variable "workos_api_key" {
  description = "WorkOS API key"
  type        = string
  sensitive   = true
}
