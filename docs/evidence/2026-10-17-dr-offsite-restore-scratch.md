# Restore from the off-site backup into scratch copies (2026-10-17)

Part A of the disaster-recovery test (`docs/compliance/isms/exercises/DR-TEST.md`): fetch the off-site copy
as after a disaster (from the bucket and the saved checksums only), restore it into **scratch** copies of
the database and of Vault on the staging cluster, and compare with live. Nothing live was restored over.
This is **not** the full rebuild test: no new server was built, so it does not measure the RTO.

## Method
- A full backup was taken after creating a marker tenant (`dr-marker-1792232784`), then a second marker
  tenant (`dr-after-…`) was created after the backup.
- `POST /offsite/fetch` with the backup id and the checksum tags from the saved backup result. The service
  decrypts each file and refuses it if its SHA-256 does not match.
- Postgres: `pg_restore --no-owner --no-privileges` into a new database `dr_scratch`.
- Vault: every secret in the backup's KV export was written into a scratch KV mount (`dr-scratch`) and
  compared by SHA-256 with the live secret at the same path. Only OK or DIFFERENT was printed, never a value.

## Results
- Vault key shares: **60 of 60 paths identical**, 0 different, after the round trip through the encrypted
  off-site copy.
- The restored database contains the marker created **before** the backup and does **not** contain the one
  created **after** it: the restore reproduces the state at backup time, and what came later is lost.
- Timing: fetch, decrypt, restore and checks took **103 seconds**; the whole exercise **207 seconds**.
- Cleaned up: `dr_scratch` dropped, the scratch Vault mount removed, the decrypted files shredded and the
  pod's staging directory deleted.

## Not captured in this record
- The fetch response, any `pg_restore` warnings and the table of row counts, trigger counts and policy
  counts (live against restored). They were run but are not recorded here; re-run the comparison before
  relying on "the restored schema matches".
- The first checks that a tampered or wrong-key file is refused (the service refuses a checksum mismatch by
  design; not exercised in this run).

## Not done (Part B)
- A rebuild on a fresh server: provisioning, `up.sh`, restoring into the real database and Vault, standing
  the signing parties back up from the restored shares (`docs/deployment/KEY-RECOVERY.md`, Route A), signing
  with a restored key and checking the address is unchanged, with the elapsed time as the **RTO**.
- The RPO is bounded by the backup schedule (nightly, so up to a day) until backups run more often.
- The shares were decrypted onto a staging host for this check. That is acceptable for test keys only.
