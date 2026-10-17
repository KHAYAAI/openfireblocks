# Asset management and data classification policy

ISO/IEC 27001:2022 A.5.9, A.5.12–5.14, A.7.10. SOC 2 CC6.1, C1.1.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. Asset inventory

The ISMS owner keeps `records/ASSET-INVENTORY.md` (or the Sprinto equivalent): laptops,
accounts, cloud resources, repositories, signing material and important documents, each with an
owner and a classification. It is checked every quarter.

## 2. Classification

| Class | Meaning | Examples | Handling |
|---|---|---|---|
| **Restricted** | Loss would allow theft of funds or total compromise | Key shares, HSM PINs, seed or recovery material, `JWT_SECRET`, `ADMIN_API_KEY`, database admin passwords, AWS root credentials, release-signing credentials | Secrets manager or HSM only; never in git, email, chat, documents or AI tools; two people for any recovery; access listed by name |
| **Confidential** | Would harm customers or FORGE if disclosed | Customer contracts, customer configuration, pentest reports before fixes, personal information, unreleased security findings, staff records, pricing | Company accounts only; shared by named access, not public links; encrypted in transit; under NDA when shared externally |
| **Internal** | Not for publication, low harm | Plans, internal notes, most of this ISMS | Company accounts |
| **Public** | Approved for publication | Website, marketing material, published documentation | Approved by the CEO before publishing |

The source code's class depends on a decision still to be made (risk R-21). While the
repository is public, the code is **Public** and must contain nothing above Public: no secrets,
no customer names in configuration, no personal information.

## 3. Labelling

Documents classed Confidential or Restricted carry the class in the title or header. Files
shared externally carry it in the file name, for example `…-CONFIDENTIAL.pdf`.

## 4. Information transfer

- Confidential information leaves the company only to a party with an NDA or contract that
  covers it, through a company account, over an encrypted channel.
- Restricted information is never transferred except as part of a documented key ceremony or
  recovery procedure (`docs/deployment/KEY-RECOVERY.md`).
- Customer data is never used in development or tests (A.8.33).

## 5. Storage media

Removable media are not used for Confidential or Restricted data, except encrypted media that is
part of a documented key-recovery procedure, recorded in the asset inventory.
