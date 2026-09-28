# OpenFireblocks

A self-hostable settlement platform for institutions that need custody
infrastructure and cannot accept a vendor holding their keys — built on
proven open-source components (Binance `tss-lib`, Temporal, OPA,
HashiCorp Vault, immudb).

The pitch, in one sentence: **the customer can prove they get their money
back without us.** Self-hosting is the product, not the deployment
option — see [`docs/LAUNCH-THESIS.md`](docs/LAUNCH-THESIS.md) for the
argument and, separately, an honest readiness assessment against what is
actually in this repository.

> **Status.** Real threshold ECDSA and EdDSA signing (`bnb-chain/tss-lib`),
> proven over genuine multi-process HTTP transport — not a single shared
> key. Proactive key refresh, sender-bound party messages, mutual TLS,
> optional ceremony co-signing, HSM-held keys for the single-key signer,
> and a recovery procedure that is executed
> against real processes and a real Vault in CI, not just described. Fully
> tested end to end with real HTTP, a real database, and (where the
> environment allows it) a real Kubernetes cluster. **Not yet cleared for
> material customer funds**: the signing parties are not yet deployed on
> isolated hosts, and no independent cryptographic audit has been
> commissioned. See [Readiness](#readiness) below.

## What it does

- **Threshold signing, for real.** k-of-n ECDSA (secp256k1 — Ethereum,
  Bitcoin, Cosmos SDK chains) and EdDSA (Ed25519 — Solana), via
  `bnb-chain/tss-lib`. The private key is never reconstructed anywhere,
  ever — DKG and signing both run as real ceremonies between independent
  processes over the network, verified by recovering the signer's address
  from a produced signature.
- **Proactive key refresh.** Shares are periodically re-randomised without
  changing the key, so a share compromised in January and another
  compromised in June cannot be combined — the property most threshold
  systems claim and few actually have. See
  `services/mpc-party/tss_resharing.go`.
- **A recovery procedure that is drilled, not documented.**
  `infrastructure/local/recovery-drill-local.sh` kills every signing
  party with `SIGKILL`, requires signing to then fail, restores each
  party from what was sealed in Vault, and requires the restored
  committee to produce a signature that verifies against the *original*
  address. It runs in CI on every push. The same procedure against a
  deployed cluster is `infrastructure/kind/recovery-drill.sh`. See
  [`docs/deployment/KEY-RECOVERY.md`](docs/deployment/KEY-RECOVERY.md).
- **Ceremony co-signing.** Every keygen, signing and refresh request can
  be required to carry a second signature from a key this platform's own
  hosts cannot reach — a cloud KMS, an approval service — so a
  compromised orchestrator cannot single-handedly move funds even though
  it can reach every party. Both the verifying half
  (`services/mpc-party/authorizer.go`) and the signing half
  (`services/temporal-worker/activities/ceremony_authorization.go`) are
  built, tested against each other with a golden vector, and on by
  default in the reference deployment.
- **Fail-closed policy on decoded transaction content**, not raw
  calldata: amount limits, allow-lists, approval workflows, geo rules and
  OFAC-style sanctions screening, evaluated by OPA/Rego before anything
  signs. Stablecoin (ERC-20) transfers are decoded and checked the same
  way. The sanctions list can be a live daily feed from OFAC's SDN
  publication (`services/policy-service/cmd/ofac-sync`) rather than a
  list baked into the binary, and the service refuses to evaluate once
  that list is too stale to mean anything.
- **A signing key in hardware, when the customer requires it.** The
  single-key signer can keep its key inside any PKCS#11 HSM (CloudHSM,
  Luna, YubiHSM): generated on the token, non-extractable, signing there
  for Ethereum, Bitcoin and Cosmos. It refuses to start on an extractable
  key, a mismatched key pair, or a software key configured alongside it.
  This is the single-key path only — threshold shares can't live in a
  PKCS#11 HSM, and the docs say why. See
  [`docs/engineering/PKCS11-HSM-SIGNING.md`](docs/engineering/PKCS11-HSM-SIGNING.md).
- **Durable settlement orchestration** (policy → sign → broadcast →
  monitor, with an approval gate) on Temporal, so a transfer cannot be
  lost halfway.
- **A dual, tamper-evident audit trail** and a compliance dashboard, so
  the answer to "what happened" doesn't require `curl` and a Postgres
  client.
- **Billing that collects**, not just invoices: a scheduled sweep raises
  invoices and a separate scheduled sweep charges them via Stripe,
  idempotent against retries, with every attempt — success, decline, or
  "this customer pays by wire" — recorded for reconciliation.
- **Multi-tenant API** with hashed API keys, Postgres row-level security,
  and JS/Go/Python SDKs.

## Repository layout

```
openfireblocks/
├── services/
│   ├── mpc-party/          # Go: real multi-process tss-lib DKG, signing, resharing, recovery
│   ├── mpc-signer/         # Go: ETH/BTC/Cosmos/Solana signing; keys in Vault or a PKCS#11 HSM
│   ├── api-gateway/        # NestJS: auth, multi-tenancy, policy checks, orchestration
│   ├── policy-service/     # Go + OPA/Rego: amount/whitelist/approval/geo/sanctions policy
│   ├── temporal-worker/    # Go: durable settlement workflow + ceremony orchestration
│   ├── settlement/         # Go: settlement domain logic
│   ├── billing/            # Go: metering, invoicing, Stripe collection
│   ├── compliance/         # Go: AML/KYC, incident response, regulatory reporting
│   ├── webhooks/           # Go: customer-facing event delivery
│   ├── marketplace/        # Go: integrations marketplace
│   ├── backup/             # Go: Postgres + Vault backup/restore
│   ├── vault-pki-init/     # Go: per-pod mTLS certs issued from Vault's PKI
│   └── vault-unseal/       # Go: Vault auto-unseal coordination
├── sdks/
│   ├── sdk-js/             # TypeScript client SDK (Apache-2.0)
│   ├── sdk-go/             # Go client SDK
│   └── sdk-python/         # Python client SDK
├── infrastructure/
│   ├── helm/               # The reference Helm chart
│   ├── kind/                # 13+ end-to-end drills against a real cluster (DKG, signing,
│   │                         recovery, billing, sanctions, node failure, party isolation…)
│   └── local/               # Drills that need no cluster — run on every push in CI
├── docs/                   # Architecture, licensing, security, commercial model, launch thesis
└── .github/workflows/      # CI — build/test every module, shellcheck the drills, gosec, e2e
```

## Quick start

```bash
cd infrastructure
docker compose up -d --build

curl -X POST http://localhost:3000/sign \
  -H "Content-Type: application/json" \
  -d '{
    "chainId": 11155111,
    "to": "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
    "data": "0x",
    "value": "0",
    "gasLimit": 21000,
    "gasPrice": "20000000000",
    "nonce": 0
  }'
```

See [`infrastructure/README-local-setup.md`](infrastructure/README-local-setup.md)
for broadcasting on Sepolia and inspecting the audit trail.

To see the real threshold cryptography run end to end — a live DKG
ceremony, then signing, with no cluster required:

```bash
cd services/mpc-party && go build -o /tmp/mpc-party . && cd ../..
./infrastructure/local/recovery-drill-local.sh
```

This provisions a key across three real processes, seals the shares in a
real Vault dev server, kills all three with `SIGKILL`, restores them from
nothing but what was sealed, and proves the restored committee signs for
the original address.

## Developing without Docker

```bash
# A signing party
cd services/mpc-party && go build ./... && go run .

# API gateway
cd services/api-gateway && npm install && npm run start:dev && npm test
```

## Readiness

The honest version, kept separate from the pitch, lives in
[`docs/LAUNCH-THESIS.md`](docs/LAUNCH-THESIS.md). Short form:

**Cleared today:** demos, testnet pilots, paid design partnerships.

**What blocks production with real money — one item, and it is not
cryptography:** the signing parties are not yet deployed on isolated
hosts (separate cloud accounts for custody). `infrastructure/kind/party-isolation-check.sh`
reports the isolation level a deployment has actually achieved and fails
the build below whatever a deployment requires; on a single-host cluster
it always reports `simulated`, which is deployment work, not research.

**What blocks material balances:** an independent cryptographic review of
how this platform drives `tss-lib` (not commissioned yet — see
[`docs/security/TSS-LIB-ADVISORY-REVIEW.md`](docs/security/TSS-LIB-ADVISORY-REVIEW.md)
for what a from-the-source review already established and what still
needs an outside firm), hardware isolation for the threshold shares
(they live in process memory, sealed in Vault at rest; PKCS#11 can't
hold them — that needs SGX/Nitro enclaves), and SOC 2 Type II. The
single-key signer *can* already keep its key in a PKCS#11 HSM; that's
tested against SoftHSM2 in CI but hasn't yet run on physical hardware.

Also see the
[technical and commercial comparison against Fireblocks, Metaco and
Taurus](docs/COMPETITIVE-ANALYSIS.md) and
[how the platform is priced and sold](docs/COMMERCIAL-MODEL.md).

## Documentation

- [Launch thesis and readiness](docs/LAUNCH-THESIS.md) — the argument and the evidence, kept separate
- [Platform overview](docs/PLATFORM-OVERVIEW.md) · [Architecture](docs/architecture.md)
- [Key recovery procedure](docs/deployment/KEY-RECOVERY.md) · [Party isolation](docs/deployment/PARTY-ISOLATION.md)
- [HSM signing over PKCS#11](docs/engineering/PKCS11-HSM-SIGNING.md) — what it does, what it can't do, how to run it
- [tss-lib dependency review](docs/security/TSS-LIB-ADVISORY-REVIEW.md) · [Threat model](docs/security/threat-model.md) · [Audit checklist](docs/security/audit-checklist.md)
- [API reference](docs/api.md) · [Policies (OPA)](docs/policies.md) · [Deployment](docs/deployment.md)
- [Competitive analysis](docs/COMPETITIVE-ANALYSIS.md) · [Commercial model](docs/COMMERCIAL-MODEL.md)
- [Troubleshooting](docs/troubleshooting.md) · [Operations runbook](docs/runbook.md)

## License

**Elastic License 2.0** ([LICENSE](LICENSE)) — source-available, not open
source.

You may read, modify, self-host and run this software for your own
business, including in your own cloud. You may not offer it to third
parties as a hosted or managed custody service.

The client SDK in [`sdks/sdk-js/`](sdks/sdk-js/) is **Apache-2.0**, so it
can be embedded in your applications without inheriting that restriction.

No copyleft dependency ships in any built binary — this is enforced in
CI (`go mod why` gated across every module), because go-ethereum (LGPL-3.0)
was deliberately removed; see
[`docs/engineering/GO-ETHEREUM-REMOVAL.md`](docs/engineering/GO-ETHEREUM-REMOVAL.md).
Third-party components are inventoried in [NOTICE](NOTICE); what the
licence means commercially is in [docs/LICENSING.md](docs/LICENSING.md).
