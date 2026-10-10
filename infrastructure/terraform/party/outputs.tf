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
