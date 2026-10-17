# Phase 3: Bank-Grade Hardening

## Overview

Phase 3 transforms OpenFireblocks from a feature-complete MVP into a production-grade platform trusted by financial institutions. Focuses on:
- **External security audits** (cryptography, penetration testing, code review)
- **SOC 2 Type II compliance** (security controls, audit trails, incident response)
- **ISO 27001 compliance** (information security management system)
- **Regulatory reporting** (AML/KYC, OFAC screening)
- **Multi-region HA** (disaster recovery, failover, replication)

---

## Part 1: External Security Audits

### 1.1 Cryptography Audit

**Scope:** MPC threshold signing, key generation, signing protocol, zero-knowledge proofs

**Timeline:** 4-6 weeks

**Auditors:** Tier-1 firms specializing in threshold cryptography
- Examples: Trail of Bits, Zellic, OpenZeppelin

**Audit Checklist:**
- [ ] DKG protocol correctness (k-of-n threshold properties)
- [ ] Signature non-malleability (no key recovery attacks)
- [ ] Key share secret sharing (Shamir's secret sharing properties)
- [ ] Zero-knowledge proof verification (TSS-Lib implementations)
- [ ] Entropy sources (random number generation)
- [ ] Side-channel resistance (no timing leaks from key material)

**Deliverables:**
- Detailed audit report with findings and severity levels
- Remediation roadmap
- Repeat audit post-remediation (optional)

### 1.2 Penetration Testing

**Scope:** API, authentication, policy engine, ceremony transport, Vault integration

**Timeline:** 3-4 weeks

**Testers:** Experienced pentest firms (e.g., Bishop Fox, Nettitude)

**Attack Vectors:**
- [ ] Authentication bypass (API key, mTLS, JWT)
- [ ] Authorization flaws (multi-tenancy isolation)
- [ ] Transaction replay (cross-chain, nonce issues)
- [ ] Policy engine circumvention (amount/whitelist/geo)
- [ ] Ceremony transport interception (MITM, message injection)
- [ ] Vault access control (key share exfiltration)
- [ ] Database injection (PostgreSQL, immudb)
- [ ] Denial of service (rate limiting, resource exhaustion)
- [ ] Privilege escalation (customer → admin)

**Deliverables:**
- Pentest report with vulnerability severity matrix
- Proof-of-concept exploits
- Remediation plan + timeline

### 1.3 Code Audit

**Scope:** Go services (mpc-signer, ceremony-orchestrator, policy-service, temporal-worker), NestJS api-gateway, SDKs

**Timeline:** 3-4 weeks

**Focus Areas:**
- [ ] Cryptographic library usage (proper key handling, no hardcoded secrets)
- [ ] Error handling (no information leakage in error messages)
- [ ] Concurrency safety (race conditions, data races)
- [ ] Input validation (injection attacks, boundary conditions)
- [ ] Memory safety (buffer overflows, use-after-free)
- [ ] Dependency security (known CVEs in transitive dependencies)
- [ ] API design (security by default, principle of least privilege)

**Deliverables:**
- Code audit report with findings categorized by severity
- Recommended remediations
- Secure coding guidelines for the team

---

## Part 2: SOC 2 Type II Compliance

### 2.1 Trust Service Criteria (AICPA)

OpenFireblocks targets compliance with the following TSC categories:

#### CC (Common Criteria) — Security
- **CC6.1:** Logical access controls protect data and information assets
  - MFA for admin accounts
  - API key rotation policies
  - mTLS for inter-service communication
  - Vault-backed secret management

- **CC6.2:** Prior to issuing system credentials, the entity identifies, authenticates, and provisions the user
  - Customer onboarding workflow
  - API key generation with rate limiting
  - Audit logging of credential issuance

- **CC6.3:** Access to assets is protected from unauthorized internal and external users
  - Multi-tenancy isolation
  - PostgreSQL row-level security (RLS)
  - OPA policy enforcement
  - RBAC (role-based access control)

#### A1 (Availability) — The system is available for operation
- **A1.1:** System availability targets (SLO: p99 < 500ms)
  - Prometheus + Grafana SLIs
  - Auto-scaling policies
  - Load balancer health checks
  - Failover to replica instances

- **A1.2:** Incidents are prevented, detected, and recovered
  - Incident response runbook
  - Automated alerting (Prometheus)
  - Chaos engineering tests
  - Disaster recovery drills

#### C1 (Confidentiality) — Assets are protected from unauthorized disclosure
- **C1.1:** Data in transit is encrypted
  - TLS 1.3 for all APIs
  - mTLS for inter-service communication
  - Encrypted DB connections (SSL mode `require`)

- **C1.2:** Data at rest is encrypted
  - Vault encryption for key material
  - PostgreSQL `pgcrypto` for sensitive columns (salted hashes)
  - Disk-level encryption (Linux dm-crypt)
  - Immutable audit log (immudb) with encryption

#### I1 (Integrity) — Data is protected from unauthorized modification
- **I1.1:** Unauthorized change is prevented, detected, and corrected
  - Database constraints (NOT NULL, UNIQUE, FOREIGN KEY)
  - Audit trails (PostgreSQL + immudb)
  - Change approval workflow (Temporal settlements)
  - Transaction signatures (immutable proof)

### 2.2 Controls Implementation

#### Control 1: MFA for Admin Accounts
```yaml
# Deployment: All admin endpoints require MFA
POST /admin/ceremonies
Authorization: Bearer <jwt>
X-MFA-Token: <TOTP or WebAuthn>
```

#### Control 2: API Key Rotation
```sql
-- Automatic key expiration
ALTER TABLE api_keys ADD COLUMN expires_at TIMESTAMP;
ALTER TABLE api_keys ADD COLUMN rotated_at TIMESTAMP;
ALTER TABLE api_keys ADD COLUMN previous_key_hash VARCHAR;  -- audit trail

-- Rotation policy: keys expire every 90 days
-- Customers notified 30 days before expiration
```

#### Control 3: Database Encryption
```sql
-- PostgreSQL encrypted columns (sensitive PII)
ALTER TABLE api_keys ADD COLUMN key_hash VARCHAR;  -- salted bcrypt
ALTER TABLE ceremonies ADD COLUMN metadata_encrypted BYTEA;  -- AES-256 + Vault key

-- immudb for audit trail (already immutable + can add encryption)
```

#### Control 4: Network Segmentation
```yaml
# Kubernetes NetworkPolicy: party services isolated from public API
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: party-isolation
spec:
  podSelector:
    matchLabels:
      app: mpc-party
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: ceremony-orchestrator
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: vault
```

#### Control 5: Audit Logging
```sql
-- Comprehensive audit trail
CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  timestamp TIMESTAMP DEFAULT NOW(),
  user_id UUID,
  action VARCHAR,  -- CREATE_API_KEY, DELETE_CEREMONY, SIGN_REQUEST, etc.
  resource_type VARCHAR,  -- customer, ceremony, transaction, etc.
  resource_id UUID,
  details JSONB,
  ip_address INET,
  status VARCHAR,  -- success, failure
  error_message TEXT
);

-- Immutable version in immudb (same schema, write-once)
```

### 2.3 SOC 2 Audit Timeline

**Months 1-2:** Evidence gathering and control documentation
- Document all security controls
- Collect logs and audit trails
- Test control effectiveness

**Months 3-6:** Observation period
- SOC 2 auditor observes controls in operation
- Monthly attestations of control effectiveness
- Remediation of any audit findings

**Month 7:** Report issuance
- SOC 2 Type II report (passable for 1 year, then renewed)
- Shared with customers under NDA

---

## Part 3: ISO 27001 Compliance

### 3.1 Information Security Management System (ISMS)

ISO 27001 requires a documented ISMS with policies, procedures, and evidence. OpenFireblocks targets:

#### A1: Policies
- Information Security Policy (governance, roles, responsibilities)
- Access Control Policy (authentication, authorization, privilege management)
- Cryptography Policy (key management, algorithms, key rotation)
- Incident Management Policy (detection, response, recovery)
- Vendor Management Policy (third-party risk assessment)
- Business Continuity Policy (RTO/RPO, failover procedures)

#### A2: Organization
- Information Security Officer (ISO) role appointed
- Security steering committee (monthly meetings)
- Risk assessment team (quarterly reviews)
- Incident response team (24/7 on-call)

#### A3: Asset Management
- Asset inventory (servers, databases, applications, data)
- Classification (public, internal, confidential, restricted)
- Ownership and custodianship documented
- Lifecycle management (creation → retention → deletion)

#### A4: Access Control
- User identity management (authentication via Vault/OAuth2)
- Access provisioning workflow (request → approval → grant)
- Privilege management (least privilege, role-based)
- Access reviews (quarterly audit of active privileges)
- Password policy (minimum entropy, rotation, history)
- Multi-factor authentication (MFA) for admin/sensitive ops

#### A5: Cryptography
- Encryption at rest (Vault, pgcrypto, dm-crypt)
- Encryption in transit (TLS 1.3, mTLS)
- Key management (generation, storage, rotation, revocation)
- Algorithm selection (secp256k1 for ECDSA, AES-256, SHA-256)
- Key destruction procedures (secure wiping via Vault)

#### A6: Physical and Environmental Security
- Data center access controls (badge, biometric)
- Network segmentation (firewall, VPC, private subnets)
- Monitoring (CCTV, intrusion detection)
- Environmental controls (temperature, humidity)
- Disaster recovery facility (geographically separate, same security)

#### A7: Operations Security
- Change management (code review, testing, approval)
- Incident management (log, investigate, remediate, review)
- System monitoring (Prometheus, Grafana, log aggregation)
- Backup and restoration (daily backups, tested recovery)
- Disposal of information (secure deletion, certificates of disposal)

#### A8: Communications Security
- Network architecture (firewalls, DMZ, private networks)
- Message authentication (digital signatures, TLS)
- Access control to network resources (VPN, bastion hosts)

#### A9: System Acquisition, Development and Maintenance
- Secure development (SSDLC, threat modeling, code review)
- Change control (version control, change log, approval workflow)
- Testing (unit, integration, security, load testing)
- Third-party software (CVE scanning, SCA tools)

#### A10: Supplier Relationships
- Vendor assessments (security questionnaire, audit rights)
- Contracts with security clauses (data protection, incident notification)
- Monitoring (quarterly reviews, CVE alerts)

#### A11: Information Security Incident Management
- Incident reporting (24/7 hotline, escalation)
- Investigation (root cause analysis, forensics)
- Response (containment, eradication, recovery)
- Post-incident review (lessons learned, process improvements)

#### A12: Business Continuity Management
- RTO: 4 hours (recovery time objective)
- RPO: 1 hour (recovery point objective)
- Failover automation (cross-region replication, DNS failover)
- Disaster recovery drills (quarterly, documented results)

#### A13: Compliance
- Legal/regulatory obligations (AML/KYC, OFAC, data privacy)
- Audit trail (immutable logs)
- Right to audit (customer contracts include audit clauses)

### 3.2 Implementation Roadmap

| Control | Status | Effort | Timeline |
|---------|--------|--------|----------|
| Information Security Policy | Planned | 2 weeks | Month 1 |
| ISO role appointment | Planned | 1 day | Month 1 |
| Asset inventory | Planned | 3 weeks | Month 1 |
| Access control procedures | Partial (existing MFA/RBAC) | 2 weeks | Month 1 |
| Cryptography policy | Partial (using Vault) | 1 week | Month 1 |
| Change management workflow | Partial (git-based) | 2 weeks | Month 2 |
| Incident response plan | Planned | 2 weeks | Month 2 |
| Business continuity plan | Planned | 3 weeks | Month 2 |
| Vendor assessments | Planned | 4 weeks | Month 2-3 |
| Audit trail review | Partial (immudb exists) | 1 week | Month 3 |
| Internal audit | Planned | 4 weeks | Month 3 |
| Management review | Recurring | 1 week | Monthly |

---

## Part 4: Regulatory Compliance

### 4.1 AML/KYC (Anti-Money Laundering / Know Your Customer)

**Implementation:**
- Customer identity verification (tier 1: name, email; tier 2: KYC document)
- Transaction monitoring (suspicious activity detection)
- OFAC screening (US Office of Foreign Assets Control)
- Reporting obligations (SARs: Suspicious Activity Reports)

**API Integration:**
```go
// policy-service: AML/KYC checks
type AMLPolicy struct {
  CustomerTier int  // 1: email-only, 2: KYC verified, 3: institutional
  RiskScore float64  // 0-100 (higher = riskier)
  SuspiciousPatterns []string
}

func (p *AMLPolicy) CheckTransaction(tx *Transaction) error {
  if tx.Amount > p.TierLimit() {
    return fmt.Errorf("amount exceeds tier limit")
  }
  ofacResult := ofacScreening(tx.Destination)
  if ofacResult.Blocked {
    return fmt.Errorf("destination blocked by OFAC")
  }
  return nil
}
```

### 4.2 Data Privacy (GDPR, CCPA)

**Implementation:**
- Data minimization (collect only necessary info)
- Right to erasure ("right to be forgotten")
- Data breach notification (72 hours)
- Privacy by design (encryption, pseudonymization)

**Controls:**
```sql
-- GDPR: Right to erasure
ALTER TABLE customers ADD COLUMN deleted_at TIMESTAMP;
-- Soft delete; actual data wiped after retention period
TRIGGER on_customer_delete EXECUTE FUNCTION wipe_pii();

-- CCPA: Opt-out tracking
ALTER TABLE customers ADD COLUMN data_sale_opt_out BOOLEAN;
```

---

## Part 5: Deployment & Operations (Phase 3)

### 5.1 Multi-Region HA

**Architecture:**
```
┌─────────────────────────────────────────────────────────┐
│ Global Load Balancer (GeoDNS)                           │
├─────────────────────────────────────────────────────────┤
│  Region 1 (US-East)    │  Region 2 (EU-West)           │
│  ┌──────────────────┐   │  ┌──────────────────┐         │
│  │ api-gateway      │   │  │ api-gateway      │         │
│  │ mpc-signer       │   │  │ mpc-signer       │         │
│  │ policy-service   │   │  │ policy-service   │         │
│  │ PostgreSQL Primary   │  │ PostgreSQL Replica        │
│  │ Vault Primary    │   │  │ Vault Replica    │         │
│  │ immudb Primary   │   │  │ immudb Replica   │         │
│  └──────────────────┘   │  └──────────────────┘         │
└─────────────────────────────────────────────────────────┘

RTO: 4 hours
RPO: 1 hour (async replication with monitoring)
```

### 5.2 Automated Failover

```yaml
# Kubernetes: Pod disruption budgets + cluster autoscaling
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: api-gateway-pdb
spec:
  minAvailable: 2
  selector:
    matchLabels:
      app: api-gateway

---
# Vault: Cross-region replication
# Primary (US-East) → Replica (EU-West)
# Automatic failover if primary unreachable > 60s
```

### 5.3 Monitoring & Alerting (Prometheus + Grafana)

**Key Metrics:**
- API latency (p50, p95, p99)
- Error rate (5xx, 4xx, policy rejection)
- Signing ceremony success rate
- Key share seal/unseal latency
- Broadcast transaction confirmation time
- Vault seal/unseal status
- Database replication lag
- Party service availability per ceremony

**Alerts:**
```yaml
- alert: HighSigningFailureRate
  expr: rate(signing_failures_total[5m]) > 0.05
  for: 5m
  annotations:
    summary: "High signing failure rate (>5%)"
    
- alert: VaultSealed
  expr: vault_core_unsealed == 0
  for: 1m
  annotations:
    summary: "Vault is sealed; customer ceremonies blocked"

- alert: DBReplicationLag
  expr: pg_replication_slot_lag_bytes > 1e9
  for: 10m
  annotations:
    summary: "DB replication lag > 1GB"
```

---

## Part 6: Documentation & Training

### 6.1 Compliance Documentation
- [ ] Security policies (10 documents, ~50 pages)
- [ ] Procedures and work instructions (8 documents, ~30 pages)
- [ ] Risk assessment report (~20 pages)
- [ ] ISMS statement of applicability (SOA)
- [ ] Internal audit reports (quarterly)
- [ ] Management review meeting minutes

### 6.2 Customer Documentation
- [ ] Security white paper
- [ ] Compliance attestations (SOC 2, ISO 27001)
- [ ] SLA and incident response procedures
- [ ] API security best practices
- [ ] Customer onboarding guide

### 6.3 Team Training
- [ ] Security fundamentals (annual, mandatory)
- [ ] Secure coding (developers, annual)
- [ ] Incident response exercises (quarterly)
- [ ] Social engineering awareness (annual)

---

## Part 7: Timeline & Milestones

| Milestone | Duration | Months |
|-----------|----------|--------|
| Evidence gathering + control documentation | 8 weeks | 1-2 |
| Remediation of pre-audit findings | 4 weeks | 2-3 |
| Cryptography audit | 6 weeks | 3-4 |
| Penetration testing | 4 weeks | 4-5 |
| Code audit | 4 weeks | 5-6 |
| SOC 2 observation period | 12 weeks | 6-9 |
| ISO 27001 internal audit | 4 weeks | 9-10 |
| SOC 2 report issuance | 2 weeks | 9 |
| ISO 27001 certification (external) | 4 weeks | 10-11 |
| Multi-region HA deployment | 6 weeks | 6-8 |
| Final security review | 2 weeks | 11 |

**Total: 6-8 months for full Phase 3 completion**

---

## References

- [SOC 2 Trust Service Criteria](https://www.aicpa.org/resources/download/trust-service-criteria)
- [ISO 27001:2022 Standard](https://www.iso.org/standard/27001)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)
- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [GDPR](https://ec.europa.eu/info/law/law-topic/data-protection_en)

