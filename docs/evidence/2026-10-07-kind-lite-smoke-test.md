# Evidence: threshold key generation and signing on a real cluster

| | |
|---|---|
| Date | 2026-10-07 |
| Run by | Project owner, on their own machine (output pasted verbatim below) |
| Machine | Apple Silicon MacBook Air, Docker Desktop (about 5.8 GB memory available to the cluster) |
| Cluster | kind, Kubernetes 1.29.14, one control plane and three workers |
| Code | Branch `claude/platform-explanation-h0st5y`, commit `f4f6535` or later (the one that added the lite profile) |
| Profile | `LITE=1 infrastructure/kind/up.sh`: gateway, three MPC parties, signer, policy service and worker, one replica each |
| Test | `infrastructure/kind/smoke-test.sh` |
| Result | **PASS** on the second run. The first run's last step failed only because Go was not installed on the Mac |

## What this shows

- A real 2-of-3 distributed key generation ran across three separate pods, and the key
  became active (52 s on the first run, 46 s on the second).
- All three parties sealed their own share in Vault.
- Raw-digest signing was refused until explicitly granted, then a threshold signature was
  produced through the public API by parties 1 and 2.
- An over-limit request was denied (403) and another tenant could not use the key (404).
- The signer recovered from the signature matches the key's address.

## What this does not show

- Billing, settlement, compliance, webhooks, the marketplace and redundant replicas were
  not deployed in this profile.
- No public blockchain, real Stripe account, live TRISA network, custodian, hardware HSM or
  isolated-host deployment was involved.
- It is a throwaway development cluster using development credentials. The keys hold no
  funds.
- The earlier failure on the full profile (gateway stopped during key creation, cause never
  captured) was not reproduced and remains unexplained.
- No audit, penetration test or SOC 2 report exists.

## Raw output

Run 1. It passes every step up to the final check, then stops because `go` was missing:

```
user@mac openfireblocks % curl -s http://127.0.0.1:3000/health/ready
infrastructure/kind/smoke-test.sh
{"status":"ready","checks":{"postgres":"ok"}}==> health
==> creating tenant
    customer 4909a85a-ef89-4c9d-97fb-d911799504d8
==> provisioning a 2-of-3 threshold key (real DKG)
    active after 52s, address 0x711b35765aED5689a6f0B52A51f64C36b8727cf4
==> confirming all three parties sealed a share in Vault
    party-1 sealed a share
    party-2 sealed a share
    party-3 sealed a share
==> raw-digest signing is refused until it is granted
    403 (default: the route policy cannot fully verify is closed)
==> granting it explicitly
==> threshold-signing through the API with 2 of the 3 parties
    signature d44ec6c53135b82cbb01b9d2f4d93dd5... from parties [1, 2]
==> policy gate denies an over-limit request
    403
==> another tenant cannot sign with this key
    404 (row-level security, all the way through the stack)
==> recovering the signer from the signature
infrastructure/kind/smoke-test.sh: line 132: go: command not found
```

Go installed (`brew install go`, version 1.27.1), then run 2:

```
user@mac openfireblocks % infrastructure/kind/smoke-test.sh
==> health
==> creating tenant
    customer a1e22291-33b2-4d9d-9038-c4d1909b82be
==> provisioning a 2-of-3 threshold key (real DKG)
    active after 46s, address 0xa842Eebdaba485bF171AF8Bb43eFdc8a09deA971
==> confirming all three parties sealed a share in Vault
    party-1 sealed a share
    party-2 sealed a share
    party-3 sealed a share
==> raw-digest signing is refused until it is granted
    403 (default: the route policy cannot fully verify is closed)
==> granting it explicitly
==> threshold-signing through the API with 2 of the 3 parties
    signature 3777df2cf08c202e1f6fb3d95547a937... from parties [1, 2]
==> policy gate denies an over-limit request
    403
==> another tenant cannot sign with this key
    404 (row-level security, all the way through the stack)
==> recovering the signer from the signature
go: downloading github.com/ethereum/go-ethereum v1.13.10
go: downloading golang.org/x/crypto v0.17.0
go: downloading github.com/holiman/uint256 v1.2.4
    0xa842Eebdaba485bF171AF8Bb43eFdc8a09deA971

PASS: threshold key 0xa842Eebdaba485bF171AF8Bb43eFdc8a09deA971 provisioned by real DKG across three pods,
      shares sealed by all three, policy and tenant isolation enforced,
      and a 2-of-3 signature from the public API recovers to it.
```

## How to reproduce

```
git clone https://github.com/KHAYAAI/openfireblocks && cd openfireblocks
git checkout claude/platform-explanation-h0st5y
LITE=1 infrastructure/kind/up.sh
# wait 5-10 minutes, then, with the gateway port-forwarded on 3000:
infrastructure/kind/smoke-test.sh      # needs Go installed for the last step
```

If it fails, run `infrastructure/kind/diagnose.sh` before changing anything.
