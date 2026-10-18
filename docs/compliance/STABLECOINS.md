# Stablecoins

## The short version

The platform can custody and move ERC-20 stablecoins on any EVM chain,
under the same 2-of-3 threshold signing as everything else. A token has to
be registered by an operator and verified against its own contract before
it can move money.

Rand-referenced tokens are first-class: they carry a `ZAR` peg, they are
held to rand limits set independently of the dollar ones, and their
activity aggregates in rand for threshold reporting.

## Registering a token

Three steps, and the middle one is the one people skip.

### 1. Register it

```bash
curl -X POST "$API/admin/tokens" \
  -H 'Content-Type: application/json' -H "x-admin-key: $ADMIN_KEY" \
  -d '{
    "chainId": 1,
    "contractAddress": "0x…",
    "symbol": "ZARP",
    "name": "ZARP Stablecoin",
    "decimals": 18,
    "pegCurrency": "ZAR",
    "issuer": "ZARP Stablecoin (Pty) Ltd"
  }'
```

`contractAddress` may be omitted. A token registered without one lands in
`awaiting_address`: visible, named, and unable to move anything. That is
the honest state for an asset the platform intends to support and whose
contract nobody has confirmed yet.

### 2. Verify it against the chain

```bash
curl -X POST "$API/admin/tokens/$TOKEN_ID/verify" -H "x-admin-key: $ADMIN_KEY"
```

This calls `symbol()` and `decimals()` on the contract and compares them to
the row. **Only a verified token can be transacted.**

This step is what makes a hand-entered or seeded address safe. If the
address is wrong — a typo, a copy from the wrong chain, a convincing scam
contract — then either it is not a token and the call reverts, or it is a
different token and its metadata will not match. Either way the token
stays unusable, and a wrong address is a configuration error rather than a
loss.

`decimals` is the one that must match exactly. USDC is 6 and DAI is 18;
reading one for the other is a factor of 10¹² on an amount limit, a
balance, and a regulatory aggregate, and nothing about the resulting
transaction looks unusual.

Verification needs `EVM_RPC_<chainId>` configured (Helm:
`external.evmRpc`). A chain with no endpoint is one the platform says it
cannot read, rather than one it guesses at.

### 3. Send

```bash
curl -X POST "$API/keys/$KEY_ID/token-transfers" \
  -H 'Content-Type: application/json' -H "x-api-key: $API_KEY" \
  -d '{
    "token": "ZARP",
    "recipient": "0x…",
    "amount": "1500.25",
    "chainId": 1,
    "nonce": 7,
    "maxFeePerGas": "30000000000",
    "maxPriorityFeePerGas": "1500000000"
  }'
```

`amount` is in the token's own units, not base units. Excess precision is
refused rather than rounded: a request to send `1.0000005` USDC is a
misunderstanding of the token, and silently sending `1.000000` is a worse
answer than saying so.

Balances: `GET /keys/$KEY_ID/balances?chainId=1`.

## Why the addresses are not in the repo

The seed migration registers the rand stablecoins by name, peg, decimals
and issuer, and **without contract addresses**.

That is deliberate. The correct source for a token's contract address is
its issuer. An address that arrives in a custody platform's registry any
other way is an address nobody vetted, and this one would have arrived by
being written into a migration file from memory.

An operator obtains each address from the issuer, records it, and verifies
it. Until then the token is registered and unusable, which is the correct
state.

The dollar stablecoins (USDC, USDT, DAI on Ethereum mainnet) are seeded
with their widely-published addresses as a convenience, and land as
`unverified` like everything else. The verification gate is what makes
them trustworthy, not the fact that they appear in a file.

## What the controls see

This is the part that was broken, and it is worth stating what changed.

An ERC-20 transfer puts the recipient and the amount inside the calldata.
The transaction's own `to` is the token contract and its `value` is zero.
Every control read those two fields, which meant:

- an amount limit compared **zero** against the ceiling;
- a counterparty whitelist compared the **token contract**, which is
  identical for every transfer of that token, so the list either allowed
  all of them or none;
- the daily regulatory aggregate summed to **nothing**.

The signing worked throughout. A customer who hand-encoded `transfer()`
got a genuine threshold-signed stablecoin transfer out of the platform.
That is what made it worse than not supporting stablecoins at all: it
looked supported.

Now the calldata is decoded before policy, and the recipient and amount
that the controls evaluate are the ones in the bytes being signed.

## Amount limits

Set in `services/policy-service/policies/token_limits.rego`, per peg
currency and tier, in the currency itself.

The rand figures are **not** the dollar figures converted. They are an
independent statement of risk appetite for a rand customer, and an
operator running this platform is expected to change both. They live in
the embedded policy bundle rather than the database because a deployed
policy set that is immutable and auditable is worth more for a limit than
the convenience of editing it at runtime.

A token pegged to a currency with no configured limits is **denied**.
Registering a token in a new currency must not silently create an
unlimited payment rail.

An unpegged token cannot be held to a money limit, so it is routed to a
human approver instead. `approve()` calls are escalated too, and an
unlimited approval is refused outright — an allowance is the authority to
take the money later, possibly long after the policy that permitted it
changed.

## Calldata the platform cannot read

`POST /keys/:keyId/transactions` accepts calldata. If it decodes to a
transfer of a registered, verified token, it is governed on the decoded
values. Anything else is **refused**.

Calling a contract that is not an ERC-20 is a real requirement with no
other route, so the capability exists per tenant
(`arbitrary_contract_calls_enabled`), off by default. With it on, the
recipient and amount that policy sees are the contract and zero — the old
blind behaviour, now reachable only by a decision somebody made on the
record. It mirrors `raw_digest_signing_enabled` and carries the same
warning.

## Not built

- **Solana SPL tokens.** Solana keys are real MPC (threshold Ed25519) and
  there is still no Solana transaction route, so SPL stablecoins are two
  pieces of work away, not one.
- **Tron**, where a lot of real USDT volume is, is not in the codebase.
- **Fee abstraction.** A key needs the native coin for gas. A customer
  holding only USDC cannot send it.
- **Permit / EIP-2612**, batching, and anything involving a paymaster.

See [THRESHOLD-REPORTING.md](THRESHOLD-REPORTING.md) for what the
compliance side does and does not do, including thresholds that need
confirming before anyone relies on them.
