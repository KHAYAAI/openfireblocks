# OpenFireblocks Python SDK

Dependency-free client for the OpenFireblocks API (Python 3.8+, stdlib only).

```python
from openfireblocks import OpenFireblocksClient, OpenFireblocksError

client = OpenFireblocksClient("https://api.openfireblocks.example", "YOUR_API_KEY")

try:
    res = client.sign({
        "chainId": 11155111,
        "to": "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
        "value": "0",
        "gasLimit": 21000,
        "gasPrice": "20000000000",
        "nonce": 0,
    })
    print(res["txHash"], res["status"])
except OpenFireblocksError as e:
    print(e.status, e.body)  # e.g. 403 {'error': 'policy denied'}
```

## Multi-Chain Signing

Sign transactions on multiple blockchains (Ethereum, Bitcoin, Solana, Cosmos):

```python
from openfireblocks import OpenFireblocksClient

client = OpenFireblocksClient("https://api.openfireblocks.example", "YOUR_API_KEY")

# Get supported chains
chains = client.get_supported_chains()
print(chains["chains"])  # ['ethereum', 'bitcoin', 'solana', 'cosmos-hub']

# Sign on Ethereum
eth_res = client.sign_multi_chain({
    "chainId": "ethereum",
    "message": "0xdeadbeef",
    "metadata": {
        "network": "mainnet",
    },
})
print(eth_res["signature"], eth_res["from"])

# Sign on Bitcoin
btc_res = client.sign_multi_chain({
    "chainId": "bitcoin",
    "message": "0x...",
    "metadata": {
        "network": "testnet",
        "utxos": [{"txid": "...", "vout": 0, "amount": 50000}],
    },
})

# Sign on Solana
solana_res = client.sign_multi_chain({
    "chainId": "solana",
    "message": "0x...",
    "metadata": {
        "recentBlockhash": "...",
    },
})

# Sign on Cosmos
cosmos_res = client.sign_multi_chain({
    "chainId": "cosmos-hub",
    "message": "0x...",
    "metadata": {
        "account_number": 123,
        "sequence": 0,
    },
})

# Broadcast a signed transaction
broadcast_res = client.broadcast_transaction({
    "chainId": "ethereum",
    "signedTx": eth_res["signedTx"],
})
print(broadcast_res["txHash"])
```

```bash
python -m unittest discover -s tests
```
