# OpenFireblocks AWS Infrastructure-as-Code

Complete Terraform configuration for deploying OpenFireblocks to AWS with multi-region high availability, disaster recovery, and compliance-ready security.

## Architecture Overview

This infrastructure provides:

- **Multi-Region Deployment**: Primary (us-east-1) and Secondary (eu-west-1) regions
- **Database High Availability**: PostgreSQL Multi-AZ primary with cross-region read replica
- **Vault Clustering**: 3-node HA Vault cluster with Raft consensus and S3 backend
- **Auto Scaling**: ECS clusters with auto-scaling for containerized services
- **Encryption**: KMS encryption at rest for all data, TLS 1.3 in transit
- **Backups**: Daily full + 4-hourly incremental backups with cross-region replication
- **Monitoring**: CloudWatch Logs, Performance Insights, Enhanced Monitoring enabled
- **VPC Peering**: Cross-region connectivity with automatic failover DNS
- **Security**: Network segmentation with security groups, IAM roles, and least-privilege access

## Prerequisites

1. **AWS Account**: Access to AWS with appropriate permissions
2. **Terraform**: Version 1.0 or higher
3. **AWS CLI**: Configured with credentials
4. **S3 Bucket**: For Terraform state (optional but recommended)
5. **DynamoDB Table**: For Terraform state locking (optional but recommended)

## Quick Start

### 1. Initialize Terraform

```bash
cd infrastructure/terraform

# Initialize working directory with required plugins
terraform init
```

### 2. Configure Variables

```bash
# Copy example configuration
cp terraform.tfvars.example terraform.tfvars

# Edit with your values
vim terraform.tfvars
```

### 3. Review Plan

```bash
# Plan changes (review what will be created)
terraform plan -out=tfplan
```

### 4. Apply Configuration

```bash
# Apply changes to AWS
terraform apply tfplan
```

## Configuration

### Required Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `db_password` | (required) | PostgreSQL master password (min 16 chars) |

### Important Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `primary_region` | us-east-1 | Primary AWS region |
| `secondary_region` | eu-west-1 | Secondary AWS region |
| `environment` | prod | Environment name (dev/staging/prod) |
| `primary_vpc_cidr` | 10.0.0.0/16 | Primary VPC CIDR block |
| `secondary_vpc_cidr` | 10.1.0.0/16 | Secondary VPC CIDR block |
| `db_instance_class` | db.r6i.xlarge | RDS instance type |
| `db_allocated_storage` | 100 | Initial storage in GB |
| `db_max_allocated_storage` | 500 | Max storage for autoscaling in GB |
| `vault_node_count` | 3 | Vault cluster nodes (1, 3, 5, or 7) |
| `vault_instance_type` | t3.large | Vault instance type |

### Optional Variables

```hcl
enable_vpn_gateway          = false                    # Enable VPN gateway
backup_retention_days       = 30                       # Backup retention
enable_performance_insights = true                     # RDS Performance Insights
enable_enhanced_monitoring  = true                     # RDS Enhanced Monitoring
cloudwatch_log_retention_days = 30                     # CloudWatch retention
```

## Modules

### VPC Module (`modules/vpc`)
- VPC with public and private subnets
- Internet Gateway and NAT Gateways
- VPC Flow Logs for network monitoring
- DB and cache subnet groups

### RDS Module (`modules/rds`)
- PostgreSQL 16.1 with Multi-AZ
- Automated backups with 30-day retention
- Performance Insights and Enhanced Monitoring
- KMS encryption at rest
- Custom parameter group with logging

### RDS Replica Module (`modules/rds-replica`)
- Read-only PostgreSQL replica in secondary region
- Streaming replication from primary
- Same security and monitoring as primary

### Vault Module (`modules/vault`)
- 3-node HA Vault cluster
- Raft consensus backend
- S3 storage with KMS encryption
- Network Load Balancer
- Auto Scaling Group for failover

### ECS Module (`modules/ecs`)
- ECS cluster for containerized services
- Auto Scaling for compute capacity
- CloudWatch Container Insights
- IAM roles for tasks and execution

## Security Groups

Organized by component:

- **RDS**: Ingress from Vault and ECS, egress to all
- **Vault**: Port 8200 (API) and 8201 (cluster) from VPC
- **ECS**: HTTP/HTTPS from internet, internal ports from VPC

All follow least-privilege principle with specific source restrictions.

## Outputs

Key outputs available after applying:

```bash
# Get all outputs
terraform output

# Specific outputs
terraform output primary_rds_endpoint
terraform output primary_vault_endpoint
terraform output primary_ecs_cluster_name
```

## State Management

### Local State (Development)
```bash
terraform init
```

### Remote State (Production - Recommended)

First, create S3 bucket and DynamoDB table:

```bash
# Using AWS CLI
aws s3api create-bucket \
  --bucket openfireblocks-terraform-state \
  --region us-east-1

aws dynamodb create-table \
  --table-name terraform-lock \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST
```

Then uncomment backend config in `main.tf`:

```hcl
terraform {
  backend "s3" {
    bucket         = "openfireblocks-terraform-state"
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "terraform-lock"
  }
}
```

Reinitialize:
```bash
terraform init
```

## Deployment Steps

### Week 1 Infrastructure Setup

1. **Day 1-2**: Validate AWS credentials and permissions
   ```bash
   aws sts get-caller-identity
   ```

2. **Day 2-3**: Review and customize terraform.tfvars
   ```bash
   terraform plan
   # Review changes carefully
   ```

3. **Day 3-4**: Deploy infrastructure
   ```bash
   terraform apply
   # Monitor AWS console for resource creation
   ```

4. **Day 4-5**: Verify deployment
   ```bash
   # Test database connectivity
   psql -h $(terraform output -raw primary_rds_address) -U postgres

   # Verify Vault endpoints
   curl $(terraform output -raw primary_vault_endpoint)/v1/sys/health
   ```

5. **Day 5**: Document access points
   ```bash
   terraform output -json > infrastructure.json
   ```

## Scaling

### Increase Vault Nodes

```hcl
vault_node_count = 5  # Change in terraform.tfvars
terraform apply
```

### Increase RDS Storage

```hcl
db_allocated_storage = 200  # Change in terraform.tfvars
terraform apply
```

### Scale ECS Capacity

Edit `modules/ecs/variables.tf`:
```hcl
max_capacity = 20
desired_capacity = 10
```

## Troubleshooting

### RDS Endpoint Not Available

```bash
# Check RDS status
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-prod-db \
  --query 'DBInstances[0].DBInstanceStatus'
```

### Vault Cluster Unhealthy

```bash
# Check Vault logs
aws logs tail /aws/vault/openfireblocks-prod --follow

# Check ASG health
aws autoscaling describe-auto-scaling-groups \
  --auto-scaling-group-names openfireblocks-prod-vault-asg
```

### VPC Peering Issues

```bash
# Verify peering connection
aws ec2 describe-vpc-peering-connections \
  --filters Name=status-code,Values=active
```

## Cost Optimization

- Use Reserved Instances for predictable workloads
- Enable S3 Intelligent-Tiering for backups
- Configure RDS autoscaling to right-size instances
- Use spot instances for non-critical ECS tasks
- Set appropriate CloudWatch log retention

## Maintenance

### Backup Verification

```bash
# Monthly: restore backup to isolated environment
aws rds restore-db-instance-from-db-snapshot \
  --db-instance-identifier openfireblocks-test-restore \
  --db-snapshot-identifier <snapshot-id>
```

### Security Group Audits

```bash
# Review security group rules
terraform state show 'aws_security_group.rds_primary'
```

### Update Vault Version

Update `modules/vault/user_data.sh` and redeploy:

```bash
terraform taint module.vault_primary.aws_launch_template.vault
terraform apply
```

## Disaster Recovery

### RTO/RPO Targets
- **RTO** (Recovery Time Objective): ≤ 4 hours
- **RPO** (Recovery Point Objective): ≤ 1 hour

### Failover Procedure

1. **Promote secondary database**:
   ```bash
   aws rds promote-read-replica \
     --db-instance-identifier openfireblocks-prod-db-replica
   ```

2. **Update Vault to secondary region**:
   - Switch API endpoint to secondary Vault cluster
   - Update application configuration

3. **DNS failover**:
   - Update Route53 to point to secondary region

4. **Test failover monthly**:
   ```bash
   terraform plan -var-file=terraform.tfvars
   # Verify both regions operational
   ```

## Support & Documentation

- Terraform: https://registry.terraform.io/providers/hashicorp/aws/latest/docs
- AWS RDS: https://docs.aws.amazon.com/rds/
- HashiCorp Vault: https://www.vaultproject.io/docs
- OpenFireblocks: See main README.md

## License

See LICENSE file in repository root.
