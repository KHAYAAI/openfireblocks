// Command starter triggers a single TransactionSettlementWorkflow. Useful for
// local demos and smoke tests once the worker and Temporal server are running:
//
//	go run ./cmd/starter
//
// Configuration via the same TEMPORAL_* env vars as the worker.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.temporal.io/sdk/client"

	"forge-crypto/temporal-worker/workflows"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	c, err := client.Dial(client.Options{
		HostPort:  getenv("TEMPORAL_HOSTPORT", "localhost:7233"),
		Namespace: getenv("TEMPORAL_NAMESPACE", "default"),
	})
	if err != nil {
		log.Fatalf("dial temporal: %v", err)
	}
	defer c.Close()

	req := workflows.TransactionRequest{
		CustomerID:   getenv("CUSTOMER_ID", "demo"),
		CustomerTier: getenv("CUSTOMER_TIER", "pro"),
		ChainID:      11155111,
		To:           getenv("TO", "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"),
		Value:        getenv("VALUE", "0"),
		GasLimit:     21000,
		GasPrice:     getenv("GAS_PRICE", "20000000000"),
		Nonce:        0,
	}

	opts := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("settlement-%d", time.Now().UnixNano()),
		TaskQueue: "transaction-settlement",
	}

	we, err := c.ExecuteWorkflow(context.Background(), opts, workflows.TransactionSettlementWorkflow, req)
	if err != nil {
		log.Fatalf("start workflow: %v", err)
	}
	log.Printf("started workflow id=%s runID=%s", we.GetID(), we.GetRunID())

	var result workflows.TransactionResult
	if err := we.Get(context.Background(), &result); err != nil {
		log.Fatalf("workflow failed: %v", err)
	}
	log.Printf("workflow result: status=%s txHash=%s block=%d reason=%s",
		result.Status, result.TxHash, result.BlockNumber, result.Reason)
}
