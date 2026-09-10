# Phase 3: Incident Response Playbook

**Status:** Security Incident Handling Procedures

## Executive Summary

This document defines OpenFireblocks' incident response procedures for security events. The objective is to:
- Detect incidents within 4 hours (MTTD < 4 hours)
- Respond to critical incidents within 1 hour
- Contain critical incidents within 4 hours
- Restore normal operations within RTO/RPO targets

## Incident Response Organization

### IR Team Roles

**Incident Commander** (Security Manager)
- Overall authority and decision making
- Escalation point
- Customer/regulatory communication
- Declares incident severity

**Security Lead** (Senior Security Engineer)
- Technical investigation and containment
- Forensics and evidence collection
- Root cause analysis

**Operations Lead** (Infrastructure Manager)
- System recovery and restoration
- Database recovery if needed
- Failover execution

**Communications Lead** (Legal/Compliance)
- Customer notification
- Regulatory reporting (GDPR, etc.)
- Media response (if needed)

**Scribe** (Support Team Member)
- Timeline documentation
- Incident log updates
- Evidence tracking

### On-Call Rotation
- 24/7 on-call schedule (1 person per shift)
- Escalation: 15-minute response time for pages
- Backup: Second responder for critical incidents
- Holiday coverage: Mandatory staffing

## Incident Severity Levels

### Critical (Severity 1)
- **Definition**: Active compromise or data breach in progress
- **Examples**:
  - Unauthorized access to customer data
  - Ransomware encryption detected
  - DDoS attack affecting availability (>1 hour)
  - Insider threat (unauthorized transactions)
- **Response Time**: Acknowledge within 15 minutes
- **Containment SLA**: 4 hours
- **Escalation**: VP of Security, CEO, Board
- **Communication**: Customer within 1 hour

### High (Severity 2)
- **Definition**: Significant security incident with temporary impact
- **Examples**:
  - Successful exploit in staging environment
  - Suspicious activity on production systems
  - Configuration error exposing data
  - Failed login attempts on privileged accounts
- **Response Time**: Acknowledge within 1 hour
- **Containment SLA**: 8 hours
- **Escalation**: CISO, VP of Security
- **Communication**: Customer within 4 hours (if affecting them)

### Medium (Severity 3)
- **Definition**: Security event with low customer impact
- **Examples**:
  - Vulnerability discovered in non-critical system
  - Failed security control test
  - Policy violation detected
  - False positive in SIEM
- **Response Time**: Acknowledge within 4 hours
- **Containment SLA**: 24 hours
- **Escalation**: Security Manager
- **Communication**: Internal only (unless required by regulation)

### Low (Severity 4)
- **Definition**: Informational security event
- **Examples**:
  - Suspicious but benign activity
  - Security awareness finding
  - Audit log anomaly
- **Response Time**: Acknowledge within 1 business day
- **Investigation SLA**: 1 week
- **Escalation**: Security team

## Incident Detection and Reporting

### Detection Mechanisms

**Automated Monitoring**:
- SIEM alerting (Elasticsearch/Splunk)
  - Failed login attempts > 5/min per IP
  - Unusual API access patterns
  - Database query anomalies
  - File system integrity changes
- IDS/IPS (Snort/Suricata)
  - Network intrusion attempts
  - DDoS patterns
  - Suspicious payload detection
- WAF (AWS WAF)
  - SQL injection attempts
  - XSS payloads
  - Rate limiting violations
- Vault monitoring
  - Unexpected key access
  - Auth token abuse
  - Unseal key access

**Manual Detection**:
- Customer reports
- Employee observation
- Third-party notification (vendor breach)
- Security researcher disclosure

### Incident Reporting Channels

**Internal**:
- Security email: security@openfireblocks.com
- Slack: #security-incidents (private channel)
- On-call pager: PagerDuty

**External**:
- Security.txt: https://openfireblocks.com/.well-known/security.txt
- Bug bounty: HackerOne program
- Responsible disclosure: security-disclosure@openfireblocks.com

### Incident Reporting Form

```
INCIDENT REPORT

Reporter: ___________________
Date/Time: ___________________
Channel: ___________________

Incident Title: _________________________________________________

Description:
_________________________________________________________________
_________________________________________________________________

Severity Assessment (self):  [ ] Critical  [ ] High  [ ] Medium  [ ] Low

Affected Systems:
[ ] API Gateway    [ ] MPC Signer    [ ] PostgreSQL    [ ] Vault
[ ] Temporal       [ ] Networks      [ ] Customer Data [ ] Other: _____

Evidence/Logs:
- Location: _________________________
- Attachments: _____________________

Contact for Questions: _________________ Phone: _________________

Initial Action Taken: _______________________________________________
```

## Incident Response Process

### Phase 1: Detection and Triage (0-1 hour)

**1.1 Severity Assessment**
- Is customer data affected? (Yes = Severity 1-2)
- Is system availability affected? (Yes = Severity 1-2)
- Duration of impact? (> 1 hour = higher severity)
- Scope: How many customers affected?

**1.2 Initial Response**
```
Severity 1: Page on-call engineer + manager + CISO (immediately)
Severity 2: Page on-call engineer + manager (within 15 min)
Severity 3: Create ticket, assign to security team
Severity 4: Log in tracking system
```

**1.3 Incident Creation**
- Assign incident ID (INC-YYYY-MM-DD-NNN)
- Create incident war room (Slack + Zoom)
- Establish incident log and timeline
- Assign roles (Incident Commander, Security Lead, Ops Lead)

**1.4 Initial Containment**
- For Severity 1: Consider isolating affected system
- For Severity 1: Disable compromised accounts
- For Severity 1: Preserve evidence (do NOT delete logs)
- For Severity 2-4: Assess need for isolation

### Phase 2: Investigation (0-4 hours)

**2.1 Gather Evidence**
```
Data Collection:
- SIEM logs (48-hour window around incident)
- IDS/IPS alerts
- WAF logs
- Database audit logs
- Application logs
- Network flow data
- Filesystem timestamps
- Memory dumps (if malware suspected)
- Cloud metadata (AWS CloudTrail, VPC Flow Logs)
```

**2.2 Timeline Reconstruction**
- When did unusual activity start?
- When was it first detected?
- What systems/data were accessed?
- What actions were taken?

**2.3 Impact Assessment**
- How many customers affected?
- What data types were accessed?
- Was data exfiltrated or just accessed?
- Duration of unauthorized access?

**Example Investigation Timeline**:
```
2026-06-30 14:20:00 - Suspicious login detected (SIEM alert)
2026-06-30 14:45:00 - Incident reported by monitoring
2026-06-30 15:00:00 - IR team assembled
2026-06-30 15:15:00 - Compromised account disabled
2026-06-30 15:30:00 - Database query logs reviewed
  → Found 523 unauthorized SELECT queries
  → Accessed customer_keys table (CRITICAL)
  → Data: 12 customer key shares for 4 customers
2026-06-30 16:00:00 - Network forensics shows IP: 192.0.2.100 (VPN user)
2026-06-30 16:15:00 - Identify user: contractor, access revoked 2 weeks ago
2026-06-30 16:30:00 - Root cause: Failed offboarding (Vault token not revoked)
```

### Phase 3: Containment (0-4 hours)

**3.1 Short-Term Containment**
- Revoke compromised credentials immediately
- Isolate affected systems (if still active threat)
- Block attacker IP address
- Kill active sessions

**3.2 Medium-Term Containment**
- Reset passwords for affected accounts
- Rotate encryption keys (if compromised)
- Force re-authentication for all sessions
- Deploy security patches (if vulnerability)

**3.3 Eradication (Severity 1-2 Only)**
- Remove malware/backdoors
- Patch vulnerabilities
- Update security rules
- Close unauthorized access points

**Containment Actions Checklist**:
```
[ ] Disable compromised user accounts
[ ] Revoke API keys/tokens
[ ] Force password reset for related accounts
[ ] Rotate customer encryption keys
[ ] Isolation of affected system
[ ] Block attacker IP address
[ ] Patch application vulnerability
[ ] Update firewall rules
[ ] Restart affected services
[ ] Verify containment (re-test attacker access)
```

### Phase 4: Recovery (varies by incident)

**4.1 System Restoration**
- Restore from known-good backup
- Rebuild systems if infected
- Reapply patches and hardening
- Verify integrity of restored data

**4.2 Validation**
- Run integrity checks (checksums, row counts)
- Verify no backdoors remain
- Confirm monitoring is working
- Test incident response: Can we detect again?

**4.3 Return to Normal**
- Restore normal operations (if disrupted)
- Monitor closely for 24 hours
- Verify customer functionality
- Resume normal staffing

### Phase 5: Post-Incident Actions (24-72 hours)

**5.1 Notification**
- **Customers** (if data accessed): Notification within 72 hours
- **Regulators** (GDPR/other): As required by regulation (72 hours for GDPR)
- **Board**: Severity 1-2 incidents within 24 hours
- **Insurance**: Notify cyber insurance provider

**5.2 Forensic Investigation**
- Deep-dive forensics (if criminal activity suspected)
- Preserve evidence for law enforcement
- Chain of custody documentation
- Professional forensic firm engagement

**5.3 Post-Incident Review**
- Conduct review meeting (48-72 hours after containment)
- Document findings and lessons learned
- Identify preventive measures
- Update incident response plan
- Assign improvement items with owners

**Post-Incident Review Template**:
```
Incident ID: INC-2026-06-30-001
Title: Unauthorized Database Access via Compromised Contractor Account
Date: 2026-07-01

Timeline:
- 14:20 - Suspicious login detected
- 15:00 - IR team activated
- 15:15 - Account disabled
- 16:30 - Root cause identified
- 18:00 - Containment complete
- TOTAL RESPONSE TIME: 3 hours 40 minutes ✓ (met 4-hour SLA)

Root Cause Analysis:
1. Contractor offboarded 2 weeks prior
2. VPN access disabled ✓
3. Application access removed ✓
4. BUT: Vault auth token not revoked ✗
5. Contractor used old VPN credentials from backup
6. Vault token still had database access permission

Lessons Learned:
1. Need automated Vault token revocation on offboarding
2. Need monitoring for old VPN credential use
3. Need regular Vault token rotation (implement 90-day policy)

Improvement Items:
[ ] Implement Vault token auto-revocation on contractor offboarding
[ ] Add SIEM rule: Alert on VPN access from revoked users
[ ] Implement Vault token rotation policy (90 days)
[ ] Audit all active Vault tokens, revoke old ones
[ ] Update offboarding checklist to include Vault

Owner: VP of Security
Target Date: 2026-08-30
```

## Specialized Incident Playbooks

### Data Breach Response

**Initial Actions**:
1. [ ] Determine scope: What data? How many records?
2. [ ] Is personal data (PII/PHI) involved? (Triggers GDPR/CCPA)
3. [ ] Is customer cryptographic material involved? (CRITICAL)
4. [ ] Disable attacker access
5. [ ] Secure evidence

**Customer Notification**:
- Timeframe: Within 72 hours (GDPR) or per state law
- Content: What happened, what data, what to do
- Method: Email + phone for critical customers
- Include evidence of steps taken to secure data

**Regulatory Notification**:
- GDPR: Notify supervisory authority within 72 hours
- CCPA: Notify California AG
- State laws: As required by jurisdiction
- SEC: If public company and material breach

**Example Notification**:
```
Subject: Data Security Incident Notice - Immediate Action Required

Dear Valued Customer,

We are writing to inform you of a security incident that may have affected
your account.

What Happened:
On June 30, 2026, we detected unauthorized access to our database. An 
individual with previously revoked access used old credentials to access
our systems.

What Information Was Affected:
- Your name and email address
- The encrypted key share associated with your custody account
- No customer funds were moved or accessed

What We Did:
- Immediately revoked the attacker's access (15 minutes)
- Disabled all related accounts
- Secured all encryption keys
- Notified law enforcement
- Engaged forensic investigators

What You Should Do:
1. Change your OpenFireblocks password immediately
2. Enable MFA if not already enabled
3. Monitor your account for unusual activity
4. Contact us with any questions: security@openfireblocks.com

We take security very seriously and deeply apologize for this incident.
We have implemented additional safeguards to prevent similar incidents.

Sincerely,
OpenFireblocks Security Team
```

### Ransomware Response

**Immediate Actions (First 30 minutes)**:
```
[ ] DO NOT pay ransom (under no circumstances)
[ ] Isolate affected systems from network
[ ] Take snapshots of current state (for forensics)
[ ] Preserve attacker communication
[ ] Notify IR team and law enforcement
[ ] Document system state before any action
[ ] DO NOT disconnect backups (separate from main network)
```

**Investigation Phase**:
```
[ ] Identify malware type/strain
[ ] Understand propagation method
[ ] Identify patient zero (where it entered)
[ ] Assess scope of infection
[ ] Identify attack path through network
[ ] Collect memory dump (if still running)
[ ] Preserve logs from last 30 days
```

**Recovery Phase**:
```
[ ] Restore from clean backup (BEFORE infection date)
[ ] Rebuild affected systems
[ ] Patch vulnerabilities
[ ] Update security controls
[ ] Restore data incrementally with monitoring
[ ] Verify no malware in restored systems
[ ] Continue monitoring for 30 days
```

### Denial of Service (DDoS) Response

**Attack Identification**:
- MTTD target: < 5 minutes (automated detection)
- Indicators:
  - Sudden traffic spike (> 10x normal)
  - Single source IP or small set
  - Specific resource under attack
  - Normal traffic excluded (legitimate users can still access)

**Immediate Response (0-15 minutes)**:
```
[ ] Confirm DDoS attack (not legitimate traffic spike)
[ ] Activate DDoS response plan
[ ] Engage CDN DDoS protection (AWS Shield/CloudFlare)
[ ] Activate rate limiting rules
[ ] Block attacking IP ranges
[ ] Notify customers (status page update)
[ ] Activate DDoS mitigation service if needed
```

**Ongoing Response (15 minutes - 4 hours)**:
```
[ ] Monitor attack progression
[ ] Adjust filtering rules
[ ] Load balance across regions
[ ] Collect attack signatures
[ ] Communicate to customers every 30 minutes
[ ] Prepare public statement if > 1 hour duration
```

**Post-Attack (After attack stops)**:
```
[ ] Analyze attack patterns
[ ] Improve DDoS defenses
[ ] Update incident response procedures
[ ] Conduct post-incident review
[ ] Update insurance documentation
```

## Compliance Obligations

### GDPR Breach Notification (Articles 33-34)

**Timeline**:
- Must notify supervisory authority: Within 72 hours of discovery
- Must notify data subjects: Without undue delay

**Content**:
- Name and contact point of Data Protection Officer
- Description of breach and likely consequences
- Measures taken or proposed to address breach
- Likely impact on individuals

**Documentation**:
- Date/time of discovery
- Description of facts
- Likely consequence of breach
- Recovery measures taken

### PCI-DSS Breach Notification

**Timeline**:
- Contact payment processors: Within 30 days
- Contact customers: Within 30 days
- Document incident: All evidence preserved

### State Law Notification (varies)

**California (CCPA)**:
- Notify California Attorney General if > 500 residents affected
- Notify affected residents without unreasonable delay

**New York (GenCyber)**:
- Notify affected individuals within most expedient time possible
- Notify NY State if personal data of NY residents

**Other states**: 
- Varying timelines (typically 30-60 days)

## Incident Response Metrics

### Key Performance Indicators

**Detection**: Mean Time To Detect (MTTD)
- Target: < 4 hours
- Measurement: (Report Time - Incident Start) / number of incidents
- Improvement: Enhanced monitoring, better alerting

**Response**: Mean Time To Respond (MTTR)
- Target: < 1 hour (critical), < 4 hours (high)
- Measurement: (Containment Time - Report Time) / number of incidents
- Improvement: Better runbooks, faster escalation

**Containment**: Containment Time
- Target: < 4 hours for critical incidents
- Measurement: Time from detection to attacker access revoked

**Recovery**: Recovery Time Objective (RTO)
- Target: ≤ 4 hours
- Measurement: Time from incident to normal operations restored

## Incident Response Testing

### Tabletop Exercises (Quarterly)
- Simulated incident scenario
- Walk through response procedures
- Identify gaps and improvements
- Duration: 2-3 hours
- Participants: IR team, management, select staff

### Simulated Breach Drills (Semi-Annual)
- Realistic incident simulation
- Execute actual response procedures
- Measure MTTD, MTTR, containment time
- Identify bottlenecks
- Document lessons learned

### Red Team Exercises (Annual)
- External team simulates attacker
- Tests actual detection and response
- Measures time from breach to detection
- Identifies blind spots in monitoring
- Provides concrete improvement recommendations

## Incident Response Training

### Mandatory Training
- All staff: Incident reporting basics (annual)
- IT staff: Incident response procedures (biannual)
- IR team: Advanced incident response (quarterly)
- Leadership: Executive briefing (annual)

### Role-Specific Training
- **Security Engineers**: Forensics, malware analysis
- **System Administrators**: System recovery procedures
- **Database Administrators**: Database forensics, recovery
- **Legal**: GDPR/CCPA notification requirements
- **PR/Communications**: Customer notification procedures

---

**Owner**: Chief Information Security Officer
**Last Updated**: 2026-06-30
**Next Review**: 2026-09-30
**Last Tested**: 2026-06-15 (Tabletop Exercise)
**Next Test**: 2026-09-15 (Simulated Breach Drill)
