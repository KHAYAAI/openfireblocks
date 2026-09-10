# Phase 2: Real MPC Threshold Signing & Multi-Chain Architecture

## Overview

Phase 2 transforms OpenFireblocks from a single-key MVP into a production-grade custodial platform with:
- **Cryptographically-secure threshold signing** (k-of-n ECDSA) replacing the single shared key
- **Multi-chain support** (Bitcoin, Solana, Cosmos) with chain-agnostic signing
- **Distributed key ceremonies** where key shares are never reconstructed
- **Per-customer key isolation** with Vault-sealed shares

**Status:** Foundation cryptography proven in `services/mpc-signer/tss/` via Binance TSS-Lib. This phase wires it into production with ceremony orchestration, multi-party transport, and multi-chain signing.

---

## Part 1: MPC Threshold Signing (k-of-n ECDSA)

### Current State (Phase 1)
- Single shared ECDSA (secp256k1) key in memory
- Key stored in Vault KV v2 (plaintext, loaded at startup)
- No threshold: one copy of the private key is sufficient to sign
- **Security risk:** Key compromise = full loss of custody

### Phase 2 Goal: Threshold Signing
Replace with k-of-n ECDSA where:
- **n parties** hold key shares
- **k+1 parties** required to create a valid signature (k = threshold)
- **Private key never reconstructed** at any point
- **Binance TSS-Lib** cryptographic core (DKG + signing + proof of correctness)
- **Vault-sealed shares** per party/customer in separate secret paths

### Architecture

#### 1. Distributed Key Generation (DKG) Ceremony

```
Customer initiates DKG ceremony for k-of-n threshold

  ┌─────────────────────────────────────────────────────┐
  │  api-gateway                                        │
  │  POST /ceremonies (customerId, n, k, partyIds)     │
  │  Creates ceremony record in PostgreSQL              │
  └──────────────┬──────────────────────────────────────┘
                 │
                 ▼
  ┌─────────────────────────────────────────────────────┐
  │ ceremony-orchestrator (new service)                 │
  │ - Polls ceremony state from DB                      │
  │ - Coordinates n parties via authenticated transport │
  │ - Drives DKG protocol (phases 1-7 per TSS-Lib)     │
  │ - Saves resulting key shares per party in Vault    │
  └──────────────┬──────────────────────────────────────┘
                 │
      ┌──────────┼──────────┐
      ▼          ▼          ▼
   Party1     Party2     Party3
  (signing     (signing   (signing
   process)    process)   process)
      │          │          │
      └──────────┼──────────┘
                 ▼
      ┌──────────────────────┐
      │ Vault                │
      │ - customer/party1    │
      │ - customer/party2    │
      │ - customer/party3    │
      └──────────────────────┘
```

**Ceremony Phases:**
1. **Round 0** — Party registration and validation
2. **Rounds 1-7** — DKG protocol execution (TSS-Lib phases)
3. **Save** — Each party seals its key share in Vault under `customer/{partyId}`
4. **Publish** — Store ceremony metadata + threshold address in PostgreSQL

**Data Structure (PostgreSQL):**
```sql
CREATE TABLE ceremonies (
  id UUID PRIMARY KEY,
  customer_id UUID,
  n INTEGER,  -- total parties
  k INTEGER,  -- threshold (k+1 signatures needed)
  status ENUM('in_progress', 'completed', 'failed'),
  threshold_address VARCHAR,  -- shared public address
  created_at TIMESTAMP,
  completed_at TIMESTAMP,
  metadata JSONB  -- TSS params, verification keys
);

CREATE TABLE ceremony_parties (
  id UUID PRIMARY KEY,
  ceremony_id UUID REFERENCES ceremonies(id),
  party_id INTEGER,
  public_key VARCHAR,  -- party's public key
  joined_at TIMESTAMP
);
```

#### 2. Threshold Signing Request Flow

```
Client signs request
  │
  ├─ POST /sign-threshold (customerId, txn, partyIds)
  │
  ▼
api-gateway validates customer owns the ceremony
  │
  ├─ policy-service checks amount/whitelist/geo
  │
  ▼
temporal-worker orchestrates signing
  │
  ├─ Activity: Get ceremony metadata from DB
  ├─ Activity: Get k+1 parties' key shares from Vault
  ├─ Activity: Distribute signing work to k+1 parties
  │            (party1, party2, party3 in parallel)
  │
  ├─ Party i (parallel):
  │   ├─ Receive task from temporal-worker
  │   ├─ Load its key share from Vault
  │   ├─ Participate in signing protocol (TSS-Lib)
  │   ├─ Send sig_share to other parties
  │   └─ Receive sig_shares from others, compute final Sig
  │
  ├─ Collect signatures from all k+1 parties
  ├─ Verify combined signature
  │
  ▼
response: signed transaction ready to broadcast
```

#### 3. Data Model: Key Shares in Vault

Each party's share for a ceremony is stored as:

```
vault write secret/data/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId} \
  share_json='{"share": {...}, "commitments": [...], "dlProof": {...}}'
```

**Recovery:** Only if k+1 parties present their shares can signing occur. No single party has enough information.

---

## Part 2: Multi-Chain Support

### Supported Chains (Phase 2)

| Chain | Type | Signing | TX Format | Notes |
|-------|------|---------|-----------|-------|
| Ethereum | EVM | ECDSA (secp256k1) | RLP-encoded | Proven in Phase 0/1 |
| Bitcoin | UTXO | ECDSA (secp256k1) | Raw binary | SegWit, Taproot ready |
| Solana | Account | EDDSA (ed25519) | Binary with program IDs | System program, custom programs |
| Cosmos | IBC | ECDSA (secp256k1) | Amino/Protobuf | IBC-enabled chains (Cosmos Hub, Osmosis, etc.) |

### Multi-Chain Signing Architecture

#### 1. Chain-Agnostic Core

```
services/mpc-signer/
├── signer.go              # Interface: Signer (no chain knowledge)
├── chains/
│   ├── ethereum/
│   │   ├── signer.go      # EVM: RLP encoding, EIP-155/1559 signing
│   │   └── types.go
│   ├── bitcoin/
│   │   ├── signer.go      # UTXO: scriptSig, witness data
│   │   └── types.go
│   ├── solana/
│   │   ├── signer.go      # Ed25519: Solana message format
│   │   └── types.go
│   └── cosmos/
│       ├── signer.go      # Protobuf encoding, Cosmos SDK
│       └── types.go
└── tss/
    └── tss.go             # Chain-agnostic threshold ECDSA
```

#### 2. Generic Signing Interface

```go
// Signer is chain-agnostic.
type Signer interface {
  // Sign accepts any message and returns a chain-specific signature.
  Sign(ctx context.Context, message []byte, partyIds []int) (sig *Signature, err error)
  
  // VerifySignature is optional (for testing).
  VerifySignature(message []byte, sig *Signature, pubKey []byte) (bool, error)
  
  // RecoverAddress returns the signing address (chain-specific format).
  RecoverAddress(message []byte, sig *Signature) (string, error)
}

// ChainSpecificRequest wraps any chain's transaction.
type ChainSignRequest struct {
  ChainId    string        // "ethereum", "bitcoin", "solana", "cosmos-hub"
  Message    []byte        // RLP (Ethereum), script (Bitcoin), instruction (Solana)
  Metadata   map[string]interface{}  // Chain-specific hints (nonce, fee, version)
}
```

#### 3. Transaction Building Per Chain

**Ethereum (RLP):**
```go
func (c *EthereumChain) BuildTx(req *ChainSignRequest) ([]byte, error) {
  // Decode req.Message as JSON SignRequest
  // Build go-ethereum *types.Transaction
  // Return RLP-encoded bytes for signing
}
```

**Bitcoin (UTXO):**
```go
func (c *BitcoinChain) BuildTx(req *ChainSignRequest) ([]byte, error) {
  // req.Message contains UTXO references, outputs, change address
  // Build unsigned tx
  // Return scriptSig placeholder for signing
}
```

**Solana (Instruction):**
```go
func (c *SolanaChain) BuildTx(req *ChainSignRequest) ([]byte, error) {
  // req.Message contains instructions, recent blockhash, feePayer
  // Serialize message
  // Return message bytes to sign
}
```

**Cosmos (Protobuf):**
```go
func (c *CosmosChain) BuildTx(req *ChainSignRequest) ([]byte, error) {
  // req.Message contains msgs, signers, fees, timeout
  // Encode as Protobuf SignDoc
  // Return bytes to sign
}
```

#### 4. API Gateway: Multi-Chain Endpoint

```
POST /sign-multi-chain
Content-Type: application/json
Authorization: Bearer <api-key>

{
  "chainId": "bitcoin",
  "message": "...",  // RLP / script / instruction / SignDoc
  "metadata": {
    "network": "mainnet",
    "utxos": [...],      // Bitcoin only
    "recentBlockhash": "...",  // Solana only
    "account_number": 123  // Cosmos only
  }
}

Response:
{
  "requestId": "...",
  "chainId": "bitcoin",
  "signature": "0x...",
  "txHash": "0x...",
  "status": "signed",
  "broadcasted": false
}
```

#### 5. Broadcasting (Chain-Agnostic)

```go
type Broadcaster interface {
  Broadcast(ctx context.Context, tx *SignedTx) (txHash string, err error)
}

// Implementations: EthereumBroadcaster, BitcoinBroadcaster, SolanaBroadcaster, CosmosBroadcaster
```

---

## Part 3: Implementation Roadmap

### Phase 2.1: Ceremony Orchestration (Weeks 1-3)
- [ ] `ceremony-orchestrator` service (Go)
- [ ] DKG ceremony state machine + PostgreSQL schema
- [ ] Vault integration for key share storage
- [ ] Ceremony REST API (create, status, sign with threshold)
- [ ] In-process testing (3-of-5 DKG)

### Phase 2.2: Multi-Chain Signing (Weeks 4-6)
- [ ] Bitcoin signing (SegWit-compatible UTXO signing)
- [ ] Solana signing (Ed25519 for non-EVM)
- [ ] Cosmos signing (Protobuf, IBC-compatible)
- [ ] Chain detection + routing in api-gateway
- [ ] Integration tests per chain

### Phase 2.3: Multi-Party Transport (Weeks 7-9)
- [ ] Authenticated transport between parties (mTLS or HTTP + HMAC)
- [ ] Party service (Go): DKG participant + signing worker
- [ ] Load balancer for party discovery
- [ ] End-to-end 3-of-5 real distributed DKG
- [ ] Signing with remote parties

### Phase 2.4: Per-Customer Key Ceremonies (Week 10)
- [ ] Customer self-service ceremony initiation
- [ ] Custody handoff (customer owns party endpoints)
- [ ] Audit trail: ceremony metadata, signature requests
- [ ] Security review + testnet deployment

---

## Part 4: Key Security Considerations

### Threat Model Updates

| Threat | Phase 1 Risk | Phase 2 Mitigation |
|--------|-------------|-------------------|
| Single key compromise | Total loss | k-of-n: attacker needs k+1 shares |
| Key share leakage (one party) | No effect | Still secure if k < n |
| Ceremony party MITM | Protocol verifification | mTLS + signature verification |
| Replay attacks | Timestamp-based | Nonce per customer per chain |
| Double-signing | Monitoring only | Temporal workflow idempotency |

### Ceremony Constraints

1. **Threshold minimum:** k ≥ 1 (requires at least 2 signatures)
2. **Party minimum:** n ≥ k + 1 (need spare party for signing)
3. **Party isolation:** Each party in separate host/process
4. **Secret sharing:** No single party knows full key
5. **Zero-knowledge proofs:** TSS-Lib proves correctness without revealing shares

---

## Part 5: Testing Strategy

### Unit Tests
- Threshold ECDSA keygen and signing (existing: `tss_test.go`)
- Per-chain transaction builders
- Signature verification per chain

### Integration Tests
- 2-of-3 DKG ceremony (in-process, Vault mock)
- 3-of-5 DKG ceremony (real Vault)
- Ethereum + Bitcoin + Solana signing in single ceremony
- Temporal workflow: DKG → signing → broadcast

### Security Tests
- Ceremony replay prevention
- Party isolation (no share reconstruction)
- MITM detection in ceremony
- Key rotation protocol

---

## Part 6: Deployment Considerations

### Vault Configuration
```hcl
path "secret/data/customers/{{.IdentityMetadata}}/ceremonies/*" {
  capabilities = ["create", "read", "update"]
}
```

### Party Service Scaling
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mpc-party-worker
spec:
  replicas: 3  # n ≥ 3 for 2-of-3
  template:
    spec:
      containers:
      - name: party
        image: openfireblocks/mpc-party:v2
        env:
        - name: PARTY_ID
          valueFrom:
            podAnnotation: ordinal
        - name: VAULT_ADDR
          value: https://vault:8200
```

### Network Requirements
- Parties must have bidirectional TCP connectivity
- mTLS certificate per party (auto-rotated)
- Ceremony orchestrator accessible from api-gateway

---

## References

- [Binance TSS-Lib](https://github.com/bnb-chain/tss-lib)
- [Threshold ECDSA (academic paper)](https://eprint.iacr.org/2020/540)
- [Bitcoin SegWit (BIP 141/143)](https://github.com/bitcoin/bips/blob/master/bip-0141.mediawiki)
- [Solana Signer Instructions](https://docs.solana.com/developing/programming-model/transactions)
- [Cosmos Protobuf](https://docs.cosmos.network/main/basics/tx_structure)

