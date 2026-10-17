# 05 Information security objectives

ISO/IEC 27001:2022 clauses 6.2 and 9.1. SOC 2 CC3.1.
Version 0.1, draft. Owner: ISMS owner. Approver: CEO. Period: October 2026 to September 2027.

Each objective has a measure, a target, an owner, and a place where the measure is recorded. The
measures are chosen so a record exists without extra work wherever possible.

| # | Objective | Measure | Target | Owner | Recorded in | Frequency |
|---|---|---|---|---|---|---|
| O-1 | No person can move funds alone | Transfers signed without the required approvals | 0 | Eng lead | Approval records; live-DB tests in CI | Every CI run |
| O-2 | Access is reviewed and kept to what is needed | Quarterly access reviews completed on time (company systems and each staging organisation) | 4 of 4 | ISMS owner | `records/ACCESS-REVIEW-COMPANY.md`; product access reviews | Quarterly |
| O-3 | Leavers lose access quickly | Accounts removed within 1 working day of leaving | 100% | ISMS owner | `records/JOINER-MOVER-LEAVER.md` | Each leaver |
| O-4 | Vulnerabilities are fixed on time | Critical fixed within 7 days, High within 30 days, from discovery | 100% | Eng lead | CI audit gate, `docs/security/accepted-vulnerabilities.json`, corrective-action log | Monthly |
| O-5 | Everyone is trained | Staff who completed security awareness training this year | 100% | ISMS owner | `records/TRAINING-LOG.md` / Sprinto | Yearly |
| O-6 | We can recover | DR test with key recovery passes within the stated RTO | 1 pass per year | Eng lead | `exercises/DR-TEST.md` | Yearly |
| O-7 | We can respond | Tabletop exercise held and its actions closed | 1 per year, actions closed within 60 days | Incident lead | `exercises/TABLETOP.md` | Yearly |
| O-8 | Company accounts are configured securely | AWS and GitHub check scripts report no FAIL | 0 FAIL for 3 months in a row | Eng lead | Check script outputs in Sprinto or `docs/evidence/` | Monthly |
| O-9 | Independent testing | Human-led penetration test, findings High and above fixed | 1 per year, 100% fixed | CEO | Pentest report; corrective-action log | Yearly |
| O-10 | Privacy duties are met | Data-subject requests answered within the legal time limit (30 days POPIA/PAIA, one month GDPR) | 100% | Information Officer | `privacy/DSR-PROCEDURE.md` log | Each request |

Progress is reported at the management review (`07-management-review.md`).
