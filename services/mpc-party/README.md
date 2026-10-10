# MPC Party Service

The MPC Party Service is a participant in OpenFireblocks' Distributed Key Generation (DKG) ceremonies. Each party runs this service to participate in threshold signing, enabling k-of-n cryptographic operations across Bitcoin, Ethereum, Solana, and Cosmos.

## Architecture

### DKG Protocol (7 Rounds)

The service implements Binance TSS-Lib's threshold ECDSA protocol (Feldman VSS variant):

- **Round 1**: Generate random polynomial coefficients and commitments
- **Round 2**: Create secret shares using Shamir secret sharing
- **Rounds 3-7**: Validation, zero-knowledge proofs, and key share finalization

### HTTP Endpoints

#### DKG Ceremony Endpoints

- `POST /round` - Receive signal to start a new DKG round
- `GET /round/{roundNum}/data` - Provide this party's round data
- `POST /round/{roundNum}/broadcast` - Receive round data from all other parties

#### Signing Endpoints

- `POST /sign` - Compute partial signature using stored key share

#### Health & Info

- `GET /health` - Health check
- `GET /info` - Party information
- `GET /metrics` - Prometheus metrics

## Configuration

Environment variables:

```bash
PORT=7000              # HTTP server port
PARTY_ID=1             # This party's ID (1-N)
VAULT_ADDR=...         # Vault server address
VAULT_TOKEN=...        # Vault authentication token
```

## Running a Party

### Docker

```bash
docker run -p 7000:7000 \
  -e PARTY_ID=1 \
  -e VAULT_ADDR=http://vault:8200 \
  -e VAULT_TOKEN=s.xxxxxxxxxx \
  openfireblocks/mpc-party:latest
```

### Local (Standalone)

```bash
go run . &
# Party listens on http://localhost:7000
```

## DKG Ceremony Flow

### 1. Orchestrator Initiates Ceremony

```bash
curl -X POST http://ceremony-orchestrator:8081/ceremonies \
  -H "Content-Type: application/json" \
  -d '{
    "chainId": "ethereum",
    "n": 7,
    "k": 3,
    "partyEndpoints": [
      "http://party-1:7000",
      "http://party-2:7000",
      ...
    ]
  }'
```

### 2. Parties Participate in 7-Round DKG

For each round (1-7):

**Step A:** Orchestrator signals start
```
POST http://party-1:7000/round
{ "ceremonyId": "...", "roundNum": 1, "action": "start" }
```

**Step B:** Orchestrator collects round data from all parties
```
GET http://party-1:7000/round/1/data
←  { "partyId": 1, "commitments": "...", "dlProof": "..." }
```

**Step C:** Orchestrator broadcasts all parties' data
```
POST http://party-1:7000/round/1/broadcast
{ 
  "ceremonyId": "...", 
  "roundNum": 1, 
  "partyDataMap": { 
    "1": {...}, "2": {...}, ... 
  } 
}
```

After round 7, each party has:
- A secret key share
- The shared public key
- Proof of the DKG execution

### 3. Threshold Signing

Once DKG is complete, parties can sign messages:

```bash
curl -X POST http://party-1:7000/sign \
  -H "Content-Type: application/json" \
  -d '{
    "ceremonyId": "ceremony-123",
    "message": "deadbeef...",
    "partyIds": [1, 2, 3]
  }'
```

Each party computes its partial signature using Lagrange interpolation. The orchestrator collects k+1 partial signatures and combines them into a valid (k+1)-of-n threshold signature.

## Key Share Storage

After DKG completes, each party stores its key share in HashiCorp Vault:

```
secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId}
├── keyShare       # Encrypted key material
├── commitment     # Commitment to key share
├── publicKey      # Shared public key
└── chainAddress   # Derived address on target blockchain
```

## Security Considerations

### Cryptography

- **secp256k1 ECDSA** for Bitcoin, Ethereum, Cosmos
- **Ed25519** for Solana
- **Feldman Verifiable Secret Sharing (VSS)** for DKG
- **Lagrange interpolation** for threshold signing

### Key Share Protection

- Key shares are encrypted before storage in Vault
- No single party can recover the shared private key
- Threshold is k+1 (default 4-of-7)
- Zero-knowledge proofs prevent malicious parties

### Network Security

- TLS required in production
- Authenticating requests via mTLS or API keys
- Network isolation between parties
- Audit logging of all operations

## Testing

### Unit Tests

```bash
go test -v ./...
```

### Integration Tests

```bash
# Start all services
docker-compose up -d

# Run integration tests
npm test tests/integration/dkg-ceremony.test.ts
```

### Local DKG Ceremony

```bash
# Start 7 party services on different ports
for i in {1..7}; do
  PARTY_ID=$i PORT=$((7000+i)) go run . &
done

# Run ceremony
./tests/local-dkg-ceremony.sh
```

## Monitoring

### Prometheus Metrics

- `dkg_round_duration_seconds` - Time spent in each DKG round
- `dkg_round_total` - Total rounds executed (by status)
- `threshold_signature_total` - Total signatures computed

### Logs

```bash
# Stream logs from all parties
docker-compose logs -f mpc-party-1 mpc-party-2 ...
```

## Deployment

### Kubernetes

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mpc-party-1
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mpc-party
      partyId: "1"
  template:
    metadata:
      labels:
        app: mpc-party
        partyId: "1"
    spec:
      containers:
      - name: mpc-party
        image: openfireblocks/mpc-party:latest
        ports:
        - containerPort: 7000
        env:
        - name: PARTY_ID
          value: "1"
        - name: VAULT_ADDR
          value: "http://vault:8200"
        - name: VAULT_TOKEN
          valueFrom:
            secretKeyRef:
              name: vault-tokens
              key: party-1
        livenessProbe:
          httpGet:
            path: /health
            port: 7000
          initialDelaySeconds: 10
          periodSeconds: 30
```

### Docker Compose

See `infrastructure/docker-compose.yml` for a complete example.

## References

- [Binance TSS-lib](https://github.com/bnb-chain/tss-lib)
- [Feldman Verifiable Secret Sharing](https://en.wikipedia.org/wiki/Verifiable_secret_sharing)
- [Threshold Signatures Explained](https://blog.threshold.network/what-are-threshold-signatures/)
- Phase 2 Architecture: `docs/phase-2-mpc-multichain.md`
