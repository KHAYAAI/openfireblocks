# Phase 2 Checklist: Real MPC Threshold Signing & Multi-Chain

**Goal:** Production-grade threshold signing (k-of-n ECDSA) + multi-chain support (Bitcoin, Solana, Cosmos)

**Go/No-Go Gate:** All items marked as ✅. Fails if cryptography audit fails or ceremony transport is untested.

## Part 1: Ceremony Orchestration

### 1.1 DKG Ceremony State Machine
- [ ] PostgreSQL schema: `ceremonies`, `ceremony_parties`, `ceremony_rounds`
- [ ] Ceremony state transitions: `pending → round1 → ... → round7 → completed`
- [ ] DKG round validation logic
- [ ] Error handling: party dropout, timeout, message loss
- [ ] Integration test: 3-of-5 in-process DKG

### 1.2 Vault Integration for Key Shares
- [ ] Vault secret paths: `secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId}`
- [ ] Encryption at rest in Vault
- [ ] Key share backup/restore procedures
- [ ] Audit logging of Vault access
- [ ] Rotation policy for sealed shares

### 1.3 Ceremony Orchestrator Service (Go)
- [ ] HTTP API: `POST /ceremonies` (initiate DKG)
- [ ] HTTP API: `GET /ceremonies/{id}` (status)
- [ ] HTTP API: `POST /ceremonies/{id}/sign` (request threshold signing)
- [ ] Temporal workflow: `DKGCeremony` (state machine driver)
- [ ] Temporal workflow: `ThresholdSign` (signing coordination)
- [ ] Metrics: ceremony duration, success rate, party latency

### 1.4 Party Service (Go)
- [ ] HTTP server (port 7000+) with mTLS
- [ ] DKG round handler: receive messages, execute TSS-Lib, send shares
- [ ] Signing handler: load share from Vault, participate in threshold signing
- [ ] Health check endpoint
- [ ] Metrics: message latency, signing time

### 1.5 Testing & Validation
- [ ] Unit test: DKG state machine transitions
- [ ] Unit test: Vault key share sealing/unsealing
- [ ] Integration test: 2-of-3 DKG in-process
- [ ] Integration test: 3-of-5 DKG with real Vault
- [ ] Load test: 100 concurrent ceremonies
- [ ] Chaos test: party failure recovery

## Part 2: Multi-Chain Signing

### 2.1 Bitcoin Signing
- [ ] Transaction builder: inputs (UTXO), outputs, change
- [ ] SegWit (BIP 141) signature generation
- [ ] Taproot (BIP 341) support (optional Phase 2B)
- [ ] RPC integration: broadcast to Bitcoin testnet
- [ ] Test vectors: sign → broadcast → verify on-chain
- [ ] Integration test: 2-of-3 threshold sign Bitcoin tx

### 2.2 Solana Signing
- [ ] Message format: Solana Transaction (instructions, signers, blockhash)
- [ ] Ed25519 signing (differs from secp256k1; Phase 2 uses ed25519 for Solana)
- [ ] RPC integration: Solana devnet/mainnet
- [ ] Instruction verification (no execution, only signature)
- [ ] Test vectors: sign → verify signature
- [ ] Integration test: Multi-sig Solana instruction

### 2.3 Cosmos Signing
- [ ] Protobuf encoding: `cosmos.tx.v1beta1.SignDoc`
- [ ] Signature chain: TX → SignDoc → hash → sign
- [ ] RPC integration: Cosmos testnet (Osmosis, Cosmos Hub)
- [ ] IBC-compatible transactions (optional)
- [ ] Test vectors: sign → broadcast → confirm
- [ ] Integration test: Cosmos MsgSend with threshold signature

### 2.4 Chain-Agnostic Signing Interface
- [ ] Interface: `Signer` (chain-agnostic)
- [ ] Interface: `ChainAdapter` (chain-specific serialization)
- [ ] Router: API gateway dispatches to correct adapter
- [ ] Metrics: signing latency per chain, error rates

### 2.5 API Gateway Multi-Chain Endpoint
- [ ] `POST /sign-multi-chain` (accepts any chain)
- [ ] Request validation: message format per chain
- [ ] Response format: chain-agnostic signature + metadata
- [ ] Broadcasting support per chain
- [ ] Test vectors: sign same message across chains (where applicable)

### 2.6 SDKs: Multi-Chain Support
- [ ] SDK-JS: chain detection, message building, signature verification
- [ ] SDK-Go: same as SDK-JS
- [ ] SDK-Python: same as SDK-JS
- [ ] Examples: sign Bitcoin, Solana, Cosmos with same SDK

## Part 3: Multi-Party Transport

### 3.1 Authenticated Transport
- [ ] mTLS: certificate per party (auto-rotated)
- [ ] Certificate authority: Vault PKI engine or self-signed for testnet
- [ ] Message authentication: HMAC-SHA256 on payloads
- [ ] Replay protection: nonce + timestamp per message

### 3.2 Party Discovery & Load Balancing
- [ ] Service discovery: DNS SRV records or Consul
- [ ] Health checks: party availability polling
- [ ] Circuit breaker: skip unavailable parties during signing
- [ ] Fallback: retry with alternate party if available

### 3.3 Distributed DKG with Remote Parties
- [ ] Party-to-party communication (TSS-Lib message routing)
- [ ] Orchestrator polling vs. pub/sub (choose one)
- [ ] Message ordering guarantees
- [ ] Timeout handling per round

### 3.4 Testing & Validation
- [ ] Integration test: 3-of-5 real distributed DKG (3 hosts)
- [ ] Chaos test: party network partition, recovery
- [ ] Load test: 10 simultaneous ceremonies
- [ ] Security test: message replay detection

## Part 4: Per-Customer Key Ceremonies

### 4.1 Customer Self-Service
- [ ] API: `POST /ceremonies?customerId=X&n=5&k=3` (request DKG)
- [ ] Webhook: customer notified of ceremony readiness
- [ ] Documentation: ceremony initiation guide (customers provide party endpoints)
- [ ] UI (optional): ceremony dashboard showing status/parties

### 4.2 Custody Model
- [ ] Single-customer isolation: one ceremony per customer per chain
- [ ] Party control: customer owns party infrastructure
- [ ] Key rotation: ceremony initiation → new key shares → old shares revoked
- [ ] Audit trail: ceremony events (initiation, party joins, completion, signing)

### 4.3 Backward Compatibility
- [ ] Phase 1 single-key signing still works (legacy path)
- [ ] Migration guide: single key → first ceremony
- [ ] Deprecation: Phase 1 single-key signing deprecated after 6 months

### 4.4 Documentation & Support
- [ ] Ceremony initiation guide (customers)
- [ ] Party implementation guide (if customers run own parties)
- [ ] API documentation updated with ceremony endpoints
- [ ] Runbook: troubleshoot ceremony failures

## Part 5: Security & Compliance

### 5.1 Cryptography Audit Readiness
- [ ] Code review: DKG implementation (peer review, code walk-through)
- [ ] Test coverage: >90% for ceremony orchestration + signing
- [ ] Vulnerability scan: Go dependencies (go mod graph)
- [ ] Documentation: DKG algorithm, key share properties, threat model

### 5.2 Ceremony Transport Security
- [ ] TLS 1.3 between all parties
- [ ] mTLS certificate validation
- [ ] Message signing (HMAC-SHA256)
- [ ] Audit logging: all ceremony messages
- [ ] Rate limiting: ceremonies per customer, signing per ceremony

### 5.3 Audit Trail
- [ ] PostgreSQL: ceremony lifecycle events
- [ ] immudb: key share operations (sealed, unsealed, rotated)
- [ ] Prometheus: ceremony duration, success rates
- [ ] Alerting: ceremony failure, timeout, anomalies

## Part 6: Deployment & Operations

### 6.1 Docker & Kubernetes
- [ ] Dockerfile: ceremony-orchestrator (Go alpine)
- [ ] Dockerfile: mpc-party (Go alpine)
- [ ] Helm chart: ceremony-orchestrator with replicas=1
- [ ] Helm chart: mpc-party with replicas=3+ (per ceremony)
- [ ] StatefulSet: party services (stable DNS for DKG)

### 6.2 Configuration & Secrets
- [ ] Environment variables: Vault address, Temporal endpoints
- [ ] Secrets: mTLS certificates (Vault)
- [ ] Config: ceremony timeouts, party timeout per round
- [ ] Monitoring: Prometheus scrape endpoints

### 6.3 Testing Infrastructure
- [ ] Docker Compose: ceremony-orchestrator + 3x mpc-party + Vault
- [ ] Integration test suite: run on every PR
- [ ] Load test script: spawn 100 ceremonies, measure throughput
- [ ] Chaos test: kill parties, assert recovery

## Part 7: Testing Validation

### 7.1 Smoke Tests (Run every PR)
- [ ] DKG state machine unit tests pass
- [ ] 2-of-3 in-process DKG completes < 30s
- [ ] Ethereum signing still works (backward compatibility)
- [ ] Bitcoin signing produces valid signatures

### 7.2 Integration Tests (Run nightly)
- [ ] 3-of-5 real distributed DKG (3 hosts)
- [ ] Bitcoin ceremony → sign → broadcast to testnet
- [ ] Solana ceremony → sign → verify off-chain
- [ ] Cosmos ceremony → sign → broadcast to testnet
- [ ] Party failure recovery (kill 1 of 3 parties, restart)

### 7.3 Security Tests (Run before release)
- [ ] Ceremony transport interception: MITM attack detection
- [ ] Replay attack prevention: same message twice fails
- [ ] Key share reconstruction: impossible with < k+1 shares
- [ ] Customer isolation: customer A cannot access customer B's shares

### 7.4 Performance Benchmarks
- [ ] DKG time per round (target: <10s per round, 7 rounds)
- [ ] Signing latency (target: <5s for 3-of-5)
- [ ] Throughput: ceremonies/sec (target: 10+)
- [ ] Memory: per-party size (target: <500MB)

## Part 8: Documentation & Examples

### 8.1 Developer Documentation
- [ ] Architecture: ceremony orchestration, multi-chain design
- [ ] API: ceremony endpoints, signing endpoints, error codes
- [ ] SDK examples: sign Bitcoin, Solana, Cosmos with ceremony
- [ ] Runbook: ceremony troubleshooting, party restart procedures
- [ ] Threat model: ceremony security assumptions

### 8.2 Customer Documentation
- [ ] Ceremony initiation guide
- [ ] Party endpoint requirements (mTLS, ports)
- [ ] API key usage with ceremonies
- [ ] Best practices: key rotation frequency, party count

### 8.3 Examples & Templates
- [ ] Go party implementation (template for customers)
- [ ] Kubernetes StatefulSet: 3 parties per ceremony
- [ ] Terraform: multi-region ceremony orchestrator deployment
- [ ] Docker Compose: local development

## Sign-Off

**Product:** ___________ Date: _________

**Engineering:** ___________ Date: _________

**Security:** ___________ Date: _________

**Go/No-Go:** ⬜ READY | ⬜ HOLD | ⬜ REJECT (choose one)

