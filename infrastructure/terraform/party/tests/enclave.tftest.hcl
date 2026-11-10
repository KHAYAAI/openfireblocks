# Plans the party root against a mocked AWS provider, so these run without
# credentials (Terraform 1.11 or later):  terraform -chdir=infrastructure/terraform/party init -backend=false
#               terraform -chdir=infrastructure/terraform/party test

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
    defaults        = { arn = "arn:aws:iam::111122223333:role/ofb-party-1-host" }
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
  region                     = "af-south-1"
  expected_account_id        = "111122223333"
  party_id                   = 1
  party_image                = "unused-in-enclave-mode"
  peer_party_cidrs           = ["203.0.113.2/32", "203.0.113.3/32"]
  platform_cidrs             = ["198.51.100.10/32"]
  ceremony_authorizer_pubkey = "aa"
}

run "default_is_unchanged" {
  command = plan

  assert {
    condition     = module.party.share_key_arn == null
    error_message = "a share key was planned without enclave mode"
  }
}

run "enclave_mode" {
  command = plan

  variables {
    enclave_mode               = true
    enclave_image_pcr0s        = ["A1B2C3D4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4"]
    enclave_eif_s3_uri         = "s3://forge-releases/party.eif"
    enclave_eif_sha256         = "0000000000000000000000000000000000000000000000000000000000000001"
    enclave_host_binary_s3_uri = "s3://forge-releases/mpc-party-host"
    enclave_host_binary_sha256 = "0000000000000000000000000000000000000000000000000000000000000002"
    peer_endpoints             = { "2" = "203.0.113.2:7000", "3" = "203.0.113.3:7000" }
    share_key_replica_region   = "eu-west-1"
    alert_email                = "custody-alerts@example.test"
  }

  assert {
    condition     = module.party.share_key_arn != null && module.party.share_key_replica_arn != null
    error_message = "enclave mode must plan the share key and its replica"
  }
}
