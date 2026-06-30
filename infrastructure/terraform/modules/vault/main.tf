data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

resource "aws_launch_template" "vault" {
  name_prefix   = "openfireblocks-vault-${var.environment}-"
  image_id      = data.aws_ami.ubuntu.id
  instance_type = var.vault_instance_type

  iam_instance_profile {
    name = var.instance_profile_name
  }

  vpc_security_group_ids = var.security_group_ids

  user_data = base64encode(templatefile("${path.module}/user_data.sh", {
    vault_version      = "1.16.1"
    kms_key_id        = var.kms_key_id
    s3_bucket_name    = var.s3_bucket_name
    environment       = var.environment
    vault_node_count  = var.vault_instance_count
  }))

  monitoring {
    enabled = true
  }

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }

  tag_specifications {
    resource_type = "instance"
    tags = merge(
      var.tags,
      {
        Name = "openfireblocks-${var.environment}-vault"
      }
    )
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_autoscaling_group" "vault" {
  name                = "openfireblocks-${var.environment}-vault-asg"
  vpc_zone_identifier = var.private_subnet_ids
  min_size            = var.vault_instance_count
  max_size            = var.vault_instance_count
  desired_capacity    = var.vault_instance_count
  health_check_type   = "ELB"
  health_check_grace_period = 300

  launch_template {
    id      = aws_launch_template.vault.id
    version = "$Latest"
  }

  target_group_arns = [aws_lb_target_group.vault.arn]

  tag {
    key                 = "Name"
    value               = "openfireblocks-${var.environment}-vault-asg"
    propagate_at_launch = true
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Network Load Balancer for Vault
resource "aws_lb" "vault" {
  name_prefix        = "vault"
  internal           = true
  load_balancer_type = "network"
  subnets            = var.private_subnet_ids

  enable_deletion_protection = false

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-vault-nlb"
    }
  )
}

resource "aws_lb_target_group" "vault" {
  name_prefix = "vault"
  port        = 8200
  protocol    = "TCP"
  vpc_id      = var.vpc_id

  health_check {
    protocol            = "TCP"
    port                = "8200"
    healthy_threshold   = 2
    unhealthy_threshold = 2
    interval            = 10
  }

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-vault-tg"
    }
  )
}

resource "aws_lb_listener" "vault_api" {
  load_balancer_arn = aws_lb.vault.arn
  port              = "8200"
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.vault.arn
  }
}

resource "aws_lb_listener" "vault_cluster" {
  load_balancer_arn = aws_lb.vault.arn
  port              = "8201"
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.vault.arn
  }
}

# ACM Certificate for Vault TLS
resource "aws_acm_certificate" "vault" {
  domain_name       = "vault.openfireblocks.internal"
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-vault-cert"
    }
  )
}

# Self-signed certificate for development (replace with real cert in production)
resource "tls_private_key" "vault" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_self_signed_cert" "vault" {
  private_key_pem = tls_private_key.vault.private_key_pem

  subject {
    common_name  = "vault.openfireblocks.internal"
    organization = "OpenFireblocks"
  }

  validity_period_hours = 8760

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "vault" {
  name              = "/aws/vault/openfireblocks-${var.environment}"
  retention_in_days = 30
  kms_key_id        = var.kms_key_id

  tags = merge(
    var.tags,
    {
      Name = "openfireblocks-${var.environment}-vault-logs"
    }
  )
}
