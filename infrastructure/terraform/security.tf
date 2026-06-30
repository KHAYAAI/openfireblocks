# RDS Security Groups
resource "aws_security_group" "rds_primary" {
  name_prefix = "openfireblocks-rds-primary-"
  description = "Security group for RDS PostgreSQL primary"
  vpc_id      = module.vpc_primary.vpc_id

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-rds-primary-sg"
    }
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "rds_primary_from_vault" {
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.vault_primary.id
  security_group_id        = aws_security_group.rds_primary.id
  description              = "PostgreSQL from Vault primary"
}

resource "aws_security_group_rule" "rds_primary_from_ecs" {
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.ecs_primary.id
  security_group_id        = aws_security_group.rds_primary.id
  description              = "PostgreSQL from ECS primary"
}

resource "aws_security_group_rule" "rds_primary_egress" {
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.rds_primary.id
}

resource "aws_security_group" "rds_secondary" {
  provider    = aws.secondary
  name_prefix = "openfireblocks-rds-secondary-"
  description = "Security group for RDS PostgreSQL secondary/replica"
  vpc_id      = module.vpc_secondary.vpc_id

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-rds-secondary-sg"
    }
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "rds_secondary_from_vault" {
  provider                 = aws.secondary
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.vault_secondary.id
  security_group_id        = aws_security_group.rds_secondary.id
  description              = "PostgreSQL from Vault secondary"
}

resource "aws_security_group_rule" "rds_secondary_from_ecs" {
  provider                 = aws.secondary
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.ecs_secondary.id
  security_group_id        = aws_security_group.rds_secondary.id
  description              = "PostgreSQL from ECS secondary"
}

resource "aws_security_group_rule" "rds_secondary_egress" {
  provider      = aws.secondary
  type          = "egress"
  from_port     = 0
  to_port       = 0
  protocol      = "-1"
  cidr_blocks   = ["0.0.0.0/0"]
  security_group_id = aws_security_group.rds_secondary.id
}

# Vault Security Groups
resource "aws_security_group" "vault_primary" {
  name_prefix = "openfireblocks-vault-primary-"
  description = "Security group for Vault primary cluster"
  vpc_id      = module.vpc_primary.vpc_id

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-vault-primary-sg"
    }
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "vault_primary_api" {
  type              = "ingress"
  from_port         = 8200
  to_port           = 8200
  protocol          = "tcp"
  cidr_blocks       = [module.vpc_primary.vpc_cidr]
  security_group_id = aws_security_group.vault_primary.id
  description       = "Vault API"
}

resource "aws_security_group_rule" "vault_primary_cluster" {
  type              = "ingress"
  from_port         = 8201
  to_port           = 8201
  protocol          = "tcp"
  cidr_blocks       = [module.vpc_primary.vpc_cidr]
  security_group_id = aws_security_group.vault_primary.id
  description       = "Vault cluster"
}

resource "aws_security_group_rule" "vault_primary_egress" {
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.vault_primary.id
}

resource "aws_security_group" "vault_secondary" {
  provider    = aws.secondary
  name_prefix = "openfireblocks-vault-secondary-"
  description = "Security group for Vault secondary cluster"
  vpc_id      = module.vpc_secondary.vpc_id

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-vault-secondary-sg"
    }
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "vault_secondary_api" {
  provider      = aws.secondary
  type          = "ingress"
  from_port     = 8200
  to_port       = 8200
  protocol      = "tcp"
  cidr_blocks   = [module.vpc_secondary.vpc_cidr]
  security_group_id = aws_security_group.vault_secondary.id
  description   = "Vault API"
}

resource "aws_security_group_rule" "vault_secondary_cluster" {
  provider      = aws.secondary
  type          = "ingress"
  from_port     = 8201
  to_port       = 8201
  protocol      = "tcp"
  cidr_blocks   = [module.vpc_secondary.vpc_cidr]
  security_group_id = aws_security_group.vault_secondary.id
  description   = "Vault cluster"
}

resource "aws_security_group_rule" "vault_secondary_egress" {
  provider      = aws.secondary
  type          = "egress"
  from_port     = 0
  to_port       = 0
  protocol      = "-1"
  cidr_blocks   = ["0.0.0.0/0"]
  security_group_id = aws_security_group.vault_secondary.id
}

# ECS Security Groups
resource "aws_security_group" "ecs_primary" {
  name_prefix = "openfireblocks-ecs-primary-"
  description = "Security group for ECS primary cluster"
  vpc_id      = module.vpc_primary.vpc_id

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-ecs-primary-sg"
    }
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "ecs_primary_alb" {
  type              = "ingress"
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.ecs_primary.id
  description       = "HTTP from internet"
}

resource "aws_security_group_rule" "ecs_primary_alb_https" {
  type              = "ingress"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.ecs_primary.id
  description       = "HTTPS from internet"
}

resource "aws_security_group_rule" "ecs_primary_internal" {
  type              = "ingress"
  from_port         = 8000
  to_port           = 8999
  protocol          = "tcp"
  cidr_blocks       = [module.vpc_primary.vpc_cidr]
  security_group_id = aws_security_group.ecs_primary.id
  description       = "Internal services"
}

resource "aws_security_group_rule" "ecs_primary_egress" {
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.ecs_primary.id
}

resource "aws_security_group" "ecs_secondary" {
  provider    = aws.secondary
  name_prefix = "openfireblocks-ecs-secondary-"
  description = "Security group for ECS secondary cluster"
  vpc_id      = module.vpc_secondary.vpc_id

  tags = merge(
    {
      Environment = var.environment
      Project     = "OpenFireblocks"
      ManagedBy   = "Terraform"
    },
    {
      Name = "openfireblocks-ecs-secondary-sg"
    }
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "ecs_secondary_alb" {
  provider      = aws.secondary
  type          = "ingress"
  from_port     = 80
  to_port       = 80
  protocol      = "tcp"
  cidr_blocks   = ["0.0.0.0/0"]
  security_group_id = aws_security_group.ecs_secondary.id
  description   = "HTTP from internet"
}

resource "aws_security_group_rule" "ecs_secondary_alb_https" {
  provider      = aws.secondary
  type          = "ingress"
  from_port     = 443
  to_port       = 443
  protocol      = "tcp"
  cidr_blocks   = ["0.0.0.0/0"]
  security_group_id = aws_security_group.ecs_secondary.id
  description   = "HTTPS from internet"
}

resource "aws_security_group_rule" "ecs_secondary_internal" {
  provider      = aws.secondary
  type          = "ingress"
  from_port     = 8000
  to_port       = 8999
  protocol      = "tcp"
  cidr_blocks   = [module.vpc_secondary.vpc_cidr]
  security_group_id = aws_security_group.ecs_secondary.id
  description   = "Internal services"
}

resource "aws_security_group_rule" "ecs_secondary_egress" {
  provider      = aws.secondary
  type          = "egress"
  from_port     = 0
  to_port       = 0
  protocol      = "-1"
  cidr_blocks   = ["0.0.0.0/0"]
  security_group_id = aws_security_group.ecs_secondary.id
}

# IAM Roles for Vault Instance Profiles
resource "aws_iam_role" "vault_primary" {
  name = "openfireblocks-vault-primary-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })

  tags = {
    Name = "openfireblocks-vault-primary-role"
  }
}

resource "aws_iam_role_policy" "vault_primary_policy" {
  name = "openfireblocks-vault-primary-policy"
  role = aws_iam_role.vault_primary.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket"
        ]
        Resource = [
          aws_s3_bucket.vault_backend_primary.arn,
          "${aws_s3_bucket.vault_backend_primary.arn}/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:Encrypt",
          "kms:ReEncrypt*",
          "kms:GenerateDataKey*",
          "kms:DescribeKey"
        ]
        Resource = aws_kms_key.primary.arn
      },
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeInstances"
        ]
        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_instance_profile" "vault_primary" {
  name = "openfireblocks-vault-primary-profile"
  role = aws_iam_role.vault_primary.name
}

resource "aws_iam_role" "vault_secondary" {
  provider = aws.secondary
  name     = "openfireblocks-vault-secondary-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })

  tags = {
    Name = "openfireblocks-vault-secondary-role"
  }
}

resource "aws_iam_role_policy" "vault_secondary_policy" {
  provider = aws.secondary
  name     = "openfireblocks-vault-secondary-policy"
  role     = aws_iam_role.vault_secondary.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket"
        ]
        Resource = [
          aws_s3_bucket.vault_backend_secondary.arn,
          "${aws_s3_bucket.vault_backend_secondary.arn}/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:Encrypt",
          "kms:ReEncrypt*",
          "kms:GenerateDataKey*",
          "kms:DescribeKey"
        ]
        Resource = aws_kms_key.secondary.arn
      },
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeInstances"
        ]
        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_instance_profile" "vault_secondary" {
  provider = aws.secondary
  name     = "openfireblocks-vault-secondary-profile"
  role     = aws_iam_role.vault_secondary.name
}
