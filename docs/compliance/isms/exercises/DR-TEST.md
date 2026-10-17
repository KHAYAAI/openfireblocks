# Disaster recovery test: plan and record

ISO/IEC 27001:2022 A.5.29, A.5.30, A.8.13, A.8.14. SOC 2 A1.2, A1.3, CC9.1. Objective O-6.
Run at least yearly, and after major infrastructure changes. Allow one working day.

The repository already contains automated drills, which run in CI or on demand. The yearly DR
test runs them against **staging**, adds the parts automation cannot prove, and **times each
step** so the recovery objectives in `../policies/BUSINESS-CONTINUITY.md` become measured numbers.

## Plan

| # | Step | How | Pass criterion | Measure |
|---|---|---|---|---|
| 1 | Record the starting point | Note the latest backup time; create a marker transfer (testnet) and approval | Marker exists | — |
| 2 | Database restore | Restore the latest backup into a fresh database. Use the off-site copy once it runs against a real bucket (`docs/deployment/OFFSITE-BACKUPS.md`) | Marker approval present; row counts match; immutability triggers present | **RPO achieved** (marker time minus newest restored row) |
| 3 | Postgres failover | `infrastructure/kind/postgres-failover-drill.sh` (or the staging equivalent) | Gateway serves reads and writes after failover | Time to recover |
| 4 | Loss of one signing party | `infrastructure/kind/node-failure-drill.sh` | A transfer is signed with the remaining 2 of 3 | Time to first signature |
| 5 | Key recovery | `infrastructure/kind/recovery-drill.sh` per `docs/deployment/KEY-RECOVERY.md` | Recovered key signs; address unchanged | Time |
| 6 | Rebuild from code | Recreate the staging cluster from Terraform and Helm in a clean namespace or account; restore data | Smoke test passes (`infrastructure/kind/smoke-test.sh` / `services-smoke-test.sh`) | **RTO achieved** (start to smoke test pass) |
| 7 | Freeze still works after recovery | Freeze and unfreeze a test organisation | Signing refused while frozen | — |
| 8 | Clean up | Remove test copies; rotate any secrets used in the test | — | — |

## Record

```
Test ID:  DR-YYYY-NN        Date:          Lead:          Participants:
Environment:  staging (account / region / commit SHA):
Backup used (time, location, off-site? Y/N):

| Step | Result (pass/fail) | Time taken | Evidence (log file, screenshot, tx hash) | Notes |
|---|---|---|---|---|
| 1 |
| 2 |   RPO achieved:
| 3 |
| 4 |
| 5 |
| 6 |   RTO achieved:
| 7 |
| 8 |

Targets met?  RPO ≤ 24h: Y/N    RTO ≤ 8h: Y/N    Key recovery: Y/N
Problems found (each to 08-corrective-actions.md):
Changes to the BCP/DR policy or runbooks:
Signed (engineering lead):                  Date:
```

Store the record and its evidence in `docs/evidence/` as `YYYY-MM-DD-dr-test.md`. It contains
no personal information, so it can go in git.
