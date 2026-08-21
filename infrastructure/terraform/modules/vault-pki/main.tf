terraform {
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "~> 4.0"
    }
  }
}

# The vault provider is configured once, by the caller (see main.tf's
# `provider "vault"` block and this module's `providers = { vault = vault }`
# argument) -- a module that declares its own local provider block can't be
# used with `count`, and this one needs count to stay off by default until
# Vault is manually initialized/unsealed (see vault_pki_enabled's
# description in the root variables.tf).

# Mutual TLS for the DKG round-relay transport (services/mpc-party <->
# services/temporal-worker's DKGRoundCoordinator -- see the doc comments in
# services/mpc-party/mtls.go and services/temporal-worker/activities/mtls.go
# for why this specific link). Vault's PKI secrets engine issues short-lived
# leaf certificates on demand rather than long-lived certs that sit on disk
# accumulating risk; every service requests a fresh one at startup.
resource "vault_mount" "pki" {
  path                      = "pki/${var.environment}"
  type                      = "pki"
  description               = "Service-to-service mTLS CA for OpenFireblocks internal transport"
  max_lease_ttl_seconds     = 86400 * 30 # 30 days -- bounds the root's own validity, not issued certs (see max_lease_ttl)
  default_lease_ttl_seconds = 86400
}

resource "vault_pki_secret_backend_root_cert" "ca" {
  backend              = vault_mount.pki.path
  type                 = "internal" # private key never leaves Vault
  common_name          = "openfireblocks-${var.environment}-internal-ca"
  ttl                  = "8760h" # 1 year
  key_type             = "rsa"
  key_bits             = 4096
  exclude_cn_from_sans = true
}

# The role every internal service requests a cert from, e.g.:
#   vault write pki/${environment}/issue/internal-service common_name=party-1.internal
# A real deployment's service startup/init-container does this, writes the
# resulting cert/key/ca_chain to the paths services/mpc-party's mtls.go and
# services/temporal-worker/activities/mtls.go read from
# (MTLS_CERT_FILE/MTLS_KEY_FILE/MTLS_CA_FILE).
resource "vault_pki_secret_backend_role" "internal_service" {
  backend            = vault_mount.pki.path
  name               = "internal-service"
  allowed_domains    = var.allowed_domains
  allow_subdomains   = false
  allow_bare_domains = true
  max_ttl            = var.max_lease_ttl
  key_type           = "rsa"
  key_bits           = 2048
  client_flag        = true
  server_flag        = true
}
