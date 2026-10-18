# Disaster-recovery rebuild on a fresh server (2026-10-18)

Part B of the disaster-recovery test (`docs/compliance/isms/exercises/DR-TEST.md` step 6; runbook
`docs/deployment/DR-REBUILD-RUNBOOK.md`). Part A (restore into scratch copies) is in
`2026-10-17-dr-offsite-restore-scratch.md`.

## Method
- On the live staging server a test tenant and an Ethereum 2-of-3 key were created
  (address `0xbf1d2e1E1F1e67444f4123b4Cce940AfB09F0a26`), digest signing was enabled for the tenant, and a full backup
  (`backup-1792343730978660581`) was taken, encrypted to the S3 bucket. The row counts at backup time were saved, and a
  second marker customer was created **after** the backup.
- A new, empty instance (`m6i.2xlarge`, 100 GiB, Ubuntu 24.04, eu-west-1) was launched. The old server was treated as lost.
  The new server was given only: the backup encryption key and the backup login's access keys (typed from a password
  manager), the saved checksums of the backup, and the source code at the old server's commit (`8814c3c`).
- Steps: install tools, `up.sh` (builds every image, applies 39 migrations), create the three backup secrets, apply the
  backup settings, `POST /offsite/fetch`, `pg_restore` into the real database as the `app` role, write the key shares into
  Vault, restart, restore the parties with `recovery-drill.sh --restore-only`, and sign.

## Results
- **Fetch from the bucket:** `fetched`, three files, each checked against its saved SHA-256. The new server read the
  backup with nothing from the old server except the saved checksums.
- **Database:** `pg_restore` printed no errors or warnings. The eight row counts (customers 20, key_pairs 24,
  user_wallets 2, travel_rule_records 13, retention_purges 4, tokens 15, triggers 25, rls_policies 47) were **identical**
  to the old server's at backup time. The marker created after the backup was absent (the only `dr-after-` customer is
  the one from the 2026-10-17 test, dated before the backup): the restore reproduces the state at backup time.
- **Vault:** 66 of 66 key-share paths written and read back identical, 0 different.
- **Tenant API key:** the test tenant's key from the old cluster worked on the new one; the key showed `active` at the
  same address.
- **Key restored and signing:** the three parties started empty (each returned 404 for the ceremony and signing failed),
  were restored from the sealed shares, each reported the original address, and a signature produced by the restored
  committee **recovered to the original address**. The script's `PASS` was printed at 18:00:50 UTC.
- **Recovery time (RTO), measured:** clock started 17:19:08 UTC (instance launch), restored signing at 18:00:50 UTC =
  **41 minutes 42 seconds**. This includes the time lost to the faults below. `up.sh` finished at about 17:42 UTC, so
  building the images was roughly half of it; a production rebuild would pull the signed images from the registry.
- **Data-loss window (RPO):** the state at backup time. The backup was taken a few minutes before the "disaster", so this
  run does not measure the nightly schedule's worst case, which is up to 24 hours.

## Faults found on the way (all fixed or recorded)
1. **No BuildKit on the new server.** `docker.io` alone cannot build these Dockerfiles; `docker-buildx` is needed. The
   runbook's tool list is fixed.
2. **The recovery drill could not authenticate to the parties on a deployed cluster.** It borrowed the temporal worker's
   client certificate by running `cat` in the worker, but that image is distroless, so it fell back to plain HTTP and the
   parties answered 400. Fixed: the drill now issues a one-hour client certificate from the cluster's CA.
3. **The drill restored the parties with peer URLs their certificates are not valid for** (`https://party-N:7000`; the
   certificates cover `party-N.internal` and `party-N.<namespace>.svc.cluster.local`). The parties restored and reported
   the right address, but the first signing round failed certificate verification (HTTP 503). Fixed in the drill.
4. **Operator slips, not product faults:** the tool install was first run on the laptop instead of the server, and the
   encryption-key secret was skipped once, which left the backup pod waiting. Both were recovered.

## Not shown by this run
- **Only one key was restored.** A rebuilt platform needs every active ceremony restored on every party; there is no single
  command for all of them yet (24 key rows existed at backup time).
- **The checksums needed to fetch a backup exist only inside the cluster** (and in the saved result of a manual run).
  After a total loss, the nightly backups cannot be fetched through the service without checksums saved elsewhere.
  Open item.
- The authorising key for ceremonies on this profile is the development key shipped in the repository, so the new cluster
  had it automatically. In production it is held outside the cluster and is one of the things that must still be reachable.
- The Vault part restored is the key-share KV tree, not a Raft snapshot; policies, auth methods and the PKI were rebuilt by
  `up.sh`, not restored.
- The key shares were decrypted onto the new server. Acceptable for test keys only. The instance was then terminated.
- The restore drill's final message still says every party "was destroyed" in restore-only mode (it adds a note that none
  were); wording only.
- The staging Vault token and the development root token were used throughout.
