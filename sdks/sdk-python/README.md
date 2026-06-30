# OpenFireblocks Python SDK

Dependency-free client for the OpenFireblocks API (Python 3.8+, stdlib only).

```python
from openfireblocks import OpenFireblocksClient, OpenFireblocksError

client = OpenFireblocksClient("https://api.openfireblocks.example", "YOUR_API_KEY")

try:
    res = client.sign({
        "chainId": 11155111,
        "to": "0x742d35Cc6634C0532925a3b844Bc9e7595f42bE",
        "value": "0",
        "gasLimit": 21000,
        "gasPrice": "20000000000",
        "nonce": 0,
    })
    print(res["txHash"], res["status"])
except OpenFireblocksError as e:
    print(e.status, e.body)  # e.g. 403 {'error': 'policy denied'}
```

```bash
python -m unittest discover -s tests
```
