# Multi-custodian orchestration

An institution rarely keeps everything in one place: some assets in this platform's
threshold keys, some with a bank custodian, some at an exchange. This is the layer that
lets it see them as one balance sheet and govern them with one set of controls.

## What it does

* **One balance sheet.** `GET /organisations/:id/custody/overview` lists every account at
  every source with its balances, and totals by asset. A source that is down shows its
  own error and the rest of the picture is still shown. Two sources that disagree about
  an asset's decimals are **not added together** (that would be a silently wrong total);
  the asset's total says so instead.
* **One quorum.** A transfer out of another custodian (`POST .../custody/transfers`) is
  *always* held for the organisation's approvers, who see exactly what was asked
  (custodian, account, asset, amount, destination). It runs once, on quorum, with the
  approval's id as its idempotency key, so a repeated decision or a retry cannot send it
  twice. The person who asked cannot approve it.
* **One freeze, one whitelist.** The organisation freeze and the destination whitelist
  apply to these transfers, checked when asked, when each approver decides, and again
  when it runs. The whitelist is checked against the chain the account is really on
  (taken from the custodian's own listing, not from the caller).
* **Routing.** Rules (asset, optional size limit, custodian, account, priority) pick the
  account when an operator names an asset and an amount (`route: true`). An amount no rule
  covers is refused, not guessed.
* **Fixed connections.** A custodian's URL and credential cannot be changed after the
  fact, only switched off and replaced (database-enforced, migration 033): a transfer
  approved against one connector cannot be redirected to another.

## What it does not do

* **It cannot apply this platform's spending-policy engine to someone else's account**,
  because it cannot see their rules. The one control it can apply universally is a human
  quorum, so every transfer from another custodian needs one, however small.
* **Travel Rule records stay with the custodian that sends.** These transfers do not go
  through this platform's Travel Rule record; if you are the originating VASP for them,
  your custodian or your own process has to carry that obligation.
* **It does not ship connectors to named vendors.** It speaks one contract (below). A
  vendor's API is put behind that contract by a small connector the customer or an
  integrator runs. Nothing here has been run against any real custodian.
* Only native assets and tokens the connector reports; no staking, no on-chain proof of
  the custodian's balances (it believes what the connector says, validated for shape).

## The connector contract

HTTPS only (plain http for localhost), `Authorization: Bearer <token>`, JSON, redirects
refused, 10 s timeout, 1 MB limit. Everything returned is validated; anything malformed is
an error.

```
GET  /accounts                       -> { "accounts": [{ "id", "name", "blockchain", "address"? }] }
GET  /accounts/{id}/balances         -> { "balances": [{ "asset", "amount", "decimals" }] }   amount: base units, integer string
POST /transfers   (Idempotency-Key)  <- { "accountId", "destination", "asset", "amount", "memo"? }
                                     -> { "id", "status": "pending"|"completed"|"failed", "txHash"?, "error"? }
GET  /transfers/{id}                 -> { "id", "status", "txHash"?, "error"? }
```

A connector must honour `Idempotency-Key`: the same key must never create a second
transfer. That is what makes a retry after a timeout safe.

## Setting one up

1. Provision the connector's token as a secret and map it in the chart:
   `custody.tokens[0].env=CUSTODY_TOKEN_BANKX`, `custody.tokens[0].secretName=...`. The API
   accepts only a variable named `CUSTODY_TOKEN_*`, so a stored value cannot make the
   gateway send some other secret to a connector, and refuses to register a custodian
   whose variable is not set.
2. As an admin: `POST /organisations/:id/custody/custodians { name, baseUrl, tokenEnv }`.
3. Optionally add routing rules, then transfer.

Endpoints (admin to register and route; operators to transfer; everyone with a role to read):
`GET custody/overview`, `GET|POST custody/custodians`, `PUT custody/custodians/:id/enabled`,
`GET|POST custody/routes`, `DELETE custody/routes/:id`, `POST custody/transfers`,
`GET custody/transfers/:approvalId` (refreshes the live state from the custodian).
