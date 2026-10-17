# Cross-border transfers of personal information

POPIA s72; GDPR chapter V (art. 44–49); UK GDPR. Owner: Information Officer. Version 0.1, draft
for counsel.

## 1. POPIA s72: when FORGE may send personal information outside South Africa

Only if one of these applies (s72(1)):

a) the recipient is subject to a law, binding corporate rules or a **binding agreement** that gives
   an adequate level of protection, substantially similar to POPIA's conditions, including rules on
   onward transfer;
b) the data subject consents;
c) the transfer is necessary to perform a contract with the data subject, or pre-contract steps
   they asked for;
d) it is necessary for a contract in the data subject's interest between FORGE and a third party;
e) it is for the data subject's benefit, consent is not reasonably practicable, and they would
   likely consent.

**FORGE's approach:** rely on (a), through each supplier's DPA (which commits it to security,
confidentiality, processing on instructions and onward-transfer limits), recorded in the vendor
register. For a hosted customer, keep the customer's data in **af-south-1 (Cape Town)** unless the
customer instructs otherwise in writing.

## 2. Current transfers

| Vendor | Data | Destination | Mechanism |
|---|---|---|---|
| Google Workspace | Email, documents (P1–P5) | Global (US, EU) | Google Cloud Data Processing Addendum incl. SCCs; s72(1)(a) |
| GitHub | Code, issues | US | GitHub DPA incl. SCCs; s72(1)(a) |
| AWS | Staging data; hosted data | Region chosen (af-south-1 keeps it local) | AWS DPA; s72(1)(a) |
| Stripe | Billing contacts | Global | Stripe DPA; s72(1)(a)/(c) |
| Sprinto | Staff and system metadata | [to confirm] | Sprinto DPA |
| Anthropic | Code and documents in development sessions (no personal information should be included) | US | Commercial terms/DPA |
| TRISA counterparties (hosted only) | Travel Rule data | Counterparty's country | Legal obligation of the customer; necessity; the counterparty's own regime [counsel] |

## 3. GDPR: EU/UK personal information leaving the EEA/UK

South Africa has **no EU adequacy decision**. When FORGE receives EU personal data as controller
(for example EU prospects' details) or processor (an EU customer's hosted data):

- **Into FORGE (South Africa):** the EU party (or FORGE, as controller importing from the EU)
  relies on the **EU Standard Contractual Clauses (Commission Implementing Decision (EU) 2021/914)**:
  Module 1 (controller to controller), Module 2 (controller to processor), or Module 3 (processor to
  processor), as fits. For UK data: the UK **International Data Transfer Addendum** to the EU SCCs,
  or the IDTA.
- A **transfer impact assessment** is done for each, considering South African law on government
  access (RICA, the Cybercrimes Act, the Intelligence Services Act) [counsel].
- Onward transfers to FORGE's sub-processors follow the SCCs' onward-transfer clause.
- For prospecting contacts in the EU, FORGE as controller processes on legitimate interest. The
  transfer is FORGE collecting the data directly, so art. 3(2) territorial scope and the need for an
  EU representative (art. 27) are for counsel to confirm.

## 4. Clause for customer contracts (operator / processor)

> The Operator shall not transfer Personal Information outside [the Republic of South Africa /
> the hosting region] except (a) to the sub-operators listed in Annex 2, under written agreements
> giving protection substantially similar to POPIA and, for personal data subject to the GDPR, the
> Standard Contractual Clauses; or (b) on the Customer's documented instructions. The Operator shall
> tell the Customer before any new transfer destination and give it the opportunity to object.
