# Production Deployment Guide

## Prerequisites

1. **AWS Account Setup**
   - Production AWS account configured
   - IAM role with permissions for ECS, RDS, ElastiCache, ALB
   - S3 bucket for Terraform state
   - DynamoDB table for state locking

2. **Tools Required**
   - Terraform >= 1.0
   - AWS CLI v2
   - Docker & Docker Buildx
   - Git

3. **Secrets**
   - GitHub secrets configured for CI/CD
   - AWS credentials configured locally
   - API keys for Onfido, Stripe, WorkOS

---

## Phase 1: Infrastructure Setup (2-3 hours)

### Step 1: Create S3 Backend

```bash
# Create state bucket
aws s3 mb s3://openfireblocks-terraform-state --region us-east-1
aws s3api put-bucket-versioning \
  --bucket openfireblocks-terraform-state \
  --versioning-configuration Status=Enabled

# Enable encryption
aws s3api put-bucket-encryption \
  --bucket openfireblocks-terraform-state \
  --server-side-encryption-configuration '{
    "Rules": [{
      "ApplyServerSideEncryptionByDefault": {
        "SSEAlgorithm": "AES256"
      }
    }]
  }'

# Create DynamoDB lock table
aws dynamodb create-table \
  --table-name terraform-locks \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region us-east-1
```

### Step 2: Initialize Terraform

```bash
cd infrastructure/terraform

# Create terraform.tfvars
cat > terraform.tfvars << 'TFVARS'
aws_region         = "us-east-1"
environment         = "prod"
vpc_cidr            = "10.0.0.0/16"
availability_zones  = ["us-east-1a", "us-east-1b", "us-east-1c"]
db_username         = "postgres"
db_instance_class   = "db.t3.large"
redis_node_type     = "cache.t3.micro"
api_gateway_image   = "123456789.dkr.ecr.us-east-1.amazonaws.com/openfireblocks:api-gateway-latest"

# Add sensitive variables
ethereum_rpc_url    = "https://eth-mainnet.alchemyapi.io/v2/YOUR_KEY"
bitcoin_rpc_url     = "https://bitcoin-mainnet.api.YOUR_PROVIDER/"
onfido_api_key      = "YOUR_ONFIDO_KEY"
workos_api_key      = "YOUR_WORKOS_KEY"
TFVARS

# Initialize Terraform
terraform init

# Plan deployment
terraform plan -out=tfplan

# Apply (requires manual approval)
terraform apply tfplan
```

### Step 3: Verify Infrastructure

```bash
# Get outputs
terraform output alb_dns_name
terraform output rds_endpoint
terraform output redis_endpoint

# Test database connection
psql -h $(terraform output -raw rds_endpoint) -U postgres -d openfireblocks

# Test Redis connection
redis-cli -h $(terraform output -raw redis_endpoint) PING
```

---

## Phase 2: Database Setup (30 minutes)

### Step 1: Create Database

```bash
# Connect to RDS
PGPASSWORD="$DB_PASSWORD" psql -h $RDS_ENDPOINT -U postgres -d openfireblocks

# Run migrations
flyway -url="jdbc:postgresql://$RDS_ENDPOINT/openfireblocks" \
  -user="postgres" \
  -password="$DB_PASSWORD" \
  -locations="filesystem:./db/migrations" \
  migrate
```

### Step 2: Seed Initial Data

```bash
# Create initial admin user
psql -h $RDS_ENDPOINT -U postgres -d openfireblocks << 'SQL'
INSERT INTO users (email, password_hash, full_name, organization, role, status)
VALUES (
  'admin@openfireblocks.io',
  '$2a$12$...',  -- bcrypt hash of secure password
  'Platform Admin',
  'OpenFireblocks',
  'admin',
  'active'
);

INSERT INTO roles (role_id, name, permissions) VALUES
  ('admin', 'Administrator', '["users:*", "keys:*", "signings:*", "compliance:*"]');
SQL
```

### Step 3: Verify Database

```bash
# Check tables exist
psql -h $RDS_ENDPOINT -U postgres -d openfireblocks -c "\dt"

# Check row security policies
psql -h $RDS_ENDPOINT -U postgres -d openfireblocks -c "\dp"
```

---

## Phase 3: Container Images (1 hour)

### Step 1: Create ECR Repository

```bash
aws ecr create-repository \
  --repository-name openfireblocks \
  --region us-east-1

# Enable image scanning
aws ecr put-image-scanning-configuration \
  --repository-name openfireblocks \
  --image-scanning-configuration scanOnPush=true \
  --region us-east-1
```

### Step 2: Build and Push Images

```bash
# Login to ECR
aws ecr get-login-password --region us-east-1 | docker login \
  --username AWS --password-stdin 123456789.dkr.ecr.us-east-1.amazonaws.com

# Build services
docker buildx build \
  --platform linux/amd64 \
  -t openfireblocks:api-gateway-latest \
  -f ./services/api-gateway/Dockerfile \
  ./services/api-gateway

# Tag and push
docker tag openfireblocks:api-gateway-latest \
  123456789.dkr.ecr.us-east-1.amazonaws.com/openfireblocks:api-gateway-latest

docker push 123456789.dkr.ecr.us-east-1.amazonaws.com/openfireblocks:api-gateway-latest

# Repeat for other services (compliance, settlement, policy, etc.)
```

### Step 3: Verify Images

```bash
aws ecr describe-images \
  --repository-name openfireblocks \
  --region us-east-1
```

---

## Phase 4: Secrets Configuration (45 minutes)

### Step 1: Create Secrets in Secrets Manager

```bash
# Database password
aws secretsmanager create-secret \
  --name openfireblocks/db/password \
  --secret-string "$(terraform output -raw db_password)" \
  --region us-east-1

# JWT secrets
aws secretsmanager create-secret \
  --name openfireblocks/jwt/secret \
  --secret-string "$(openssl rand -hex 32)" \
  --region us-east-1

# API credentials
aws secretsmanager create-secret \
  --name openfireblocks/onfido/api-key \
  --secret-string "$ONFIDO_API_KEY" \
  --region us-east-1

aws secretsmanager create-secret \
  --name openfireblocks/stripe/api-key \
  --secret-string "$STRIPE_API_KEY" \
  --region us-east-1

aws secretsmanager create-secret \
  --name openfireblocks/workos/api-key \
  --secret-string "$WORKOS_API_KEY" \
  --region us-east-1
```

### Step 2: Configure Vault

```bash
# Initialize Vault (if using on-premise)
vault operator init -key-shares=5 -key-threshold=3

# Unseal Vault
vault operator unseal <key1>
vault operator unseal <key2>
vault operator unseal <key3>

# Create policies
vault policy write openfireblocks - <<EOF
path "/customer/+/keys/*" {
  capabilities = ["read", "list"]
}

path "/service/database/*" {
  capabilities = ["read"]
}
