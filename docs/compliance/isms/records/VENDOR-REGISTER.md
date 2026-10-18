# Vendor register

ISO/IEC 27001:2022 A.5.19–5.22. SOC 2 CC9.2. GDPR art. 28. POPIA s20–21, s72.
Pre-filled on 11 October 2026 from the repository and known tools. **Assurance and DPA columns: to
obtain.** Detail and history are in `docs/security/vendor-risk-assessment.md`.

| Vendor | Service | Data held | Rating | Assurance to obtain | DPA / operator agreement | Data location | Owner | Last review |
|---|---|---|---|---|---|---|---|---|
| Amazon Web Services | Staging hosting (EC2/EKS, S3, KMS) | Staging data; test keys; backups | Critical | SOC 2 Type II, ISO 27001 (AWS Artifact) | AWS DPA (part of the service terms; accept in Artifact) | af-south-1 / eu-west-1 | Eng lead | [to complete] |
| GitHub (Microsoft) | Source code, CI (Actions), releases | Source code; CI secrets | Critical | SOC 2 Type II (GitHub trust portal) | GitHub DPA | US/global | Eng lead | |
| Google Workspace | Email, documents | Contacts, contracts, staff data | High | SOC 2 Type II, ISO 27001 | Cloud Data Processing Addendum (accept in admin console) | Global | CEO | |
| Sprinto | Compliance automation (planned) | Read access to cloud, code, HR; policies; evidence | High | SOC 2 Type II, ISO 27001 | Sprinto DPA | [to confirm] | ISMS owner | Not yet signed |
| Stripe | Billing (test mode) | Customer billing contacts | High when live | PCI DSS AOC, SOC 2 | Stripe DPA | Global | CEO | |
| WorkOS | SSO for the product (optional) | Customer users' identity data if used in hosting | High if hosted | SOC 2 Type II | WorkOS DPA | US | Eng lead | |
| Let's Encrypt (ISRG) | TLS certificates | None (hostnames only) | Low | Public CA audits (WebTrust) | Not needed | — | Eng lead | |
| Sigstore (Linux Foundation) | Keyless image signing, transparency log | Signing identities (CI workflow identity) are public by design | Low | Public project | Not needed | — | Eng lead | |
| Public RPC endpoints and testnet faucets | Development and staging chain access | Public chain data; IP addresses | Low (staging) / **Critical for production** (use own nodes or a contracted provider) | Provider's reports when contracted | When contracted | — | Eng lead | |
| TRISA network (Travel Rule) | Travel Rule message exchange | Travel Rule personal information (customer's data) | High when used | TRISA certificate authority and network rules | Customer is responsible party; FORGE operator only when hosting | Global | Product owner | |
| Anthropic (Claude Code) | AI coding assistant used in development | Source code and documentation in sessions; no secrets allowed | High | SOC 2 Type II (Anthropic trust portal) | Commercial terms / DPA | US | Eng lead | |
| DeepSeek (via Shannon pentest tool) | AI model used in automated pentests | Staging responses during tests | Medium | [to assess]; data may be processed in China | [to assess] | China | Eng lead | Consider a different model for future rounds |
| Background-check provider | Staff screening | Staff personal information (special category: criminal records) | High | [to choose] | Operator agreement required (POPIA s21) | South Africa | CEO | Not yet chosen |
| Penetration test firm | Human-led pentest | Staging access; findings | High | Firm's certifications (CREST, OSCP staff) | NDA + DPA | [to choose] | CEO | Not yet chosen |
