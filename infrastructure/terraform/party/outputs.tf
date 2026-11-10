output "party_id" {
  value = module.party.party_id
}

output "account_id" {
  value = module.party.account_id
}

output "public_ip" {
  description = "The only thing to hand the other operators and the platform."
  value       = module.party.public_ip
}

output "ceremony_endpoint" {
  value = module.party.ceremony_endpoint
}

output "health_endpoint" {
  value = module.party.health_endpoint
}

output "instance_id" {
  value = module.party.instance_id
}

output "tls_secret_arn" {
  value = module.party.tls_secret_arn
}

output "vault_token_secret_arn" {
  value = module.party.vault_token_secret_arn
}

output "host_role_arn" {
  value = module.party.host_role_arn
}

output "share_key_arn" {
  description = "Enclave mode: bake into the image as this party's entry in ENCLAVE_KMS_KEYS."
  value       = module.party.share_key_arn
}

output "share_key_replica_arn" {
  value = module.party.share_key_replica_arn
}

output "share_key_alerts_topic_arn" {
  value = module.party.share_key_alerts_topic_arn
}
