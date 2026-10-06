# Customer flow recordings (the real console, real signing)

Three recordings of the **running console**, one each for a fintech, a bank and
a government customer. Nothing on screen is an animation. They were made by
`infrastructure/local/e2e/record.js` against the full local stack
(`infrastructure/local/e2e-fullstack-local.sh`): real Postgres, Temporal, policy
service, mpc-signer, three mpc-party processes, the worker and the gateway.
Title cards and the caption strip are the only additions.

| File | Story |
|---|---|
| `openfireblocks-fintech-console.webm` | a real API-key call is held with 202; two approvers decide; the stored request runs and is signed by the three parties; billing card; reconciliation |
| `openfireblocks-bank-console.webm` | roles and policy; an operator asks with Travel Rule details; held; two approvers; the transfer is signed and sent; evidence; reconciliation |
| `openfireblocks-government-console.webm` | quorum raised to three; clerk asks; three officials approve; the transfer runs; audit record; reconciliation |

**Real in these recordings:** sign-in and one-time codes, roles, the policy
service's decision, the stored request, quorum and segregation of duties, the
database rules, Travel Rule capture, a real 2-of-3 key ceremony (tss-lib) and
real threshold Ed25519 signatures.

**Stand-ins, labelled on screen:** the chain is a Solana RPC stand-in
(`infrastructure/local/mock-solana-node.js`) that checks each signature against
the key's address before accepting a transaction, as a validator would. It is
not Solana, and nothing here confirmed on a live network. Stripe is a stand-in
(`infrastructure/local/e2e/mock-billing.js`). Single sign-on is built and tested
(`e2e-oidc-local.sh`) but is not shown; these recordings use password and
one-time code. Not shown: self-hosted deployment on a cluster, separate-owner
hosting, a hardware HSM. Customers are fictional.

(The first recordings made here, before the full stack ran locally, ended in a
failed send against a mock chain with 5 SOL; they have been replaced.)

Re-record, which takes about twenty minutes (needs the `temporal` CLI, Go, Node,
Postgres binaries and Playwright's Chromium):

    TEMPORAL_BIN=$(which temporal) \
    E2E_AFTER="node infrastructure/local/e2e/record.js" \
    ./infrastructure/local/e2e-fullstack-local.sh

The animated scene player at `/console/showcase` remains in the product,
restyled to the same tokens; it is a scripted illustration, not a recording.
