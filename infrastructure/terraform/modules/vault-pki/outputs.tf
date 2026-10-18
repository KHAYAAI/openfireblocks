output "pki_backend_path" {
  description = "Mount path of the PKI secrets engine (e.g. \"pki/production\")"
  value       = vault_mount.pki.path
}

output "role_name" {
  description = "PKI role name services request certificates from"
  value       = vault_pki_secret_backend_role.internal_service.name
}

output "ca_certificate_pem" {
  description = "The CA's own certificate (PEM) -- distribute to every service as MTLS_CA_FILE"
  value       = vault_pki_secret_backend_root_cert.ca.certificate
}

output "kubernetes_auth_role" {
  description = "Vault Kubernetes auth role name pods authenticate as (VAULT_K8S_ROLE for services/vault-pki-init). Null when kubernetes_auth_enabled is false."
  value       = var.kubernetes_auth_enabled ? vault_kubernetes_auth_backend_role.internal_service[0].role_name : null
}
