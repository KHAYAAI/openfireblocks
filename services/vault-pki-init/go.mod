// vault-pki-init is a small standalone binary meant to run as a Kubernetes
// init container: it requests a short-lived leaf certificate from Vault's
// PKI secrets engine (infrastructure/terraform/modules/vault-pki) and
// writes it to a shared volume the main container reads its
// MTLS_CERT_FILE/MTLS_KEY_FILE/MTLS_CA_FILE env vars from -- automating
// the per-service cert issuance that, before this, required a human or an
// External Secrets Operator integration to populate a Kubernetes Secret by
// hand (see infrastructure/helm/openfireblocks/templates/secret.yaml's
// doc comment).
module forge-crypto/vault-pki-init

go 1.24
