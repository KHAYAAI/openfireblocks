# Customer flow recordings (the real console)

Three recordings of the **running console**, one each for a fintech, a bank and
a government customer. Nothing on screen is an animation: they are Chromium
driving the live gateway with real sign-ins, one-time codes, policy,
approvals and database rules. Title cards and the caption strip are the only
additions.

| File | Story |
|---|---|
| `openfireblocks-fintech-console.webm` | a real API-key call is held with 202; two approvers decide; the stored request runs; billing card; reconciliation |
| `openfireblocks-bank-console.webm` | roles and policy; an operator asks with Travel Rule details; held; two approvers; evidence; reconciliation |
| `openfireblocks-government-console.webm` | quorum raised to three; clerk asks; three officials approve; audit record; reconciliation |

Real in these recordings: sign-in, TOTP step-up, roles, policy-service
decision, the stored request, quorum, segregation of duties, the database
triggers, Travel Rule capture, reconciliation.

Stand-ins, labelled on screen: the chain is a mock Solana node (it holds 5 SOL,
so the approved 15 SOL send fails with a real "insufficient balance" error
and an admin retry is offered), Stripe is a mock billing service, and single
sign-on is not shown because there is no identity provider here (password and
one-time code are used). Not shown: a signing ceremony completing, a
transaction confirming on a live network, self-hosted deployment, a hardware
HSM. Customers are fictional.

Re-record against a seeded dev stack:

    SEED_JSON=/path/seed.json OUT_DIR=docs/showcase node scripts/record-console.js [fintech|bank|government]

The animated scene player at `/console/showcase` remains in the product and is
restyled to the same Forge tokens, but it is a scripted illustration.
