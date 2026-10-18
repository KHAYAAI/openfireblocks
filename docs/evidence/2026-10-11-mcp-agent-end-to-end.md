# AI agents pay through the MCP server, end to end (2026-10-11)

A real MCP client started the real MCP server (`services/mcp-server`, over stdio) and drove it
against the full local stack (`infrastructure/local/e2e-fullstack-local.sh`: Postgres, Temporal,
the policy service, the signer, three mpc-party processes, the worker, the gateway) and a real
EVM development chain (ganache, chain 1337). Scenario: `infrastructure/local/e2e/mcp-agent.js`.

Result: **20 of 20**, after the platform's main scenario passed 21 of 21 in the same run.

```
ok   real 2-of-3 DKG for the agent's key (62s)
ok   an operator registers the token
ok   and the platform verifies it against the contract
ok   an admin issues an agent key, shown once
ok   the MCP server offers the five tools
ok   the agent reads its limits: R12,000 for 24 hours
ok   and the token it may pay in
ok   pays R2,500: two of three parties sign and the platform broadcasts
ok   the chain moved 2,500 tZAR to the supplier
ok   and the agent confirms it on-chain
ok   R9,000 is refused: over the per-payment limit, and reported as final
ok   R6,000 without the beneficiary: Travel Rule required, nothing signed
ok   with the beneficiary from the invoice it is paid, with a Travel Rule record
ok   and the chain moved 6,000 tZAR
ok   a recipient not on its list is refused
ok   and nothing reached that address: refused before signing, no transaction
ok   a retry with the same idempotency key is reported as already paid, and pays nothing
ok   the agent sees its own history: paid and refused
ok   the admin revokes the agent
ok   and its next call is refused
```

What it shows: an agent holding only a scoped agent key (no key share, no customer API key) can
read its limits, pay a listed supplier in a registered and verified token (two of three parties
sign, the platform reads nonce and fees and broadcasts, the chain moves the tokens), and confirm
it on-chain. Above the Travel Rule threshold (R5,000) it is refused without the beneficiary and paid with
it, with a Travel Rule record; the organisation's own originator details come from the MCP
server's configuration, never from the agent. It is refused, before anything is reserved or signed, when it exceeds its
per-payment limit or names a recipient not on its list; a retry under the same idempotency key
returns the original payment and pays nothing; once revoked, its next call is refused.

One earlier run failed the check "nothing reached the refused address". The cause was the test:
the address chosen as the stranger was the dev chain's funding account (derived from
`EVM_FUNDER_KEY`), which deployed the test token and held its remaining supply. The gateway's
record of that request showed `refused`, reason "recipient not allowed", no transaction. The
scenario now uses an address that has never been used on the chain.

What it does not show: a public network (the chain is a local development chain; public test
networks are evidenced separately), a language model choosing the calls (the scenario issues
them directly; any MCP client issues the same calls), or production limits. Agent limits are in
rand; tZAR is a test token on the dev chain.
