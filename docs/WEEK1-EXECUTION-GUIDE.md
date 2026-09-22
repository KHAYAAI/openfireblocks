# Week 1 Execution Guide: Infrastructure & Security

**Target**: Deploy production VPC, RDS, Vault, and ECS with security hardening  
**Duration**: 10 business days  
**Owner**: Infrastructure Lead + Security Engineer  
**Go-Live Gate**: All infrastructure operational and tested

---

## Day 1-2: AWS Account Setup & Preparation

### Prerequisites Validation
```bash
# Verify AWS CLI configured
aws sts get-caller-identity

# Expected output:
# {
#   "UserId": "...",
#   "Account": "123456789012",
#   "Arn": "arn:aws:iam::123456789012:root"
# }

# Verify terraform installed (≥1.0)
terraform version

# Verify AWS permissions for Terraform operations
aws ec2 describe-vpcs --max-results 1
aws rds describe-db-instances --max-results 1
```

### AWS Account Configuration

**Create S3 bucket for Terraform state** (production best practice):
```bash
aws s3api create-bucket \
  --bucket openfireblocks-terraform-state-$(aws sts get-caller-identity --query Account --output text) \
  --region us-east-1

aws s3api put-bucket-versioning \
  --bucket openfireblocks-terraform-state-$(aws sts get-caller-identity --query Account --output text) \
  --versioning-configuration Status=Enabled

aws s3api put-bucket-encryption \
  --bucket openfireblocks-terraform-state-$(aws sts get-caller-identity --query Account --output text) \
  --server-side-encryption-configuration '{
    "Rules": [{
      "ApplyServerSideEncryptionByDefault": {
        "SSEAlgorithm": "AES256"
      }
    }]
  }'

aws s3api put-bucket-public-access-block \
  --bucket openfireblocks-terraform-state-$(aws sts get-caller-identity --query Account --output text) \
  --public-access-block-configuration \
    "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"
```

**Create DynamoDB table for Terraform state locking**:
```bash
aws dynamodb create-table \
  --table-name terraform-lock \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region us-east-1
```

### Enable S3 Backend in Terraform

Uncomment in `infrastructure/terraform/main.tf`:
```hcl
terraform {
  backend "s3" {
    bucket         = "openfireblocks-terraform-state-ACCOUNT_ID"
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "terraform-lock"
  }
}
```

Then reinitialize:
```bash
cd infrastructure/terraform
terraform init
```

---

## Day 2-3: Configuration & Planning

### Customize Terraform Variables

```bash
cd infrastructure/terraform

# Copy example configuration
cp terraform.tfvars.example terraform.tfvars

# Edit for your environment
vim terraform.tfvars
```

**Critical settings to customize**:
```hcl
# Authentication & Secrets
db_password = "GENERATE_SECURE_PASSWORD_MIN_16_CHARS"  # Use: openssl rand -base64 16

# Regions
primary_region   = "us-east-1"     # Change if preferred
secondary_region = "eu-west-1"     # Change if preferred

# Environment
environment = "prod"

# Capacity Sizing (adjust based on expected load)
vault_node_count   = 3               # High Availability (min 3)
db_instance_class  = "db.r6i.xlarge" # Memory-optimized for performance
api_gateway_instance_count = 3       # Min 3 for HA
```

### Review Infrastructure Plan

```bash
# Generate plan (review all changes)
terraform plan -out=week1.tfplan

# Expected resources: ~80 resources across 5 modules
# Expected cost: $2,500-3,500/month for primary + secondary regions

# Document the plan
terraform show week1.tfplan > week1-plan.txt
```

**Review checklist**:
- [ ] VPC CIDR blocks don't conflict with existing networks
- [ ] All 3 regions have proper security groups
- [ ] RDS Multi-AZ enabled for high availability
- [ ] Vault using S3 backend with KMS encryption
- [ ] KMS keys set to auto-rotate
- [ ] All resources tagged appropriately
- [ ] CloudWatch logs enabled on all services

---

## Day 3-4: Infrastructure Deployment

### Phase 1: Deploy Core Infrastructure (2-3 hours)

```bash
# Apply Terraform configuration
terraform apply week1.tfplan

# Monitor deployment in AWS console:
# - EC2 > VPCs (check VPC creation)
# - RDS > Databases (monitor db creation, typically 5-10 min)
# - S3 > Buckets (verify backup buckets)
# - VPC > Peering Connections (verify cross-region peering)
```

**During deployment, watch for**:
- RDS Multi-AZ sync (primary + standby)
- Vault cluster bootstrap (~5 minutes)
- ECS cluster instance registration
- Security group creation

### Phase 2: Verify Infrastructure (30-45 min)

```bash
# Get outputs
terraform output

# Save for integration
terraform output -json > infrastructure-outputs.json

# Export key endpoints for testing
export PRIMARY_DB_ENDPOINT=$(terraform output -raw primary_rds_endpoint)
export PRIMARY_VAULT=$(terraform output -raw primary_vault_endpoint)
export SECONDARY_DB_ENDPOINT=$(terraform output -raw secondary_rds_endpoint)
```

**Verification checklist**:

```bash
# 1. Test RDS Primary connectivity
psql -h ${PRIMARY_DB_ENDPOINT%:*} \
     -U postgres \
     -d openfireblocks \
     -c "SELECT version();"

# Expected: PostgreSQL 16.1

# 2. Test RDS Secondary is in sync
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-prod-db-replica \
  --query 'DBInstances[0].DBReplicationSourceIdentifier'

# Expected: openfireblocks-prod-db

# 3. Verify Vault cluster health
curl -k ${PRIMARY_VAULT}/v1/sys/health

# Expected: {"initialized":false,"sealed":true,...}

# 4. Test VPC peering
aws ec2 describe-vpc-peering-connections \
  --filters "Name=status-code,Values=active" \
  --query 'VpcPeeringConnections[*].{ID:VpcPeeringConnectionId,Status:Status.Code}'

# Expected: one active peering connection

# 5. Check KMS keys are operational
aws kms list-keys --region us-east-1
aws kms describe-key --key-id alias/openfireblocks-primary

# Expected: key is Enabled
```

---

## Day 4-5: Security Hardening

### Network Isolation Verification

```bash
# Check security groups are correctly configured
aws ec2 describe-security-groups \
  --filters "Name=tag:Project,Values=OpenFireblocks" \
  --query 'SecurityGroups[*].{Name:GroupName,Rules:IpPermissions}' \
  --output json | jq '.[]'

# Verify RDS is NOT publicly accessible
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-prod-db \
  --query 'DBInstances[0].PubliclyAccessible'

# Expected: false

# Verify encryption enabled
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-prod-db \
  --query 'DBInstances[0].StorageEncrypted'

# Expected: true
```

### TLS Certificate Configuration

```bash
# Request ACM certificate for API domain
aws acm request-certificate \
  --domain-name api.openfireblocks.io \
  --subject-alternative-names "*.openfireblocks.io" \
  --validation-method DNS \
  --region us-east-1

# Get certificate ARN (for manual validation)
aws acm list-certificates --region us-east-1

# Validate via DNS (check ACM console for CNAME record to add)
# Add _validation records to your DNS provider

# Verify certificate status
aws acm describe-certificate \
  --certificate-arn arn:aws:acm:us-east-1:ACCOUNT:certificate/ID \
  --query 'Certificate.Status'

# Expected: ISSUED (after DNS validation)
```

### WAF Rules Validation

```bash
# Check WAF web ACL is active
aws wafv2 list-web-acls \
  --scope REGIONAL \
  --region us-east-1

# Review rule metrics
aws cloudwatch get-metric-statistics \
  --namespace AWS/WAFV2 \
  --metric-name BlockedRequests \
  --dimensions Name=Rule,Value=RateLimitRule \
  --start-time $(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%S) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%S) \
  --period 300 \
  --statistics Sum
```

**Security hardening checklist**:
- [ ] All instances in private subnets (no public IP)
- [ ] Security groups follow least-privilege principle
- [ ] Encryption at rest enabled (KMS)
- [ ] Encryption in transit (TLS 1.3)
- [ ] VPC Flow Logs enabled for network monitoring
- [ ] WAF rules deployed on load balancers
- [ ] AWS Secrets Manager configured for database credentials
- [ ] CloudTrail enabled for audit logging
- [ ] GuardDuty enabled for threat detection

---

## Day 5: Backup & Disaster Recovery Setup

### Backup Configuration Verification

```bash
# Check RDS automated backups
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-prod-db \
  --query 'DBInstances[0].[BackupRetentionPeriod,PreferredBackupWindow]'

# Expected: [30, "00:00-01:00"]

# Check S3 backup bucket exists
aws s3 ls s3://openfireblocks-backups-us-east-1-ACCOUNT/

# Verify bucket encryption
aws s3api get-bucket-encryption \
  --bucket openfireblocks-backups-us-east-1-ACCOUNT

# Expected: KMS encryption configured
```

### First Backup Test

```bash
# Create manual snapshot for testing
aws rds create-db-snapshot \
  --db-instance-identifier openfireblocks-prod-db \
  --db-snapshot-identifier openfireblocks-first-backup-$(date +%s)

# Monitor snapshot progress (typically 10-30 min)
aws rds describe-db-snapshots \
  --query 'DBSnapshots[0].[DBSnapshotIdentifier,PercentProgress,Status]'

# Wait until Status=available

# Test restore to isolated environment (non-production)
aws rds restore-db-instance-from-db-snapshot \
  --db-instance-identifier openfireblocks-test-restore \
  --db-snapshot-identifier openfireblocks-first-backup-TIMESTAMP \
  --db-instance-class db.t3.micro  # Smaller instance for testing

# Verify restore completed successfully
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-test-restore \
  --query 'DBInstances[0].[DBInstanceStatus,Endpoint.Address]'

# Expected: available + accessible endpoint

# Perform data integrity check
psql -h $(aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-test-restore \
  --query 'DBInstances[0].Endpoint.Address' \
  --output text) \
  -U postgres \
  -d openfireblocks \
  -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public';"

# Clean up test restore
aws rds delete-db-instance \
  --db-instance-identifier openfireblocks-test-restore \
  --skip-final-snapshot
```

**Backup verification checklist**:
- [ ] Automated backups running on schedule
- [ ] Manual backup created successfully
- [ ] Backup restore test passed
- [ ] Data integrity verified
- [ ] Cross-region backup replication configured
- [ ] Backup lifecycle policies set
- [ ] Restore procedures documented
- [ ] RTO/RPO targets confirmed (<4h/<1h)

---

## Week 1 Completion: Success Criteria

### Infrastructure Checklist
- [ ] VPC deployed in primary region with public/private subnets
- [ ] VPC deployed in secondary region
- [ ] VPC peering active between regions
- [ ] RDS primary instance operational (Multi-AZ)
- [ ] RDS secondary replica in sync
- [ ] Vault cluster initialized (3 nodes)
- [ ] ECS cluster operational (3+ instances)
- [ ] All security groups configured
- [ ] All KMS keys operational with rotation enabled
- [ ] All CloudWatch log groups created

### Security Checklist
- [ ] Network ACLs configured on all subnets
- [ ] Security groups follow least-privilege
- [ ] No public access to RDS/Vault
- [ ] TLS certificates requested (pending validation)
- [ ] WAF rules active on load balancers
- [ ] Encryption at rest enabled (KMS)
- [ ] Encryption in transit enabled (TLS)
- [ ] CloudTrail enabled
- [ ] GuardDuty enabled
- [ ] VPC Flow Logs enabled

### Backup & DR Checklist
- [ ] First automated backup completed
- [ ] Manual snapshot created and tested
- [ ] Restore procedure validated
- [ ] RTO measured: ≤4 hours
- [ ] RPO measured: ≤1 hour
- [ ] Backup retention policy set (30 days)
- [ ] Cross-region replication configured
- [ ] DR runbook drafted

### Operational Readiness
- [ ] Infrastructure diagram created (from terraform output)
- [ ] Access procedures documented
- [ ] Escalation paths defined
- [ ] On-call rotation starting
- [ ] Monitoring baseline established
- [ ] Alerting configured
- [ ] Team trained on infrastructure
- [ ] Security review scheduled (Day 5)

---

## Troubleshooting

### RDS Creation Stuck
```bash
# Check CloudFormation stack (Terraform uses CloudFormation under the hood)
aws cloudformation describe-stack-events \
  --stack-name openfireblocks-rds-primary

# Common issues:
# - Subnet group doesn't exist: Recreate module/vpc first
# - IAM role missing: Check IAM permissions
# - KMS key not accessible: Verify key policy allows RDS service
```

### Vault Cluster Not Healthy
```bash
# Check EC2 instance status
aws ec2 describe-instance-status \
  --filters "Name=tag:Name,Values=openfireblocks-prod-vault*" \
  --query 'InstanceStatuses[*].[InstanceId,InstanceStatus.Status]'

# Check Vault logs
aws logs tail /aws/vault/openfireblocks-prod --follow

# Common issues:
# - Instances not running: Check ASG status
# - Storage unreachable: Verify S3 bucket permissions
# - Unseal failing: Check KMS key permissions
```

### VPC Peering Not Working
```bash
# Check peering connection status
aws ec2 describe-vpc-peering-connections \
  --query 'VpcPeeringConnections[0].Status'

# Check route tables
aws ec2 describe-route-tables \
  --filters "Name=vpc-id,Values=$(terraform output -raw primary_vpc_id)" \
  --query 'RouteTables[*].[RouteTableId,Routes]'

# If routes missing: Add manually or update security groups
aws ec2 create-route \
  --route-table-id rtb-xxxxx \
  --destination-cidr-block 10.1.0.0/16 \
  --vpc-peering-connection-id pcx-xxxxx
```

---

## Next Steps (Week 2)

- [ ] Deploy monitoring stack (Prometheus, Grafana)
- [ ] Set up centralized logging (ELK/CloudWatch)
- [ ] Configure alerting (PagerDuty/CloudWatch)
- [ ] Create runbooks for common operations
- [ ] Schedule disaster recovery test (Week 5)

---

## Reference Documents

- [Launch Strategy](LAUNCH-STRATEGY.md) - Full 12-week plan
- [Production Readiness Checklist](PRODUCTION-READINESS-CHECKLIST.md) - Complete requirements
- [Terraform README](../infrastructure/terraform/README.md) - Infrastructure details
- [Phase 3 Implementation Roadmap](PHASE3-IMPLEMENTATION-ROADMAP.md) - Compliance & HA

---

**Week 1 Owner**: Infrastructure Lead  
**Week 1 Review Date**: End of Day 5  
**Sign-off**: Infrastructure Lead + Security Engineer
