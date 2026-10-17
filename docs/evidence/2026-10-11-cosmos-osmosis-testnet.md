# Osmosis testnet accepted a threshold-signed transfer (2026-10-11)

Before this, the Cosmos path had only been exercised against a stand-in node. This is the first
transaction a public Cosmos SDK network has confirmed that was signed by the platform's threshold key.

| | |
|---|---|
| Network | Osmosis testnet `osmo-test-5`, through its public LCD (`https://lcd.testnet.osmosis.zone`), via the platform's own signer |
| Sending key | `osmo1qyfjpl94cjp3pnz96a4hvf483rv3dc3mx4zj5p` (2-of-3 threshold secp256k1, created by DKG across three pods) |
| Recipient | `osmo14k3084ltt3d2prle2jg8c96sm3gtqfwgf8usyn` (a second threshold key) |
| Amount | 1000 uosmo, bank send in `SIGN_MODE_DIRECT` |
| Signed by | parties 1 and 2 of 3 |
| Transaction | `4C7F0A6B84932D01296A66011A7E091094F200D63278D5FFF691F2173A0FA363` |
| Confirmation | included at height 75734857; the node's parsed send matches sender, recipient, denom and amount |
| Verify | `https://lcd.testnet.osmosis.zone/cosmos/tx/v1beta1/txs/4C7F0A6B84932D01296A66011A7E091094F200D63278D5FFF691F2173A0FA363` |

Run with `infrastructure/kind/native-chain-test.sh cosmos` (with `DENOM=uosmo`) on the staging deployment.

## Findings on the way
1. The first attempt was rejected by the chain: "insufficient fees; got 5000uosmo, required 6000uosmo". The default
   gas price (0.025) is below Osmosis testnet's (0.03). **Each Cosmos chain needs its own gas price configured**
   (`mpcSigner.cosmos.gasPrice`); it was set to 0.05, costing about 10,000 uosmo per transfer. A mock node could
   not have surfaced this.
2. As with Sepolia and Solana, the platform required Travel Rule details for a native transfer, because it has
   no price source for the coin.

## What this does and does not show
- Shows: the Cosmos transaction encoding and the threshold signature are accepted by a real public network.
- Does not show: mainnet, IBC, staking or CosmWasm (not offered), or behaviour under load.
