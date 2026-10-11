# Bitcoin: a threshold-signed spend accepted by the public testnet4 network (2026-10-11)

**Status: mined.** Included in testnet4 block 156069. It was first accepted into the public mempool, then
confirmed once testnet4 mined its next block (the chain had stalled at 156067 for hours).

Before this, Bitcoin had only been driven against a private regtest node. Solana, Ethereum, Cosmos and ERC-20
tokens already had public-network evidence; this is the same for Bitcoin.

| | |
|---|---|
| Network | Bitcoin testnet4 (Bitcoin Core 28 node run by the platform, fully synced to block 156067) |
| From | `tb1qqaezcr07azvwql2dp807x9cd9q2lyx06hz5slx` (2-of-3 threshold ECDSA key, native segwit) |
| To | `tb1q05ckv66ac5du44dc3s227xcye25vrrtzxq7a9r` (a second 2-of-3 platform key) |
| Funding | 339,159 sats from a public faucet, confirmed on testnet4 before the spend (the platform's node saw it through `scantxoutset`) |
| Amount | 10,000 sats via `POST /keys/:id/bitcoin-transactions`; the platform chose the coin, set the fee, built and hashed the transaction |
| Fee | 282 sats at 2 sat/vB, 140 virtual bytes |
| Signed by | parties 1 and 2 of 3 |
| Transaction | `280f69b1cc8d0a208c43f61d171d6e5e149f43a465527dabe0d7d203f0703c1a` |
| Block | 156069, hash `00000000000000002bb0351b1890578335f0c10d9925f1a4290f37738ed091e8`; the recipient's confirmed balance rose from 0 to 10,000 sats (read from the platform's own node) |
| Explorer | https://mempool.space/testnet4/tx/280f69b1cc8d0a208c43f61d171d6e5e149f43a465527dabe0d7d203f0703c1a |

Checked independently of the platform: the public explorer (mempool.space) returned the transaction with fee 282
and size 140 vbytes, unconfirmed while it waited, then confirmed in block 156069. The platform's own node also
accepted it into its mempool and later saw the recipient's balance.

Run with `infrastructure/kind/bitcoin-testnet4-test.sh` on the staging deployment (node set up by
`bitcoin-testnet4-setup.sh`). The test printed PASS only after the recipient's confirmed balance rose.

Shows: the Bitcoin path (address derivation on testnet, UTXO discovery on a real node, coin selection, fee, BIP143
sighash, two-of-three threshold signature, broadcast) produces a transaction the real network accepts. Does not
show: mainnet Bitcoin, behaviour under load, or fee estimation (a fee rate was given explicitly because a fresh
node has no fee history). testnet4 coins have no value. testnet4 blocks are irregular, so confirmation time here
says nothing about mainnet.
