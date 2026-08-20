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

	svc := NewPolicyService(db)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"policy"}`))
	})
	mux.HandleFunc("/v1/policies", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			svc.HandleCreatePolicy(w, r)
			return
		}
		svc.HandleListPolicies(w, r)
	})
	mux.HandleFunc("/v1/policies/get", svc.HandleGetPolicy)
	mux.HandleFunc("/v1/policies/evaluate", svc.HandleEvaluatePolicy)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	log.Printf("policy service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
