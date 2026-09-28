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

	svc := NewMarketplaceService(db)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"marketplace"}`))
	})
	mux.HandleFunc("/v1/integrations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			svc.HandleCreateIntegration(w, r)
			return
		}
		svc.HandleListIntegrations(w, r)
	})
	mux.HandleFunc("/v1/integrations/get", svc.HandleGetIntegration)
	mux.HandleFunc("/v1/integrations/test", svc.HandleTestIntegration)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8087"
	}
	log.Printf("marketplace service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
