# Agent attestations

A signed statement of one agent's record, for an operator to give to a third party. Built for the FORGE credit bureau, which uses
it to measure two things public chain data cannot show: how often an agent's payments go through, and how often it stayed within
its budget.

## What it contains, and what it deliberately does not

Counts only: payments made, payments abandoned after passing every limit, attempts refused, and refusals grouped by reason. It also
names the **address of the key the agent pays from**, so the receiver can tie the statement to a wallet it already knows. It
contains no amounts, recipients or purposes.

It is valid for 7 days by default and never more than 14, so an old, better record cannot outlive a worse present.

## Turn it on

1. Generate a signing key and keep it as a secret. It is separate from every other key in the platform:
   `openssl genpkey -algorithm ed25519 -out agent-attestation.pem` (mode 0600; never commit it).
2. Mount it and set `AGENT_ATTESTATION_KEY_FILE=/path/to/agent-attestation.pem` on the api-gateway. Without it the route answers 503.
3. Give the receiver the **public** key, out of band (not from the response). For the bureau this is
   `CUSTODY_ATTESTATION_PUBLIC_KEYS` (comma-separated, so keys can be rotated). Derive it with:
   `openssl pkey -in agent-attestation.pem -pubout -outform DER | tail -c 32 | base64`

## Ask for one

An organisation **admin** (only) calls, signed in:

```
GET /organisations/{customerId}/agents/{agentId}/attestation?subject=<receiver's id for the agent>&validityDays=7
```

`subject` is optional and ties the statement to one audience. The response is `{ payload, signature, publicKey }`: `payload` is the
JSON text that was signed (carried as a string so there is nothing to canonicalise), `signature` is base64 ed25519 over its UTF-8
bytes. `publicKey` is informational: a receiver must verify against the key it was given out of band, never this one.

The request is recorded in the audit log (`agent.attested`).

## What the receiver should check

The signature; that `issuer` is `forge-custody`; that it has not expired and is not dated in the future; that `keyAddress` is the
wallet it expects; that the counts are consistent (the refusal reasons add up to `refused`); and, across statements, that a new one
is newer and its lifetime counts did not go down.
