output "party_id" {
  value = var.party_id
}

output "account_id" {
  description = "The account this party runs in. Collect all parties' values into OFB_PARTY_ACCOUNTS for party-isolation-check.sh."
  value       = data.aws_caller_identity.this.account_id
}

output "public_ip" {
  description = "Give this to the other parties' operators (their peer_party_cidrs) and the platform."
  value       = aws_eip.party.public_ip
}

output "ceremony_endpoint" {
  value = "https://${aws_eip.party.public_ip}:${var.ceremony_port}"
}

output "health_endpoint" {
  value = "http://${aws_eip.party.public_ip}:${var.health_port}/health"
}

output "instance_id" {
  description = "For SSM sessions: aws ssm start-session --target <this>"
  value       = aws_instance.party.id
}

output "tls_secret_arn" {
  value = aws_secretsmanager_secret.tls.arn
}

output "vault_token_secret_arn" {
  value = aws_secretsmanager_secret.vault_token.arn
}
