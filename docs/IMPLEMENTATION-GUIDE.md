# OpenFireblocks Implementation Guide: Phase 2 & Phase 3

This guide explains the complete roadmap from the current Phase 1 MVP through Phase 3 (bank-grade production). It includes architecture decisions, implementation strategies, and go/no-go gates.

## Table of Contents

1. [Phase 1 → Phase 2 Transition](#phase-1--phase-2-transition)
2. [Phase 2 Implementation Path](#phase-2-implementation-path)
3. [Phase 3 Implementation Path](#phase-3-implementation-path)
4. [Code Organization](#code-organization)
5. [Testing & Validation](#testing--validation)
6. [Deployment Strategy](#deployment-strategy)

---

## Phase 1 → Phase 2 Transition

### Current State (Phase 1)
- ✅ Single shared ECDSA key (loaded at startup from env or Vault)
- ✅ Ethereum signing only (RLP-encoded transactions)
- ✅ Multi-tenant API with OPA policies
- ✅ Temporal workflow for durable settlements
- ✅ PostgreSQL audit trail + immudb immutable ledger
- ✅ Prometheus/Grafana metrics

### Phase 2 Adds
- 🔄 Real MPC threshold signing (k-of-n ECDSA via Binance TSS-Lib)
  - Distributed Key Generation (DKG) ceremony orchestration
  - Per-party key shares sealed in Vault
  - Signing requires k+1 parties in agreement
- 🔄 Multi-chain support (Bitcoin, Solana, Cosmos)
  - Chain-agnostic signing interface
  - Chain-specific transaction builders
  - Multi-chain API endpoints

### Backward Compatibility
- Phase 1 single-key signing continues to work
- Migration path: `single key → first ceremony → threshold signing`
- Deprecation: Phase 1 path deprecated after 6 months, sunsetting in 12 months

---

## Phase 2 Implementation Path

### 2.1 Ceremony Infrastructure (Weeks 1-2)

**Goal:** Build the foundation for DKG ceremony orchestration.

**Tasks:**
1. Create `ceremony-orchestrator` service (Go)
   - HTTP API for ceremony lifecycle
   - Ceremony state machine (pending → round1-7 → completed)
   - PostgreSQL schema for ceremonies + parties + rounds
   
2. Create `mpc-party` service (Go)
   - HTTP server (mTLS) for party-to-party communication
   - DKG round handler
   - Signing participant

3. Database migrations
   - `ceremonies` table (ceremony metadata)
   - `ceremony_parties` table (party tracking)
   - `ceremony_rounds` table (round tracking)
   - `threshold_transactions` table (multi-chain tx logging)

**Deliverables:**
- [ ] `services/ceremony-orchestrator/` with HTTP API stubs
- [ ] `services/mpc-party/` with basic structure
- [ ] `infrastructure/migrations/phase-2-ceremonies.sql`
- [ ] In-process test: 2-of-3 ceremony creates ceremony record

### 2.2 Ceremony State Machine & Temporal Workflows (Weeks 2-3)

**Goal:** Implement ceremony orchestration using Temporal.

**Tasks:**
1. Define Temporal workflows
   - `DKGCeremonyWorkflow` — drives ceremony through 7 rounds
   - `ThresholdSigningWorkflow` — orchestrates multi-party signing
   - `KeyRotationWorkflow` — manage key share rotation

2. Implement ceremony activities
   - `RegisterParties` — validate and enroll parties
   - `ExecuteRound` — coordinate DKG round across parties
   - `SealKeyShares` — save shares in Vault after ceremony completion
   - `RequestSignatures` — collect signatures from parties

3. Wire ceremony-orchestrator to Temporal
   - Create ceremony → start DKG workflow
   - Query workflow state for API responses

**Deliverables:**
- [ ] `services/temporal-worker/ceremonies/` with workflows
- [ ] Integration test: 3-of-5 in-process DKG via Temporal
- [ ] Ceremony status API reflects workflow state
- [ ] Error handling: party dropout, timeout, message loss

### 2.3 Vault Integration for Key Shares (Week 3)

**Goal:** Securely store key shares in Vault.

**Tasks:**
1. Design Vault secret structure
   - Path: `secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId}`
   - Secret: serialized key share (shares, commitments, proofs)
   
2. Implement key share sealing
   - After DKG completion: seal each party's share
   - Trigger: ceremony completion activity
   - Validation: at least k+1 shares exist before sealing

3. Implement key share retrieval
   - For signing: fetch k+1 shares by ceremony + party IDs
   - Audit logging: every access to key shares
   - Rate limiting: prevent brute-force attempts

4. Backup & rotation policies
   - Automated daily backup of key shares (Vault snapshot)
   - Rotation: old ceremony shares → delete after 90 days (configurable)

**Deliverables:**
- [ ] Vault policy: customer isolation, TLS enforcement
- [ ] Key share sealing activity
- [ ] Key share retrieval service
- [ ] Audit logging for Vault access
- [ ] Integration test: seal → retrieve → verify

### 2.4 Multi-Chain Signing Adapters (Weeks 4-5)

**Goal:** Implement chain-agnostic signing with per-chain adapters.

**Tasks:**
1. Design multi-chain interface
   ```go
   type ChainSigner interface {
     SignMessage(ctx, messageHash, privKey) (*Signature, error)
     VerifySignature(ctx, message, sig, pubKey) (bool, error)
     RecoverAddress(ctx, message, sig) (string, error)
     BuildTransaction(ctx, txData) ([]byte, error)
     BroadcastTransaction(ctx, signedTx) (txHash, error)
   }
   ```

2. Implement chain adapters
   - **Ethereum**: RLP encoding, EIP-155/1559, ethers.js broadcast
   - **Bitcoin**: UTXO, SegWit, bitcoind RPC
   - **Solana**: Message format, Ed25519 signing, Web3.js broadcast (note: Phase 2 ECDSA proof-of-concept only)
   - **Cosmos**: Protobuf, Cosmos SDK, Tendermint RPC

3. MPC signer routing
   - Detect chain from request
   - Route to appropriate adapter
   - Return chain-specific signature

**Deliverables:**
- [ ] `services/mpc-signer/chains/` directory structure
- [ ] `ethereum.go`, `bitcoin.go`, `solana.go`, `cosmos.go`
- [ ] Unit tests per chain: sign → verify → broadcast
- [ ] Integration tests: threshold sign across chains

### 2.5 API Gateway Multi-Chain Endpoint (Week 5)

**Goal:** Expose multi-chain signing via REST API.

**Tasks:**
1. New endpoint: `POST /sign-multi-chain`
   ```json
   {
     "chainId": "bitcoin",
     "message": "0x...",  // RLP/script/instruction/SignDoc
     "metadata": {
       "network": "testnet",
       "utxos": [...],
       "recentBlockhash": "..."
     }
   }
   ```

2. Route to ceremony-orchestrator or mpc-signer
   - Check if threshold key available for customer + chain
   - If yes: use ceremony-orchestrator (threshold signing)
   - If no: use legacy mpc-signer (single key, phase 1)

3. Response format (chain-agnostic)
   ```json
   {
     "requestId": "...",
     "chainId": "bitcoin",
     "signature": "0x...",
     "signedTx": "0x...",
     "status": "signed",
     "broadcasted": true
   }
   ```

**Deliverables:**
- [ ] `POST /sign-multi-chain` endpoint in api-gateway
- [ ] Chain validation + request routing
- [ ] Integration test: sign on 4 chains in sequence
- [ ] Documentation: API, examples, error codes

### 2.6 SDKs: Multi-Chain Support (Week 6)

**Goal:** Update client SDKs for multi-chain signing.

**Tasks:**
1. SDK-JS (TypeScript)
   - New method: `client.signMultiChain(chainId, message, options)`
   - Chain-specific helpers: Ethereum, Bitcoin, Solana, Cosmos
   - Examples: sign and broadcast on each chain

2. SDK-Go
   - Same API as SDK-JS
   - Zero-dependency signing helpers

3. SDK-Python
   - Same API as SDK-JS
   - Examples: Jupyter notebooks

**Deliverables:**
- [ ] Updated SDK-JS with `signMultiChain` method
- [ ] Updated SDK-Go with `SignMultiChain` method
- [ ] Updated SDK-Python with `sign_multi_chain` method
- [ ] Examples: sign Bitcoin, Solana, Cosmos
- [ ] Integration tests: SDKs work with multi-chain API

### 2.7 Testing & Validation (Weeks 3-6, ongoing)

**Goal:** Ensure ceremony infrastructure is robust.

**Tasks:**
1. Unit tests
   - DKG state machine transitions
   - Per-chain signature verification
   - Vault key share operations

2. Integration tests
   - 2-of-3 DKG in-process
   - 3-of-5 DKG with real Vault
   - Ethereum + Bitcoin + Solana + Cosmos signing in single ceremony
   - Party failure + recovery

3. Load testing
   - 100 concurrent ceremonies
   - 1000 signing requests per second
   - Memory + CPU profiles

4. Security testing
   - MITM attack on party communication
   - Replay attack prevention
   - Key share reconstruction (impossible with < k+1)
   - Customer isolation

**Deliverables:**
- [ ] 90%+ code coverage (ceremony + chains)
- [ ] Nightly integration test suite
- [ ] Load test benchmark results
- [ ] Security test report

### 2.8 Multi-Party Transport (Week 7-8, optional for Phase 2.1)

**Goal:** Enable distributed DKG with remote parties.

**Tasks:**
1. Authenticated transport
   - mTLS between parties (certificate per party)
   - Message authentication (HMAC-SHA256)
   - Replay protection (nonce + timestamp)

2. Party discovery
   - Service discovery (DNS SRV or Consul)
   - Health checks
   - Circuit breaker pattern

3. Message routing
   - Party-to-party message delivery
   - Acknowledgments + retries
   - Timeout handling

4. End-to-end distributed DKG
   - 3 parties on separate hosts
   - Full DKG ceremony
   - Verification: threshold address matches

**Deliverables:**
- [ ] mTLS certificates per party (Vault PKI or self-signed)
- [ ] Party discovery service
- [ ] Message routing (orchestrator polls vs. pub/sub)
- [ ] Integration test: 3-of-5 real distributed DKG
- [ ] Chaos test: network partition + recovery

---

## Phase 3 Implementation Path

### 3.1 Security Audits (Months 1-2)

**Goal:** External validation of security.

**Tasks:**
1. Cryptography audit (6 weeks)
   - Engage Tier-1 firm (Trail of Bits, Zellic, etc.)
   - Audit DKG implementation, key share properties, proofs
   - Remediate findings
   - Re-audit critical/high findings

2. Penetration testing (4 weeks)
   - Full API attack surface
   - Authentication/authorization flaws
   - Ceremony transport security
   - Vault access controls
   - Transaction replay

3. Code audit (4 weeks)
   - Cryptography library usage
   - Error handling + information leakage
   - Concurrency safety
   - Input validation
   - Dependency security (CVEs)

**Deliverables:**
- [ ] Audit firm contracts signed
- [ ] Code snapshot prepared (git tag)
- [ ] Audit reports (findings + remediation)
- [ ] Post-audit artifacts archived

### 3.2 SOC 2 Type II (Months 1-3)

**Goal:** Achieve SOC 2 Type II certification.

**Tasks:**
1. Control documentation (weeks 1-2)
   - Select Trust Service Criteria: CC, A1, C1, I1
   - Document 20+ security controls
   - Evidence collection: logs, configs, tests

2. Control testing (weeks 3-4)
   - MFA for admin accounts
   - API key rotation (90-day expiration)
   - Database encryption (at rest + in transit)
   - Audit logging + retention
   - Incident response procedures

3. Observation period (weeks 5-12)
   - SOC 2 auditor reviews documentation
   - Monthly attestations of control effectiveness
   - Remediation of findings
   - Monthly evidence updates

4. Report issuance (week 13)
   - SOC 2 Type II report (1-year validity)
   - Management Letter with recommendations
   - Distribution to customers (under NDA)

**Deliverables:**
- [ ] SOC 2 audit firm selected
- [ ] Control documentation (50+ pages)
- [ ] SOC 2 Type II report issued
- [ ] Customer attestation letter

### 3.3 ISO 27001 (Months 2-4)

**Goal:** Achieve ISO 27001 certification.

**Tasks:**
1. ISMS documentation (weeks 1-3)
   - Information Security Policy
   - Access Control Policy
   - Cryptography Policy
   - Incident Management Policy
   - 7 more policies + procedures

2. Organizational structure (week 1)
   - ISO (Information Security Officer) appointed
   - Security steering committee formed
   - Incident response team on-call

3. Asset management (weeks 2-4)
   - Asset inventory (servers, databases, applications)
   - Classification (public, internal, confidential, restricted)
   - Ownership + custodianship

4. Control implementation (weeks 1-8)
   - Access control (MFA, RBAC, LDAP)
   - Cryptography (Vault, TLS, encryption)
   - Physical security (data center access)
   - Incident management (24/7 reporting)
   - Business continuity (RTO/RPO, failover)

5. Internal audit (weeks 9-10)
   - Audit scope defined
   - Test controls
   - Document findings + remediation

6. Certification (weeks 11-12)
   - External auditor reviews ISMS
   - Stage 1 (documentation) + Stage 2 (testing)
   - Certification issued (3-year validity)

**Deliverables:**
- [ ] ISMS Statement of Applicability (SOA)
- [ ] Policy documentation (10 documents)
- [ ] Internal audit report
- [ ] ISO 27001 certification (3 years)

### 3.4 Multi-Region HA (Months 1-3)

**Goal:** Deploy to multiple regions with automatic failover.

**Tasks:**
1. Architecture (week 1)
   - Primary: US-East (active)
   - Secondary: EU-West (hot standby)
   - Global load balancer (GeoDNS)

2. Database replication (weeks 1-2)
   - Postgres Streaming Replication (primary → replica)
   - WAL archival to S3
   - Tested recovery procedure

3. Vault replication (week 2)
   - Vault Performance Replication (async)
   - Auto-failover after 60s
   - Failback procedure

4. Application deployment (weeks 2-3)
   - Kubernetes multi-region (ArgoCD + Flux)
   - StatefulSets for stateful services
   - Persistent volumes with cross-region backup

5. Testing (weeks 3-4)
   - Failover test: primary down → secondary active
   - Data consistency check: replication lag
   - Failback test: restore primary
   - Load test: failover under high load
   - Quarterly disaster recovery drill

**Deliverables:**
- [ ] Multi-region infrastructure code (Terraform)
- [ ] Failover runbook (documentation)
- [ ] Failover test results (monthly)
- [ ] RTO ≤ 4 hours, RPO ≤ 1 hour

### 3.5 Regulatory Compliance (Months 2-4)

**Goal:** AML/KYC, OFAC, GDPR, data privacy.

**Tasks:**
1. AML/KYC (weeks 1-2)
   - Customer tier system (basic, KYC-verified, institutional)
   - Identity verification (email, documents)
   - OFAC screening (API integration)
   - SARs (Suspicious Activity Reports) workflow
   - Policy enforcement in API gateway

2. Data privacy (weeks 2-3)
   - GDPR: right to erasure, data subject requests, breach notification
   - CCPA: opt-out tracking, data minimization
   - Privacy notice + data handling disclosure

3. Anti-corruption (week 3)
   - Trade sanctions (OFAC SDN List)
   - Export controls
   - Vendor due diligence

**Deliverables:**
- [ ] AML/KYC policy + implementation
- [ ] GDPR/CCPA privacy policy
- [ ] Compliance attestations

### 3.6 Team Training & Documentation (Month 4)

**Goal:** Prepare team and customers for production.

**Tasks:**
1. Team training
   - Security Awareness (annual, mandatory)
   - Secure Coding (developers)
   - Incident Response (operations, quarterly)

2. Customer documentation
   - Security White Paper
   - SOC 2 / ISO 27001 attestations
   - SLA + incident response procedures
   - API security best practices

3. Operations documentation
   - Deployment runbook
   - Incident response playbook
   - Disaster recovery procedures
   - Monitoring + alerting setup

**Deliverables:**
- [ ] Security training completed (100% of team)
- [ ] Customer-facing documentation (5+ documents)
- [ ] Operations documentation (10+ pages)

---

## Code Organization

### Directory Structure

```
services/
├── mpc-signer/
│   ├── chains/
│   │   ├── types.go           # ChainSigner interface
│   │   ├── ethereum.go        # Ethereum adapter
│   │   ├── bitcoin.go         # Bitcoin adapter
│   │   ├── solana.go          # Solana adapter
│   │   └── cosmos.go          # Cosmos adapter
│   ├── tss/
│   │   └── tss.go             # Binance TSS-Lib integration
│   ├── main.go
│   └── ...
│
├── ceremony-orchestrator/      # NEW: Phase 2
│   ├── main.go
│   ├── types.go
│   ├── handlers.go
│   ├── db.go
│   └── workflows.go            # Temporal workflows
│
├── mpc-party/                  # NEW: Phase 2
│   ├── main.go
│   ├── dkg.go
│   ├── signing.go
│   └── transport.go
│
├── api-gateway/
│   ├── src/
│   │   ├── sign/               # Existing Phase 1
│   │   ├── multi-chain/        # NEW: Phase 2
│   │   ├── ceremonies/         # NEW: Phase 2
│   │   └── ...
│   └── ...
│
├── policy-service/
├── temporal-worker/
└── ...

sdks/
├── sdk-js/
├── sdk-go/
└── sdk-python/

docs/
├── architecture.md             # Updated: Phase 0-3
├── phase-2-mpc-multichain.md   # NEW
├── phase-3-bank-grade.md       # NEW
├── phase-2-checklist.md        # NEW
├── phase-3-checklist.md        # NEW
└── ...

infrastructure/
├── migrations/
│   └── phase-2-ceremonies.sql  # NEW
├── docker-compose.yml
├── k8s/
├── terraform/                  # NEW: multi-region
└── ...
```

---

## Testing & Validation

### Test Matrix

| Test Level | Phase 1 | Phase 2.1 | Phase 2.2 | Phase 3 |
|-----------|---------|-----------|-----------|---------|
| Unit | ✅ | ✅ | ✅ | ✅ |
| Integration | ✅ (single-key) | ✅ (2-of-3 DKG) | ✅ (multi-chain) | ✅ (distributed) |
| Load | ❌ | 10 tx/sec | 100 tx/sec | 1000 tx/sec |
| Security | ❌ | ⚠️ (pre-audit) | ⚠️ (pre-audit) | ✅ (post-audit) |
| Compliance | ❌ | ❌ | ❌ | ✅ (SOC 2 + ISO) |
| Failover | ❌ | ❌ | ❌ | ✅ (< 4 hours) |

### Go/No-Go Gates

**Phase 2.1 (Ceremony Infrastructure):**
- [ ] 90%+ code coverage
- [ ] 2-of-3 in-process DKG test passes
- [ ] Vault integration tested
- [ ] No high/critical vulnerabilities (SAST scan)

**Phase 2.2 (Multi-Chain):**
- [ ] All 4 chains can sign independently
- [ ] 100% chain adapter test coverage
- [ ] Multi-chain API integration test passes
- [ ] SDKs support all 4 chains

**Phase 2.3 (Multi-Party Transport):**
- [ ] 3-of-5 real distributed DKG works
- [ ] Network partition recovery tested
- [ ] mTLS certificates rotate automatically
- [ ] Pre-audit cryptography review passed

**Phase 3 (Bank-Grade):**
- [ ] Cryptography audit passed
- [ ] Penetration testing passed
- [ ] Code audit passed
- [ ] SOC 2 Type II report issued
- [ ] ISO 27001 certification obtained
- [ ] Multi-region failover works
- [ ] AML/KYC implemented
- [ ] Team trained, customers notified

---

## Deployment Strategy

### Phase 2 Deployment

1. **Development** (Week 0-5)
   - Docker Compose: ceremony-orchestrator + 3x mpc-party + Vault
   - Integration tests run on every commit

2. **Staging** (Week 5-6)
   - Kubernetes cluster (1 region)
   - Real Vault instance
   - Real Temporal cluster
   - Full integration test suite

3. **Canary** (Week 7)
   - 10% traffic to new ceremony-orchestrator
   - Monitor error rates, latency
   - Rollback plan ready

4. **Production** (Week 8)
   - 100% traffic to ceremony infrastructure
   - Monitor & alert on SLOs
   - Keep Phase 1 single-key signing available for 6 months

### Phase 3 Deployment

1. **Pre-Audit** (Month 1)
   - All security controls documented
   - Evidence collected

2. **During Audit** (Months 2-3)
   - Remediations applied
   - Re-audit critical findings

3. **Post-Audit** (Month 4)
   - Deploy multi-region infrastructure
   - Enable AML/KYC
   - Release to regulated customers

4. **Ongoing** (Month 5+)
   - Quarterly disaster recovery drills
   - Annual compliance renewal
   - Continuous security monitoring

---

## References

- [Binance TSS-Lib](https://github.com/bnb-chain/tss-lib)
- [Threshold ECDSA (academic)](https://eprint.iacr.org/2020/540)
- [Bitcoin BIP 141 (SegWit)](https://github.com/bitcoin/bips/blob/master/bip-0141.mediawiki)
- [Solana Transaction Format](https://docs.solana.com/developing/programming-model/transactions)
- [Cosmos Protobuf](https://docs.cosmos.network/main/basics/tx_structure)
- [SOC 2 Trust Service Criteria](https://www.aicpa.org/resources/download/trust-service-criteria)
- [ISO 27001:2022](https://www.iso.org/standard/27001)

