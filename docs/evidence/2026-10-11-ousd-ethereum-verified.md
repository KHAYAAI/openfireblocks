# OUSD (Open Standard's Open USD) registered and verified against the live Ethereum contract (2026-10-11)

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

OUSD is dollar-pegged, so the existing USD ceilings apply (1,000,000 global, 100,000 pro, 10,000 free per
transaction) and no policy change was needed, unlike EURC.

Not yet done, and not claimed: an OUSD transfer (it would need real OUSD and gas on a mainnet, and no test-network
deployment is known); Base (`0xB2000000000000000000002fEb517dFeC7415344`) and Solana (`ousd2mJsPEckLHcSCDxyKD7NDGARZcfLbDZkKiatYHB`)
were not checked by the platform. The platform's token transfers are EVM-only; Solana SPL tokens are not supported.
The contract's features (any blocklist, pause or upgrade function) were not reviewed. Wording to use: "OUSD is
registered and verified on-chain against the real contract on Ethereum"; not "OUSD transfers proven".
