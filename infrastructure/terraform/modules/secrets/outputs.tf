output "secret_arn" {
  description = "ARN of the app secrets Secrets Manager secret"
  value       = aws_secretsmanager_secret.app.arn
}

output "secret_name" {
  description = "Name of the app secrets Secrets Manager secret"
  value       = aws_secretsmanager_secret.app.name
}
