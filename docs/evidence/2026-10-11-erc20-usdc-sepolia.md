# A real ERC-20 stablecoin moved from a threshold key on a public chain (2026-10-11)

Before this, the stablecoin controls (token registry, on-chain verification of symbol and decimals, the
platform encoding the ERC-20 call, policy evaluated on the decoded recipient and amount) had only been
exercised against a private chain and a test token. This runs the same path against Circle's real test USDC
contract on Ethereum Sepolia.

| | |
|---|---|
| Network | Ethereum Sepolia (11155111) |
| Token | USDC `0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238`, registered by an operator and **verified on-chain by the platform** (symbol and decimals read from the contract) before it could be used |
| From | `0xc378bf1A3085000AeD970aFc3fBBc6F0240fd129` (2-of-3 threshold ECDSA key) |
| To | `0x000000000000000000000000000000000000dEaD` |
| Amount | 1.00 USDC (1,000,000 base units) via `POST /keys/:id/token-transfers`; the platform built the calldata |
| Signed by | parties 1 and 2 of 3 |
| Transaction | `0xb0d70ac3f9c5439a466396c153a6235d81188dbc88670c52bcc19497ee21ccaa` |
| Block | 11890265, receipt status success |
| Explorer | https://sepolia.etherscan.io/tx/0xb0d70ac3f9c5439a466396c153a6235d81188dbc88670c52bcc19497ee21ccaa |

Run with `infrastructure/kind/erc20-sepolia-test.sh` on the staging deployment (commit `3142a84`). Success was
checked two ways beyond the receipt: the USDC contract's own `Transfer` event carried exactly 1,000,000 base
units, and the recipient's token balance rose by exactly that amount.

Shows: the stablecoin path (registry, on-chain verification, platform-encoded transfer, threshold signature)
works against a real token contract on a public chain. Does not show: a mainnet stablecoin, rand-pegged tokens,
or behaviour under load. Circle's Sepolia USDC is a test token with no value.
