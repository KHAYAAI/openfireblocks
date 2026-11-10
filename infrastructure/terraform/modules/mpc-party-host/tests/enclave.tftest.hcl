# What enclave mode plans, checked against a mocked AWS provider. Needs
# Terraform 1.11 or later: override_during = plan supplies the mocked values
# at plan time, so nothing is applied, and the share key's prevent_destroy
# never meets a test teardown.
#   terraform -chdir=infrastructure/terraform/modules/mpc-party-host init -backend=false
#   terraform -chdir=infrastructure/terraform/modules/mpc-party-host test

mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = { account_id = "111122223333" }
  }
  mock_data "aws_region" {
    defaults = { name = "af-south-1" }
  }
  mock_data "aws_partition" {
    defaults = { partition = "aws" }
  }
  mock_data "aws_availability_zones" {
    defaults = { names = ["af-south-1a"] }
  }
  mock_data "aws_ssm_parameter" {
    defaults = { value = "ami-0123456789abcdef0" }
  }
  mock_resource "aws_iam_role" {
    override_during = plan
    defaults        = { arn = "arn:aws:iam::111122223333:role/ofb-party-2-host", id = "ofb-party-2-host" }
  }
  mock_resource "aws_kms_key" {
    override_during = plan
    defaults        = { arn = "arn:aws:kms:af-south-1:111122223333:key/mrk-0123", key_id = "mrk-0123" }
  }
  mock_resource "aws_cloudwatch_log_group" {
    override_during = plan
    defaults        = { arn = "arn:aws:logs:af-south-1:111122223333:log-group:mock" }
  }
  mock_resource "aws_sns_topic" {
    override_during = plan
    defaults        = { arn = "arn:aws:sns:af-south-1:111122223333:mock" }
  }
  mock_resource "aws_secretsmanager_secret" {
    override_during = plan
    defaults        = { arn = "arn:aws:secretsmanager:af-south-1:111122223333:secret:mock" }
  }
  mock_resource "aws_iam_instance_profile" {
    override_during = plan
    defaults        = { arn = "arn:aws:iam::111122223333:instance-profile/mock" }
  }
  mock_resource "aws_cloudwatch_event_rule" {
    override_during = plan
    defaults        = { arn = "arn:aws:events:af-south-1:111122223333:rule/mock" }
  }
}

mock_provider "aws" {
  alias = "replica"
  mock_resource "aws_kms_replica_key" {
    override_during = plan
    defaults        = { arn = "arn:aws:kms:eu-west-1:111122223333:key/mrk-0123" }
  }
}

variables {
  party_id                   = 2
  party_image                = "unused"
  peer_party_cidrs           = ["203.0.113.1/32", "203.0.113.3/32"]
  platform_cidrs             = ["198.51.100.10/32"]
  ceremony_authorizer_pubkey = "aa"
  enclave_mode               = true
  enclave_image_pcr0s        = ["a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4"]
  enclave_eif_s3_uri         = "s3://forge-releases/party.eif"
  enclave_eif_sha256         = "0000000000000000000000000000000000000000000000000000000000000001"
  enclave_host_binary_s3_uri = "s3://forge-releases/mpc-party-host"
  enclave_host_binary_sha256 = "0000000000000000000000000000000000000000000000000000000000000002"
  peer_endpoints             = { "1" = "203.0.113.1:7000", "3" = "203.0.113.3:7000" }
}

run "enclave_host_and_share_key" {
  command = plan

  assert {
    condition     = aws_instance.party.enclave_options[0].enabled
    error_message = "the instance must be enclave-enabled"
  }
  assert {
    condition     = aws_instance.party.instance_type == "m6i.xlarge"
    error_message = "m6i.large has too few vCPUs for an enclave; enclave mode must move to m6i.xlarge"
  }
  assert {
    condition     = strcontains(aws_kms_key.shares[0].policy, "a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4")
    error_message = "the share key policy must name the allowed image"
  }
  assert {
    condition     = strcontains(aws_kms_key.shares[0].policy, "NothingWithoutAnAttestedRecipient")
    error_message = "the share key policy must deny cryptography without attestation"
  }
  assert {
    condition     = strcontains(aws_kms_key.shares[0].policy, "party-2")
    error_message = "the share key must be bound to this party's encryption context"
  }
  assert {
    condition     = aws_kms_key.shares[0].deletion_window_in_days == 30 && !aws_kms_key.shares[0].multi_region
    error_message = "30-day deletion window; single-region without a replica region"
  }
  assert {
    condition     = length(aws_kms_replica_key.shares) == 0
    error_message = "no replica without a replica region"
  }
  assert {
    condition     = strcontains(aws_instance.party.user_data, "party-1.internal:7000=7101") && strcontains(aws_instance.party.user_data, "7103=203.0.113.3:7000")
    error_message = "enclave and host egress maps must pair each peer name with its address"
  }
  assert {
    condition     = strcontains(aws_instance.party.user_data, "a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4") && !strcontains(aws_instance.party.user_data, "--debug-mode --")
    error_message = "the host must check the EIF against the allowed PCR0, and never run debug mode"
  }
  assert {
    condition     = strcontains(aws_instance.party.user_data, "kms.af-south-1.amazonaws.com:443=8443")
    error_message = "the enclave must reach the regional KMS endpoint"
  }
}

run "replica" {
  command = plan
  variables {
    share_key_replica_region = "eu-west-1"
  }
  assert {
    condition     = aws_kms_key.shares[0].multi_region && length(aws_kms_replica_key.shares) == 1
    error_message = "a replica region makes the key multi-region with a replica"
  }
}

run "refuses_a_debug_mode_image" {
  command = plan
  variables {
    enclave_image_pcr0s = ["000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"]
  }
  expect_failures = [var.enclave_image_pcr0s]
}

run "refuses_enclave_mode_without_an_image" {
  command = plan
  variables {
    enclave_image_pcr0s = []
  }
  expect_failures = [aws_kms_key.shares]
}

run "refuses_enclave_mode_without_a_co_signer" {
  command = plan
  variables {
    ceremony_authorizer_pubkey = ""
  }
  expect_failures = [aws_kms_key.shares]
}

run "refuses_an_unpinned_eif" {
  command = plan
  variables {
    enclave_eif_sha256 = ""
  }
  expect_failures = [aws_kms_key.shares]
}

run "off_by_default" {
  command = plan
  variables {
    enclave_mode        = false
    enclave_image_pcr0s = []
  }
  assert {
    condition     = length(aws_kms_key.shares) == 0 && !aws_instance.party.enclave_options[0].enabled && aws_instance.party.instance_type == "m6i.large"
    error_message = "without enclave mode the host must be unchanged"
  }
}
