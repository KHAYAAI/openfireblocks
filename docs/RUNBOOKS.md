# OpenFireblocks Operations Runbooks

**Owner**: Operations Lead  
**Audience**: On-call engineers, infrastructure team  
**Review Cycle**: Quarterly

---

## Table of Contents

1. [Incident Response Framework](#incident-response-framework)
2. [Common Runbooks](#common-runbooks)
3. [Escalation Procedures](#escalation-procedures)
4. [Post-Incident Review](#post-incident-review)

---

## Incident Response Framework

### Alert Severity & Response Times

| Severity | MTTD | MTTR | Examples |
|----------|------|------|----------|
| **Critical (P1)** | <15 min | <4 hours | Service down, data loss, security breach |
| **High (P2)** | <1 hour | <8 hours | High error rate, degraded performance |
| **Medium (P3)** | <4 hours | <24 hours | Non-critical alerts, warnings |
| **Low (P4)** | 1 day | 5 days | Informational, planned maintenance |

### Incident Commander Duties

1. **Acknowledge** alert within SLA
2. **Assess** severity and impact
3. **Assemble** response team
4. **Coordinate** remediation efforts
5. **Communicate** status to stakeholders
6. **Document** all actions in incident log
7. **Resolve** the incident
8. **Debrief** with post-incident review

---

## Common Runbooks

### RB-001: Database Connection Pool Exhaustion

**Alert**: `RDSHighConnections` (>200 connections)

**Impact**: API timeouts, customer-facing errors

**Response**:

1. **Immediate** (0-5 min):
   ```bash
   # Check current connections
   psql -h $DB_ENDPOINT -U postgres -c \
     "SELECT datname, count(*) FROM pg_stat_activity GROUP BY datname;"

   # Check long-running queries
   psql -h $DB_ENDPOINT -U postgres -c \
     "SELECT pid, duration, query FROM pg_stat_statements ORDER BY duration DESC LIMIT 10;"
   ```

2. **Investigation** (5-15 min):
   - Are connections from expected services?
   - Are there stuck transactions?
   - Is query performance degraded?
   - Check CloudWatch RDS metrics

3. **Mitigation**:
   ```bash
   # Terminate idle connections (>30 min)
   psql -h $DB_ENDPOINT -U postgres -c \
     "SELECT pg_terminate_backend(pid) FROM pg_stat_activity 
      WHERE datname='openfireblocks' AND state='idle' AND idle_in_transaction_session_timeout > '30 min';"

   # Scale connection pool in application
   # (requires code deployment or config update)

   # As last resort: failover to replica (RTO: 15-30 min)
   ```

4. **Resolution**:
   - Monitor connections return to normal
   - Review application connection pool settings
   - Check for connection leaks in application code
   - Update database connection limits if needed

5. **Post-Incident**:
   - Document root cause
   - Implement connection pooling middleware if not present
   - Add alert for idle transactions >30 min
   - Review database performance regularly

---

### RB-002: Vault Cluster Unsealing

**Alert**: `VaultSealed`

**Impact**: Cannot perform key material operations, signing halted

**Response**:

1. **Immediate** (0-2 min):
   ```bash
   # Check Vault status
   vault status
   # Expected: sealed=false, unsealed_shares=3/5

   # Check cluster health
   vault operator raft list-peers
   ```

2. **If sealed**:
   ```bash
   # Retrieve unseal keys (from Secrets Manager)
   aws secretsmanager get-secret-value \
     --secret-id openfireblocks-vault-unseal-keys \
     --region us-east-1 | jq -r '.SecretString' | base64 -d

   # Unseal each key
   vault operator unseal KEY_1
   vault operator unseal KEY_2
   vault operator unseal KEY_3

   # Verify unsealed
   vault status | grep "sealed"  # Should be false
   ```

3. **If cluster degraded** (e.g., 1/3 nodes down):
   ```bash
   # Check Raft status
   vault operator raft list-peers

   # Remove dead node
   vault operator raft remove-peer nodeID

   # Add new node (requires provisioning EC2 instance)
   # ASG will auto-replace failed instance
   ```

4. **Recovery**:
   - Verify cluster is healthy: `vault status`
   - Test signing operations
   - Monitor for errors
   - Document root cause

---

### RB-003: RDS Replication Lag Exceeding Target

**Alert**: `RDSReplicationLagged` (>60 seconds)

**Impact**: DR failover may lose data >RPO target

**Response**:

1. **Immediate** (0-5 min):
   ```bash
   # Check replication lag
   aws rds describe-db-instances \
     --db-instance-identifier openfireblocks-prod-db-replica \
     --query 'DBInstances[0].ReplicationLag'

   # Check replica status
   aws rds describe-db-instances \
     --db-instance-identifier openfireblocks-prod-db-replica \
     --query 'DBInstances[0].[DBInstanceStatus, AvailabilityZone]'
   ```

2. **Investigation**:
   - Check primary database performance (query latency, throughput)
   - Check network connectivity between regions
   - Check replica instance resources (CPU, I/O, storage)
   - Review CloudWatch metrics for anomalies

3. **Mitigation**:
   ```bash
   # Reduce primary workload if possible (throttle non-critical operations)
   # Reduce replica workload (stop maintenance jobs, etc.)
   # Monitor lag - should catch up within 5 minutes

   # If still lagged after 10 min:
   # Check if there's a blocking query on primary
   ```

4. **Resolution**:
   - Once lag returns to normal (<60s), mark incident resolved
   - Document root cause (network, resources, workload spike)
   - Implement monitoring for proactive detection
   - Schedule capacity review if resource-constrained

---

### RB-004: API High Error Rate (>5%)

**Alert**: `APIHighErrorRate`

**Impact**: Customer-facing errors, transaction failures

**Response**:

1. **Immediate** (0-2 min):
   ```bash
   # Check error rate and error type
   curl -s http://prometheus:9090/api/v1/query \
     'rate(http_requests_total{status=~"5.."}[5m])' | jq

   # Check application logs
   curl -s http://kibana:5601/api/saved_objects/search/application-errors

   # Check which services are affected
   kubectl get pods -n production --show-labels | grep -E 'Error|NotReady|CrashLoopBackOff'
   ```

2. **Investigation**:
   - What error codes (500, 502, 503, 504)?
   - Which services/endpoints affected?
   - Check for recent deployments
   - Check for resource exhaustion
   - Check database connectivity
   - Check external service dependencies

3. **Mitigation**:
   ```bash
   # Option 1: Roll back recent deployment
   kubectl rollout undo deployment/api-gateway -n production

   # Option 2: Scale up if resource-constrained
   kubectl scale deployment api-gateway --replicas=5 -n production

   # Option 3: Restart services
   kubectl delete pods -l app=api-gateway -n production

   # Option 4: Failover to secondary region (if primary is degraded)
   ```

4. **Resolution**:
   - Error rate should drop below 1%
   - Verify affected operations complete successfully
   - Monitor for 10 minutes for stability

5. **Root Cause Analysis**:
   - Review application logs for specific errors
   - Check database performance
   - Verify external dependencies (Vault, Temporal)
   - Review code changes if recent deployment

---

### RB-005: Signing Operation Timeout

**Alert**: `SigningOperationTimeout`

**Impact**: Transactions cannot be signed, customer operations blocked

**Response**:

1. **Immediate** (0-5 min):
   ```bash
   # Check Temporal workflow status
   temporal workflow list -A | grep -i timeout

   # Check Vault is operational
   curl -k https://vault.openfireblocks.internal:8200/v1/sys/health | jq

   # Check DKG ceremony status
   kubectl logs -l app=dkg-coordinator -n production | tail -100
   ```

2. **Investigation**:
   - How many operations timing out?
   - Which parties are slow/unresponsive?
   - Network latency between parties?
   - Vault unsealed and healthy?
   - Temporal workflow engine healthy?

3. **Mitigation**:
   ```bash
   # Increase timeout (temporary, requires config change)
   # Restart parties if stuck
   kubectl delete pods -l app=signing-party -n production

   # Restart Temporal
   kubectl delete pods -l app=temporal -n production

   # Clear stuck workflows
   temporal workflow terminate WORKFLOW_ID \
     --reason "Timeout - Manual termination"
   ```

4. **Resolution**:
   - Verify new signing operations succeed
   - Check operation latency returns to <100ms p95
   - Retry failed operations

5. **Investigation**:
   - Review Jaeger traces for slow steps
   - Check network performance between regions/parties
   - Verify Vault performance (key retrieval latency)
   - Review Temporal workflow logs

---

### RB-006: Security Incident Detected

**Alert**: `SecurityIncidentDetected` or `UnauthorizedAccessAttempt`

**Impact**: Potential unauthorized access, data exfiltration, compliance violation

**Response**:

**IMMEDIATE - ACTIVATE INCIDENT RESPONSE TEAM**

1. **Within 15 minutes**:
   ```bash
   # Isolate affected resources
   # CHECK: AWS WAF logs for attack pattern
   # NOTIFY: Security team, incident commander
   # INITIATE: Incident response procedure
   # DOCUMENT: All actions in incident log
   ```

2. **Investigation**:
   - Check CloudTrail for unauthorized API calls
   - Review WAF logs for attack patterns
   - Check VPC Flow Logs for suspicious network traffic
   - Audit Vault audit logs for unauthorized access
   - Review PostgreSQL audit logs for queries on sensitive data
   - Check GuardDuty findings

3. **Containment**:
   - Block offending IP addresses in WAF
   - Revoke compromised credentials
   - Isolate affected instances if needed
   - Rotate database passwords

4. **Recovery**:
   - Restore from backup if data modified
   - Verify data integrity
   - Resume normal operations

5. **Post-Incident**:
   - **GDPR Breach Notification**: If PII compromised, notify within 72 hours
   - **Regulatory Notification**: Notify relevant authorities
   - **Customer Notification**: Notify affected customers
   - Full security audit of affected systems
   - Implement preventive measures

---

### RB-007: Backup Failure

**Alert**: `BackupFailed` or `BackupOlderThan24Hours`

**Impact**: No recovery point, cannot restore if disaster occurs

**Response**:

1. **Immediate** (0-5 min):
   ```bash
   # Check backup status
   aws rds describe-db-snapshots \
     --query 'DBSnapshots[?DBInstanceIdentifier==`openfireblocks-prod-db`] | [0].[Status, PercentProgress]'

   # Check backup logs
   kubectl logs -l app=backup-manager -n production | tail -50
   ```

2. **Investigation**:
   - What failed (RDS, Vault, Temporal)?
   - Error messages?
   - S3 bucket full or permission denied?
   - Network issues?
   - Time taken (hanging)?

3. **Mitigation**:
   ```bash
   # Try manual backup
   aws rds create-db-snapshot \
     --db-instance-identifier openfireblocks-prod-db \
     --db-snapshot-identifier openfireblocks-manual-$(date +%s)

   # Monitor progress
   watch 'aws rds describe-db-snapshots --db-snapshot-identifier openfireblocks-manual-TIMESTAMP --query "DBSnapshots[0].[Status,PercentProgress]"'
   ```

4. **If still failing**:
   - Contact AWS Support
   - Check S3 bucket for space
   - Verify IAM permissions
   - Check network connectivity

5. **Resolution**:
   - Successful backup confirms recovery capability
   - Test restore from backup weekly

---

### RB-008: Database Failover

**Alert**: Primary DB unresponsive or explicitly triggered

**Impact**: Temporary unavailability during failover (~5-10 min)

**Response**:

**PLANNED FAILOVER** (maintenance window):

```bash
# 1. Notify stakeholders 30 min before
# 2. Drain connections from primary
# 3. Stop write operations for 1 minute to ensure consistency
# 4. Promote read replica
aws rds promote-read-replica \
  --db-instance-identifier openfireblocks-prod-db-replica

# 5. Wait for promotion to complete (5-10 min)
aws rds describe-db-instances \
  --db-instance-identifier openfireblocks-prod-db-replica \
  --query 'DBInstances[0].ReadReplicaSourceDBInstanceIdentifier'
# Expected: null (no longer a replica)

# 6. Update applications to new endpoint
# 7. Create new read replica from new primary
aws rds create-db-instance-read-replica \
  --db-instance-identifier openfireblocks-prod-db-replica-restored \
  --source-db-instance-identifier openfireblocks-prod-db-replica
```

**UNPLANNED FAILOVER** (primary down):

```bash
# Same steps as planned, but executed immediately
# Expected downtime: 5-10 minutes
# Expected data loss: 0-60 seconds (RPO target)
```

**After Failover**:
- Test database connectivity from all services
- Verify replication to new replica
- Monitor for 1 hour
- Document root cause of failure

---

## Escalation Procedures

### Severity 1 (Critical - P1) Escalation

```
On-Call Engineer
    ↓ (no resolution within 30 min)
Engineering Manager + Security Lead
    ↓ (no resolution within 1 hour)
VP of Engineering + CISO
    ↓ (no resolution within 2 hours)
CEO + Board
```

### Severity 2 (High - P2) Escalation

```
On-Call Engineer
    ↓ (no resolution within 4 hours)
Engineering Manager
    ↓ (no resolution within 8 hours)
VP of Engineering
```

### Communication Channels

- **Immediate**: PagerDuty (on-call notifications)
- **Real-time**: Slack #openfireblocks-incidents
- **Stakeholders**: Email + customer notification portal
- **Public**: Status page (status.openfireblocks.io)

---

## Post-Incident Review

**Conducted**: Within 48 hours of incident resolution

**Participants**: Incident commander, affected teams, manager

**Template**:

```markdown
## Incident Report: [Incident Name]

**Date**: YYYY-MM-DD HH:MM UTC  
**Duration**: X minutes  
**Severity**: P1/P2/P3/P4  
**Status**: Resolved / In Progress

### Impact
- Services affected: [List]
- Customers impacted: [Count]
- Data loss: [None / <amount>]
- Revenue impact: [$X]

### Timeline
- **HH:MM** - Alert triggered
- **HH:MM** - Incident acknowledged
- **HH:MM** - Root cause identified
- **HH:MM** - Mitigation started
- **HH:MM** - Incident resolved

### Root Cause
[Technical explanation of why this happened]

### Remediation
[What was done to fix it]

### Prevention
[What changes prevent this in future]
- [ ] Action 1 (assigned to X, due Y)
- [ ] Action 2 (assigned to X, due Y)

### Metrics
- MTTD (Mean Time To Detect): X min
- MTTR (Mean Time To Resolve): Y min
- Data loss (RPO): Z seconds
```

---

## On-Call Schedule

**Primary**: Engineer on rotation  
**Secondary**: Backup engineer  
**Manager**: Engineering manager (if P1 escalation)

**Handoff**: Mondays 9:00 AM UTC

**Escalation**:
- P1: Immediate
- P2: During business hours
- P3: Next business day
- P4: Planned maintenance

---

## Tools & Access

### Monitoring & Alerts
- **Grafana**: https://grafana.openfireblocks.internal:3000
- **Prometheus**: https://prometheus.openfireblocks.internal:9090
- **Kibana**: https://kibana.openfireblocks.internal:5601
- **Jaeger**: https://jaeger.openfireblocks.internal:16686
- **PagerDuty**: https://openfireblocks.pagerduty.com

### Infrastructure Access
- **AWS Console**: https://console.aws.amazon.com
- **Kubernetes**: `kubectl` (configured)
- **Database**: `psql` (with credentials from Secrets Manager)
- **Vault**: `vault` CLI (authenticated)

### Communication
- **Slack**: #openfireblocks-incidents, #openfireblocks-alerts
- **Email**: on-call@openfireblocks.io
- **Phone**: PagerDuty notifications
- **Status Page**: status.openfireblocks.io

---

**Last Updated**: 2026-07-15  
**Next Review**: Q3 2026  
**Owner**: Operations Lead
