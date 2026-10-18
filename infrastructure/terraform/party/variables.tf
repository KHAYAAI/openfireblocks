variable "region" {
  description = "AWS region for this party. Different regions per party is good; different accounts is what matters."
  type        = string
}

variable "expected_account_id" {
  description = "The AWS account this party must be deployed into. The provider refuses any other."
  type        = string
}

variable "party_id" {
  description = "1, 2 or 3."
  type        = number
  validation {
    condition     = var.party_id >= 1 && var.party_id <= 9 && floor(var.party_id) == var.party_id
    error_message = "party_id must be a whole number from 1 to 9."
  }
}

variable "party_image" {
  description = "mpc-party image, pinned by digest in production (…/mpc-party@sha256:…)."
  type        = string
}

variable "vault_image" {
  type    = string
  default = "hashicorp/vault:1.17.6"
}

variable "instance_type" {
  type    = string
  default = "m6i.large"
}

variable "peer_party_cidrs" {
  description = "The other parties' public_ip outputs, as /32s."
  type        = list(string)
  validation {
    condition     = length(var.peer_party_cidrs) >= 1 && alltrue([for c in var.peer_party_cidrs : can(cidrhost(c, 0)) && endswith(c, "/32")])
    error_message = "peer_party_cidrs must be single addresses (/32): a party accepts ceremony traffic from named peers, not from ranges."
  }
}

variable "platform_cidrs" {
  description = "The platform's egress addresses (orchestrator and gateway), as /32s."
  type        = list(string)
  validation {
    condition     = length(var.platform_cidrs) >= 1 && alltrue([for c in var.platform_cidrs : can(cidrhost(c, 0)) && endswith(c, "/32")])
    error_message = "platform_cidrs must be single addresses (/32)."
  }
}

variable "ceremony_authorizer_pubkey" {
  description = "Ceremony co-signer public key. Required: a party reachable over the internet should not sign for an orchestrator alone."
  type        = string
  validation {
    condition     = length(var.ceremony_authorizer_pubkey) > 0
    error_message = "Set ceremony_authorizer_pubkey. A party on the public internet without ceremony co-signing will sign for anything that can reach it with a valid certificate."
  }
}
