package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/open-policy-agent/opa/rego"
)

// policy-service evaluates OPA/Rego policies for every transaction the platform
// is asked to sign. The api-gateway calls POST /evaluate before signing; this
// service is the single source of truth for amount limits, whitelist, approval
// and geographic rules.
//
// Policies are embedded into the binary so the container is self-contained and
// the deployed policy set is immutable and auditable.

//go:embed policies/*.rego
var policyFS embed.FS

// PolicyRequest mirrors the api-gateway PolicyService client payload.
type PolicyRequest struct {
	CustomerID       string   `json:"customerId"`
	CustomerTier     string   `json:"customerTier"`
	To               string   `json:"to"`
	Value            string   `json:"value"` // wei, base-10 string
	ChainID          int      `json:"chainId"`
	Whitelist        []string `json:"whitelist"`
	BlockedCountries []string `json:"blockedCountries"`
	Country          string   `json:"country"`
}

// PolicyDecision is the response consumed by the api-gateway.
type PolicyDecision struct {
	Approved         bool     `json:"approved"`
	Denials          []string `json:"denials"`
	RequiresApproval bool     `json:"requiresApproval"`
	Reason           string   `json:"reason"`
}

// evaluator holds a prepared OPA query reused across requests.
type evaluator struct {
	query rego.PreparedEvalQuery
}

func newEvaluator(ctx context.Context) (*evaluator, error) {
	modules := map[string]string{}
	entries, err := policyFS.ReadDir("policies")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := "policies/" + e.Name()
		content, err := policyFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		modules[name] = string(content)
	}

	opts := []func(*rego.Rego){rego.Query("data.policies")}
	for name, src := range modules {
		opts = append(opts, rego.Module(name, src))
	}

	pq, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare policy query: %w", err)
	}
	return &evaluator{query: pq}, nil
}

// evaluate converts the request into rego input, runs the policies and folds the
// deny/require_approval sets into a decision.
func (e *evaluator) evaluate(ctx context.Context, req *PolicyRequest) (*PolicyDecision, error) {
	valueWei, err := weiToFloat(req.Value)
	if err != nil {
		return nil, err
	}

	input := map[string]interface{}{
		"value_wei":         valueWei,
		"customer_tier":     req.CustomerTier,
		"to":                req.To,
		"whitelist":         req.Whitelist,
		"blocked_countries": req.BlockedCountries,
		"country":           req.Country,
	}

	rs, err := e.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, err
	}

	denials := []string{}
	requiresApproval := false
	if len(rs) > 0 && len(rs[0].Expressions) > 0 {
		if doc, ok := rs[0].Expressions[0].Value.(map[string]interface{}); ok {
			denials = toStringSlice(doc["deny"])
			requiresApproval = len(toStringSlice(doc["require_approval"])) > 0
		}
	}

	decision := &PolicyDecision{
		Approved:         len(denials) == 0,
		Denials:          denials,
		RequiresApproval: requiresApproval,
	}
	if decision.Approved {
		decision.Reason = "approved"
		if requiresApproval {
			decision.Reason = "approved, manual approval required"
		}
	} else {
		decision.Reason = fmt.Sprintf("%d policy violation(s)", len(denials))
	}
	return decision, nil
}

// weiToFloat parses a base-10 wei string into a float64 for rego comparison.
// Empty / "0" map to 0.
func weiToFloat(s string) (float64, error) {
	if s == "" || s == "0" {
		return 0, nil
	}
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return 0, fmt.Errorf("invalid wei value: %q", s)
	}
	f, _ := new(big.Float).SetInt(bi).Float64()
	return f, nil
}

func toStringSlice(v interface{}) []string {
	out := []string{}
	if v == nil {
		return out
	}
	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func main() {
	ctx := context.Background()
	eval, err := newEvaluator(ctx)
	if err != nil {
		log.Fatalf("failed to init policy evaluator: %v", err)
	}
	log.Print("policy engine loaded")

	mux := http.NewServeMux()

	mux.HandleFunc("/evaluate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST", http.StatusMethodNotAllowed)
			return
		}
		var req PolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		decision, err := eval.evaluate(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decision)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("policy-service listening on :%s", port)
	log.Fatal(srv.ListenAndServe())
}
