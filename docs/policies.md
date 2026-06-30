# Policies (OPA / Rego)

The policy-service evaluates every transaction before signing. Policies are
written in Rego and embedded into the service binary
(`services/policy-service/policies/`). The gateway and the Temporal workflow both
call it and **fail closed** — an unreachable policy service denies the request.

## Decision contract

Request (from the gateway):

```json
{
  "customerId": "demo",
  "customerTier": "pro",
  "to": "0x...",
  "value": "1000000000000000000",
  "chainId": 11155111,
  "whitelist": ["0x..."],
  "blockedCountries": ["KP", "IR"],
  "country": "ZA"
}
```

Response:

```json
{ "approved": true, "denials": [], "requiresApproval": false, "reason": "approved" }
```

`requiresApproval` (high-value transactions) is advisory: the Temporal workflow
pauses for a human approval signal before signing; the synchronous `/sign` path
treats only `approved` as gating.

## Bundled rules

| Rule | Behaviour |
|------|-----------|
| Global limit | Deny `value > 100 ETH` (all tenants) |
| Tier limit | Deny `> 10 ETH` (free), `> 50 ETH` (pro); enterprise only bound by the global limit |
| Approval | Flag `> 10 ETH` for manual approval |
| Whitelist | When a tenant supplies a non-empty `whitelist`, deny recipients not on it (case-insensitive) |
| Geographic | Deny when `country` is in the tenant's `blockedCountries` |

Per-tenant `whitelist` / `blockedCountries` come from the customer's `policies`
JSON (set via `PUT /admin/customers/:id/policies`).

## Precision note

`value` is compared as a float64 in Rego, so threshold checks near the boundary
are accurate to a few thousand wei. Exact-boundary hard limits should be enforced
with big-integer comparison in a future iteration.

## Adding a policy

1. Add/extend a `.rego` file under `services/policy-service/policies/` in
   `package policies`, contributing to `deny[msg]` or `require_approval[msg]`.
2. Add a case to `main_test.go`.
3. `go test ./...` then rebuild the image — policies ship inside the binary.
