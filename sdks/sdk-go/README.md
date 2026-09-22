# OpenFireblocks Go SDK

Standard-library-only client for the OpenFireblocks API.

```go
import (
	"context"
	"fmt"
	ofb "github.com/openfireblocks/sdk-go"
)

func main() {
	c := ofb.New("https://api.openfireblocks.example", "YOUR_API_KEY")

	res, err := c.Sign(context.Background(), ofb.SignRequest{
		ChainID:  11155111,
		To:       "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
		Value:    "0",
		GasLimit: 21000,
		GasPrice: "20000000000",
		Nonce:    0,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.TxHash, res.Status)
}
```

## Multi-Chain Signing

Sign transactions on multiple blockchains (Ethereum, Bitcoin, Solana, Cosmos):

```go
// Get supported chains
chains, err := c.GetSupportedChains(context.Background())
if err != nil {
	panic(err)
}
fmt.Println(chains.Chains) // [ethereum bitcoin solana cosmos-hub]

// Sign on Ethereum
ethRes, err := c.SignMultiChain(context.Background(), &ofb.SignMultiChainRequest{
	ChainID: "ethereum",
	Message: "0xdeadbeef",
	Metadata: map[string]interface{}{
		"network": "mainnet",
	},
})
if err != nil {
	panic(err)
}
fmt.Println(ethRes.Signature, ethRes.From)

// Sign on Bitcoin
btcRes, err := c.SignMultiChain(context.Background(), &ofb.SignMultiChainRequest{
	ChainID: "bitcoin",
	Message: "0x...",
	Metadata: map[string]interface{}{
		"network": "testnet",
		"utxos": []map[string]interface{}{
			{"txid": "...", "vout": 0, "amount": 50000},
		},
	},
})
if err != nil {
	panic(err)
}

// Broadcast a signed transaction
broadcastRes, err := c.BroadcastTransaction(context.Background(), &ofb.BroadcastRequest{
	ChainID: "ethereum",
	SignedTx: ethRes.SignedTx,
})
if err != nil {
	panic(err)
}
fmt.Println(broadcastRes.TxHash)
```

Non-2xx responses return `*APIError` with `StatusCode` and `Body`.

```bash
go test ./...
```
