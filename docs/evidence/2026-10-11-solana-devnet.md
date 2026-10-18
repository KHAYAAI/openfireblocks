# Solana devnet accepted a threshold-signed transfer (2026-10-11)

Before this, the Solana path had only been exercised against a stand-in node that verified the signature
but was not Solana. This is the first transaction a public validator set has confirmed.

| | |
|---|---|
| Network | Solana devnet (`https://api.devnet.solana.com`), reached through the platform's own signer |
| Sending key | `6CqUnufoLmgAwWX8VeYvuMcA2VaE3BacuDTQXWWWN2rP` (2-of-3 threshold Ed25519, created by DKG across three pods) |
| Recipient | `Ge1VThiMW7fuFbWSoJ2XbW4g4ULqJYJ3AXrq5DUSWLJp` (a second threshold key) |
| Amount | 1,000,000 lamports (0.001 SOL) |
| Signed by | parties 1 and 2 of 3 |
| Transaction | `4g1XwcoR7McmtS6thB3WC3eKXaJyTMKcHe7pFFgK9rVVCRZWKCiyjrQZx3UscyMRsUEySiH2Bx5t79cVCLDVY4da` |
| Confirmation | `finalized`, slot 509819089; the node's parsed transfer matches sender, recipient and amount |
| Explorer | https://explorer.solana.com/tx/4g1XwcoR7McmtS6thB3WC3eKXaJyTMKcHe7pFFgK9rVVCRZWKCiyjrQZx3UscyMRsUEySiH2Bx5t79cVCLDVY4da?cluster=devnet |

Run with `infrastructure/kind/native-chain-test.sh solana` on the staging deployment (commit `fe0ebe9`).

## Finding on the way
The platform refused the first attempt with "Travel Rule information is required": it has no price source for
native coins, so it treats every native transfer as over the R5000 threshold. That is the compliance control
behaving as designed, and the test scripts now send clearly-labelled testnet originator/beneficiary details.
Note for pilots: a customer will need to supply Travel Rule details on native-coin transfers until a price
source is configured.

## What this does and does not show
- Shows: the Solana transaction format and the threshold Ed25519 signature are accepted by a real network.
- Does not show: mainnet behaviour, SPL tokens (not built), or behaviour under load. Devnet only.
