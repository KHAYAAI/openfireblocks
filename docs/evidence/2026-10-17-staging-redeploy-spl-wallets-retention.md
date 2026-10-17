# Staging redeploy, Solana SPL token, user wallet and retention (2026-10-17)

Deployment: the single-VM kind cluster (full profile) at `staging.52.213.16.179.sslip.io`, redeployed from
`claude/platform-explanation-h0st5y` at `61079b5` with the existing generated secrets (first four
characters of the admin key and JWT secret checked against the cluster; no secret is recorded here).

## Redeploy
- Fast-forwarded the checkout 41 commits (`f764798` to `61079b5`) and re-ran `infrastructure/kind/up.sh`.
- Migrations: **4 applied, 35 already present** (036 access reviews, 037 Travel Rule retention,
  038 Solana tokens, 039 user wallets).
- Helm settings reapplied after `up.sh` (`~/reapply.sh`): `apiGateway.trustProxyHops=1`,
  `mpcSigner.solana.rpcUrl` (devnet), `external.redisUrl=redis://redis:6379`,
  `external.riskAllowDisabledVelocityLimiting=false`. Confirmed with `helm get values`.
- `infrastructure/kind/smoke-test.sh`: **PASS**. Real key generation across three pods (active after 61 s),
  all three shares sealed in Vault, raw-digest signing refused until granted, 2-of-3 signature recovers to
  the key's address, over-limit request denied (403), cross-tenant use refused (404).
- Public check from outside the instance: `/health/ready` returned `200`.

## Solana SPL token on devnet
- The token registry refused the symbol `USDC` for the devnet mint (already used by the mainnet row that
  migration 038 seeds), and the route answered 500. **Fixed afterwards:** it now answers 409 with an
  explanation (covered by a test). The devnet token was registered as `USDC-DEV`.
- Registered devnet USDC (mint `4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU`) as chain 501, then verified
  it: **status `verified`, decimals 6**, read from the chain through the signer.
- Created a 2-of-3 Solana key (`6yD3Fk1YuSYH48GfmHYCJoLSfvHs7d4gS2tAQtk3DeAg`), funded with devnet SOL and
  20 USDC from faucets. The balances route listed SOL and USDC-DEV.
- A send of 1 USDC-DEV with no Travel Rule details was **refused before any signing**: the token has no
  price source, so it is treated as over the R5000 threshold. With test details (fictional names, an
  unhosted beneficiary), the send **completed**: signed by parties 1 and 2, fee 5,000 lamports,
  `broadcast: true`, Travel Rule record `not_applicable_unhosted`.
  - Signature: `eR83g4arPLD7zFzXdYcpzW6P23JmcFniR8TCXarpx1BpKGAhMSR8MydSTH4bcDUwGNMRLJ2dpNdLhyvTJyVByaV`
  - Explorer: `https://explorer.solana.com/tx/eR83g4arPLD7zFzXdYcpzW6P23JmcFniR8TCXarpx1BpKGAhMSR8MydSTH4bcDUwGNMRLJ2dpNdLhyvTJyVByaV?cluster=devnet`
- **Not proven by this run:** the recipient already held a USDC account, so the transfer reported
  `created_token_account: false` and rent 0. Creating the recipient's token account (and the sender paying
  its rent) is not proven on a public network yet. It is covered by the signer's tests and by the message
  check against `@solana/spl-token`.

## User wallet
- Created wallet `test-001` with one Solana key (`H91Njt6GqVdEoBgLMynuB7HTQWbm46zRoAHdL9FES1NJ`), active.
- Sent 0.5 USDC-DEV from the wallet's own key back to the first key through `POST /wallets/:id/transfers`:
  **completed**.
  - Signature: `5i991rNVhXXqJEfc3pSCuba5pfFNFYGT7FCGT4HUKmRsQpxFEata8R4MkhUQRubYwSuQYhaYZYSDT2wWwAoKEeta`
  - Explorer: `https://explorer.solana.com/tx/5i991rNVhXXqJEfc3pSCuba5pfFNFYGT7FCGT4HUKmRsQpxFEata8R4MkhUQRubYwSuQYhaYZYSDT2wWwAoKEeta?cluster=devnet`
- Froze the wallet through the API (`frozen`, reason recorded). Every route that could move the wallet's
  funds was then refused:

  | Route | Result |
  |---|---|
  | `POST /wallets/:id/transfers` | 409 |
  | `POST /keys/:keyId/solana-transactions` (the wallet's own key) | 403, "belongs to user wallet … which is frozen" |
  | `POST /keys/:keyId/transfers` | 403 |

  No signature was produced by any of them.
- **Not done:** unfreezing. It needs a signed-in organisation admin in the console, and this demo
  tenant has no console user.

## Travel Rule retention (dry run only)
- `POST /admin/retention/travel-rule` with `dryRun: true`: cutoff exactly five years back
  (2021-10-17), **0 rows** in both `travel_rule_records` and `trisa_inbound`, nothing deleted.
- Each run appears in `GET /admin/retention/purges` (who ran it, why, the cutoff, `dryRun: true`).
- A retention period of 30 days was refused with 400 ("retainDays must not be less than 1826").
- No real purge was run: staging holds no records old enough.

## Checks not done
- A look at the two transactions on the Solana explorer is still to be done by a person; the signatures
  above are the broadcast responses, not independently confirmed here.
- The first-send behaviour when the recipient has no token account (above).
- Unfreeze through the console (above).
- The Shannon pentest against this deployment (round 4).
