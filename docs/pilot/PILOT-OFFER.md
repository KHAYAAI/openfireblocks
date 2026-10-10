# OpenFireblocks design-partner pilot

**Draft for review by a lawyer before it is sent. Prices and dates are proposals, not commitments.**

## What this is

A six-month, testnet-only evaluation of OpenFireblocks, self-hosted digital-asset custody
software. Your team runs it in your own environment and tests it against your own control
requirements. We never hold your keys or funds.

## What you get

- Threshold (2-of-3) key custody. No complete private key ever exists.
- Policy that reads decoded transactions, including stablecoin transfers, and denies by
  default.
- Approvals with named approvers, quorum, and enforced separation of duties.
- Emergency freeze, address whitelists, an audit trail, and webhooks for transfer events.
- Support for Ethereum and EVM chains, Bitcoin, Solana and Cosmos on test networks.
- Direct access to our engineers during the pilot, and influence over our roadmap.

## What we have shown so far

A real 2-of-3 key generation, threshold signature, policy denial and tenant-isolation check
passing on a Kubernetes cluster. The raw output is available on request. See also our
published readiness status.

## What this pilot is not

- **Testnet only.** No real funds, no mainnet.
- **Not audited.** An independent cryptographic audit is being procured. No report exists yet.
- **Not certified.** There is no SOC 2 or ISO 27001 report.
- **Not production-ready.** Production use follows the audit, isolated-host deployment, and
  your own review.

## Structure (about six months)

| Phase | Weeks | What happens |
|---|---|---|
| Qualify and scope | 1-4 | Confirm your use case, chains and control requirements. Agree written success criteria. Sign the agreement |
| Deploy | 5-8 | We guide your team through installing it in your environment |
| Integrate | 9-16 | Your engineers connect through the API and SDKs. Your compliance team uses the console |
| Test your controls | 17-20 | Your people run approval, policy, freeze, failure and recovery scenarios |
| Review | 21-24 | Joint review against the success criteria. A written findings report and a production plan |

## Example success criteria (agreed in writing at the start)

- A key is created by a 2-of-3 ceremony across three separate parties.
- A transfer above the approval threshold cannot execute without the required approvers and
  cannot be approved by its initiator.
- A transfer breaching a limit or going to a non-whitelisted address is denied with a reason
  and recorded.
- Emergency freeze stops signing within an agreed time.
- Signing continues when one party is unavailable and correctly refuses when two are.
- Your team recovers a key following the runbook.
- Your risk team can answer "who did what, when, with whose approval" from the audit trail.

## What we ask of you

- A named champion and an executive sponsor.
- Engineering time for integration and a compliance or risk reviewer for the control tests.
- A weekly call and honest written feedback.

## Commercial terms (proposed)

- **Fee:** about R450,000 (roughly US$25,000) for the six months, covering implementation
  support and direct access to the team. The fee is meant to ensure the pilot has an internal
  owner.
- **In return:** we ask for a named reference and logo, a written case study with real
  numbers, and the right to cite your compliance team's review, each subject to your approval.
- **If you convert:** a year-one production licence locked at 50% of list price, exercisable
  for twelve months after the pilot ends.
- **Production licence (indicative):** from about US$75,000 a year, plus implementation and
  support, scoped after the pilot.

## How it ends

Convert to a production plan, extend for an unmet criterion, or stop. If you stop, you delete
your environment and both sides keep the findings report.

## Next step

A 45-minute call to confirm fit, followed by a short scoping document.
