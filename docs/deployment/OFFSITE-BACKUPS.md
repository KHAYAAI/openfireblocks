# Off-site backups

By default a backup is a pair of dumps (Postgres, Vault) on a volume in the
same cluster, so losing the cluster loses the backups. With
`backup.offsite.enabled=true` every completed full backup is also copied to an
S3-compatible bucket outside the cluster, **encrypted before it leaves**.

## Setting it up

1. A bucket in another account or region, with versioning and object lock
   (write-once) if you can. Grant the backup pods write and read, not delete.
2. A 32-byte key, as 64 hex characters, in a Secret under `backup-offsite-key`
   (`openssl rand -hex 32`). **Keep a copy somewhere that survives the
   cluster**: without the key the backups cannot be read.
3. Credentials: a role bound to the service account (`serviceAccount.annotations`)
   or a Secret with `aws-access-key-id` and `aws-secret-access-key`.
4. Set `backup.offsite.{bucket,region,keySecret}` (and `endpoint` for MinIO,
   Ceph or R2), then upgrade the release.

A run now fails, loudly, if the off-site copy fails, even when the local copy
is good, so the nightly job going red means "this is not yet safe".

## What is stored

`<prefix>/<backup-id>/{postgres,vault,manifest,checksums}.enc`: AES-256-GCM in
1 MiB chunks, each bound to its position and to whether it is the last, so a
reordered, altered or truncated object fails to decrypt. The SHA-256 of each
plaintext is recorded in the backup's metadata and checked on the way back.

`checksums.enc` is a small sidecar holding those three SHA-256 values and the
backup id, encrypted in the same format under the same key and written last.
It exists so the bucket and the key are enough to restore after total cluster
loss. GCM already stops anyone without the key altering an object; the SHA-256
adds that it pins *which* content this backup is, since a bucket writer could
otherwise swap in another valid object made under the same key (an older
backup's dump). The sidecar names its backup and is rejected for any other.
Backups taken before the sidecar existed have none; restore those with the
saved checksums.

## Restoring when the cluster is gone

1. New cluster, same chart, same key secret, same bucket settings.
2. `POST /offsite/fetch {"backup_id": "..."}` with the backup token. With only
   the bucket and the key it reads and authenticates `checksums.enc`, then
   decrypts and verifies each file into a staging directory. To pin the
   checksums independently of the bucket, also pass
   `"tags": {"offsite_postgres_sha256": "...", "offsite_vault_sha256": "...", "offsite_manifest_sha256": "..."}`
   (from the backup's metadata or your saved run output); supplied tags are used
   instead of the sidecar and must cover all three files. It refuses anything it
   cannot check, including a missing or altered sidecar with no tags.
3. Restore from those files with the usual procedure.

## Honest status

Tested: the encryption format (every size around the chunk boundary, tampering,
truncation, reordering, wrong key), the manager flow, and the S3 client against
a local S3-compatible server. **Run once against a real bucket** on 2026-10-17
(`docs/evidence/2026-10-17-offsite-backup-s3.md`): the upload, the lock and the refused delete. **Not tested:** a real restore
into a fresh cluster. Do that drill before relying on it; it is the only
test that counts for a backup.
