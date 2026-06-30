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

Non-2xx responses return `*APIError` with `StatusCode` and `Body`.

```bash
go test ./...
```
