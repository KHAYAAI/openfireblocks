# FORGE CUSTODY MCP server

Lets an AI agent (Claude, or any client that speaks the Model Context Protocol) make
stablecoin payments from a FORGE CUSTODY account, **within limits a person set for it**.

The agent never holds a key. It holds an *agent key* (`ofb_agent_...`) that an organisation
admin issued with:

- the tokens it may pay in,
- optionally, the only recipients it may pay,
- a per-payment limit and a 24-hour limit (in rand),
- an expiry, and a revoke button.

Every payment it asks for goes through the same path as a person's: token registry,
the organisation's policy engine, the Travel Rule, then a 2-of-3 threshold signature by
independent signing parties. The platform reads the nonce and fees and broadcasts.

## Tools

| Tool | What it does | Moves money? |
|---|---|---|
| `get_spending_status` | Limits, allowed tokens and recipients, what remains of the 24-hour budget | No |
| `list_payment_tokens` | Tokens the agent may pay in, with chain id and contract | No |
| `send_payment` | Pay `amount` of `token` to `recipient` on `chain_id`, with a `purpose` for the record | **Yes** |
| `get_payment` | One payment by idempotency key; with `chain_id`, on-chain confirmation | No |
| `list_payments` | The agent's own recent payments and refusals | No |

What it deliberately cannot do: create or see keys, raise its own limits, add recipients,
approve a held payment, or read another agent's record. Those stay with people, in the console.

## Configure

Issue the agent key (an organisation admin, signed in):

```
POST /organisations/{customerId}/agents
{ "name": "procurement bot", "keyId": "...", "allowedTokens": ["USDC"],
  "allowedRecipients": ["0x..."], "perTransferLimitZar": 5000, "dailyLimitZar": 20000, "expiresInDays": 30 }
```

The key is shown once. Then, in the MCP client's configuration (Claude Desktop shown):

```json
{
  "mcpServers": {
    "forge-custody": {
      "command": "node",
      "args": ["/path/to/services/mcp-server/dist/src/index.js"],
      "env": {
        "FORGE_API_URL": "https://custody.example.com",
        "FORGE_AGENT_KEY": "ofb_agent_..."
      }
    }
  }
}
```

Claude Code: `claude mcp add forge-custody --env FORGE_API_URL=... --env FORGE_AGENT_KEY=... -- node /path/to/dist/src/index.js`

The server refuses to start with anything but an agent key, and refuses plain `http://`
except to localhost.

## Build and test

```
npm ci && npm test
```

## Security notes

- **Prompt injection is the main risk.** Someone can try to talk an agent into paying them.
  The defence is the server side, not the prompt: set `allowedRecipients` for any agent
  that can be reached by untrusted text, keep limits small, and keep expiry short.
- Refusals are returned as final, and the tool descriptions tell the model not to split a
  payment or change the recipient to get around a limit. The limits hold regardless: they
  are enforced by the gateway in one database transaction under a row lock.
- A payment the organisation's policy holds for approval is reported as held; only a person
  can release it.
- Revoking the agent in the console stops it on its next call.
- Agent limits are in rand. Dollar stablecoins are valued with `TRAVEL_RULE_ZAR_PER_USD`;
  a token with no rand valuation is refused for agents.

Status: pilot-stage, test networks only. Not independently audited.
