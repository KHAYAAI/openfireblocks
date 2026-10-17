variable "party_id" {
  description = "This party's number in the committee (1, 2, 3 ...). Must match the id the platform addresses it by."
  type        = number
}

variable "name_prefix" {
  description = "Prefix for resource names."
  type        = string
  default     = "ofb-party"
}

variable "party_image" {
  description = "The mpc-party container image, from a registry this account can pull (e.g. this account's ECR)."
  type        = string
}

variable "vault_image" {
  description = "The Vault container image for this party's own Vault."
  type        = string
  default     = "hashicorp/vault:1.17.6"
}

variable "instance_type" {
  description = "EC2 instance type. Nitro-based, so the same host could later run the party in a Nitro Enclave."
  type        = string
  default     = "m6i.large"
}

variable "vpc_cidr" {
  description = "CIDR for this party's own VPC. Parties share no network; this only needs to not collide inside this account."
  type        = string
  default     = "10.77.0.0/24"
}

variable "peer_party_cidrs" {
  description = "The public addresses (/32) of the OTHER parties. Only they may reach this party's ceremony port."
  type        = list(string)
}

variable "platform_cidrs" {
  description = "Egress addresses of the platform (the orchestrator that drives ceremonies, and the gateway that probes health)."
  type        = list(string)
}

variable "ceremony_port" {
  description = "mTLS port for ceremony traffic."
  type        = number
  default     = 7000
}

variable "health_port" {
  description = "Plaintext liveness-only port (GET /health, nothing else)."
  type        = number
  default     = 7001
}

variable "ceremony_authorizer_pubkey" {
  description = "Public key of the ceremony co-signer (CEREMONY_AUTHORIZER_PUBKEY). Empty disables co-signing, which production should not."
  type        = string
  default     = ""
}

variable "data_volume_gb" {
  description = "Size of the encrypted volume holding this party's Vault (and so its key shares)."
  type        = number
  default     = 20
}

variable "tags" {
  description = "Extra tags."
  type        = map(string)
  default     = {}
}
