package main

import (
	"log"
	"os"
	"strconv"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"forge-crypto/temporal-worker/activities"
	"forge-crypto/temporal-worker/workflows"
)

// The temporal-worker hosts the transaction settlement workflow and its
// activities. It connects to the Temporal frontend and polls the
// "transaction-settlement" task queue. The api-gateway starts workflows on the
// same queue via a Temporal client.

const taskQueue = "transaction-settlement"

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	hostPort := getenv("TEMPORAL_HOSTPORT", "localhost:7233")
	namespace := getenv("TEMPORAL_NAMESPACE", "default")

	c, err := client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if err != nil {
		log.Fatalf("failed to connect to Temporal at %s: %v", hostPort, err)
	}
	defer c.Close()

	confirmations := int64(3)
	if v := os.Getenv("REQUIRED_CONFIRMATIONS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			confirmations = n
		}
	}

	acts := activities.NewActivities(
		getenv("POLICY_SERVICE_URL", "http://localhost:8081"),
		getenv("MPC_SIGNER_URL", "http://localhost:8080"),
		getenv("ETHEREUM_RPC_SEPOLIA", "http://localhost:8545"),
		confirmations,
	)

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(workflows.TransactionSettlementWorkflow)
	w.RegisterActivity(acts)

	log.Printf("temporal-worker polling task queue %q at %s/%s", taskQueue, hostPort, namespace)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}
