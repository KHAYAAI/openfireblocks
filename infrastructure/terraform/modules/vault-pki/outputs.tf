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
