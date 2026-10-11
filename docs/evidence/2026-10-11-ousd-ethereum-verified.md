# OUSD (Open Standard's Open USD) registered and verified against the live Ethereum and Base contracts (2026-10-11)

**Read-only.** No transaction was made, no key was funded and no value moved. This shows that the platform's token
registry accepts the real OUSD contract and that the platform itself reads the contract's symbol and decimals. It
does **not** show an OUSD transfer.

| | |
|---|---|
| Network | Ethereum mainnet (chain id 1), read access only via a public RPC |
| Contract | `0x9f6F3991D525015a6F8CaF062C83b62fD3AC4436` (the address given on Open Standard's integration page and also listed by CoinGecko) |
| Read directly from the contract | name `OpenUSD`, symbol `OUSD`, decimals 6, total supply 123,767,235.43 OUSD (raw 123767235430000) |
| Registered as | symbol OUSD, peg USD, issuer "Open Standard" (the operator's entry), token id `d6d1bc7e-2c98-4bee-b761-5f8eddd7ced6` |
| Platform verification | status `verified`: the platform called the contract and confirmed symbol and decimals match |

Run with `infrastructure/kind/token-verify-readonly.sh` on the staging deployment (commit `a9b3406`).

## Base (added the same day)

| | |
|---|---|
| Network | Base mainnet (chain id 8453), read access only via a public RPC |
| Contract | `0xB2000000000000000000002fEb517dFeC7415344` (given with the Ethereum and Solana addresses for OUSD) |
| Registered as | symbol OUSD, peg USD, decimals 6, issuer "Open Standard", token id `39d26dbc-1d95-4a46-82d6-612568f7fa38` |
| Platform verification | status `verified`: symbol and decimals read from the Base contract match |

Run with `CHAIN_ID=8453 TOKEN_ADDRESS=0xB2000000000000000000002fEb517dFeC7415344 token-verify-readonly.sh`. Read-only: no transaction, no funded key.

OUSD is dollar-pegged, so the existing USD ceilings apply (1,000,000 global, 100,000 pro, 10,000 free per
transaction) and no policy change was needed, unlike EURC.

Not yet done, and not claimed: an OUSD transfer (it would need real OUSD and gas on a mainnet, and no test-network
deployment is known); Solana (`ousd2mJsPEckLHcSCDxyKD7NDGARZcfLbDZkKiatYHB`)
was not checked by the platform. The platform's token transfers are EVM-only; Solana SPL tokens are not supported.
The contract's features (any blocklist, pause or upgrade function) were not reviewed. Wording to use: "OUSD is
registered and verified on-chain against the real contracts on Ethereum and Base"; not "OUSD transfers proven".
