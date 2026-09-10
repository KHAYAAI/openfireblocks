# OpenFireblocks Security Documentation

## Executive Summary

OpenFireblocks is built with enterprise-grade security from the ground up. This document outlines our security architecture, compliance measures, and best practices.

---

## Security Architecture

### 1. Cryptographic Standards

- **Transport Security**: TLS 1.3 for all communications
- **Symmetric Encryption**: AES-256-GCM for sensitive data at rest
- **Key Management**: HashiCorp Vault with automatic rotation
- **Signing**: HMAC-SHA256 for webhook verification
- **Threshold Cryptography**: 4-of-7 ECDSA using Binance TSS-lib

### 2. Authentication & Authorization

#### Multi-Factor Authentication (MFA)
- TOTP (Time-based One-Time Password) via RFC 6238
- WebAuthn/FIDO2 support for hardware keys
- Fallback to passcode-based authentication
- Session timeout: 24 hours by default
- Automatic logout on suspicious activity

#### JWT Access Tokens
- Short-lived tokens (1 hour expiration)
- Refresh tokens valid for 30 days
- Token revocation on logout
- Permission scopes encoded in claims

#### API Keys
- 32-byte cryptographic random keys
- Bcrypt hashing with cost factor 12
- Per-endpoint rate limiting
- Automatic expiration support
- Comprehensive audit logging of usage

#### SSO Integration
- WorkOS OIDC/SAML support
- Automatic user provisioning
- Just-in-time (JIT) access control
- Centralized session management

### 3. Data Protection

#### Encryption at Rest
- Database: AES-256-GCM with per-customer keys
- S3: Server-side encryption with KMS
- Secrets Manager: Automatic encryption
- Backups: Encrypted with master key
- Archive: GLACIER with KMS key

#### Encryption in Transit
- TLS 1.3 mandatory on all public endpoints
- Certificate pinning recommended for mobile apps
- HSTS: max-age=31536000 with preload
- Perfect forward secrecy enabled

#### Key Rotation
- Signing keys: Monthly rotation
- API keys: Quarterly review
- Database encryption key: Annual rotation
- Master key: Never rotated (AWS managed)

### 4. Network Security

#### VPC Isolation
- Public subnet: ALB only
- Private subnet: ECS tasks + databases
- NAT Gateway: Controlled egress
- VPC Endpoints: AWS service access without internet

#### Security Groups
```
ALB Security Group:
  - Inbound: 80/443 from 0.0.0.0/0
  - Outbound: 8080 to ECS security group

ECS Security Group:
  - Inbound: 8080 from ALB, 9090 from monitoring
  - Inbound: Service discovery ports
  - Outbound: Database, Redis, external APIs

Database Security Group:
  - Inbound: 5432 from ECS security group only
  - Outbound: None required

Redis Security Group:
  - Inbound: 6379 from ECS security group only
  - Outbound: None required
```

#### WAF Rules
- SQL injection prevention
- XSS protection
- Rate limiting (1000 req/min per IP)
- Geographic blocking (if configured)
- Bot detection

### 5. Application Security

#### Input Validation
- Content-Type enforcement (application/json only)
- Payload size limits (10MB maximum)
- JSON schema validation on all inputs
- Sanitization of special characters
- Email format validation

#### Output Encoding
- JSON responses: UTF-8 with escaping
- HTML responses: Entity encoding
- SQL queries: Parameterized prepared statements
- Command execution: Disabled (no shell.exec)

#### CSRF Protection
- SameSite=Strict on all cookies
- CSRF token validation on state-changing operations
- Custom headers for API requests

#### XSS Prevention
- Content Security Policy (CSP): default-src 'self'
- X-XSS-Protection: 1; mode=block
- X-Content-Type-Options: nosniff
- No inline scripts in HTML

### 6. Access Control

#### Row-Level Security (RLS)
All customer data enforced at database level:
```sql
CREATE POLICY customer_isolation ON customers
  FOR ALL USING (customer_id IN (
    SELECT customer_id FROM customer_users 
    WHERE user_id = current_user_id
  ));
```

#### Role-Based Access Control (RBAC)
- **Admin**: Full platform access
- **Billing Admin**: User and billing management
- **User**: Key and signing operations
- **Viewer**: Read-only access

#### Attribute-Based Access Control (ABAC)
- Time-based restrictions (business hours only)
- IP-based restrictions (whitelist)
- Device-based restrictions (mobile only)
- Location-based restrictions (geographic)

### 7. Logging & Audit Trails

#### Immutable Audit Logs
- All data modifications logged
- Action, actor, timestamp, changes stored
- Replicated to separate storage
- Retention: 7 years (regulatory requirement)

#### Audit Events
- User login/logout with IP, device
- Key creation/rotation/compromise
- Transaction signing and approval
- Policy changes
- Compliance check results
- Failed authentication attempts

#### Log Integrity
- Append-only tables with immutable database
- Hash chain validation
- Regular backup verification
- Automated alerting on log tampering

### 8. Monitoring & Detection

#### Real-Time Alerts
- Failed login attempts (5+ in 15 min)
- Unusual geographic access
- Mass API key creation
- Elevated error rates (>1%)
- Database connection failures

#### Metrics
- Failed authentication rate
- API error rate (4xx, 5xx)
- Transaction success rate
- Policy rejection rate
- Compliance check failures

#### Incident Response
- Automated escalation to on-call
- Slack/email notifications
- PagerDuty integration
- Incident timeline tracking
- Post-incident analysis

---

## Compliance Requirements

### SOC 2 Type II

**Audited Controls**:
- CC6.1: Logical access control
- CC6.2: MFA implementation
- CC7.2: System monitoring
- CC7.3: Baseline security

**Audit Schedule**: Annual with 6-month interim

### PCI DSS Compliance

**Applicable if handling payment cards:**
- Requirement 2: Default configurations
- Requirement 3: Cryptography
- Requirement 6: Secure development
- Requirement 8: User identification
- Requirement 10: Logging and monitoring

**Scope**: Only non-card payment integrations (Stripe tokenized)

### GDPR Requirements

- **Data Processing Agreement (DPA)**: Required with customers
- **Privacy Notice**: Published on website
- **Data Subject Rights**: Implemented (access, deletion, portability)
- **Breach Notification**: 72-hour requirement
- **Data Impact Assessment (DPIA)**: Completed
- **Cross-Border Transfers**: Standard Contractual Clauses (SCCs)

### HIPAA (if applicable)

- Business Associate Agreement (BAA) available
- Encryption: AES-256 at rest, TLS 1.3 in transit
- Audit controls: Comprehensive logging
- Access controls: MFA, RBAC, RLS

---

## Secrets Management

### HashiCorp Vault Integration

#### Secret Paths
```
/customer/{customer_id}/
  ├── keys/signing/   (signing keys)
  ├── keys/crypto/    (encryption keys)
  ├── api_keys/       (API credentials)
  └── webhooks/       (webhook secrets)

/service/
  ├── database/credentials
  ├── redis/password
  ├── kms/master_key
  └── third_party_api/
      ├── onfido/key
      ├── stripe/key
      └── workos/key
```

#### Access Policies
```hcl
path "/customer/+/keys/*" {
  capabilities = ["read", "list"]
  min_wrapping_ttl = 1m
  max_wrapping_ttl = 1h
}

path "/customer/+/keys/signing/*" {
  capabilities = ["read", "create", "update"]
  require_mfa = true
}

path "/service/database/*" {
  capabilities = ["read"]
  identity {
    group_names = ["ecs_tasks"]
  }
}
```

#### Key Rotation
```
Signing Keys: Monthly
  - New key generated in Vault
  - Old key retired but kept for verification
  - Grace period: 7 days for transition

API Keys: Quarterly
  - Automatic expiration set
  - User must generate new key
  - Old key invalidated at expiration

Master Keys: Annual
  - Offline ceremony for HSM devices
  - Documented change control
  - Recovery procedures tested
```

---

## Security Checklist

### Pre-Deployment
- [ ] All dependencies scanned for vulnerabilities (Snyk)
- [ ] Security tests passing (SAST, DAST)
- [ ] Secrets not committed to repository
- [ ] SSL/TLS certificates valid and proper domain
- [ ] WAF rules configured and tested
- [ ] Security group rules reviewed by 2+ engineers
- [ ] Database backups encrypted and restorable
- [ ] Vault access policies reviewed

### Post-Deployment
- [ ] CloudWatch alarms configured and tested
- [ ] PagerDuty integration verified
- [ ] Incident response team notified
- [ ] Security hotline active
- [ ] Monitoring dashboard accessible
- [ ] Log ingestion confirmed
- [ ] Backup restoration tested

### Ongoing
- [ ] Weekly vulnerability scanning
- [ ] Monthly penetration testing (third-party)
- [ ] Quarterly security training
- [ ] Biannual disaster recovery drills
- [ ] Annual security audit (SOC 2)
- [ ] Compliance checklist review

---

## Incident Response

### Classification
1. **Critical**: Confirmed data breach, system down
2. **High**: Potential breach, degraded service
3. **Medium**: Policy violation, failed backup
4. **Low**: Suspicious activity, config drift

### Response Timeline
- **0 min**: Alert and acknowledge
- **15 min**: Initial investigation and containment
- **30 min**: Root cause identified
- **1 hour**: Mitigation deployed
- **4 hours**: Full resolution
- **24 hours**: Post-incident report

### Escalation
- Level 1: On-call engineer
- Level 2: Security team lead
- Level 3: Chief Security Officer
- Level 4: Executive team (data breach)

---

## Third-Party Security

### Vendor Assessment
- Security questionnaire (ISO 27001)
- SOC 2 report review
- Penetration testing results
- Financial stability check
- Support and SLA review

### Current Integrations
- **Onfido**: KYC verification (SOC 2 Type II certified)
- **Stripe**: Payment processing (PCI DSS Level 1)
- **WorkOS**: Enterprise SSO (SOC 2 Type II certified)
- **AWS**: Cloud infrastructure (SOC 2, ISO 27001, PCI DSS)

---

## References

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [CWE Top 25](https://cwe.mitre.org/top25/)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)
- [SOC 2 Trust Service Criteria](https://www.aicpa.org/resources/article/how-sox-affects-financial-professionals)
- [GDPR Compliance Guide](https://gdpr-info.eu/)

---

**Last Updated**: 2026-08-19
**Status**: Production Ready
**Review Cycle**: Quarterly
