# Enclave mode for this party. Everything here has count = 0 unless
# var.enclave_mode is set, so a party without it is unchanged.
#
# What it adds:
#
#   * an enclave-enabled instance (main.tf), at least m6i.xlarge: a Nitro
#     Enclave needs vCPUs of its own, and the parent keeps the rest;
#   * the share key: a KMS key whose policy releases a data key only to an
#     attested enclave running one of var.enclave_image_pcr0s, for this
#     party's encryption context, and denies all cryptographic use without
#     attestation to every principal. Multi-region with a replica when
#     var.share_key_replica_region is set; 30-day deletion window;
#     prevent_destroy;
#   * alerts when anyone rewrites the key's policy, schedules its deletion
#     or disables it -- the one action that can undo the enclave boundary;
#   * read access to the EIF and the host binary in S3, and a first boot
#     that checks both hashes and the EIF's PCR0 before starting anything.
#
# The key policy is the same as infrastructure/nitro/kms-key-policy.json,
# which forge-enclave-admin kms-policy renders for the upgrade procedure.

locals {
  # m6i.large has 2 vCPUs, too few to give an enclave its own.
  instance_type = var.enclave_mode && var.instance_type == "m6i.large" ? "m6i.xlarge" : var.instance_type

  enclave_vsock_port   = 5000
  enclave_control_port = 9000
  enclave_cid          = 16

  # Enclave egress: party-N.internal:<port> and vault.internal:8200 in the
  # enclave, each to one vsock port the host relays to one destination.
  peer_vsock_ports = { for id, _ in var.peer_endpoints : id => 7100 + tonumber(id) }
  enclave_egress = join(",", concat(
    [for id, port in local.peer_vsock_ports : "party-${id}.internal:${var.ceremony_port}=${port}"],
    ["vault.internal:8200=7200", "kms.${data.aws_region.this.name}.amazonaws.com:443=8443"],
  ))
  host_egress = join(",", concat(
    [for id, port in local.peer_vsock_ports : "${port}=${var.peer_endpoints[id]}"],
    ["7200=127.0.0.1:8200", "8443=kms.${data.aws_region.this.name}.amazonaws.com:443"],
  ))

  share_key_policy = jsonencode({
    Version = "2012-10-17"
    Id      = "${local.name}-shares"
    Statement = [
      {
        Sid       = "KeyAdministrationNoCryptography"
        Effect    = "Allow"
        Principal = { AWS = "arn:${data.aws_partition.this.partition}:iam::${data.aws_caller_identity.this.account_id}:root" }
        Action = [
          "kms:Describe*", "kms:Get*", "kms:List*",
          "kms:PutKeyPolicy", "kms:EnableKeyRotation", "kms:DisableKeyRotation",
          "kms:TagResource", "kms:UntagResource",
          "kms:CreateAlias", "kms:UpdateAlias", "kms:DeleteAlias",
          "kms:ScheduleKeyDeletion", "kms:CancelKeyDeletion",
          "kms:ReplicateKey", "kms:UpdatePrimaryRegion",
        ]
        Resource = "*"
      },
      {
        Sid       = "MeasuredEnclaveOfThisPartyOnly"
        Effect    = "Allow"
        Principal = { AWS = aws_iam_role.host.arn }
        Action    = ["kms:Decrypt", "kms:GenerateDataKey"]
        Resource  = "*"
        Condition = {
          StringEqualsIgnoreCase = { "kms:RecipientAttestation:ImageSha384" = [for p in var.enclave_image_pcr0s : lower(p)] }
          StringEquals           = { "kms:EncryptionContext:forge:party" = "party-${var.party_id}" }
        }
      },
      {
        Sid       = "NothingWithoutAnAttestedRecipient"
        Effect    = "Deny"
        Principal = "*"
        Action = [
          "kms:Decrypt", "kms:GenerateDataKey", "kms:GenerateDataKeyWithoutPlaintext",
          "kms:GenerateDataKeyPair", "kms:GenerateDataKeyPairWithoutPlaintext",
          "kms:Encrypt", "kms:ReEncryptFrom", "kms:ReEncryptTo",
        ]
        Resource  = "*"
        Condition = { Null = { "kms:RecipientAttestation:ImageSha384" = "true" } }
      },
    ]
  })
}

resource "aws_kms_key" "shares" {
  count = var.enclave_mode ? 1 : 0

  description             = "${local.name} key shares, released only to the measured enclave image"
  multi_region            = var.share_key_replica_region != ""
  enable_key_rotation     = true
  deletion_window_in_days = 30
  policy                  = local.share_key_policy
  tags                    = local.tags

  lifecycle {
    # This key is the only way the enclave opens its shares. Losing it is
    # losing them, unless the break-glass recovery keys were set up.
    prevent_destroy = true

    precondition {
      condition     = length(var.enclave_image_pcr0s) > 0
      error_message = "enclave_mode needs enclave_image_pcr0s: the image(s) the share key may be released to."
    }
    precondition {
      condition     = var.enclave_eif_s3_uri != "" && can(regex("^[0-9a-f]{64}$", var.enclave_eif_sha256))
      error_message = "enclave_mode needs enclave_eif_s3_uri and its enclave_eif_sha256 (64 lowercase hex)."
    }
    precondition {
      condition     = var.enclave_host_binary_s3_uri != "" && can(regex("^[0-9a-f]{64}$", var.enclave_host_binary_sha256))
      error_message = "enclave_mode needs enclave_host_binary_s3_uri and its enclave_host_binary_sha256 (64 lowercase hex)."
    }
    precondition {
      condition     = var.ceremony_authorizer_pubkey != ""
      error_message = "enclave_mode needs ceremony_authorizer_pubkey; the enclave image refuses to start without a co-signer anyway."
    }
  }
}

resource "aws_kms_alias" "shares" {
  count         = var.enclave_mode ? 1 : 0
  name          = "alias/${local.name}-shares"
  target_key_id = aws_kms_key.shares[0].key_id
}

resource "aws_kms_replica_key" "shares" {
  count    = var.enclave_mode && var.share_key_replica_region != "" ? 1 : 0
  provider = aws.replica

  description             = "${local.name} key shares (replica in ${var.share_key_replica_region})"
  primary_key_arn         = aws_kms_key.shares[0].arn
  deletion_window_in_days = 30
  policy                  = local.share_key_policy
  tags                    = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

# ---- alerts on the share key ----

resource "aws_sns_topic" "share_key_alerts" {
  count             = var.enclave_mode ? 1 : 0
  name              = "${local.name}-share-key-alerts"
  kms_master_key_id = aws_kms_key.disk.arn
  tags              = local.tags
}

resource "aws_sns_topic_policy" "share_key_alerts" {
  count = var.enclave_mode ? 1 : 0
  arn   = aws_sns_topic.share_key_alerts[0].arn
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "EventBridgePublishes"
      Effect    = "Allow"
      Principal = { Service = "events.amazonaws.com" }
      Action    = "sns:Publish"
      Resource  = aws_sns_topic.share_key_alerts[0].arn
    }]
  })
}

resource "aws_sns_topic_subscription" "share_key_alerts" {
  count     = var.enclave_mode && var.alert_email != "" ? 1 : 0
  topic_arn = aws_sns_topic.share_key_alerts[0].arn
  protocol  = "email"
  endpoint  = var.alert_email
}

resource "aws_cloudwatch_event_rule" "share_key_changes" {
  count       = var.enclave_mode ? 1 : 0
  name        = "${local.name}-share-key-changes"
  description = "Someone changed the policy of, scheduled deletion of, or disabled party ${var.party_id}'s share key"
  event_pattern = jsonencode({
    source        = ["aws.kms"]
    "detail-type" = ["AWS API Call via CloudTrail"]
    detail = {
      eventSource = ["kms.amazonaws.com"]
      eventName   = ["PutKeyPolicy", "ScheduleKeyDeletion", "DisableKey", "CreateGrant", "UpdatePrimaryRegion"]
      resources   = { ARN = [aws_kms_key.shares[0].arn] }
    }
  })
  tags = local.tags
}

resource "aws_cloudwatch_event_target" "share_key_changes" {
  count = var.enclave_mode ? 1 : 0
  rule  = aws_cloudwatch_event_rule.share_key_changes[0].name
  arn   = aws_sns_topic.share_key_alerts[0].arn
}

# ---- the host's access to the enclave artefacts ----

locals {
  eif_object  = trimprefix(var.enclave_eif_s3_uri, "s3://")
  host_object = trimprefix(var.enclave_host_binary_s3_uri, "s3://")
}

resource "aws_iam_role_policy" "enclave_artefacts" {
  count = var.enclave_mode ? 1 : 0
  name  = "enclave-artefacts"
  role  = aws_iam_role.host.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "ReadEnclaveArtefacts"
      Effect   = "Allow"
      Action   = ["s3:GetObject"]
      Resource = ["arn:${data.aws_partition.this.partition}:s3:::${local.eif_object}", "arn:${data.aws_partition.this.partition}:s3:::${local.host_object}"]
    }]
  })
}

locals {
  enclave_user_data = var.enclave_mode ? templatefile("${path.module}/user_data_enclave.sh.tftpl", {
    party_id               = var.party_id
    vault_image            = var.vault_image
    region                 = data.aws_region.this.name
    seal_key_id            = aws_kms_key.vault_seal.key_id
    vault_token_secret_arn = aws_secretsmanager_secret.vault_token.arn
    ceremony_port          = var.ceremony_port
    health_port            = var.health_port
    eif_s3_uri             = var.enclave_eif_s3_uri
    eif_sha256             = var.enclave_eif_sha256
    host_s3_uri            = var.enclave_host_binary_s3_uri
    host_sha256            = var.enclave_host_binary_sha256
    allowed_pcr0s          = join(" ", [for p in var.enclave_image_pcr0s : lower(p)])
    enclave_cpu_count      = var.enclave_cpu_count
    enclave_memory_mib     = var.enclave_memory_mib
    enclave_cid            = local.enclave_cid
    enclave_vsock_port     = local.enclave_vsock_port
    enclave_control_port   = local.enclave_control_port
    enclave_egress         = local.enclave_egress
    host_egress            = local.host_egress
  }) : ""
}
