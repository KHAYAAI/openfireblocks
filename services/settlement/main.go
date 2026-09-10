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

	svc := NewSettlementService(db)

	if ethRPC := os.Getenv("ETHEREUM_RPC_URL"); ethRPC != "" {
		client, err := NewEthereumClient(ethRPC)
		if err != nil {
			log.Printf("failed to connect Ethereum client, ethereum settlements disabled: %v", err)
		} else {
			svc.RegisterChain("ethereum", client)
			svc.RegisterChain("polygon", client)
		}
	} else {
		log.Printf("ETHEREUM_RPC_URL not set; ethereum/polygon settlements disabled")
	}

	if btcRPC := os.Getenv("BITCOIN_RPC_URL"); btcRPC != "" {
		svc.RegisterChain("bitcoin", NewBitcoinClient(btcRPC, os.Getenv("BITCOIN_RPC_USER"), os.Getenv("BITCOIN_RPC_PASS")))
	} else {
		log.Printf("BITCOIN_RPC_URL not set; bitcoin settlements disabled")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"settlement"}`))
	})
	mux.HandleFunc("/v1/settle", svc.HandleSettle)
	mux.HandleFunc("/v1/settlements/get", svc.HandleGetSettlement)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}
	log.Printf("settlement service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
