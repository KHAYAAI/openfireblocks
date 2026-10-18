# Off-site backup to a real S3 bucket (2026-10-17)

First run of the off-site backup path against a real bucket. Until now it had been tested only against a
local S3-compatible stand-in.

## Setup
- Bucket in `eu-central-1` (a different region from staging, `eu-west-1`), same AWS account. Object Lock on,
  default retention **Governance mode, 7 days** (a first-test choice; production should use Compliance mode),
  versioning on, all four public-access blocks on.
- A dedicated IAM user for the backup service with **write-only** access: put, get, abort/list multipart
  uploads and list the bucket. No delete permission of any kind.
- A 32-byte encryption key generated on the server, stored in the cluster as a secret and in a password
  manager; the file on disk was shredded. The bucket never holds plaintext.
- `backup.enabled=true`, `backup.offsite.*` and the new `backup.vault.addr` set through the reapply script, so
  a rebuild keeps them.

## Faults found and fixed on the way
1. The access key's secret was wrong (the first pair); a fresh pair worked.
2. **The backup image had no CA bundle** (the Postgres base image does not ship one), so every upload failed
   with "x509: certificate signed by unknown authority". Fixed in `services/backup/Dockerfile` and rebuilt
   with the new `infrastructure/kind/reload-image.sh`.
3. The chart read the Vault address only from the signer's Vault setting, which is empty on staging, so the
   Vault step would have failed. `backup.vault.addr` added to the chart.

## Result
- A full backup (`POST /backup/full` on the backup service) completed, and the bucket holds, under one backup
  id: `postgres.enc` (594,183 bytes), `vault.enc` (750,194 bytes; the signing parties' key shares) and
  `manifest.enc` (1,027 bytes). All three are encrypted on the server before upload.
- `head-object` on `postgres.enc`: lock mode **GOVERNANCE**, retained until 2026-10-24.
- With the backup writer's credentials, `DeleteObject` on that object was **refused** (`AccessDenied`: no
  identity-based policy allows `s3:DeleteObject`).

## Not yet done
- A delete as an administrator without the bypass option was not run, so the lock itself is shown by
  `head-object` but its refusal is not.
- **No restore has been done.** A backup that has not been restored is not yet shown to work. That is the
  disaster-recovery test (`docs/compliance/isms/exercises/DR-TEST.md`): decrypt and verify the off-site copy,
  restore Postgres and Vault into a fresh cluster, and time it.
- The nightly CronJob has not been seen to run (the manual call was used instead); the first scheduled run
  is 02:17 UTC.
- The Vault step exports the key-share KV tree using a Vault token that, on staging, is the development root
  token. Production needs a read-only Vault policy for the backup.
- Compliance-mode locking, and a bucket in a separate AWS account, for production.
