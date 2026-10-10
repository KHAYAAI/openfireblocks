# Vendor / Third-Party Risk Assessment

**Status**: Draft. Reflects vendors actually integrated in this codebase
(confirmed by grepping for their SDKs/API calls) plus vendors referenced in
documentation as planned. The two categories are labeled separately below —
do not present a "planned" integration to an auditor as "assessed and
approved," since it hasn't been assessed against a live integration.

**Owner**: [assign]
**Review cycle**: Annually per vendor, or on contract renewal, or
immediately on a vendor's own disclosed security incident.

---

## 1. Purpose

Tracks every third party with access to OpenFireblocks systems, data, or
that OpenFireblocks systems depend on for availability/integrity. Satisfies
SOC 2 CC9.2 (vendor risk management).

## 2. Assessment Criteria

For each vendor:
- **Data access**: what customer/company data does this vendor see or
  process?
- **Criticality**: does an outage of this vendor cause a customer-facing
  outage, degrade a security control, or is it non-critical?
- **Compliance posture**: does the vendor hold relevant certifications
  (SOC 2, ISO 27001, PCI DSS) for the service tier being used?
- **Contractual protection**: DPA (Data Processing Agreement) in place for
  any vendor touching personal data?

## 3. Vendor Inventory

### 3.1 Infrastructure (Confirmed — Live in Terraform/Code)

| Vendor | Service | Data Access | Criticality | Compliance |
|---|---|---|---|---|
| **AWS** | Compute (ECS), database (RDS), storage (S3), KMS, networking | Full — hosts all customer data and key material (encrypted) | Critical — total platform dependency | SOC 2 Type II, ISO 27001, PCI DSS Level 1 (AWS's own certifications; inherited, not OpenFireblocks') |

**Assessment**: AWS is the platform's foundational dependency. Risk is
managed via AWS's own compliance posture (very mature) plus OpenFireblocks'
own configuration of it (encryption at rest/in transit, IAM least
privilege, VPC isolation) — see `infrastructure/terraform/`. AWS DPA is a
standard clickthrough as part of the AWS Customer Agreement; confirm it's
been formally accepted for the account in use.

### 3.2 Application Dependencies (Confirmed — Live in Code)

| Vendor/Library | Purpose | Data Access | Criticality |
|---|---|---|---|
| **HashiCorp Vault** (self-hosted on AWS, not a HashiCorp Cloud service) | Secrets/key management | Key material, secrets — but self-hosted, so this is infrastructure OpenFireblocks operates, not a third party with independent access | Critical |
| **go-ethereum** (`ethclient`, `core/types`) | Ethereum transaction encoding/broadcast | None (library, not a service) | N/A — not a vendor, a dependency |
| **Bitcoin Core RPC** (via `services/settlement`) | Transaction broadcast | Whatever bitcoind endpoint is configured — could be self-hosted or a third-party RPC provider depending on `BITCOIN_RPC_URL` | Depends on deployment — **assess the specific provider once one is chosen; not yet configured with a real endpoint** |

**Note**: go-ethereum and similar open-source libraries are not "vendors" in
the risk-assessment sense (no ongoing data relationship), but any *hosted
RPC provider* used in production (Infura, Alchemy, QuickNode, or
self-hosted nodes) is a real vendor relationship and must be added to this
table once selected — it would see every transaction broadcast through it.

### 3.3 Planned Integrations (NOT Yet Live — Do Not Represent as Assessed)

These appear in service code as client wrappers but are not wired to real
credentials/endpoints, or the calls are stubbed to fail rather than
actually reach the vendor:

| Vendor | Planned Purpose | Status |
|---|---|---|
| **Onfido** | KYC/identity verification | Client code exists (`services/compliance/onfido_integration.go`) with real API call shapes; requires a real API key to operate. Not yet contracted/assessed as a live vendor relationship. |
| **Stripe** | Billing/payment processing | Client stub exists (`services/billing/billing.go`); `CreatePaymentIntent` deliberately fails rather than fabricate a call, since no real integration has been built or verified. **Do not treat as integrated.** |
| **A sanctions-screening provider** (OFAC/EU/UN lists — e.g. Chainalysis, ComplyAdvantage, TRM Labs) | Address/transaction sanctions screening | No provider selected. `services/compliance` and `services/policy` both fail closed (explicit error) rather than silently pass screening, specifically because no vendor is wired up yet. **This is a blocking gap for handling real customer funds**, not a nice-to-have — see the audit checklist's Go/No-go section. |
| **WorkOS** | Enterprise SSO | Referenced in earlier planning documentation; no live integration code as of this assessment. |

**For each of these**: before going live, this table must be updated with
the actual vendor selected, their compliance certifications, a signed DPA
(if they process personal data — Onfido certainly will), and a completed
assessment. Treat "we have client code for it" as zero risk-assessment
credit — the assessment is about the vendor relationship and data flow,
not the existence of an SDK wrapper.

## 4. Data Processing Agreements (DPA) Status

| Vendor | Processes Personal Data? | DPA Status |
|---|---|---|
| AWS | Yes (hosts all customer PII) | Standard AWS DPA — confirm formal acceptance |
| Onfido (when live) | Yes (identity documents, biometric data) | **Required before any real KYC traffic** — do not send real customer documents without one |
| Stripe (when live) | Yes (payment/billing details) | Required before live billing |
| Sanctions screening provider (when selected) | Likely (names, addresses) | Required |

## 5. Offboarding a Vendor

When a vendor relationship ends or is replaced:
1. Revoke API credentials immediately.
2. Confirm contractual data deletion obligations and request confirmation
   of deletion where applicable.
3. Remove the vendor's client code/config from production if fully
   decommissioned, or clearly mark it disabled if kept for reference.
4. Update this document.

## 6. Action Items

- [ ] Select and contract a sanctions-screening provider — currently the
      single highest-priority gap, since both `services/compliance` and
      `services/policy` are deliberately fail-closed (broken, not
      insecure) until one exists.
- [ ] Formally contract Onfido (or an alternative KYC provider) and execute
      a DPA before processing any real identity documents.
- [ ] Formally contract Stripe (or an alternative payment processor) before
      any live billing.
- [ ] Decide on and assess a Bitcoin/Ethereum RPC provider if not
      self-hosting nodes.
- [ ] Confirm AWS DPA acceptance for the production account.
- [ ] Re-run this assessment before each new vendor integration ships to
      production, not after.
