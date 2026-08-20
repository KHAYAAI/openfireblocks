package main

import (
	"database/sql"
	"log"
	"os"
	"strconv"

	_ "github.com/lib/pq"
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

	// NewActivities accepts a nil *sql.DB (round persistence just stays
	// disabled), so a database outage delays startup rather than blocking
	// it -- log and continue with db == nil instead of log.Fatalf.
	dsn := getenv("DATABASE_URL", "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Printf("failed to open database connection, ceremony round persistence disabled: %v", err)
		db = nil
	} else if err := db.Ping(); err != nil {
		log.Printf("database ping failed, ceremony round persistence disabled: %v", err)
		db = nil
	}

	acts := activities.NewActivities(
		getenv("POLICY_SERVICE_URL", "http://localhost:8081"),
		getenv("MPC_SIGNER_URL", "http://localhost:8080"),
		getenv("ETHEREUM_RPC_SEPOLIA", "http://localhost:8545"),
		confirmations,
		db,
	)

	w := worker.New(c, taskQueue, worker.Options{})

	// Phase 1: Transaction settlement workflows
	w.RegisterWorkflow(workflows.TransactionSettlementWorkflow)

	// Phase 2: DKG ceremony workflows
	w.RegisterWorkflow(workflows.DKGCeremonyWorkflow)
	w.RegisterWorkflow(workflows.ThresholdSigningWorkflow)
	w.RegisterWorkflow(workflows.KeyRotationWorkflow)

	// Register all activities
	w.RegisterActivity(acts)

	log.Printf("temporal-worker polling task queue %q at %s/%s", taskQueue, hostPort, namespace)
	log.Printf("registered workflows: TransactionSettlement, DKGCeremony, ThresholdSigning, KeyRotation")
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}
