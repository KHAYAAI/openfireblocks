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

# ---------------------------------------------------------------------------
# Enclave mode (services/mpc-party/enclave_mode.go,
# docs/engineering/NITRO-ENCLAVE-MPC-PARTY.md). Off by default; with it off,
# nothing below changes the host.
# ---------------------------------------------------------------------------

variable "enclave_mode" {
  description = "Run the party in a Nitro Enclave: enclave-enabled instance, attestation-bound KMS key for the shares, host relay instead of the container."
  type        = bool
  default     = false
}

variable "enclave_image_pcr0s" {
  description = "PCR0 of every enclave image this party's KMS key may release shares to. One normally; old and new during an upgrade."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for p in var.enclave_image_pcr0s :
      can(regex("^[0-9a-fA-F]{96}$", p)) && lower(p) != join("", [for i in range(96) : "0"])
    ])
    error_message = "Each PCR0 must be 96 hex characters (SHA-384), and never all zeros: that is what a debug-mode enclave reports."
  }
}

variable "enclave_eif_s3_uri" {
  description = "s3://bucket/key of the enclave image file (EIF), built by infrastructure/nitro/Dockerfile.enclave and nitro-cli build-enclave."
  type        = string
  default     = ""
}

variable "enclave_eif_sha256" {
  description = "SHA-256 of the EIF file. The host refuses a download that does not match, and an EIF whose PCR0 is not in enclave_image_pcr0s."
  type        = string
  default     = ""
}

variable "enclave_host_binary_s3_uri" {
  description = "s3://bucket/key of the mpc-party-host binary (services/mpc-party/cmd/mpc-party-host)."
  type        = string
  default     = ""
}

variable "enclave_host_binary_sha256" {
  description = "SHA-256 of the mpc-party-host binary."
  type        = string
  default     = ""
}

variable "enclave_cpu_count" {
  description = "vCPUs given to the enclave. The parent keeps the rest; the instance needs at least two more than this."
  type        = number
  default     = 2
}

variable "enclave_memory_mib" {
  description = "Memory given to the enclave. tss-lib pre-parameter generation is the large user."
  type        = number
  default     = 4096
}

variable "peer_endpoints" {
  description = "Enclave mode: where to reach each OTHER party, by party number, as host:port (e.g. {\"2\" = \"203.0.113.7:7000\"}). The enclave dials party-N.internal; the host relays to this address."
  type        = map(string)
  default     = {}
}

variable "share_key_replica_region" {
  description = "Enclave mode: region for a replica of the share key (multi-region KMS key), so losing this region does not lose the shares. Empty: no replica."
  type        = string
  default     = ""
}

variable "alert_email" {
  description = "Email for alerts on changes to the share key (policy rewritten, deletion scheduled, key disabled). Needs CloudTrail management events in this account."
  type        = string
  default     = ""
}
