# One MPC party. Applied once per party, by that party's own operator,
# with that party's own AWS credentials and its own state.
#
# This is a separate root on purpose, not three module calls in one
# configuration. One configuration applying all three parties would need
# one person holding credentials for all three accounts -- the single
# administrator the separation exists to rule out. Each operator runs:
#
#   cd infrastructure/terraform/party
#   cp terraform.tfvars.example party-<n>.tfvars   # fill in
#   terraform init -backend-config=backend-party-<n>.hcl
#   terraform apply -var-file=party-<n>.tfvars
#
# then hands the other operators and the platform only the public_ip
# output. The full procedure is docs/deployment/SEPARATE-HOSTS.md.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  # Each party keeps its own state, in its own account. Configure with
  # -backend-config; see backend.hcl.example.
  backend "s3" {}
}

provider "aws" {
  region = var.region

  # Refuse to apply into an account other than the one this party was
  # assigned. The failure this prevents is quiet and total: an operator
  # with a stale profile deploys party 2 into party 1's account, every
  # check still passes, and two shares now share an administrator.
  allowed_account_ids = [var.expected_account_id]

  default_tags {
    tags = {
      Project   = "OpenFireblocks"
      Component = "mpc-party"
      ManagedBy = "Terraform"
    }
  }
}

module "party" {
  source = "../modules/mpc-party-host"

  party_id                   = var.party_id
  party_image                = var.party_image
  vault_image                = var.vault_image
  instance_type              = var.instance_type
  peer_party_cidrs           = var.peer_party_cidrs
  platform_cidrs             = var.platform_cidrs
  ceremony_authorizer_pubkey = var.ceremony_authorizer_pubkey
}
