variable "environment" {
  description = "Environment name (e.g. production, staging)"
  type        = string
}

variable "max_lease_ttl" {
  description = "Maximum TTL for issued service certificates (Vault duration format, e.g. \"24h\")"
  type        = string
  default     = "24h"
}

variable "allowed_domains" {
  description = "Domains/hostnames the PKI role is allowed to issue certificates for -- the internal service names services actually connect to, e.g. [\"party-1.internal\", \"party-2.internal\", \"temporal-worker.internal\"]. Keep this as narrow as the real service list; it is not a wildcard by default."
  type        = list(string)
}

variable "kubernetes_auth_enabled" {
  description = "Whether to configure Vault's Kubernetes auth method so pods can authenticate with their own ServiceAccount JWT (via services/vault-pki-init) instead of a static VAULT_TOKEN. Requires a real cluster's API server CA/host to be reachable and correct -- see kubernetes_host/kubernetes_ca_cert. Defaults to false for the same reason vault_pki_enabled does: not safe to default on until someone has actually supplied real cluster values."
  type        = bool
  default     = false
}

variable "kubernetes_host" {
  description = "Kubernetes API server URL Vault validates TokenReview calls against, e.g. \"https://kubernetes.default.svc:443\" for an in-cluster Vault. Only used when kubernetes_auth_enabled is true."
  type        = string
  default     = "https://kubernetes.default.svc:443"
}

variable "kubernetes_ca_cert" {
  description = "PEM-encoded CA certificate of the Kubernetes API server referenced by kubernetes_host. Only used when kubernetes_auth_enabled is true; required (no safe default -- a wrong or placeholder CA would silently accept the wrong cluster)."
  type        = string
  default     = ""
}

variable "kubernetes_token_reviewer_jwt" {
  description = "JWT Vault itself uses to call the Kubernetes TokenReview API. Leave empty when Vault runs inside the target cluster and can use its own pod's mounted ServiceAccount token automatically (disable_local_ca_jwt is derived from this being empty). Only used when kubernetes_auth_enabled is true."
  type        = string
  default     = ""
  sensitive   = true
}

variable "kubernetes_service_account_names" {
  description = "ServiceAccount name(s) allowed to assume the internal-service Kubernetes auth role -- see infrastructure/helm/openfireblocks/templates/serviceaccount.yaml (every workload in that chart shares one ServiceAccount). Only used when kubernetes_auth_enabled is true."
  type        = list(string)
  default     = ["openfireblocks"]
}

variable "kubernetes_service_account_namespaces" {
  description = "Namespace(s) the bound ServiceAccount(s) must live in to assume the internal-service Kubernetes auth role. Only used when kubernetes_auth_enabled is true."
  type        = list(string)
  default     = ["default"]
}
