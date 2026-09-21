# Removing the LGPL dependency

Scoped work to eliminate `github.com/ethereum/go-ethereum`, the only
copyleft component in the tree.

## Why, and why it is not urgent

The LGPL permits selling proprietary software that links against it. What
it requires is that whoever receives a distributed binary can relink it
against their own modified build of the library. Go links statically, so
there is nothing for a recipient to swap — which makes this an awkward
fit rather than a formality.

Under the current model it is already discharged:

| Model | LGPL status |
|---|---|
| Source licence to a self-hosting customer | **Satisfied** — they can rebuild |
| Binary-only distribution (images, no source) | **Not satisfied** |
| SaaS you operate | **Not triggered** — no network clause |

So this is not a blocker for the first deals. It becomes one the moment a
customer wants images without source, which is the natural thing to want
as the customer count grows, and it is the kind of question a bank's
procurement or a due-diligence licence scan raises at the worst possible
moment.

Do it when a deal needs it, or when there is slack. Not before the
cryptographic audit.

## What is actually used

Nine files, and the dependency is far shallower than the count suggests.
Measured, not estimated:

| Import | Files | Replaceable with |
|---|---|---|
| `crypto` | 6 | `decred/dcrd/dcrec/secp256k1/v4` (ISC) + `golang.org/x/crypto/sha3` (BSD-3) — **both already in the module graph**, pulled in indirectly by tss-lib and btcec, so this adds no new supply-chain surface |
| `common` | 5 | ~40 lines: a 20-byte address type and hex parsing |
| `common/hexutil` | 4 | ~20 lines: `0x`-prefixed hex encode/decode |
| `core/types` | 5 | the hard one — see below |
| `ethclient` | 3 | plain JSON-RPC over `net/http` |

Four of the nine files (`mpc-party/curve.go`, `mpc-signer/tss/tss.go`,
`mpc-signer/vault.go`, `mpc-signer/chains/cosmos.go`) use **only**
`crypto`, and between them only ten symbols: `S256`, `FromECDSAPub`,
`FromECDSA`, `PubkeyToAddress`, `GenerateKey`, `HexToECDSA`, `Sign`,
`SigToPub`, `VerifySignature`, `CompressPubkey`.

Every one of those is thin. `PubkeyToAddress` is the last 20 bytes of a
Keccak-256 hash of the uncompressed public key. `S256` is the same curve
`dcrec/secp256k1` already provides, and which `btcec` — already a
dependency, ISC-licensed — wraps. There is no cryptography to reimplement
here; it is adapter code.

## The one genuinely hard part

`core/types` is transaction construction, RLP encoding, and the EIP-155 /
EIP-1559 signing hashes. `signer.go` and `chains/ethereum.go` build
`LegacyTx` and `DynamicFeeTx` and sign them.

**This is the part not to hand-roll.** An RLP or signing-hash mistake
produces a signature that recovers to the right address and is rejected by
every node on the network — or worse, one that is valid for a transaction
other than the one that was authorised. It is exactly the class of bug
`chain-test.sh` exists to catch, and exactly the class that is expensive
to catch any other way.

Two honest options:

**Option A — move construction to the gateway (recommended).**
The gateway already builds and assembles Ethereum transactions in
`eth-transaction.ts`, using `ethers` (MIT), and the token path added this
session does the same. The Go services would sign digests and stop
constructing transactions at all. This is also the better architecture
independently of licensing: there would be one place that decides what
bytes get signed, rather than two implementations that can drift.

**Option B — a small Go RLP/typed-transaction package**, ported from a
permissive source rather than written fresh, with differential tests
against go-ethereum in the test build only (test-only use is not
distributed, so it carries no obligation).

## Sequencing

Bottom-up, so each step is independently shippable and testable. Nothing
here needs a flag day.

| # | Step | Files | Effort |
|---|---|---|---|
| 1 | `internal/ethcrypto`: address derivation, keccak, secp256k1 sign/verify/recover over `dcrec` + `x/crypto/sha3`. Differential tests against go-ethereum across thousands of random keys. | new | 3 days |
| 2 | Cut the four `crypto`-only files over | `curve.go`, `tss/tss.go`, `vault.go`, `chains/cosmos.go` | 1 day |
| 3 | `internal/ethtypes`: `Address`, `Hash`, hexutil equivalents | new | 1 day |
| 4 | Replace `ethclient` with JSON-RPC over `net/http`. Exactly seven methods are called: `BalanceAt`, `ChainID`, `EstimateGas`, `PendingNonceAt`, `SendTransaction`, `SuggestGasPrice`, `TransactionReceipt` | `settlement.go`, `activities.go`, `balance_migration.go` | 2 days |
| 5 | Transaction construction, Option A or B | `signer.go`, `chains/ethereum.go` | **A: 4 days · B: 8 days** |
| 6 | Drop the dependency, verify with `go mod why`, update NOTICE | all | 1 day |

**Option A: ~12 working days. Option B: ~16.**

One engineer, sequential. Step 1 is the only one that needs care and it is
mostly writing differential tests; steps 2–4 are mechanical.

## How you know it worked

The proof already exists and does not need to be built:

- `chain-test.sh` mines a threshold-signed transaction on a real
  multi-node network. An encoding mistake fails here.
- `stablecoin-drill.sh` does the same for ERC-20 transfers, and checks the
  recipient's balance on chain rather than trusting the platform's account
  of what it did.
- `services/mpc-signer/chains/correctness_test.go` and the mpc-party suite
  cover signature correctness directly.

Add one gate at step 6:

```bash
go mod why github.com/ethereum/go-ethereum   # must report "not needed"
```

in CI, so the dependency cannot return through a transitive path without
somebody noticing.

## What this does not fix

The `ethereum/client-go` container image used by the kind cluster is
**GPL-3.0** — a different licence on the same project. It is irrelevant:
it is a test fixture that is run, not linked, and never redistributed. Do
not let a licence scanner's report conflate the two.
