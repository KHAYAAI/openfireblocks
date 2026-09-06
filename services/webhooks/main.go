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

	svc := NewWebhookService(db)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"webhooks"}`))
	})
	mux.HandleFunc("/v1/events", svc.HandlePublishEvent)
	mux.HandleFunc("/v1/deliveries", svc.HandleGetDeliveries)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8086"
	}
	log.Printf("webhooks service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
