# Data protection impact assessment: Travel Rule data and AI-agent payments

GDPR art. 35; POPIA s19 and s57 (prior authorisation for some processing; counsel to confirm it
does not apply); ISO/IEC 27001:2022 A.5.34. Owner: Information Officer. Version 0.1, draft,
11 October 2026. **Needed before FORGE hosts the platform for a customer.** Customers running the
platform themselves can use this as the basis for their own DPIA.

## 1. Description of the processing

**Travel Rule.** For outbound crypto-asset transfers at or above a threshold (default R5,000,
`TRAVEL_RULE_THRESHOLD_ZAR`), the platform requires originator and beneficiary information in
IVMS101 form before it will sign (otherwise HTTP 422, or `travel_rule_required` through the MCP
server). It stores the record with the transfer and, where the beneficiary's VASP is reachable,
sends it over TRISA. Data: names of natural or legal persons, geographic addresses, national
identifiers or customer numbers, account numbers or wallet addresses, the amount and its rand
value. Inbound records from other VASPs are stored the same way.

**AI-agent payments.** An organisation's admin creates an agent with limits (tokens, recipient
list, per-payment and 24-hour limits in rand, expiry). An AI system (for example an assistant
connected through MCP) calls `send_payment` with a recipient, an amount, a free-text purpose and,
above the threshold, a beneficiary. The originator is configured once in the MCP server
(`FORGE_TRAVEL_RULE_ORIGINATOR`). The platform records each spend.

**Why it is in scope for a DPIA:** a legal obligation that forces collection of identity data
about people who are not the customer's users (beneficiaries); transfers to other VASPs,
including abroad; a new technology (an AI system choosing payment details); immutable records.

## 2. Necessity and proportionality

| Question | Answer |
|---|---|
| Lawful basis | The customer's legal obligation (FIC Act and Directive 9; FATF Recommendation 16; EU Transfer of Funds Regulation 2023/1113 for EU VASPs). FORGE is an operator/processor when hosting. |
| Minimisation | Collected only at or above the threshold. Only the IVMS101 fields the rule requires. Natural-person identifiers are optional where an address or customer number suffices under the applicable rule [customer to configure]. KYC documents are never stored; only references. |
| Purpose limitation | Used for the Travel Rule, sanctions screening and the customer's reporting. Not used for marketing or analytics. |
| Accuracy | Data comes from the customer's KYC records or the counterparty VASP; the customer corrects it at source. |
| Retention | 5 years (FIC Act) or as the customer decides; immutable during retention; removed only through a deliberate database-owner process. |
| Data-subject rights | Access through the customer; erasure is limited by the legal retention duty. The customer's notice must say so. |
| Transfers | To counterparty VASPs, possibly abroad. TRISA uses mutual TLS with certificates from the TRISA CA. Each transfer is necessary to meet the legal obligation (POPIA s72(1)(e)-style necessity, counsel to confirm); GDPR art. 49 derogations or the counterparty's own safeguards apply. |

## 3. Risks to individuals

| # | Risk | Likelihood | Severity | Existing controls | Residual |
|---|---|---|---|---|---|
| D1 | Travel Rule data of one customer seen by another | Remote | Significant | Row-level security on every tenant table; tenant-isolation tests in CI | Low |
| D2 | Data sent to the wrong or a fraudulent VASP | Possible | Significant | TRISA directory and certificates; the beneficiary VASP is identified before sending | Medium |
| D3 | Excessive collection (more fields than needed) | Possible | Limited | Required fields checked in code; optional fields optional | Low |
| D4 | An AI agent invents or leaks beneficiary details (prompt injection, hallucination) | Possible | Significant | Recipient list per agent; limits; the person approving above thresholds sees the record; an agent key can read only its own spend records (recipient, amount, purpose, status: `agent_spend`, migration 025), never Travel Rule records, which the agent routes do not return (checked 2026-10-11); purposes recorded | Medium |
| D5 | Free-text "purpose" fields collect personal or special information | Possible | Limited | Length limits; guidance to customers: no personal information in purposes | Medium |
| D6 | Breach of the hosted database | Remote | Significant | ISMS controls; encryption in transit; immutable audit; backups encrypted off-site [once live] | Low |
| D7 | Data kept beyond need because records are immutable | Likely | Limited | Retention schedule; deliberate deletion process at expiry | Medium |

## 4. Measures to add

1. Customer guidance: configure recipient lists for every agent; no personal information in purposes.
2. A retention job (customer-controlled) that removes Travel Rule records past their retention date
   through the database owner role, with a record of what was removed.
3. Keep it that way: a test that the agent routes return no Travel Rule fields (D4).
4. Encryption at rest of the hosted database confirmed and recorded (AWS check script).
5. DPA (`DPA-TEMPLATE.md`) signed with every hosted customer.

## 5. Conclusion

With the measures above, residual risk to individuals is acceptable, and prior consultation with a
supervisory authority (GDPR art. 36) is not expected to be needed [Information Officer and counsel
to confirm].

| Role | Name | Decision | Date |
|---|---|---|---|
| Information Officer | [to complete] | | |
| CEO | [to complete] | | |
