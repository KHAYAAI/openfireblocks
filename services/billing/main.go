package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	db, err := NewPostgresDB()
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	stripe := NewStripeClient(os.Getenv("STRIPE_API_KEY"))
	svc := NewBillingService(db, stripe)

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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}
	log.Printf("billing service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
