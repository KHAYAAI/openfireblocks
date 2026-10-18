terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

# Random, Terraform-generated credentials -- never the "dev-only" placeholder
# used in local development (infrastructure/database/migrations and every
# service's db.go default to that string, but only as a fallback when
# DATABASE_URL is unset; production always sets it from this secret).
resource "random_password" "app" {
  length  = 32
  special = false # simplifies embedding in a connection-string URL
}

resource "random_password" "app_admin" {
  length  = 32
  special = false
}

resource "random_password" "jwt_secret" {
  length  = 48
  special = false
}

resource "random_password" "admin_api_key" {
  length  = 40
  special = false
}

# A single JSON secret holding everything api-gateway and the backend Go
# services need. Splitting this into one secret per key is also a valid
# design (finer-grained IAM/rotation), but ECS's `secrets` container
# definition field can pull individual JSON keys out of one secret via
# `valueFrom = "<secret-arn>:key::"`, so one secret keeps the Terraform
# surface smaller without losing per-key granularity at the task
# definition level.
resource "aws_secretsmanager_secret" "app" {
  name       = "${var.environment}/openfireblocks/app-secrets"
  kms_key_id = var.kms_key_id

  tags = merge(var.tags, {
    Name = "${var.environment}-openfireblocks-app-secrets"
  })
}

resource "aws_secretsmanager_secret_version" "app" {
  secret_id = aws_secretsmanager_secret.app.id
  secret_string = jsonencode({
    # Row-level-security-scoped role (migration 011) -- api-gateway sets
    # app.current_customer_id per request and only sees that tenant's rows.
    database-url = "postgres://app:${random_password.app.result}@${var.db_address}:${var.db_port}/${var.db_name}?sslmode=require"
    # BYPASSRLS role -- for services that don't yet thread a per-request
    # tenant context (see the dsn comment in each of their db.go files) and
    # for api-gateway's AuditService system-actor fallback.
    database-admin-url    = "postgres://app_admin:${random_password.app_admin.result}@${var.db_address}:${var.db_port}/${var.db_name}?sslmode=require"
    jwt-secret            = random_password.jwt_secret.result
    admin-api-key         = random_password.admin_api_key.result
    app-db-password       = random_password.app.result
    app-admin-db-password = random_password.app_admin.result
  })
}

# Grants the ECS task EXECUTION role (not the task role) read access to
# exactly this secret -- the execution role is what ECS itself uses to
# resolve a container definition's `secrets` block at container start,
# before the application's own task-role credentials are relevant. Scoped
# to one ARN, replacing the "Resource": ["*"] secretsmanager grant in
# modules/ecs/main.tf's task ROLE policy (a separate, broader grant for
# whatever an application decides to read at runtime -- this is narrower
# and is what should actually gate access to this specific secret).
resource "aws_iam_role_policy" "read_app_secrets" {
  name = "${var.environment}-openfireblocks-read-app-secrets"
  role = var.ecs_task_execution_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret",
        ]
        Resource = [aws_secretsmanager_secret.app.arn]
      },
      {
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = [var.kms_key_id]
      }
    ]
  })
}
