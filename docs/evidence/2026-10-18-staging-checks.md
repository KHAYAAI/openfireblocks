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

## Not done
- The explorer check of the signature above had not been done when this was written.
- Unfreezing a wallet in the console (no console login for the demo tenant).
- The old IAM access key for the backup writer was not yet deleted when this was written.
