# Staging checks (2026-10-18)

Follow-up to `2026-10-17-staging-redeploy-spl-wallets-retention.md`: the nightly backup, the duplicate-token fix
on the running gateway, and the token-account creation path that the first run did not exercise.

## Merge
- Pull request #44 (branch `claude/platform-explanation-h0st5y` into the default branch
  `claude/openfireblocks-main`) was merged as `6e62356` after all 30 of its CI checks succeeded, including the
  repaired gate jobs, the real key-generation and threshold-signing runs and the HSM run. The merge started the
  Deploy workflow (run 3) with its gates on; its outcome is recorded separately once it finishes.

## Nightly backup
- The bucket holds three backups: two taken by hand on 2026-10-17 (10:16 and 10:27, the second from the restore
  test) and one at **2026-10-18 02:17:02 UTC**, the scheduled time. Each is `manifest.enc`, `postgres.enc` and
  `vault.enc`; `postgres.enc` grew with the data (594,183, 594,280, 594,672 bytes). On the cluster, the
  schedule's pod ran to `Completed` about 14 hours earlier. **The scheduled backup path has now run by itself.**

## Duplicate-token fix on the running gateway
- Registering `USDC-DEV` again on chain 501 answered **409** with an explanation ("a token with symbol … is
  already registered on chain 501 …"); it was a bare 500 before. The first attempt after the rebuild still hit
  the old gateway pod through a port-forward that had not been restarted.

## A recipient with no token account
- A second user wallet (`test-002`) was created for a fresh address with no USDC account. A send of 1 USDC-DEV
  from the first key to it **completed**: `created_token_account: true`, rent **1,488,440 lamports** (read from
  the node: a different figure from the 2,039,280 first assumed), fee 5,000, `broadcast: true`.
  - Signature: `3A7AZ86U8VzGkrmf8dhRj5Jn6mMMmjLe1AUybgfD18DUG4ySGc9J9DrXqLBZAkLmt6akcMWE3R3qyZq22nVv5oQg`
  - Explorer: `https://explorer.solana.com/tx/3A7AZ86U8VzGkrmf8dhRj5Jn6mMMmjLe1AUybgfD18DUG4ySGc9J9DrXqLBZAkLmt6akcMWE3R3qyZq22nVv5oQg?cluster=devnet`

## Handling notes
- The Tenant A key was rotated after a wrong paste; the new key was verified (HTTP 200) and saved to a password
  manager. Keys are never recorded here.

## Explorer check
- The signature above was opened on the Solana explorer (devnet) by a person: **Success, Finalized**, slot 512,497,572,
  fee 0.000005 SOL, and the fee payer is the first key. The recipient's token account is created in the same
  transaction.

## Signed images, first tag
- `~/verify-images.sh <tag>` run in CloudShell for the 14 repositories (`api-gateway mpc-party mpc-signer
  policy-service temporal-worker vault-pki-init vault-unseal billing webhooks marketplace settlement policy
  compliance backup`): **14 of 14 PASS** against the deploy workflow's identity. The images for the merged commit
  `6e62356` do not exist yet: the Deploy run's secret-scan gate failed (the checkout was shallow, so the scan's
  commit range was missing), which skipped the builds. The fix was pull request #46, merged as `2571281`.
- **Rebuilt from `2571281`:** Security Scanning, Run Tests and all 14 Build jobs succeeded on the Deploy run, and
  `~/verify-images.sh 2571281b80042ebcf46ebbf45691efd23527bd7c` in CloudShell printed **14 PASS, problems: 0**
  (signature and SBOM attestation, against the deploy workflow's identity). This is the first Deploy run in which
  the gates ran and passed.
- The same run's **Deploy to Staging** job failed at its first step, "Configure AWS credentials for the registry",
  and the rest of that job was skipped. It targets a cluster setup that the single-server staging does not use;
  the cause has not been investigated. The push-event CI run of the merged commit also had one failure,
  `TestParticipantsCanBeRestoredFromSealedMaterialAndStillSign` ("timed out signing with restored shares"); the
  pull-request run of the same commit passed it. The push-event run for the merge commit itself then failed a different heavy test in the same package,
  `TestSealAndLoadKeyShareRoundTrip` ("timed out waiting for keygen to complete"), with logs showing the Paillier
  pre-parameter (safe-prime) generation timing out and retrying: both failures are key-generation timing under CI load,
  and both happened only in push-event runs while the pull-request runs of the same commits passed.
- **Two causes were found and fixed** (`3192cae`, `75bb1f6`; verified locally, and CI on `75bb1f6` passed all 30 checks including the mpc-party job):
  1. *Test timing.* Every party a test built started a background safe-prime search that never stopped, and each
     secp256k1 ceremony paid for an inline search under a fixed deadline. Tests now disable the background search and
     seed their parties from three pre-parameter sets generated once per run. The five heavy tests went from 7-13
     minutes in CI to about 90 seconds on a 4-core machine.
  2. *A real bug in the signing, key-generation and refresh drivers.* A party's last round queues a message and then
     finishes as soon as it has everyone else's, so both the message and the result can be ready together. The driver
     chose between them at random; choosing the result returned without sending the message. The party reported
     success and its partner waited for ever. It needs the sender to be a round ahead, so it appeared only under load,
     and most often in restore-then-sign (`TestAKeyCanStillBeRecoveredAfterARefresh` and
     `TestParticipantsCanBeRestoredFromSealedMaterialAndStillSign` hung for 120 s with one party reporting
     "produced its share"). It did not reproduce in 50 local runs on an idle machine. A regression test offers both
     at once: without the fix the peer received 95 of 200 final messages, with it 200 of 200. Every driver now relays
     what is still queued before finishing.
  - Effect: an intermittent signing or key-generation request could hang and time out. It could not produce a wrong
    signature and no funds were at risk; a retry worked. Staging runs the unfixed images until the next deploy.
- **Merged and redeployed.** The fix was merged as `c585a2e` (pull request #47, all 60 checks green). The Deploy
  workflow's Security Scanning, Run Tests and all 14 Build jobs passed on that commit. Staging was moved to that
  commit and the three signing parties were rebuilt and restarted one at a time with
  `infrastructure/kind/reload-image.sh mpc-party party-N`; `infrastructure/kind/smoke-test.sh` then **passed**: a new
  key from a real DKG across three pods, shares sealed by all three, raw-digest signing refused until granted, an
  over-limit request denied, another tenant refused (404), and a 2-of-3 signature from the public API recovered to the
  key's address. Restarting the parties drops every earlier key from their memory (the shares stay sealed in Vault), so
  the older test keys on staging need a restore before they sign again.
- **Signed images from the merged commit:** `~/verify-images.sh c585a2e60fce2a2283e6f194057bc665107f2503` in CloudShell
  printed **14 PASS, problems: 0** (signature and SBOM attestation against the deploy workflow's identity).
- Not shown: that the earlier test keys were restored.

## Old backup access key
- The first (wrong) IAM access key for the backup writer is gone: the user has one working key.

## Saved secrets
- Tenant A and Tenant B API keys, entered from the password manager: length 52 each, and `GET /wallets` returned
  **200** for both.
- Backup encryption key: the saved key's SHA-256 fingerprint was compared with the cluster's (64 characters each):
  **MATCH**. Neither the key nor the fingerprint was recorded here.

## Unfreeze by a signed-in admin
- The frozen wallet `test-001` (demo tenant `spl-demo`) was unfrozen through the organisation route, the path the console
  uses (there is no wallets screen yet). Steps: the tenant's API key was rotated with the admin key (neither saved tenant
  key owned this wallet); a console account was registered (`POST /v1/auth/register`); the tenant's API key appointed it
  first admin (`POST /organisation/first-admin`, allowed once); it signed in (`POST /v1/auth/login`, status `ok`, no
  second factor was enrolled); then `POST /organisations/:id/wallets/:walletId/unfreeze` returned **`active`** with the
  reason recorded as "… (console-admin@example.com)".
- An API key's attempt to unfreeze answered **404** (the route does not exist for API keys), and the wallet stayed `active`.
- Not shown by this run: signing from the wallet's key after the unfreeze; a console screen (none exists); an admin with
  a second factor enrolled.

## First real run of `restore-all-keys.sh`
- On staging, after the party restart, the script found **23 active keys** and restored **21** of them on all three
  parties, including the Solana keys, the Bitcoin keys and the user-wallet keys. A few had already been restored by the
  earlier smoke test and were skipped.
- **The two Cosmos keys were refused by every party**, with "the restored share controls 0x69... but was sealed against
  cosmos1...; refusing to load it". Cause: at key generation the party seals the address written for the key's chain
  (bech32 for Cosmos), but the restore re-derived the curve's default (Ethereum-style) address and compared the two, so a
  Cosmos key could never match its own sealed address. The refusal was the safe outcome and no share was exposed.
  **A proactive refresh of a Cosmos key had the same flaw** and would have been refused (confirmed by a test that reverts
  only that half). Both now compare the public key, which determines every address the key can have, and keep the
  address the key was created with. New test `TestACosmosKeyCanBeRefreshedAndRestored` (a real Cosmos DKG, a restore, a
  refresh, a restore after the refresh); without the fix it fails with the production error.
- **Fixed and confirmed live (2026-10-19).** The fix was merged as `b868b0b` (pull request #48, 60 of 60 checks); the
  Deploy workflow's gates and all 14 Build jobs passed on it. Staging was moved to that commit and the three signing
  parties rebuilt and restarted. (The first attempt rebuilt the old code: the checkout had silently refused because the
  server held an untracked copy of `restore-all-keys.sh` that the new commit also contains, and the build output gave it
  away: every step was cached and the image hash was unchanged. The checkout was repeated after removing the copy.)
  `restore-all-keys.sh` then printed **PASS: 23 of 23 keys fully restored on all three parties, 69 party restores, 0
  failed**, including both Cosmos keys.
- Not shown: a signature from a restored Cosmos key.

## Not done
- The two Cosmos keys on staging (see above).
