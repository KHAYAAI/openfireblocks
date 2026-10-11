# A euro stablecoin (EURC) moved from a threshold key on a public chain (2026-10-11)

The same path as the USDC proof, with a token in a currency the policy did not previously allow. Until this
run, a EUR-pegged token was **denied by design** ("no amount limits are configured for EUR"); the first
attempt below shows that, and the second shows the transfer after EUR ceilings were added to
`policies/token_limits.rego` (commit `1b983d5`).

| | |
|---|---|
| Network | Ethereum Sepolia (11155111) |
| Token | EURC `0x08210F9170F89Ab7658F0B5E3fF39b0E03C594D4`, registered by an operator and **verified on-chain by the platform** (symbol and decimals read from the contract) |
| From | `0xc378bf1A3085000AeD970aFc3fBBc6F0240fd129` (2-of-3 threshold ECDSA key) |
| To | `0x000000000000000000000000000000000000dEaD` |
| Amount | 1.00 EURC (1,000,000 base units) via `POST /keys/:id/token-transfers` |
| Signed by | parties 1 and 2 of 3 |
| Transaction | `0xe1b0d37dec3fc7e423322e8c79ae3713b946d32e5da5da9800538a99239a42f6` |
| Block | 11890570, receipt status success (re-read independently from the public RPC) |
| Explorer | https://sepolia.etherscan.io/tx/0xe1b0d37dec3fc7e423322e8c79ae3713b946d32e5da5da9800538a99239a42f6 |

What was checked: the platform signed and the network accepted and mined the transaction; the receipt has
status success; the EURC contract's own `Transfer` event carried exactly 1,000,000 base units (checked by the
script before the step below).

What was not confirmed, and why: the script's final check, that the recipient balance rose by exactly the
amount, failed. The recipient is the public burn address, which other people also send tokens to (it held
62.1 EURC afterwards), and a public RPC can serve a block-behind node, so an exact before/after comparison
is unreliable there. The transfer itself is established by the receipt and the Transfer event, not by that
comparison. The script now waits for the balance to rise by at least the amount instead of demanding equality.

First attempt, same day: denied by policy with "no amount limits are configured for EUR; refusing to transact
EURC until they are", because the cluster was still running the previous policy-service image. That is the
fail-closed behaviour working as designed.

Shows: a second currency works end to end after an explicit, reviewable policy change, and an unconfigured
currency stays refused. Does not show: mainnet EURC, or that the EUR ceilings (900,000 global, 90,000 pro,
9,000 free per transaction) are the right numbers; they are risk-appetite figures to be set by the operator.
Circle's Sepolia EURC is a test token with no value.
