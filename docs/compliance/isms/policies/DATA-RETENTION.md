# Data retention and disposal policy

ISO/IEC 27001:2022 A.5.33, A.7.14, A.8.10. SOC 2 C1.2, P4.2. POPIA s14. GDPR art. 5(1)(e).
Version 0.1, draft, 11 October 2026. Owner: Information Officer. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. Principle

Keep information only as long as there is a purpose or a legal duty, then delete it securely.

## 2. Schedule

| Information | Retention | Reason | How disposed |
|---|---|---|---|
| Outreach contacts (prospects) | Until they object, or 2 years after last contact | Legitimate interest; POPIA s69 | Delete from Gmail/CRM; keep the address on the opt-out list only |
| Customer contracts and correspondence | 5 years after the contract ends | Contract, tax, limitation periods | Delete; destroy paper copies |
| Staff records | Duration of employment + 5 years (tax and labour records as law requires) | BCEA, tax law | Delete |
| Background-check results | Until the hiring decision + 6 months, then a pass/fail note only | POPIA minimisation | Delete the report |
| Security logs (company systems) | At least 1 year online | Investigation, SOC 2 | Automatic expiry |
| ISMS records (audits, reviews, training, access reviews) | 5 years | Certification evidence | Delete |
| Product records in a FORGE-hosted environment (transfers, approvals, Travel Rule) | 5 years from the transaction or end of relationship, or as the customer instructs | FIC Act s23 (customer's duty); customer contract | Per the customer's instruction at contract end |
| Retired key shares (staging) | 30 days after rotation (`ShareRetention`), then deleted | Recovery window | Vault soft-delete then destroy |
| Backups | [to complete: retention period; the chart sets no expiry today] | Recovery | Expiry; off-site copies encrypted |

Product records in **customer-operated** installations follow the customer's own policy. The
product's immutable tables keep records until the customer's own retention process removes them
through the database owner role, a deliberate step outside the application.

## 3. Legal hold

If an investigation, dispute or regulator request is expected, the CEO may suspend disposal of the
relevant information. The hold is recorded and lifted in writing.

## 4. Secure disposal of equipment

Laptops and drives are wiped (built-in cryptographic erase) before re-use or disposal, or
physically destroyed. HSMs are zeroised according to the vendor's procedure. Each disposal is
recorded in the asset inventory with the date and method.
