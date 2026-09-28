# One MPC signing party, alone in one cloud account.
#
# Apply this once per party, in a different AWS account each time, by a
# different operator each time (see infrastructure/terraform/party/). What
# a party gets here is deliberately everything it needs and nothing it
# shares:
#
#   - its own VPC: no peering, no transit gateway, no route to anything
#     but the internet and, through a security group, two peer addresses
#   - its own Vault, auto-unsealed by this account's own KMS key; the key
#     share never leaves this account and the key that seals it cannot be
#     used from any other
#   - its own encrypted volume, keyed by this account's KMS
#   - no SSH and no inbound admin port at all: operators reach the host
#     through SSM Session Manager, which is logged in this account's
#     CloudTrail -- so access to a share leaves a record the other two
#     parties' operators cannot erase
#
# The security property this buys is the one the threshold claims: to
# reconstruct the key, an attacker needs two of these accounts, not one.
# See docs/deployment/PARTY-ISOLATION.md.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

data "aws_caller_identity" "this" {}
data "aws_region" "this" {}
data "aws_partition" "this" {}

data "aws_ssm_parameter" "al2023" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  name = "${var.name_prefix}-${var.party_id}"
  tags = merge(var.tags, {
    "openfireblocks:component" = "mpc-party"
    "openfireblocks:party-id"  = tostring(var.party_id)
  })
}

# ---------------------------------------------------------------- keys

# Seals this party's Vault. Only this account's instance role can use it,
# so a Vault data volume copied to any other account is ciphertext.
resource "aws_kms_key" "vault_seal" {
  description             = "${local.name} Vault auto-unseal"
  enable_key_rotation     = true
  deletion_window_in_days = 30
  tags                    = local.tags
}

resource "aws_kms_alias" "vault_seal" {
  name          = "alias/${local.name}-vault-seal"
  target_key_id = aws_kms_key.vault_seal.key_id
}

resource "aws_kms_key" "disk" {
  description             = "${local.name} volume encryption"
  enable_key_rotation     = true
  deletion_window_in_days = 30
  tags                    = local.tags
}

# ------------------------------------------------------------- network

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = merge(local.tags, { Name = local.name })
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = merge(local.tags, { Name = local.name })
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.this.id
  cidr_block              = var.vpc_cidr
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = false
  tags                    = merge(local.tags, { Name = "${local.name}-public" })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
  tags = merge(local.tags, { Name = local.name })
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

# Flow logs: every connection attempt to the party is recorded in this
# account, whoever made it.
resource "aws_cloudwatch_log_group" "flow" {
  name              = "/openfireblocks/${local.name}/vpc-flow"
  retention_in_days = 365
  kms_key_id        = aws_kms_key.disk.arn
  tags              = local.tags
}

resource "aws_iam_role" "flow" {
  name = "${local.name}-flow-logs"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "vpc-flow-logs.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy" "flow" {
  role = aws_iam_role.flow.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"]
      Resource = "${aws_cloudwatch_log_group.flow.arn}:*"
    }]
  })
}

resource "aws_flow_log" "this" {
  vpc_id          = aws_vpc.this.id
  traffic_type    = "ALL"
  log_destination = aws_cloudwatch_log_group.flow.arn
  iam_role_arn    = aws_iam_role.flow.arn
  tags            = local.tags
}

resource "aws_security_group" "party" {
  name        = local.name
  description = "MPC party ${var.party_id}: ceremony port from peers and the platform only; no admin ports"
  vpc_id      = aws_vpc.this.id
  tags        = merge(local.tags, { Name = local.name })
}

# Ceremony traffic (mTLS) from the other parties and the orchestrator.
resource "aws_vpc_security_group_ingress_rule" "ceremony" {
  for_each          = toset(concat(var.peer_party_cidrs, var.platform_cidrs))
  security_group_id = aws_security_group.party.id
  cidr_ipv4         = each.value
  ip_protocol       = "tcp"
  from_port         = var.ceremony_port
  to_port           = var.ceremony_port
  description       = "ceremony (mTLS)"
}

# Liveness only, from the platform only.
resource "aws_vpc_security_group_ingress_rule" "health" {
  for_each          = toset(var.platform_cidrs)
  security_group_id = aws_security_group.party.id
  cidr_ipv4         = each.value
  ip_protocol       = "tcp"
  from_port         = var.health_port
  to_port           = var.health_port
  description       = "liveness probe"
}

# Outbound: the peers' ceremony port, and HTTPS (KMS, Secrets Manager,
# SSM, the image registry). Nothing else -- in particular no route to
# another party's Vault, because no party's Vault listens beyond loopback.
resource "aws_vpc_security_group_egress_rule" "peers" {
  for_each          = toset(var.peer_party_cidrs)
  security_group_id = aws_security_group.party.id
  cidr_ipv4         = each.value
  ip_protocol       = "tcp"
  from_port         = var.ceremony_port
  to_port           = var.ceremony_port
  description       = "ceremony to peer"
}

resource "aws_vpc_security_group_egress_rule" "https" {
  security_group_id = aws_security_group.party.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  description       = "AWS APIs and image registry"
}

resource "aws_eip" "party" {
  domain = "vpc"
  tags   = merge(local.tags, { Name = local.name })
}

# --------------------------------------------------------- secrets

# Values are put here out of band, never through Terraform (which would
# write them into state):
#   tls:         {"cert": "...", "key": "...", "ca": "..."}  the party's mTLS identity
#   vault-token: the token party-bootstrap.sh creates after vault init
resource "aws_secretsmanager_secret" "tls" {
  name       = "${local.name}/tls"
  kms_key_id = aws_kms_key.disk.arn
  tags       = local.tags
}

resource "aws_secretsmanager_secret" "vault_token" {
  name       = "${local.name}/vault-token"
  kms_key_id = aws_kms_key.disk.arn
  tags       = local.tags
}

# ------------------------------------------------------------- host

resource "aws_iam_role" "host" {
  name = "${local.name}-host"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.host.name
  policy_arn = "arn:${data.aws_partition.this.partition}:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy_attachment" "ecr" {
  role       = aws_iam_role.host.name
  policy_arn = "arn:${data.aws_partition.this.partition}:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_iam_role_policy" "host" {
  role = aws_iam_role.host.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "VaultAutoUnseal"
        Effect   = "Allow"
        Action   = ["kms:Encrypt", "kms:Decrypt", "kms:DescribeKey"]
        Resource = aws_kms_key.vault_seal.arn
      },
      {
        Sid      = "ReadOwnSecrets"
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = [aws_secretsmanager_secret.tls.arn, aws_secretsmanager_secret.vault_token.arn]
      },
      {
        # Only so party-bootstrap.sh, run on the host, can store the token
        # it creates. It cannot read or write any other secret.
        Sid      = "StoreVaultToken"
        Effect   = "Allow"
        Action   = ["secretsmanager:PutSecretValue"]
        Resource = aws_secretsmanager_secret.vault_token.arn
      },
      {
        Sid      = "DecryptOwnSecrets"
        Effect   = "Allow"
        Action   = ["kms:Decrypt", "kms:GenerateDataKey"]
        Resource = aws_kms_key.disk.arn
      },
    ]
  })
}

resource "aws_iam_instance_profile" "host" {
  name = "${local.name}-host"
  role = aws_iam_role.host.name
}

resource "aws_instance" "party" {
  ami                    = data.aws_ssm_parameter.al2023.value
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.party.id]
  iam_instance_profile   = aws_iam_instance_profile.host.name

  # IMDSv2 only, one hop: a container on the host cannot reach the
  # instance credentials through a forwarded request.
  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    http_endpoint               = "enabled"
  }

  root_block_device {
    encrypted   = true
    kms_key_id  = aws_kms_key.disk.arn
    volume_size = 20
    volume_type = "gp3"
  }

  user_data_replace_on_change = true
  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    party_id                   = var.party_id
    party_image                = var.party_image
    vault_image                = var.vault_image
    region                     = data.aws_region.this.name
    seal_key_id                = aws_kms_key.vault_seal.key_id
    tls_secret_arn             = aws_secretsmanager_secret.tls.arn
    vault_token_secret_arn     = aws_secretsmanager_secret.vault_token.arn
    ceremony_port              = var.ceremony_port
    health_port                = var.health_port
    ceremony_authorizer_pubkey = var.ceremony_authorizer_pubkey
  })

  tags = merge(local.tags, { Name = local.name })

  lifecycle {
    # A new AMI must not silently replace the host holding a key share.
    # Replacing the host is a deliberate operation with a restore drill.
    ignore_changes = [ami]
  }
}

resource "aws_ebs_volume" "vault" {
  availability_zone = aws_subnet.public.availability_zone
  size              = var.data_volume_gb
  type              = "gp3"
  encrypted         = true
  kms_key_id        = aws_kms_key.disk.arn
  tags              = merge(local.tags, { Name = "${local.name}-vault" })

  lifecycle {
    # This volume holds the sealed key share. Destroying it is destroying
    # a share; Terraform must not do that as a side effect of a plan.
    prevent_destroy = true
  }
}

resource "aws_volume_attachment" "vault" {
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.vault.id
  instance_id = aws_instance.party.id
}

resource "aws_eip_association" "party" {
  instance_id   = aws_instance.party.id
  allocation_id = aws_eip.party.id
}
