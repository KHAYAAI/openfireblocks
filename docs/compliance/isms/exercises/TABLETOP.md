# Incident response tabletop exercise: script and record

ISO/IEC 27001:2022 A.5.24–5.27. SOC 2 CC7.3–CC7.5, CC2.3. GDPR art. 33–34. POPIA s22. Objective O-7.
Run at least yearly. Allow 90 minutes. Facilitator: someone who is not the incident lead.

## Before

- Participants: incident lead, engineering lead, CEO, Information Officer, and whoever is on call.
- Everyone has `docs/security/incident-response-plan.md` open. Nobody has seen the injects.
- The facilitator reads the injects one at a time and asks the questions. Do not touch real
  systems, except to show where a button or command is (for example the freeze).

## Scenario A — an approver's account is used to push through a transfer (fraud, funds)

| Time | Inject | Questions |
|---|---|---|
| T+0 | A customer (bank, staging pilot) emails at 07:40: "We see a pending approval for 2,400,000 tZAR to an address we don't recognise. One approval is already recorded, from Thandi, who is on leave." | Who receives this email, and how do they reach the incident lead? What severity is it (plan §3)? |
| T+10 | The customer asks if they should press Freeze. | Who in the customer's organisation can freeze? (Approver or admin.) What does freezing stop? What doesn't it stop? Who can lift it? (Admin only.) |
| T+20 | The approval record shows Thandi's decision was verified by a one-time code. | What does that tell you? (Her password **and** TOTP secret, or her device, are compromised, or it was her.) What do you check next? (Login history, audit events, other organisations she belongs to.) |
| T+30 | A second, smaller transfer from the same key to the same address went through yesterday, under the approval threshold. | Is this now P1? What evidence do you preserve, and how (hash and export; plan §7)? Is the address on the whitelist? Why not? |
| T+45 | A journalist asks whether "FORGE custody was hacked". | Who answers? What do you say, and what don't you say? Does anything in your contracts require notifying the customer's regulator, or yours? |
| T+60 | Thandi's personal information (name, email) and the customer's Travel Rule records may have been viewed. | Is this a personal-information compromise under POPIA s22? Who notifies the Information Regulator and the people affected, and by when? If an EU person's data is involved, what changes under GDPR art. 33 (72 hours)? Which role is FORGE here: operator or responsible party? |

## Scenario B — a dependency advisory says the signing library leaks share material

| Time | Inject | Questions |
|---|---|---|
| T+0 | A Critical advisory is published for the threshold-signing library version you ship. | Who sees it first, and how (CI gate, GitHub advisory, mailing list)? Deadline under the vulnerability policy? |
| T+15 | No patched version exists yet. | What is the mitigation? (Pause signing; a freeze per customer org; refresh shares after the fix.) Who tells customers, and when (within 24 hours of a mitigation)? |
| T+40 | A customer says they cannot freeze because a payroll run is due. | Who decides? What is the risk record for an exception? |

## Scenario C — a laptop is stolen

| Time | Inject | Questions |
|---|---|---|
| T+0 | An engineer's laptop is stolen from a car. It was locked. | What must already be true for this to be low-impact (encryption, screen lock, password manager)? How do you know it was? (Sprinto agent record.) |
| T+10 | The engineer had an AWS session, GitHub SSH key and the staging admin console open in a browser. | List every credential to revoke and rotate. Who does it, and how fast? |

## Record

```
Exercise ID:  TT-YYYY-NN        Date:           Facilitator:
Participants (name, role):
Scenarios run:

What went well:

Gaps found (each becomes a corrective action in 08-corrective-actions.md):
| # | Gap | Action | Owner | Due |

Changes needed to the incident response plan:
Contact list verified as current?  Y / N   (plan §11)
Signed (incident lead):                 Date:
```
