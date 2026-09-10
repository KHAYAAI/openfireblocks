package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	// One binary, two jobs: serve, or run the month-end sweep once and
	// exit. The CronJob uses the second, so the scheduled work runs the
	// same code the service does rather than a shell script that
	// reimplements the rule -- and there is no question about how a
	// CronJob authenticates to an internal endpoint, because it does not
	// make a request at all.
	runBilling := flag.Bool("run-billing", false,
		"raise invoices for every subscription whose period has ended, then exit")
	flag.Parse()

	db, err := NewPostgresDB()
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	stripe := NewStripeClient(os.Getenv("STRIPE_API_KEY"))
	svc := NewBillingService(db, stripe)

	if *runBilling {
		// A generous ceiling rather than no ceiling: a sweep that hangs on
		// a wedged database connection would otherwise hold the CronJob's
		// concurrencyPolicy: Forbid open forever, and every subsequent
		// night would be skipped without anything looking wrong.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		result, err := svc.RunBilling(ctx, time.Now())
		if err != nil {
			log.Fatalf("billing run failed: %v", err)
		}
		// The whole result to stdout, so `kubectl logs` on the job is a
		// reconcilable record of which subscriptions were billed what.
		encoded, _ := json.MarshalIndent(result, "", "  ")
		log.Printf("billing run complete:\n%s", encoded)

		// Non-zero when any subscription failed, so the Job is marked
		// failed and shows up in failedJobsHistory rather than looking like
		// a clean night. The successful invoices are already committed --
		// this reports, it does not roll back.
		if len(result.Failed) > 0 {
			os.Exit(1)
		}
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"billing"}`))
	})
	mux.HandleFunc("/v1/subscribe", svc.HandleSubscribe)
	mux.HandleFunc("/v1/usage", svc.HandleGetUsage)
	// The chain that turns platform activity into collected money:
	// measure what was used, raise an invoice for it, charge it.
	mux.HandleFunc("/v1/usage/measure", svc.HandleMeasureUsage)
	mux.HandleFunc("/v1/invoices/generate", svc.HandleGenerateInvoice)
	mux.HandleFunc("/v1/invoices", svc.HandleListInvoices)
	mux.HandleFunc("/v1/invoices/charge", svc.HandleChargeInvoice)
	// What a schedule calls. Everything above is per-subscription and
	// driven by a caller who knows which one; this is the month-end sweep.
	mux.HandleFunc("/v1/billing/run", svc.HandleRunBilling)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}
	log.Printf("billing service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
