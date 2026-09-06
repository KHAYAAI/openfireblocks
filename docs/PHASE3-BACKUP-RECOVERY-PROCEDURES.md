# Phase 3: Backup and Disaster Recovery Procedures

**Status:** Implementation Guide for RTO ≤ 4 hours, RPO ≤ 1 hour

> ## ⚠️ Reality check (added after auditing this document against the actual codebase)
> This document reads as an operational runbook for a system that is running
> in production. It is not — treat everything below as a **target design**,
> not a description of what exists today. Specifically:
>
> - **`services/backup` has no `func main()` anywhere in it.** It's two Go
>   files (`backup_manager.go`, `disaster_recovery.go`) with no entrypoint —
>   nothing invokes them, there's no binary, no cron job, no scheduled
>   Temporal workflow calling into this package. None of the "Daily at 00:00
>   UTC" / "Every 4 hours" schedules below run anywhere.
> - **No S3 backup bucket, WAL archiving, or cross-region replication is
>   provisioned in Terraform** for this purpose (`infrastructure/terraform`
>   has S3 buckets for Vault's Raft storage backend, not for the database/
>   Temporal/Vault backup scheme this document describes).
> - **The `2024-06-30 14:30:00` example timestamp** in the PITR procedure
>   below is template boilerplate, not evidence anyone has run this
>   procedure — a sign this document was generated as a plan, not written
>   from operational experience.
> - **The RTO/RPO figures (≤4h / ≤1h) and backup sizes ("~500GB-1TB",
>   "~300GB") are illustrative placeholders**, not measurements — there is
>   no production data to size a backup against yet.
>
> None of this means the design is wrong — PITR via WAL archiving, Raft
> snapshot export for Vault, and a replica-promotion runbook are all
> reasonable, standard choices. It means: before quoting an RTO/RPO number
> to a customer, auditor, or regulator, someone needs to actually build
> `services/backup`'s entrypoint, provision the S3/WAL infrastructure, and
> **run a real restore drill** to measure the real numbers — see
> `docs/security/audit-checklist.md`'s Disaster Recovery section for
> current status, and `docs/security/key-rotation.md` for the credential/
> key-rotation side of durability, which this document doesn't cover at all.
>
> **Update**: `services/backup` now has a real entrypoint
> (`cmd/backup-server`), real `pg_dump`/`pg_restore`-backed Postgres
> backup/restore, and a real Vault KV export/import — see that command's
> doc comment and `docs/security/audit-checklist.md` for what's real vs.
> still a gap (true WAL-based incrementals, S3 storage, and cross-region
> failover promotion are not built). A real drill has been run against
> real Postgres and Vault (`services/backup/integration_test.go`,
> `TestFullBackupAndRestoreDrill`): full backup completed in **~110-125ms**
> and full restore (into an isolated database, verified byte-identical
> against the original) in well under a second end-to-end, against a
> dev-scale database (1 customer row, ~128KB combined Postgres+Vault
> payload). Those are real measurements, not estimates — but at dev scale,
> not production scale; they say nothing about how long a restore takes
> against hundreds of GB, which is what the RTO figures in this document
> are actually meant to describe. Re-run that test against a
> production-sized dataset before trusting an RTO number derived from it.

## Executive Summary

This document defines backup, recovery, and disaster recovery procedures for OpenFireblocks' multi-region deployment. All systems (PostgreSQL, Vault, Temporal) are designed to achieve:
- **RTO (Recovery Time Objective)**: ≤ 4 hours
- **RPO (Recovery Point Objective)**: ≤ 1 hour

## Backup Strategy

### Full Backup Schedule
- **Frequency**: Daily at 00:00 UTC
- **Retention**: 30 days
- **Location**: S3 (cross-region backup)
- **Type**: Full snapshot of all systems
- **Estimated Duration**: 45-60 minutes
- **Estimated Size**: ~500GB-1TB per backup

### Incremental Backup Schedule
- **Frequency**: Every 4 hours (6 times daily)
- **Retention**: 7 days
- **Location**: S3 (same region as primary)
- **Type**: Changes since last backup
- **Estimated Duration**: 5-10 minutes per backup
- **Data Loss Risk**: 4-hour window (meets RPO requirement)

### Incremental Backup Times
- 04:00 UTC
- 08:00 UTC
- 12:00 UTC
- 16:00 UTC
- 20:00 UTC
- 00:00 UTC (combined with full backup)

### Backup Components

#### PostgreSQL Primary Backup
```
Database: openfireblocks
Size: ~300GB
Method: pg_basebackup (streaming replication)
WAL Archiving: S3 continuous archiving
Retention: 30 days full + 7 days incremental
```

#### PostgreSQL Replica Backup
```
Database: openfireblocks
Size: ~300GB
Method: Read-only snapshot (no disruption to replication)
Purpose: Alternative restore point if primary corrupted
Retention: 7 days
```

#### HashiCorp Vault Backup
```
Components:
- Unseal keys (encrypted, separate location)
- Auth tokens and policies
- Transit engine keys (critical)
- Secret engines data
- Audit logs
Size: ~50GB
Method: Raft snapshot export
Retention: 30 days
Encryption: AES-256-GCM
```

#### Temporal Backup
```
Database: temporal
Size: ~150GB
Method: PostgreSQL backup (same as primary)
Critical Data:
- Workflow history
- Activity state
- Timer events
Retention: 7 days (Temporal history retention: 30 days)
```

## Restore Procedures

### Prerequisites for Any Restore
1. [ ] Verify backup integrity (checksum validation)
2. [ ] Confirm restore environment (secondary region or isolated primary)
3. [ ] Notify security team (audit trail)
4. [ ] Document restore decision and authorization

### PostgreSQL Point-in-Time Recovery (PITR)

**Objective**: Restore to any point within WAL archiving retention (30 days)

**Procedure**:
1. Stop PostgreSQL on target system
2. Move current data directory to backup location
3. Initialize recovery.conf with:
   ```
   restore_command = 'aws s3 cp s3://openfireblocks-backups/wal/%f %p'
   recovery_target_timeline = 'latest'
   recovery_target_time = '2024-06-30 14:30:00'  # Adjust timestamp
   ```
4. Start PostgreSQL (reads base backup + applies WAL archive)
5. Verify recovery:
   ```sql
   SELECT pg_current_xlog_location();
   SELECT COUNT(*) FROM ceremonies; -- Sanity check
   ```
6. Promote to read-write when verified

**Estimated RTO**: 1-2 hours (depends on backup size and WAL volume)

### PostgreSQL Replica Promotion

**Objective**: Promote read-only replica to primary

**Procedure**:
1. Verify replica is in sync with primary
   ```sql
   SELECT slot_name, restart_lsn, confirmed_flush_lsn 
   FROM pg_replication_slots;
   ```
2. Stop writes to primary:
   ```sql
   ALTER DATABASE openfireblocks SET default_transaction_read_only = on;
   ```
3. On replica, stop replication:
   ```sql
   SELECT pg_wal_replay_resume(); -- If paused
   SELECT pg_wal_replay_pause();  -- Pause to get consistent state
   ```
4. Promote replica:
   ```sh
   pg_ctl promote -D /var/lib/postgresql/data
   ```
5. Verify promotion:
   ```sql
   SELECT pg_is_wal_replay_paused(); -- Should return false
   ```
6. Update connection strings in applications
7. Reinitialize old primary as new replica when ready

**Estimated RTO**: 15-30 minutes

### Vault Cluster Recovery

**Objective**: Restore Vault cluster to known good state

**Procedure**:
1. Retrieve unseal keys from secure storage (separate from backup)
2. Stop all Vault instances
3. Restore Raft snapshot:
   ```sh
   vault operator raft snapshot restore <backup-file>
   ```
4. Start Vault instances
5. Unseal cluster:
   ```sh
   vault operator unseal <key1>
   vault operator unseal <key2>
   vault operator unseal <key3>
   ```
6. Verify cluster health:
   ```sh
   vault status
   vault operator members
   ```
7. Verify transit engine keys are available:
   ```sh
   vault list transit/keys/
   ```

**Estimated RTO**: 30-45 minutes

**Critical**: Unseal keys are NEVER backed up with the snapshot. Store separately in:
- Physical vault (3 of 5 keys)
- Key management system (HSM)
- Encrypted cold storage

### Temporal Workflow Recovery

**Objective**: Restore Temporal database and resume workflows

**Procedure**:
1. Identify backup point (latest PITR checkpoint)
2. Restore PostgreSQL database to that point (see PITR procedure)
3. Restart Temporal services:
   ```sh
   docker-compose restart temporal
   ```
4. Verify Temporal frontend is responding:
   ```sh
   tctl admin cluster describe
   ```
5. Check for missing workflow executions:
   ```sh
   tctl workflow list --query='ExecutionStatus=RUNNING'
   ```
6. Resume paused workflows if needed
7. Monitor for workflow failures/replays

**Estimated RTO**: 1-2 hours (depends on workflow complexity)

### Complete System Restore (All Components)

**Objective**: Full system restore from disaster

**Procedure**:
1. Provision new infrastructure (secondary region)
2. Restore PostgreSQL (PITR to point before failure)
3. Restore Vault cluster from snapshot
4. Restore Temporal (comes with PostgreSQL restore)
5. Verify all services are healthy:
   ```bash
   curl https://postgres:5432/health
   curl https://vault:8200/v1/sys/health
   curl https://temporal:7233/health
   ```
6. Update DNS/load balancer to point to new region
7. Monitor for data consistency issues
8. Run post-recovery audit (see below)

**Estimated RTO**: 3-4 hours (meets RTO ≤ 4 hours target)

## Testing Procedures

### Monthly Backup Verification (First Friday of Each Month)
1. Restore latest full backup to isolated environment
2. Verify all databases are accessible
3. Run data integrity checks (row counts, checksums)
4. Perform application connectivity test
5. Document results

### Quarterly DR Test (Q1, Q2, Q3, Q4)
1. Plan test for low-traffic period (Saturday 02:00 UTC)
2. Provision secondary region resources
3. Execute complete system restore procedure
4. Run application smoke tests
5. Measure actual RTO (document as "Test RTO")
6. Verify RPO (check for data loss)
7. Compare actual vs. planned RTO/RPO
8. Document findings and improvement items

### Annual Full Failover Test
1. Execute complete disaster recovery procedure
2. Fail over all traffic to secondary region
3. Run 24-hour production workload test
4. Verify monitoring and alerting in secondary region
5. Test failback to primary region
6. Document lessons learned

## Post-Recovery Validation

After any restore (test or emergency), validate:

### Data Integrity
```sql
-- Check all tables
SELECT schemaname, tablename 
FROM pg_tables 
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
ORDER BY tablename;

-- Validate key business objects
SELECT COUNT(*) as customer_count FROM customers;
SELECT COUNT(*) as ceremony_count FROM ceremonies;
SELECT COUNT(*) as transaction_count FROM transactions;
```

### Replication Status
```sql
-- On primary
SELECT slot_name, active, restart_lsn FROM pg_replication_slots;
SELECT * FROM pg_stat_replication;
```

### Temporal Workflow Status
```
tctl workflow list --query='ExecutionStatus=COMPLETED' --limit=100
tctl workflow list --query='ExecutionStatus=FAILED' --limit=100
```

### Application Connectivity
```bash
# API Gateway health
curl https://api-gateway:3000/health

# MPC Signer health
curl https://mpc-signer:8080/health

# Ceremony Orchestrator health
curl https://ceremony-orchestrator:8081/health
```

## Monitoring and Alerting

### Backup Success Monitoring
- Alert if daily full backup fails
- Alert if 4 consecutive incremental backups fail
- Alert if backup exceeds 2x estimated duration

### Restore Point Verification
- Daily: Verify latest backup is available and accessible
- Weekly: Perform checksum validation of weekly backup
- Monthly: Perform test restore to isolated environment

### Database Replication Monitoring
- Alert if replication lag exceeds 5 minutes
- Alert if replica falls behind (not receiving WAL)
- Alert if replication slot is not active

### Vault Health Monitoring
- Alert if cluster is not healthy (> 1 node down)
- Alert if any node is unsealed
- Alert if transit keys are inaccessible

## Backup Storage Architecture

### Primary Backup Location (S3)
```
s3://openfireblocks-backups/
├── daily/
│   └── backup-YYYY-MM-DD.tar.gz
├── incremental/
│   └── backup-YYYY-MM-DD-HH.tar.gz
├── wal-archive/
│   └── 000000010000000000000001
└── metadata/
    └── backup-manifest.json
```

### Backup Retention Policy
- Full backups: 30 days
- Incremental backups: 7 days
- WAL archives: 30 days (enables PITR)
- Deleted backups: Archive to Glacier after 30 days

### Encryption
- AES-256-GCM for all backups in transit
- AES-256-GCM at rest in S3
- Separate encryption keys per environment (prod, staging)
- KMS key rotation every 90 days

## Disaster Scenarios and Response

### Scenario 1: Single Server Failure (PostgreSQL Replica)
**Recovery Time**: 15-30 minutes
**Procedure**: Replace failed server, reinitialize from streaming replication
**Data Loss**: None (primary unaffected)

### Scenario 2: Primary Database Corruption
**Recovery Time**: 1-2 hours
**Procedure**: PITR to point before corruption, verify data integrity
**Data Loss**: < 1 hour (depends on backup frequency)

### Scenario 3: Ransomware/Malware Encryption
**Recovery Time**: 3-4 hours
**Procedure**: 
1. Immediately disconnect all systems from network
2. Verify offline backup (not yet encrypted)
3. Restore from known-good backup
4. Scan for malware before bringing online
**Data Loss**: Up to 1 hour

### Scenario 4: Regional Outage (AWS Region Down)
**Recovery Time**: 3-4 hours
**Procedure**: Activate disaster recovery plan, failover to secondary region
**Data Loss**: < 1 hour

### Scenario 5: Vault Encryption Key Loss
**Recovery Time**: Cannot recover (catastrophic)
**Prevention**: Multiple encrypted copies of unseal keys + HSM backup
**Mitigation**: Regular key rotation and redundant escrow

## Escalation Procedures

### Backup Failure
1. Page on-call engineer
2. Investigate backup logs
3. If < 4 hours since last successful backup: escalate to manager
4. If > 4 hours: page VP of Infrastructure

### Failed Restore Test
1. Document findings
2. Review and fix procedures
3. Re-test within 48 hours
4. If recurring failure: escalate to architecture review

### Actual Disaster
1. Declare incident (Severity: Critical)
2. Activate IR team
3. Begin recovery (follow appropriate scenario above)
4. Communicate status to customers every 15 minutes
5. Post-incident review within 24 hours

## Documentation and Compliance

- [ ] Backup procedures documented and reviewed
- [ ] Restore procedures documented and tested
- [ ] DR procedures documented and tested
- [ ] MTTR/MTTR targets defined and tracked
- [ ] Test results documented quarterly
- [ ] Lessons learned captured and actioned
- [ ] Compliance requirements met (SOC 2 A1, ISO 27001 A.11.5)
- [ ] Annual management review of DR plan

---

**Owner**: VP of Infrastructure
**Last Updated**: 2026-06-30
**Next Review**: 2026-09-30
**Certification Target**: Passed in Q3 2026
