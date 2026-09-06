output "vault_lb_dns_name" {
  description = "Vault Network Load Balancer DNS name"
  value       = aws_lb.vault.dns_name
}

output "vault_lb_arn" {
  description = "Vault Network Load Balancer ARN"
  value       = aws_lb.vault.arn
}

output "vault_asg_name" {
  description = "Vault Auto Scaling Group name"
  value       = aws_autoscaling_group.vault.name
}

output "vault_target_group_arn" {
  description = "Vault target group ARN"
  value       = aws_lb_target_group.vault.arn
}

output "vault_certificate_pem" {
  description = "PEM certificate for the self-signed cert every Vault node presents on its listener (import into a client trust store to verify TLS without -tls-skip-verify)"
  value       = tls_self_signed_cert.vault.cert_pem
}

output "cloudwatch_log_group_name" {
  description = "CloudWatch log group for Vault"
  value       = aws_cloudwatch_log_group.vault.name
}

output "vault_api_endpoint" {
  description = "Vault API endpoint"
  value       = "https://${aws_lb.vault.dns_name}:8200"
}

output "vault_cluster_endpoint" {
  description = "Vault cluster endpoint"
  value       = "https://${aws_lb.vault.dns_name}:8201"
}
