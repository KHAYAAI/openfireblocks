# Week 11-12: Soft Launch Procedures

**Target**: Deploy to production, 24-hour monitoring, internal validation  
**Duration**: 2 weeks (Week 11: prep, Week 12: launch + monitoring)  
**Owner**: Product Lead + Operations Lead  
**Success Criteria**: 0 critical issues in first 24 hours

---

## Pre-Launch Checklist (Week 11)

### Infrastructure Validation

- [ ] All Terraform modules applied successfully
- [ ] Multi-region setup verified (primary + secondary)
- [ ] Database replication tested and confirmed (<1 hour RPO)
- [ ] Vault cluster initialized with all 3 nodes operational
- [ ] Load balancers responding to health checks
- [ ] ECS services running in all regions
- [ ] CloudWatch logs flowing for all components
- [ ] VPC peering active between regions
- [ ] WAF rules deployed and logging
- [ ] KMS keys operational in both regions
- [ ] S3 backups enabled and tested

### Monitoring Stack Validation

- [ ] Prometheus scraping all targets (13 jobs)
- [ ] AlertManager routing working (test alert)
- [ ] Grafana dashboards accessible and populated
- [ ] Jaeger tracing active
- [ ] ELK stack running (Elasticsearch, Logstash, Kibana)
- [ ] Filebeat collecting logs from all services
- [ ] Alert thresholds reviewed and tuned
- [ ] Slack integration confirmed
- [ ] PagerDuty routing confirmed
- [ ] Email alerts working

### Security Review

- [ ] Security audit completed (no critical findings outstanding)
- [ ] TLS certificates valid (check expiry dates)
- [ ] WAF rules active and tested
- [ ] Network ACLs reviewed
- [ ] IAM roles reviewed for least privilege
- [ ] Secrets Manager populated
- [ ] Database encryption verified
- [ ] Backup encryption verified
- [ ] VPC Flow Logs enabled
- [ ] CloudTrail logging enabled
- [ ] GuardDuty enabled

### Testing Completion

- [ ] Unit tests: 95%+ pass rate
- [ ] Integration tests: All passing
- [ ] E2E tests: Critical paths validated
- [ ] Security tests: SAST/DAST/pen test completed
- [ ] Performance tests: P95 <100ms, throughput >100 sig/sec
- [ ] Load testing: 10x expected peak load
- [ ] Chaos engineering: System survives failures
- [ ] Backup/restore: Tested and working
- [ ] Failover: Tested in secondary region

### Documentation Review

- [ ] OpenAPI spec complete and valid
- [ ] SDKs functional and tested
- [ ] Integration guides (Bitcoin, Ethereum, etc)
- [ ] Code examples working
- [ ] Runbooks reviewed by team
- [ ] Operations procedures documented
- [ ] Incident response playbooks approved
- [ ] Customer onboarding materials ready
- [ ] Support documentation complete

### Team Readiness

- [ ] Support team trained (3-day training completed)
- [ ] On-call rotation scheduled
- [ ] Incident response team identified
- [ ] Escalation paths documented
- [ ] Communication channels tested
- [ ] War room setup (Slack, bridge line)
- [ ] Customer notification template created
- [ ] Status page configured
- [ ] Handoff procedures documented

---

## Launch Week (Week 12)

### Monday: Final Go-Live Review

**9:00 AM - Launch Review Meeting** (30 min)

Attendees: Product Lead, VP Engineering, CISO, Operations Lead

Agenda:
```
1. Review pre-launch checklist (5 min)
2. Confirm all critical items complete (10 min)
3. Identify any blockers (10 min)
4. Approve go-live (5 min)
```

Decision Point: **Go** or **No-Go** for launch

If **NO-GO**: 
- Document blocker
- Create mitigation plan
- Reschedule launch (typically 1 week)

If **GO**:
- Proceed to deployment

### Monday Afternoon: Production Deployment

**2:00 PM - Deployment Window** (2-4 hours)

Pre-deployment checklist:
- [ ] All critical services have recent successful tests
- [ ] Database backups completed
- [ ] Team assembled in war room
- [ ] Communication channels active (Slack #openfireblocks-launch)
- [ ] Status page created (status.openfireblocks.io)
- [ ] Customer notification email drafted
- [ ] Internal notification sent

**Deployment Steps**:

```
1. Update status page to "Deploying" (2:00 PM)
2. Deploy API services from main branch
   - Docker build
   - ECR push
   - ECS rolling update (30 min)
3. Verify API responding (curl health endpoint)
4. Run smoke tests
   - Create key pair
   - Sign transaction
   - List keys
5. Monitor metrics (15 min monitoring period)
6. If issues: Rollback (10-15 min)
7. If OK: Mark production ready (6:00 PM)
```

**Post-Deployment**:
- [ ] Health check passed
- [ ] Smoke tests passed
- [ ] Metrics normal
- [ ] No errors in logs
- [ ] Announce "Live" on status page

### Tuesday-Friday: 24-Hour Intensive Monitoring

**Day 1-5 Monitoring Schedule**:

```
On-Call Rotation:
- Primary: VP Engineering
- Secondary: Operations Lead
- Tertiary: Infrastructure Lead

Monitoring Intensity:
- Metrics: 5-minute checks
- Logs: Continuous monitoring
- Alerts: <5 min response
- User feedback: Every 2 hours

Shift Schedule:
- 9:00 AM - 1:00 PM: Team standup every hour
- 1:00 PM - 5:00 PM: Check-ins every 2 hours
- 5:00 PM - 9:00 PM: Check-ins every 1 hour (peak load time)
- 9:00 PM - 12:00 AM: Check-ins every 2 hours
- 12:00 AM - 9:00 AM: Sleeping rotation (on-call alerts only)
```

**Metrics to Monitor**:

```
Infrastructure:
- CPU usage: <70%
- Memory usage: <80%
- Disk space: >20% free
- Network latency: <50ms

Database:
- Connections: <100 (of 500 max)
- Query latency: <50ms p95
- Replication lag: <60 seconds
- Backup status: Running on schedule

Application:
- Error rate: <0.5%
- API latency: <100ms p95
- Signing operations: 100% success
- Throughput: Increasing toward 100 sig/sec

Compliance:
- AML/KYC checks: <100 pending
- Security alerts: 0
- Unauthorized access: 0
```

**Daily Standup** (9:00 AM):
```
Agenda (15 min):
1. Incidents overnight? (5 min)
2. Current metrics status (5 min)
3. Customer feedback (3 min)
4. Plan for day (2 min)
```

### Week 12 Post-Launch

**Friday 5:00 PM - Launch Retrospective**

After 5 days of successful operation:

Attendees: All core team + management

Agenda (60 min):
```
1. Review incident log (10 min)
2. Celebrate success (5 min)
3. Discuss learnings (20 min)
4. Improvement items (15 min)
5. Next steps (10 min)
```

**Retrospective Template**:
```markdown
## Launch Retrospective

**Date**: Week 12 launch

### Incidents
- None / List incidents with resolution

### Metrics Summary
- Error rate: X%
- Uptime: Y%
- Peak throughput: Z sig/sec
- Customer satisfaction: N/A (internal only)

### What Went Well
1. Item 1
2. Item 2
3. Item 3

### What Could Be Better
1. Item 1 (owner, deadline)
2. Item 2 (owner, deadline)
3. Item 3 (owner, deadline)

### Action Items
- [ ] Action 1 (assigned to X, due Y)
- [ ] Action 2 (assigned to X, due Y)

### Sign-Off
- Operations Lead: ___________
- VP Engineering: ___________
```

---

## Launch Rollback Plan

If critical issues found in first 24 hours:

### Severity 1 (Service Down, Security Breach)

```
1. Immediately page on-call team (5 min response)
2. Assess impact (internal systems only)
3. If root cause > 30 min away:
   - Rollback to previous version (10-15 min)
   - Redeploy if fix available (30-60 min)
4. Communicate status
5. Post-incident review within 24 hours
```

### Rollback Procedure

```bash
# Mark as rolling back
# Switch load balancer to previous version
# Verify health checks passing
# Monitor for 15 minutes
# If stable: Investigate root cause
# If unstable: Continue rollback
```

### Communication During Rollback

- Status page: "Incident - Investigating"
- Slack: Real-time updates
- Email: Summary (if extended >30 min)

---

## Post-Launch Transition

### Week 12 Evening: Documentation Update

- [ ] Update architecture diagram with real metrics
- [ ] Document any deviations from plan
- [ ] Update runbooks with lessons learned
- [ ] Publish customer-facing documentation
- [ ] Create launch retrospective report

### Week 13: Early Access Planning

- [ ] Identify 5-10 beta customers
- [ ] Create onboarding flow
- [ ] Prepare customer support procedures
- [ ] Plan feedback collection mechanism
- [ ] Schedule weekly check-ins with beta customers

### Week 14+: Phase B (Early Access) Starts

---

## Monitoring Dashboard Template

**During Launch Week Dashboard** (visible in war room):

```
┌─ OpenFireblocks Launch Dashboard ────────────────────────┐
│                                                            │
│  Status: [●●●●●●●●●●] 100% Live (XX hours 0 min)        │
│                                                            │
│  ┌─ Infrastructure ─────┐  ┌─ Application ──────────┐   │
│  │ CPU: 45% / 100%     │  │ Error Rate: 0.2% ✓     │   │
│  │ Memory: 62% / 128GB │  │ Latency p95: 87ms ✓    │   │
│  │ Disk: 24GB / 100GB  │  │ Throughput: 45 sig/sec │   │
│  │ Connections: 34/500 │  │ Signing Success: 99.8%  │   │
│  └─────────────────────┘  └────────────────────────┘   │
│                                                            │
│  ┌─ Database ───────────┐  ┌─ Compliance ───────────┐   │
│  │ Repl Lag: 12s ✓      │  │ AML Checks: 234 pend   │   │
│  │ Query p95: 42ms ✓    │  │ KYC Approved: 12       │   │
│  │ Backup: OK           │  │ Security Alerts: 0 ✓   │   │
│  │ WAL Arch: 4.2GB      │  │ Incidents: 0           │   │
│  └─────────────────────┘  └────────────────────────┘   │
│                                                            │
│  Recent Events:                                            │
│  ✓ 14:00 - Deployment complete                           │
│  ✓ 14:15 - All health checks passing                     │
│  ✓ 14:30 - Smoke tests passed                            │
│  ✓ 15:00 - Peak load test successful (100 sig/sec)      │
│                                                            │
└────────────────────────────────────────────────────────┘
```

---

## Success Criteria for Soft Launch

Launch is considered successful if:

✅ **Infrastructure**:
- All services up and responding (uptime 100%)
- Multi-region failover operational
- Database replication <1 hour lag
- Backup completed successfully

✅ **Operations**:
- Monitoring dashboard stable
- No critical alerts
- On-call team responding <5 min
- No escalations

✅ **Security**:
- No security alerts
- No unauthorized access attempts
- All encryption operational
- WAF blocking attacks

✅ **Application**:
- Error rate <1%
- Latency p95 <100ms
- Throughput >50 sig/sec
- Signing success >99%

✅ **Team**:
- Support team operational
- Communication smooth
- Incident response tested
- Team morale high

---

**Launch Coordinator**: Product Lead  
**Status Updates**: Every 2 hours during Week 12  
**Escalation**: VP Engineering (on-call)  
**Post-Launch Review**: Friday Week 12, 5:00 PM
