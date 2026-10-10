output "db_instance_id" {
  description = "RDS replica instance ID"
  value       = aws_db_instance.replica.id
}

output "db_instance_endpoint" {
  description = "RDS replica endpoint (hostname:port)"
  value       = aws_db_instance.replica.endpoint
}

output "db_instance_address" {
  description = "RDS replica address (hostname only)"
  value       = aws_db_instance.replica.address
}

output "db_instance_port" {
  description = "RDS replica port"
  value       = aws_db_instance.replica.port
}

output "db_instance_arn" {
  description = "RDS replica ARN"
  value       = aws_db_instance.replica.arn
}

output "cloudwatch_log_group_name" {
  description = "CloudWatch log group name"
  value       = aws_cloudwatch_log_group.postgres.name
}

output "connection_string" {
  description = "PostgreSQL replica connection string (read-only)"
  value       = "postgres://readonly:***@${aws_db_instance.replica.address}:${aws_db_instance.replica.port}/openfireblocks?sslmode=require"
  sensitive   = true
}
