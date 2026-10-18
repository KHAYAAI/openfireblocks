# Vault or OpenBao

**Status: an evaluation to run, not a conclusion.** Nothing here has been
tested against OpenBao.

## Why look

HashiCorp relicensed Vault from MPL-2.0 to the Business Source License (from
1.14). A customer who must be able to run, inspect and support the whole stack
without a vendor licence may not be able to accept that. OpenBao is the
Linux Foundation fork of the last MPL-2.0 Vault, with a compatible API.

## What this platform uses Vault for

| Use | Where | Needs |
|---|---|---|
| Sealing signing-party key shares | `services/mpc-party/vault_seal.go` | KV v2 |
| Issuing party/service mTLS certificates | `services/vault-pki-init`, chart `autoIssue` | PKI secrets engine |
| Unseal automation | `services/vault-unseal` (dev only) | seal/unseal API |
| Terraform-provisioned Vault | `infrastructure/terraform/modules/vault*` | the server binary |

The Go client is `github.com/hashicorp/vault/api` (MPL-2.0, so still free to
use). It speaks the HTTP API, which is the compatibility surface that matters.

## How to evaluate (about a day)

1. Run OpenBao in place of Vault in `infrastructure/kind` (swap the image and
   keep the same address and token).
2. Run the Vault-backed tests against it:
   `vault_seal_test.go` and `vault_fake_test.go`'s real-Vault counterpart,
   and the kind PKI bootstrap (`vault-pki-bootstrap.yaml`).
3. Check, specifically, KV v2 metadata deletion (`RetireSealedShare` uses
   `DeleteMetadata`), PKI role and issue semantics, and the unseal flow.
4. Record differences. If all pass, support both and say which one is tested
   in CI; if not, list what breaks.

Until step 2 has passed, do not tell a customer OpenBao is supported.

## Alternative that needs no Vault

`SHARE_STORE_DIR` (see `docs/deployment/SHARE-STORE.md`) stores shares in an
encrypted file for a deployment with no Vault, and the chart can take an
externally supplied CA instead of Vault PKI. A fully Vault-free sovereign
deployment is therefore possible for shares today; certificate issuance is
the remaining Vault dependency.
