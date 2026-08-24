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

# Kubernetes auth: lets each pod fetch its own leaf cert (via
# services/vault-pki-init, run as an init container -- see
# infrastructure/helm/openfireblocks/templates/mpc-party.yaml and
# temporal-worker.yaml) by presenting its own projected ServiceAccount
# JWT, instead of a long-lived VAULT_TOKEN baked into a Secret. Separate
# toggle from the PKI mount above: a cluster can run PKI issuance with a
# manually-distributed token (VAULT_TOKEN) without ever configuring this,
# and configuring it requires real in-cluster values (the API server's
# CA and a token reviewer JWT) that don't exist outside an actual
# Kubernetes cluster -- see this repo's vault-pki-init doc comment for
# the corresponding "not verifiable without a cluster" disclosure on the
# client side of this same handshake.
resource "vault_auth_backend" "kubernetes" {
  count = var.kubernetes_auth_enabled ? 1 : 0
  type  = "kubernetes"
  path  = "kubernetes"
}

resource "vault_kubernetes_auth_backend_config" "this" {
  count              = var.kubernetes_auth_enabled ? 1 : 0
  backend            = vault_auth_backend.kubernetes[0].path
  kubernetes_host    = var.kubernetes_host
  kubernetes_ca_cert = var.kubernetes_ca_cert
  # Vault calls the Kubernetes TokenReview API as itself to validate a
  # pod's presented JWT; this is Vault's own credential for that call, not
  # a per-service secret. When Vault runs inside the same cluster it can
  # instead use its own pod's mounted SA token automatically -- leave
  # empty in that case.
  token_reviewer_jwt   = var.kubernetes_token_reviewer_jwt
  disable_local_ca_jwt = var.kubernetes_token_reviewer_jwt == ""
}

# Only allows requesting certs from the internal-service PKI role above --
# nothing else. Bound to the chart's single ServiceAccount (see
# infrastructure/helm/openfireblocks/templates/serviceaccount.yaml) since
# every workload in this chart shares one ServiceAccount name; namespace
# scoping is what actually limits which pods can assume this role.
resource "vault_policy" "pki_issue" {
  count  = var.kubernetes_auth_enabled ? 1 : 0
  name   = "${var.environment}-pki-issue-internal-service"
  policy = <<-EOT
    path "${vault_mount.pki.path}/issue/${vault_pki_secret_backend_role.internal_service.name}" {
      capabilities = ["create", "update"]
    }
  EOT
}

resource "vault_kubernetes_auth_backend_role" "internal_service" {
  count                            = var.kubernetes_auth_enabled ? 1 : 0
  backend                          = vault_auth_backend.kubernetes[0].path
  role_name                        = "internal-service"
  bound_service_account_names      = var.kubernetes_service_account_names
  bound_service_account_namespaces = var.kubernetes_service_account_namespaces
  token_policies                   = [vault_policy.pki_issue[0].name]
  token_ttl                        = 900 # 15m -- just long enough for vault-pki-init to run once at pod startup
}
