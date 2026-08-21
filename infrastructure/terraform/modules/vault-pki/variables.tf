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
