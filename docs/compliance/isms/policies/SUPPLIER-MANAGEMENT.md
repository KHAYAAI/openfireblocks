# Supplier management policy

ISO/IEC 27001:2022 A.5.19–5.23. SOC 2 CC9.2. GDPR art. 28. POPIA s20–21, s72.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet approved.** Review: yearly.

Vendor details and history: `docs/security/vendor-risk-assessment.md`. Live register:
`records/VENDOR-REGISTER.md` (or Sprinto).

## 1. Before using a supplier

Before a new supplier gets company data or access:

1. Record the purpose, the data it will hold, and its class.
2. Rate it: **Critical** (could affect funds, source integrity or production), **High** (holds
   Confidential data), **Low** (neither).
3. For Critical and High: obtain its SOC 2 Type II or ISO 27001 certificate (and read the
   exceptions and complementary user entity controls), its DPA, and where its data is stored.
4. If it processes personal information: sign a DPA (operator agreement under POPIA s21) and,
   for data leaving South Africa or the EEA, transfer terms (`privacy/CROSS-BORDER-TRANSFERS.md`).
5. The CEO approves Critical suppliers; the ISMS owner approves the rest.

## 2. During the relationship

- Review Critical and High suppliers yearly: new report, incidents, changes in service or terms.
- Apply the controls the supplier's report expects of its customers (for example MFA on the AWS
  root account).
- Track supplier incidents that affect FORGE in the corrective-action log.

## 3. Ending the relationship

Remove access and integrations, export what is needed, obtain confirmation of deletion where
personal or Confidential data was held, and update the register.

## 4. Open-source and the software supply chain

Open-source components are suppliers of a kind. They are controlled by the secure development
policy (pinning, audit gates, SBOM) rather than by contracts.
