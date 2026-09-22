# OpenFireblocks Custody Layer MVP

**Enterprise-grade institutional cryptocurrency key management and threshold signing platform.**

18-month MVP roadmap. $500K-1M capital. 6-8 engineers. REST/gRPC API-only (no web UI).

## What is the Custody Layer?

Core infrastructure for institutional cryptocurrency asset management:
- **Threshold Cryptography**: 4-of-7 ECDSA key generation using Binance TSS-lib
- **Multi-Chain Support**: Bitcoin, Ethereum, Solana, Cosmos, Polygon
- **Institutional Security**: High-assurance signing, audit logging, compliance automation
- **Multi-Tenant**: Complete isolation for multiple institutional customers
- **Production-Ready**: Designed for AWS production deployment with HA/DR

## Architecture

### Core Services

| Service | Language | Purpose |
|---------|----------|---------|
| **API Gateway** | TypeScript/NestJS | REST/gRPC endpoints for customers |
| **MPC Party** | Go | Threshold signing using Binance TSS-lib |
| **Ceremony Orchestrator** | Go | DKG ceremony coordination |
| **Temporal Worker** | Go | Distributed workflow execution |
| **Compliance Engine** | Go | KYC/AML, risk analysis, audit |
| **Policy Service** | Go | Signing policy enforcement |

### Data Layer

- **PostgreSQL**: Customer data, key metadata, audit logs
- **HashiCorp Vault**: Key share storage with HA clustering
- **Temporal**: Workflow execution for DKG and signing
- **Prometheus/Grafana**: Observability

### Deployment

- **Multi-Region**: Primary (us-east-1) + Secondary (eu-west-1)
- **RTO**: ≤4 hours, **RPO**: ≤1 hour
- **ECS**: Containerized services with auto-scaling
- **Infrastructure-as-Code**: Terraform modules

## Getting Started (Local Development)

### Prerequisites

```bash
# Install Docker and Docker Compose
docker --version  # 24.0+
docker-compose --version  # 2.0+

# Verify Go 1.21+ and Node 18+
go version
node --version
```

### Start Development Environment

```bash
# Clone repository
git clone https://github.com/openfireblocks/openfireblocks
cd openfireblocks

# Start all services (PostgreSQL, Vault, Temporal, etc.)
docker-compose -f docker-compose.dev.yml up -d

# Wait for services to be healthy
docker-compose -f docker-compose.dev.yml ps

# Initialize database
docker-compose -f docker-compose.dev.yml exec postgres psql -U postgres -d openfireblocks < infrastructure/database/migrations/001_initial_schema.sql
```

### Access Services

| Service | URL | Credentials |
|---------|-----|-------------|
| API Gateway | http://localhost:3000 | N/A |
| Vault | http://localhost:8200 | Token: dev-token |
| Temporal UI | http://localhost:8080 | N/A |
| Grafana | http://localhost:3001 | admin / grafana-dev-password |
| Prometheus | http://localhost:9090 | N/A |

## API Usage

### Go SDK

```go
import "github.com/openfireblocks/sdk-go"

client := openfireblocks.NewClient(
    "https://api.openfireblocks.io",
    "your-api-key",
)

// Create threshold key
key, err := client.CreateKeyPair(ctx, &openfireblocks.CreateKeyPairRequest{
    Name: "bitcoin-cold-wallet",
    Blockchain: "bitcoin",
    Threshold: 4,
    TotalParties: 7,
})

// Sign transaction
sig, err := client.Sign(ctx, &openfireblocks.SigningRequest{
    KeyPairID: key.ID,
    Transaction: "02f86a0102848c51d60085...",
})

// Wait for completion
signed, err := client.GetSigningStatus(ctx, sig.ID)
```

### Python SDK

```python
from openfireblocks import Client, CreateKeyPairRequest, SigningRequest

client = Client(
    base_url="https://api.openfireblocks.io",
    api_key="your-api-key"
)

# Create key
key = client.create_key_pair(CreateKeyPairRequest(
    name="ethereum-hot-wallet",
    blockchain="ethereum",
    threshold=3,
    total_parties=5,
))

# Sign transaction
sig = client.sign(SigningRequest(
    key_pair_id=key.id,
    transaction="02f86a0102848c51d60085...",
))

# Poll for completion
completed = client.wait_for_signing(sig.id, max_wait=300)
print(f"Signature: {completed.signature}")
```

### JavaScript SDK (Existing)

```javascript
import { OpenFireblocks } from '@openfireblocks/sdk';

const client = new OpenFireblocks({
  baseUrl: 'https://api.openfireblocks.io',
  apiKey: 'your-api-key',
});

// Create key
const key = await client.keys.create({
  name: 'solana-signing-key',
  blockchain: 'solana',
  threshold: 4,
  totalParties: 7,
});

// Sign
const sig = await client.sign({
  keyPairId: key.id,
  transaction: '...',
});

// Wait
const result = await client.sign.waitForCompletion(sig.id);
```

## Key Concepts

### Threshold ECDSA (k-of-n)

Default: **4-of-7** (4 signatures required out of 7 parties)

Each DKG ceremony:
1. **Round 1**: Each party generates random coefficients (Feldman VSS)
2. **Round 2-7**: Parties exchange commitments and proofs
3. **Result**: Key shares distributed to Vault, no central key

### DKG Ceremony Flow

```
Client Request
    ↓
API Gateway validates
    ↓
Ceremony Orchestrator creates ceremony record
    ↓
Temporal DKG workflow starts
    ↓
Parties participate in 7 rounds
    ↓
MPC Party services exchange messages
    ↓
Key shares sealed in Vault per party
    ↓
Key pair marked as "active"
    ↓
Ready for signing
```

### Signing Flow

```
Sign Request
    ↓
API Gateway validates idempotency
    ↓
Policy Engine checks policies
    ↓
Compliance Engine checks transaction
    ↓
Temporal signing workflow starts
    ↓
Parties reconstruct from k-of-n shares
    ↓
Threshold ECDSA signing
    ↓
Signature combined and returned
    ↓
Audit logged
```

## Database Schema

### Core Tables

- **customers**: Tenant isolation
- **key_pairs**: Threshold keys (Bitcoin, Ethereum, etc.)
- **dkg_ceremonies**: DKG ceremony execution records
- **dkg_rounds**: Per-round data from each party
- **key_shares**: Sealed shares in Vault
- **signing_requests**: Transaction signing requests
- **audit_logs**: Complete audit trail
- **policies**: Signing policies per customer
- **compliance_checks**: Risk analysis results

See `infrastructure/database/migrations/001_initial_schema.sql` for complete schema.

## Security Model

### Defense in Depth

1. **Network Layer**: VPC isolation, security groups, WAF
2. **Transport Layer**: TLS 1.3 encryption
3. **Cryptographic Layer**: Threshold ECDSA, no single key
4. **Application Layer**: API key auth, policy enforcement
5. **Operational Layer**: Audit logging, compliance automation

### Key Share Protection

- Sealed in HashiCorp Vault
- KMS encryption at rest (AWS KMS)
- Each party controls only 1 of k-of-n shares
- Requires network consensus for signing
- No single party can sign unilaterally

### Audit Trail

Every action logged:
- DKG ceremony creation and completion
- Key operations (create, rotate, revoke)
- Signing requests (submitted, approved, signed)
- Policy changes
- Access attempts
- Compliance checks

## Deployment (Production)

### Prerequisites

```bash
# AWS account with terraform permissions
aws sts get-caller-identity

# Terraform ≥1.0
terraform version

# AWS CLI configured
aws configure
```

### Deploy Infrastructure (Week 1)

```bash
cd infrastructure/terraform

# Copy and customize configuration
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars:
#   - db_password: secure 16+ char password
#   - primary_region: us-east-1
#   - secondary_region: eu-west-1
#   - vault_node_count: 3

# Create S3 backend
aws s3api create-bucket \
  --bucket openfireblocks-terraform-state-$(aws sts get-caller-identity --query Account --output text) \
  --region us-east-1

aws dynamodb create-table \
  --table-name terraform-lock \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST

# Initialize and deploy
terraform init
terraform plan -out=prod.tfplan
terraform apply prod.tfplan
```

Expected cost: **$2,500-3,500/month** (primary + secondary, including HA failover)

### Verify Deployment

```bash
# Get outputs
terraform output -json > infrastructure-outputs.json

# Test database
psql -h $(terraform output -raw primary_rds_endpoint) \
  -U postgres -d openfireblocks -c "SELECT 1;"

# Test Vault
curl -k $(terraform output -raw primary_vault_endpoint)/v1/sys/health

# Test ECS cluster
aws ecs list-services --cluster openfireblocks-prod
```

## Testing

### Unit Tests

```bash
# Go services
cd services/mpc-signer && go test ./...
cd services/ceremony-orchestrator && go test ./...

# NestJS API Gateway
cd services/api-gateway && npm test
```

### Integration Tests

```bash
cd infrastructure/tests && go test -tags=integration ./...
```

### E2E Tests

```bash
# Full ceremony from key creation to signing
npm run test:e2e
```

## Monitoring

### Metrics (Prometheus)

- DKG ceremony duration
- Signing latency (p50, p95, p99)
- Key share status
- API request rate and errors
- Database connections
- Vault health

### Dashboards (Grafana)

- Service health overview
- DKG ceremony status
- Signing performance
- Customer usage metrics
- Compliance audit trail

### Alerts

- DKG ceremony failures
- Signing timeout
- Database replication lag
- Vault unsealing issues
- Key share corruption detected

## Roadmap (18 Months)

| Phase | Timeline | Deliverables |
|-------|----------|--------------|
| **Phase 1: Foundation** | Months 1-3 | Core API, threshold signing, DKG |
| **Phase 2: Hardening** | Months 4-6 | Security audit, compliance (SOC 2), HA/DR |
| **Phase 3: Scale** | Months 7-12 | Performance optimization, multi-chain, webhooks |
| **Phase 4: Commercialize** | Months 13-18 | Billing, sales, customer success, GTM |

## Support & Documentation

- [API Specification](api/openapi.yaml)
- [Terraform README](infrastructure/terraform/README.md)
- [Week 1 Execution Guide](docs/WEEK1-EXECUTION-GUIDE.md)
- [Production Readiness](docs/PRODUCTION-READINESS-CHECKLIST.md)
- [Runbooks](docs/RUNBOOKS.md)

## License

Apache License 2.0
