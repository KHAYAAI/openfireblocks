# In-depth recordings of the running platform (v2)

Four recordings, one per customer segment, made by `infrastructure/local/e2e/record-v2.js`
against the full local stack (`infrastructure/local/e2e-fullstack-local.sh`): real Postgres,
Temporal, the policy service, the signer, three mpc-party processes, the worker, the gateway, a
real EVM development chain (ganache), and the MCP server for AI agents.

Each recording has the same three layers:

1. **How an application connects.** Real API calls, made while recording, with the request and
   the gateway's answer on screen: creating a 2-of-3 key (a real key ceremony), deposit
   addresses, balances read from the chain, a payout signed by two of three parties and
   accepted by the chain, a large transfer held with HTTP 202, key rotation, Travel Rule data.
2. **How the customer's people keep control.** The real console: sign-in with one-time codes,
   roles, approval policy, segregation of duties, M-of-N approvals, the emergency freeze,
   Travel Rule records, reconciliation against the chain.
3. **How an AI agent uses custody.** An admin issues an agent key in the console; a real MCP
   client starts the real MCP server (`services/mcp-server`), which calls the gateway. The agent
   pays within its limits, is refused outside them (over the limit, a recipient not on its list,
   a prompt-injection attempt, the freeze), confirms on-chain, and is revoked.

| File | Story |
|---|---|
| `forge-custody-fintech.mp4` (7 min) | Northwind Pay: the app creates a key, takes a deposit, pays out, and has a large move held; the finance team approves; a payables agent pays invoices and refuses an injected payee; evidence |
| `forge-custody-bank.mp4` (6.5 min) | Meridian Bank: API key rotation, IVMS101 Travel Rule on the API (and refusal without it); roles, dual control and segregation of duties; a settlements agent paying R15,000 with Travel Rule data, then stopped by the emergency freeze; audit evidence |
| `forge-custody-government.mp4` (5 min) | National Treasury: quorum raised to three; a grants system's request approved by three officials; safety controls; a disbursement assistant that refuses a diversion attempt; revoked at programme end |
| `forge-custody-asset-manager.mp4` (6.5 min) | Kestrel AM: keys on Ethereum, Bitcoin and Solana from three parallel ceremonies; a rebalancing move approved by portfolio manager and risk; reconciliation; a treasury agent paying fund expenses, including a Travel Rule payment completed with the payee from the invoice, and a retry that does not pay twice |

**Real:** every API call shown, the console, one-time codes, roles, policy decisions,
approvals, the Travel Rule records, the freeze, real tss-lib key ceremonies, real threshold
ECDSA signatures accepted by an EVM chain, the MCP server and every tool call and answer.

**Stand-ins, said on screen:** the chain is a local EVM development chain (ganache), which
checks every signature the way any EVM node does but is not a public network; `tZAR` is a test
token deployed on it; the console's network list gains a "Local dev chain (1337)" option for
this recording; the customers are fictional. Public test networks are proven separately
(`docs/evidence/2026-10-11-*.md`).

**Checked while recording:** every narrated outcome. Each tool call states the outcome its
narration depends on (paid, refused, Travel Rule required, already paid, revoked), and the
recorder stops if the platform answers otherwise. Two problems were found that way and fixed in
the product before these takes: idempotency keys under 8 characters were rejected by the MCP
input schema, and agents had no way to supply Travel Rule details above the R5,000 threshold.

**Scripted:** the user's and the AI agent's sentences. No language model runs in the
recording; the agent's tool calls and the platform's answers are live, and the video says so.

**Not shown:** single sign-on (built and tested in `e2e-oidc-local.sh`), separate hosts for the
three parties, an HSM, Bitcoin and Solana spends (proven on public test networks), multi-custodian
orchestration and tokenisation (built, tested in CI). Pilot-stage: not independently audited.

Re-record (about an hour; needs the `temporal` CLI, Go, Node, Postgres binaries and
Playwright's Chromium, and `npm ci && npm run build` in `services/mcp-server`):

    TEMPORAL_BIN=$(which temporal) E2E_CONSOLE=0 \
    E2E_AFTER="node infrastructure/local/e2e/record-v2.js" \
    ./infrastructure/local/e2e-fullstack-local.sh

One segment only: `E2E_AFTER="node infrastructure/local/e2e/record-v2.js bank"`.

## Voice-over

Each recording has a cue file, `<name>.cues.json`, written by the recorder. It lists every caption, card,
diagram, code panel and chat line, with the second it appeared. `scripts/video/narrate.py` turns
it into a spoken narration track through a local VoiceStudio server: see
[`../NARRATION.md`](../NARRATION.md). Use a voice model whose licence allows commercial use;
VoiceStudio's default OmniVoice weights are non-commercial.
