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

output "host_role_arn" {
  description = "IAM role of the parent instance. Its PCR3 measurement is what the other parties pin for this party (ENCLAVE_PARTY_PCR3); the enclave reports the value in its start-up report."
  value       = aws_iam_role.host.arn
}

output "share_key_arn" {
  description = "Enclave mode: the KMS key the enclave seals this party's shares with (ENCLAVE_KMS_KEYS). Null without enclave mode."
  value       = var.enclave_mode ? aws_kms_key.shares[0].arn : null
}

output "share_key_replica_arn" {
  description = "Enclave mode: the replica of the share key, for a rebuild in the replica region. Null without one."
  value       = var.enclave_mode && var.share_key_replica_region != "" ? aws_kms_replica_key.shares[0].arn : null
}

output "share_key_alerts_topic_arn" {
  description = "Enclave mode: SNS topic alerted when the share key's policy, deletion or state changes."
  value       = var.enclave_mode ? aws_sns_topic.share_key_alerts[0].arn : null
}
