# FORGE CUSTODY — website copy (11 October 2026)

Every claim below matches docs/LAUNCH-READINESS.md and docs/evidence/. Do not add "mainnet",
"production-ready", "audited" or "certified" until those are true.

## Hero

**Custody you run yourself. Controls nobody can skip.**

FORGE CUSTODY is self-hosted digital-asset custody software. Keys are created by threshold
signing, so no single person, machine or vendor ever holds a whole key. Every transfer, from
your app, your people or an AI agent, passes the same policy engine and the same approvals
before anything is signed.

[Book a pilot briefing] [Watch the platform in use]

## What it does

- **Threshold signing (2-of-3, configurable).** Three independent parties each hold a key share;
  any two sign. The full private key never exists, at any time.
- **A policy engine that fails closed.** Amount limits, allow-lists, blocked countries, sanctions
  screening and token controls are checked before signing. If a check cannot run, nothing is signed.
- **Approvals people cannot bypass.** M-of-N named approvers with one-time codes. The person who
  asks can never approve their own transfer.
- **Emergency freeze.** One switch stops all signing across the console, the API, settlements and
  agents. Only an administrator can lift it.
- **Travel Rule built in.** Originator and beneficiary details in IVMS101 form are captured and
  checked before signing, above the threshold.
- **Reconciliation.** What the platform signed is checked against what the chain recorded, and the
  result is kept as evidence.
- **Runs in your environment.** Deployed into your own cloud or data centre. Recovery does not
  depend on us. Source available for review under NDA.

## New: custody for AI agents

**Let AI agents pay — within limits a person sets.**

AI agents can now make stablecoin payments from FORGE CUSTODY through the Model Context Protocol
(MCP). An administrator issues each agent a narrowly scoped credential:

- the tokens it may pay in, and the only recipients it may pay;
- a per-payment limit and a daily limit;
- an expiry, and revocation that takes effect immediately.

The agent never holds a key. Every payment it requests still goes through your policy engine, the
Travel Rule and the 2-of-3 signature. Refusals are final: an agent cannot split a payment or change
the recipient to get around a limit, however it is instructed — the limits are enforced by the
platform, not the prompt. Every payment and every refusal is recorded for your team to review.

*Proven end to end on a development network; not yet on a public network.*

## Built for

**Fintechs.** Hold customer and treasury funds in stablecoins and pay out through one API, while
your finance team approves what policy holds. Let an AI agent pay approved suppliers within a budget.

**Banks.** Run digital-asset operations inside the bank, under bank controls: named roles, dual
control, segregation of duties, Travel Rule records and an evidence trail your auditors can test.

**Governments and public bodies.** Keep keys in the state's own infrastructure, with no dependence
on the vendor. Require three officials for large disbursements; restrict payments to approved
recipients; stop everything with one switch.

**Asset managers.** One set of controls for fund assets on Ethereum, Bitcoin, Solana and Cosmos.
Investment-committee approval for large moves, reconciliation for the fund administrator, and an
AI treasury agent for routine fund expenses.

## Supported networks and assets

Ethereum and EVM chains, Bitcoin, Solana and Cosmos. ERC-20 stablecoins registered and verified
against their contracts, with limits in USD, EUR and ZAR.

Transactions signed by our threshold keys have been accepted by public test networks: Ethereum
Sepolia, Solana devnet, the Cosmos (Osmosis) testnet and Bitcoin testnet4, including USDC and
EURC test-token transfers. OUSD (Open Standard) is registered and verified against its live
contracts on Ethereum and Base.

## Where we are

We are at pilot stage, and we would rather say so plainly:

- Suitable for supervised pilots on test networks or with capped balances; not yet for material funds.
- A three-round automated penetration test is complete, and every finding has been fixed.
- An independent human-led penetration test and an independent cryptographic review are being arranged.
- A SOC 2 programme is planned to begin within one to two months.

## Call to action

**Run a pilot with us.** Six-month supervised pilots on test networks, with your keys, your
policies and your approvers in your own environment. We will show you the platform working —
including an AI agent paying within its limits — in a 30-minute briefing.

Tebogo Mvelase, Founder and CEO · TKM@myforgepay.com · myforgepay.com
