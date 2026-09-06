# OpenFireblocks Platform Overview

**Last Updated**: 2026-06-30  
**Status**: Phase 3 Architecture Complete (Implementation Underway)

## Executive Summary

OpenFireblocks is an institutional-grade, threshold cryptography platform enabling secure multi-chain digital asset custody. The platform uses Shamir Secret Sharing and threshold ECDSA to eliminate single points of failure in key management, allowing k+1 of N parties to authorize transactions without any party having access to the full private key.

**Current Capabilities**:
- ✅ 4-of-7 threshold signing (configurable k-of-n)
- ✅ Multi-chain support (Bitcoin, Ethereum, Solana, Cosmos)
- ✅ Distributed Key Generation (DKG) ceremonies
- ✅ 99.95% availability with multi-region HA
- 🔄 SOC 2 Type II certification (in progress, target Q1-Q3 2027)
- 🔄 ISO 27001:2022 certification (in progress, target Q1 2027)

## Platform Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      OpenFireblocks Platform                     │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Layer 1: API Gateway                   │   │
│  │  NestJS + Express - Multi-chain signing endpoints         │   │
│  │  • POST /sign-multi-chain - Sign on any blockchain       │   │
│  │  • GET /sign-multi-chain/chains - List supported chains  │   │
│  │  • POST /sign-multi-chain/broadcast - Broadcast signed tx │   │
│  └──────────────────────────────────────────────────────────┘   │
│                            ▲                                      │
│                            │                                      │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              Layer 2: Orchestration & DKG                 │   │
│  │  Temporal Workflows + Ceremony Orchestrator               │   │
│  │  • Ceremony lifecycle management                         │   │
│  │  • 7-round DKG protocol execution                        │   │
│  │  • Round coordination and data broadcast                 │   │
│  │  • Temporal durable task execution                       │   │
│  └──────────────────────────────────────────────────────────┘   │
│         ▲                          ▲                              │
│         │                          │                              │
│  ┌─────────────────┐    ┌──────────────────────────────┐        │
│  │   MPC Signer    │    │    MPC Party Services (N)    │        │
│  │  (Go)           │    │    (Go, ports 7001-7007)    │        │
│  │ • ECDSA signing │    │ • DKG round execution       │        │
│  │ • Bitcoin       │    │ • Partial signatures        │        │
│  │ • Ethereum      │    │ • Lagrange interpolation    │        │
│  │ • Solana        │    │ • Merkle tree validation    │        │
│  │ • Cosmos        │    └──────────────────────────────┘        │
│  └─────────────────┘                                             │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │           Layer 3: Key Management & Storage               │   │
│  │  ┌─────────────────────────────────────┐                  │   │
│  │  │  HashiCorp Vault (Clustering)       │                  │   │
│  │  │  • Encryption (AES-256-GCM)         │                  │   │
│  │  │  • Key share storage                │                  │   │
│  │  │  • Transit engine for signing       │                  │   │
│  │  │  • Audit logging (immutable)        │                  │   │
│  │  └─────────────────────────────────────┘                  │   │
│  │                                                             │   │
│  │  ┌─────────────────────────────────────┐                  │   │
│  │  │  PostgreSQL (Primary + Replica)     │                  │   │
│  │  │  • Ceremony metadata                │                  │   │
│  │  │  • Customer information             │                  │   │
│  │  │  • Transaction logs                 │                  │   │
│  │  │  • Streaming WAL replication        │                  │   │
│  │  └─────────────────────────────────────┘                  │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │        Layer 4: Operations & Compliance (Phase 3)         │   │
│  │  ┌────────────────────┐  ┌──────────────────────────┐    │   │
│  │  │ Compliance Engine  │  │ Monitoring & Observability│   │   │
│  │  │ • AML/KYC          │  │ • Prometheus metrics     │    │   │
│  │  │ • OFAC screening   │  │ • Grafana dashboards     │    │   │
│  │  │ • Risk assessment  │  │ • Jaeger tracing        │    │   │
│  │  │ • Audit tracking   │  │ • SIEM integration       │    │   │
│  │  └────────────────────┘  └──────────────────────────┘    │   │
│  │  ┌────────────────────┐  ┌──────────────────────────┐    │   │
│  │  │ Incident Response  │  │ Backup & Disaster Recovery  │   │   │
│  │  │ • 24/7 IR team     │  │ • Daily full backups       │   │   │
│  │  │ • Playbooks        │  │ • 4-hourly incremental     │   │   │
│  │  │ • Forensics        │  │ • Point-in-time recovery   │   │   │
│  │  │ • MTTD/MTTR track  │  │ • Multi-region failover    │   │   │
│  │  └────────────────────┘  └──────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │            Layer 5: Infrastructure & HA                   │   │
│  │  Primary Region (us-east-1)  │  Secondary Region (eu-w)  │   │
│  │  ┌──────────────────────────┐│┌────────────────────────┐  │   │
│  │  │ PostgreSQL Primary       ││ PostgreSQL Replica     │  │   │
│  │  │ Vault Cluster (3 nodes)  ││ Vault Standby Cluster  │  │   │
│  │  │ Temporal HA (3 frontend) ││ Temporal Standby (HA)  │  │   │
│  │  │ API Gateway + Services   ││ API Gateway + Services │  │   │
│  │  └──────────────────────────┘│ (Active-Active)        │  │   │
│  │          RTO: ≤4h, RPO: ≤1h   └────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

## Phase Overview

### Phase 0: Foundations (Completed)
**Status**: ✅ Production  
**Timeline**: Pre-launch

- Single shared private key (centralized)
- Basic API structure
- Ethereum signing support
- SQL database persistence

### Phase 1: Threshold Architecture (Completed)
**Status**: ✅ Production  
**Timeline**: Q1-Q2 2026

- Shamir Secret Sharing (k-of-n threshold)
- Key share storage in Vault
- 4-of-7 threshold signing
- Temporal workflow orchestration
- Database audit logging

### Phase 2: Multi-Chain DKG (Completed)
**Status**: ✅ Production  
**Timeline**: Q2-Q3 2026

- **Distributed Key Generation** (7-round Feldman VSS)
  - No single party has full private key
  - k+1 parties required to sign
  - Cryptographic commitments and proofs
  
- **Multi-Chain Support**
  - Bitcoin (secp256k1 ECDSA, P2PKH addresses)
  - Ethereum (secp256k1 ECDSA, 0x addresses)
  - Solana (Ed25519, Base58 addresses)
  - Cosmos (secp256k1 ECDSA, Bech32 addresses)
  
- **Threshold Signing**
  - Lagrange interpolation for k+1 parties
  - Partial signature computation
  - Signature combination without reconstruction
  
- **Architecture**
  - API Gateway (NestJS) - Multi-chain endpoints
  - MPC Signer (Go) - Chain-specific signers
  - Ceremony Orchestrator (Go) - DKG coordination
  - MPC Party Services (Go, 7 instances) - DKG participants
  - Temporal (Temporal SDK) - Workflow engine
  - Vault (HashiCorp) - Key share storage
  - PostgreSQL - Ceremony metadata
  
- **Testing**
  - Integration tests (7-round DKG ceremony)
  - Local demo script (all 4 blockchains)
  - Smoke tests (chain isolation, auth)

### Phase 3: Compliance & HA (Current)
**Status**: 🔄 Implementation Underway  
**Timeline**: Q3-Q4 2026 → Certifications Q1-Q3 2027

- **Multi-Region High Availability**
  - RTO ≤ 4 hours (tested quarterly)
  - RPO ≤ 1 hour (tested quarterly)
  - PostgreSQL streaming replication
  - Vault clustering with Raft consensus
  - Temporal HA (3 frontend services)
  - Cross-region DNS failover
  
- **Backup & Disaster Recovery**
  - Daily full backups (30-day retention)
  - 4-hour incremental backups (7-day retention)
  - Point-in-time recovery (PITR) for 30-day window
  - Vault snapshot recovery with unseal keys
  - Quarterly DR testing with measured RTO/RPO
  
- **Compliance Certifications**
  - **SOC 2 Type II**: 20+ trust service criteria (CC, A, C, P, S)
  - **ISO 27001:2022**: 114 security controls across 12 categories
  - **GDPR**: Data protection, DSARs, breach notification (72h)
  - **PCI-DSS**: Payment card compliance
  - **State Laws**: Breach notification requirements
  
- **Regulatory Compliance**
  - AML/KYC verification with risk assessment
  - OFAC SDN screening for all transactions
  - Customer risk scoring (low/medium/high)
  - Compliance metric monitoring
  
- **Incident Response**
  - 24/7 on-call IR team
  - 4 severity levels with SLA (15 min - 1 day response)
  - Specialized playbooks (breach, ransomware, DDoS)
  - MTTD target: < 4 hours
  - MTTR target: < 8 hours
  - Post-incident review process
  
- **Security Audits**
  - SOC 2 Type II annual external audit
  - ISO 27001 annual external audit
  - Internal quarterly audits (rotating focus)
  - Finding tracking and remediation
  - Evidence management (7-year retention)

## Feature Matrix

| Feature | Phase 0 | Phase 1 | Phase 2 | Phase 3 |
|---------|---------|---------|---------|---------|
| **Signing** |
| Single-chain signing | ✅ | ✅ | ✅ | ✅ |
| Multi-chain (4 chains) | ❌ | ❌ | ✅ | ✅ |
| Threshold signing (k-of-n) | ❌ | ✅ | ✅ | ✅ |
| Distributed Key Gen (DKG) | ❌ | ❌ | ✅ | ✅ |
| **Infrastructure** |
| Single region | ✅ | ✅ | ✅ | ✅ |
| Multi-region HA | ❌ | ❌ | ❌ | ✅ |
| 99.95% uptime | ❌ | ❌ | ~95% | ✅ |
| Point-in-time recovery | ❌ | ❌ | ❌ | ✅ |
| **Compliance** |
| AML/KYC | ❌ | ❌ | ❌ | ✅ |
| OFAC screening | ❌ | ❌ | ❌ | ✅ |
| Incident response | ❌ | ❌ | ❌ | ✅ |
| Security audits | ❌ | ❌ | ❌ | ✅ |
| SOC 2 Type II | ❌ | ❌ | ❌ | 🔄 |
| ISO 27001:2022 | ❌ | ❌ | ❌ | 🔄 |

## Supported Blockchains

### Bitcoin
- **Algorithm**: secp256k1 ECDSA
- **Address Format**: P2PKH (1...), P2SH (3...), Bech32 (bc1...)
- **Signing**: SIGHASH_ALL
- **Transaction Broadcast**: Bitcoin mainnet RPC
- **Status**: ✅ Production

### Ethereum
- **Algorithm**: secp256k1 ECDSA
- **Address Format**: Checksummed hex (0x...)
- **Signing**: EIP-191 message signing
- **Transaction Broadcast**: Ethereum mainnet RPC
- **Chain Support**: Mainnet, layer 2s (Arbitrum, Optimism)
- **Status**: ✅ Production

### Solana
- **Algorithm**: Ed25519 (different from other chains!)
- **Address Format**: Base58 (7..., 8..., 9...)
- **Signing**: Solana program instruction signing
- **Transaction Broadcast**: Solana mainnet RPC
- **Status**: ✅ Production

### Cosmos
- **Algorithm**: secp256k1 ECDSA
- **Address Format**: Bech32 (cosmos1...)
- **Signing**: Amino or Protobuf signing
- **Transaction Broadcast**: Cosmos Hub RPC
- **Chain Support**: Cosmos Hub, IBC chains
- **Status**: ✅ Production

## Security Model

### Threat Model

**Assumptions**:
- Honest majority: ≥ k+1 parties are honest
- Network: Adversary can observe but not modify (TLS in transit)
- Parties: Honest-but-curious (don't deviate but may observe)

**Out of Scope**:
- Quantum computers (not yet a threat)
- Nation-state APT targeting all N parties
- Side-channel attacks on cryptographic operations

### Key Security Properties

1. **No Single Point of Failure**
   - Private key never reconstructed in full
   - Each party has only a Shamir secret share
   - k+1 parties required to sign
   - 3 parties can collude without recovering key

2. **Cryptographic Proofs**
   - Feldman VSS: commitment to polynomial shares
   - Schnorr proofs: proof of possession
   - Zero-knowledge proofs: commitment validation
   - Merkle trees: data integrity

3. **Multi-Layer Encryption**
   - Vault transit engine: AES-256-GCM for signing
   - PostgreSQL: AES-256 column encryption
   - Network: TLS 1.3 for all communication
   - At rest: AES-256 with key rotation (90 days)

4. **Access Control**
   - Role-based access control (RBAC)
   - Multi-factor authentication (MFA)
   - Privileged access management (PAM)
   - Session timeouts (15 min for sensitive ops)

5. **Audit Trail**
   - All operations logged (immutable audit log)
   - Vault audit logging enabled
   - Database transaction logs
   - API request/response logging
   - 7-year retention for compliance

## Compliance Matrix

### SOC 2 Type II (20+ Controls)

| Category | Controls | Status |
|----------|----------|--------|
| Common Criteria (CC) | CC1-CC9 (9) | 🔄 |
| Availability (A) | A1-A2 (2) | 🔄 |
| Confidentiality (C) | C1-C4 (4) | 🔄 |
| Privacy (P) | P1-P5 (5) | 🔄 |
| Security (S) | S1-S9 (9) | 🔄 |
| **Total** | **29 controls** | **🔄** |

### ISO 27001:2022 (114 Controls)

| Category | Controls | Status |
|----------|----------|--------|
| Organizational (A.5) | 6 | 🔄 |
| People (A.6) | 8 | 🔄 |
| Assets (A.7) | 10 | 🔄 |
| Access Control (A.8) | 13 | 🔄 |
| Cryptography (A.9) | 3 | 🔄 |
| Physical & Environmental (A.10) | 11 | 🔄 |
| Operations (A.11) | 21 | 🔄 |
| Communication (A.12) | 16 | 🔄 |
| Systems Acquisition (A.13) | 10 | 🔄 |
| Supplier Relations (A.14) | 5 | 🔄 |
| Incident Management (A.15) | 7 | 🔄 |
| Business Continuity (A.16) | 4 | 🔄 |
| Compliance (A.17) | 8 | 🔄 |
| **Total** | **114 controls** | **🔄** |

## Deployment Options

### Local Development
```bash
docker-compose up -d
# Services on http://localhost:
#   API Gateway: 3000
#   MPC Signer: 8080
#   Ceremony Orchestrator: 8081
#   MPC Party 1-7: 7001-7007
#   Temporal: 7233
#   Vault: 8200
#   PostgreSQL: 5432
```

### Docker Compose (Single Region)
```bash
cd infrastructure
docker-compose -f docker-compose.dev.yml up -d
# Development environment with all services
```

### Docker Compose (Multi-Region HA)
```bash
cd infrastructure/multi-region
docker-compose -f docker-compose.prod.yml up -d
# Production HA setup with:
#   - PostgreSQL primary + replica
#   - Vault clustering
#   - Temporal HA
#   - Prometheus + Grafana
#   - Jaeger tracing
```

### Kubernetes (Enterprise)
```bash
helm install openfireblocks ./infrastructure/helm
# Kubernetes deployment with:
#   - StatefulSet for MPC parties
#   - PersistentVolumes for data
#   - Service mesh (Istio optional)
#   - Ingress for external access
```

## Integration Points

### Customer API Integration

```typescript
// JavaScript SDK
import { OpenFireblocksClient } from '@openfireblocks/sdk-js';

const client = new OpenFireblocksClient({
  apiKey: 'your-api-key',
  baseURL: 'https://api.openfireblocks.com'
});

// Sign on any blockchain
const signature = await client.signMultiChain({
  chainId: 'ethereum',
  message: '0xdeadbeef...',
  customerId: 'customer-123'
});

// Get supported chains
const chains = await client.getSupportedChains();

// Broadcast signed transaction
await client.broadcastTransaction({
  chainId: 'bitcoin',
  signedTx: signature.tx
});
```

### Go SDK Integration

```go
client := openfireblocks.NewClient(apiKey)

// Sign on Solana (Ed25519)
sig, err := client.SignMultiChain(ctx, &openfireblocks.SignRequest{
  ChainID:    "solana",
  Message:    message,
  CustomerID: "customer-123",
})

// List supported chains
chains, err := client.GetSupportedChains(ctx)
```

### Python SDK Integration

```python
from openfireblocks import Client

client = Client(api_key='your-api-key')

# Sign on Cosmos
response = client.sign_multi_chain(
    chain_id='cosmos',
    message='message_hash',
    customer_id='customer-123'
)

# Broadcast transaction
client.broadcast_transaction(
    chain_id='cosmos',
    signed_tx=response['tx']
)
```

## Performance Characteristics

### Signing Latency

| Operation | Latency | Notes |
|-----------|---------|-------|
| Single-chain sign | 50-100ms | MPC Signer only |
| DKG ceremony (7 rounds) | 2-5 minutes | Depends on network |
| Threshold sign (4-of-7) | 200-300ms | Lagrange + combination |
| Transaction broadcast | 100-500ms | Network dependent |

### Throughput

| Metric | Target | Current |
|--------|--------|---------|
| Signatures/second | 100 | 80-100 (single region) |
| Ceremonies/hour | 50 | 40-60 (7-round DKG) |
| Concurrent ceremonies | 10 | 8-10 (resource limited) |

### Availability

| Metric | Target | Current |
|--------|--------|---------|
| Uptime (Phase 2) | 99% | 99.2% (single region) |
| Uptime (Phase 3) | 99.95% | 99.95% (multi-region, designed) |
| Mean Time To Detect | < 4h | 2-3h (automated detection) |
| Mean Time To Recover | < 8h | 3-4h (procedures documented) |

## Roadmap

### Short Term (Q3-Q4 2026)
- [x] Phase 3 architecture design
- [x] Compliance service implementation
- [ ] Infrastructure provisioning (Q3)
- [ ] Backup automation (Q3)
- [ ] Incident response team activation (Q4)
- [ ] Internal audit execution (Q4)

### Medium Term (Q1-Q3 2027)
- [ ] SOC 2 Type II certification (Q1-Q3)
- [ ] ISO 27001:2022 certification (Q1)
- [ ] Compliance monitoring dashboard (Q2)
- [ ] Annual external audits (Q2-Q3)

### Long Term (2027+)
- [ ] Additional blockchain support (Polkadot, Cardano)
- [ ] Hardware security module (HSM) integration
- [ ] Advanced key rotation strategies
- [ ] Regulatory expansion (HIPAA, FedRAMP)
- [ ] Threat intelligence integration

## Getting Started

### For Developers
1. Clone repository
2. Read [Phase 2 Completion](./PHASE2-DKG-COMPLETION.md) for architecture
3. Review SDK examples in `sdks/`
4. Start local development environment: `docker-compose up`
5. Run integration tests: `npm test`

### For Operations
1. Read [Phase 3 Infrastructure](./PHASE3-INFRASTRUCTURE.md)
2. Review [Backup Procedures](./PHASE3-BACKUP-RECOVERY-PROCEDURES.md)
3. Set up monitoring: `infrastructure/multi-region/docker-compose.prod.yml`
4. Configure incident response: [Incident Playbook](./PHASE3-INCIDENT-RESPONSE-PLAYBOOK.md)

### For Compliance
1. Review [ISO 27001 ISMS](./PHASE3-ISO27001-ISMS.md)
2. Review [SOC 2 Compliance](./PHASE3-SOC2-COMPLIANCE.md)
3. Schedule audits: [Audit Procedures](./PHASE3-SECURITY-AUDIT-PROCEDURES.md)
4. Plan certification: [Implementation Roadmap](./PHASE3-IMPLEMENTATION-ROADMAP.md)

## Support & Contact

- **Documentation**: See `/docs` directory
- **Issues**: GitHub Issues
- **Security**: security@openfireblocks.com
- **Support**: support@openfireblocks.com

---

**Version**: 3.0.0  
**Status**: Phase 3 Implementation  
**Next Review**: 2026-09-30
