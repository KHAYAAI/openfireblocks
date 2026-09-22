package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/open-policy-agent/opa/rego"
	"github.com/open-policy-agent/opa/storage/inmem"
)

//go:embed sanctions.json
var sanctionsJSON []byte

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
	CustomerID   string `json:"customerId"`
	CustomerTier string `json:"customerTier"`

	// To is who receives the value, which for a token transfer is the
	// ERC-20 recipient decoded out of the calldata -- NOT the transaction's
	// own `to`, which is the token contract and is identical for every
	// transfer of that token. Sending the contract address here is what
	// made a counterparty whitelist meaningless for stablecoins: it either
	// allowed every transfer of that token or none of them.
	To string `json:"to"`

	// Value is the native value attached to the transaction, in wei. For a
	// token transfer this is legitimately "0" -- the money is in the
	// calldata -- so the amount rules that read it are not the ones that
	// govern a stablecoin.
	Value   string `json:"value"`
	ChainID int    `json:"chainId"`

	// What actually moves. Asset is "NATIVE" for an ordinary transfer, or a
	// registry symbol such as USDC or ZARP.
	//
	// These exist because the wei-denominated limits above cannot govern a
	// token: 50,000 USDC is 50,000,000,000 base units, which compared
	// against a hundred-ETH ceiling expressed in wei (1e20) passes without
	// coming close. The units are not comparable and treating them as if
	// they were is worse than having no limit, because it looks like one.
	Asset         string `json:"asset"`
	AssetAmount   string `json:"assetAmount"`
	AssetDecimals int    `json:"assetDecimals"`

	// PegCurrency is the ISO code a pegged token claims to track, empty for
	// anything unpegged. When set, the amount is evaluated in that currency
	// against limits denominated in it.
	PegCurrency string `json:"pegCurrency"`

	// IsAllowance marks an approve() call: nothing moves now, and the
	// spender may move AssetAmount whenever they choose. Evaluated as the
	// exposure it is rather than as a zero-value call.
	IsAllowance bool `json:"isAllowance"`

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
	// Where the screening list came from and how old it is. Held so
	// /evaluate can refuse once it is too stale to mean anything, and so
	// /health can say so before it gets there. See sanctions_source.go.
	sanctions *sanctionsSource
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

	// The sanctions list, from the synced file when one is configured and
	// from the build-time copy otherwise. Loading through sanctionsSource
	// rather than unmarshalling here is what makes the list's age a thing
	// this service knows about instead of a thing nobody can see.
	source, err := loadSanctionsSource(os.Getenv, sanctionsJSON)
	if err != nil {
		return nil, err
	}
	// Lowercased at load (normaliseList), so the Rego comparison is
	// against normalised data and cannot be defeated by casing.
	addresses := make([]interface{}, 0, len(source.list.Addresses))
	for _, a := range source.list.Addresses {
		addresses = append(addresses, a)
	}
	store := inmem.NewFromObject(map[string]interface{}{
		"sanctions": map[string]interface{}{"addresses": addresses},
	})

	opts := []func(*rego.Rego){rego.Query("data.policies"), rego.Store(store)}
	for name, src := range modules {
		opts = append(opts, rego.Module(name, src))
	}

	pq, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare policy query: %w", err)
	}
	return &evaluator{query: pq, sanctions: source}, nil
}

// evaluate converts the request into rego input, runs the policies and folds the
// deny/require_approval sets into a decision.
func (e *evaluator) evaluate(ctx context.Context, req *PolicyRequest) (*PolicyDecision, error) {
	valueWei, err := weiToFloat(req.Value)
	if err != nil {
		return nil, err
	}

	asset := req.Asset
	if asset == "" {
		asset = "NATIVE"
	}

	// The amount in the peg currency, for the rules that work in money.
	//
	// Only computed for a pegged token. An unpegged one has no currency
	// value this service can assert, and inventing one -- by pretending the
	// base units are dollars, say -- would produce a limit check that reads
	// as authoritative and is arbitrary.
	pegged, havePegged, err := peggedAmount(req)
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
		"asset":             asset,
		"is_token":          asset != "NATIVE",
		"is_allowance":      req.IsAllowance,
		"peg_currency":      req.PegCurrency,
		"has_pegged_value":  havePegged,
		"pegged_value":      pegged,
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

// peggedAmount converts a token amount in base units into its peg currency.
//
// Returns ok=false when there is nothing to convert: a native transfer, or
// a token with no peg. Those are governed by other rules, and a zero here
// must not be mistaken for "worth nothing" -- hence the separate flag
// rather than a sentinel value.
//
// Done in big.Float, not by dividing float64s. A token amount is an exact
// integer of base units and 10^18 is well beyond what a float64 holds
// exactly, so converting first and dividing second loses the low digits of
// every large amount -- precisely the amounts a limit exists to catch.
// The result is narrowed to a float64 only at the end, because rego
// compares JSON numbers; at that point the value is a quantity of currency
// rather than of base units, and a float64 is exact to the cent well past
// any limit a custody platform would set.
func peggedAmount(req *PolicyRequest) (float64, bool, error) {
	if req.PegCurrency == "" || req.AssetAmount == "" {
		return 0, false, nil
	}
	if req.AssetDecimals < 0 || req.AssetDecimals > 36 {
		return 0, false, fmt.Errorf("implausible decimals %d for asset %q", req.AssetDecimals, req.Asset)
	}

	units, ok := new(big.Int).SetString(req.AssetAmount, 10)
	if !ok {
		// Not silently treated as zero. An unparseable amount that
		// evaluated to zero would pass every limit in this file, which is
		// the exact bypass the gateway's own amount parser was hardened
		// against.
		return 0, false, fmt.Errorf("invalid asset amount: %q", req.AssetAmount)
	}
	if units.Sign() < 0 {
		return 0, false, fmt.Errorf("negative asset amount: %q", req.AssetAmount)
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(req.AssetDecimals)), nil)
	value := new(big.Float).SetPrec(256).Quo(
		new(big.Float).SetPrec(256).SetInt(units),
		new(big.Float).SetPrec(256).SetInt(scale),
	)

	f, _ := value.Float64()
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false, fmt.Errorf("asset amount %q does not convert to a finite value", req.AssetAmount)
	}
	return f, true, nil
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
	initial, err := newEvaluator(ctx)
	if err != nil {
		log.Fatalf("failed to init policy evaluator: %v", err)
	}
	log.Print("policy engine loaded")

	// Held behind a swap so the sanctions list can be re-read without a
	// restart -- a CronJob rewrites the file daily, and a process that
	// only reads it at boot would refuse transactions over a file that
	// had in fact been updated. See sanctions_reload.go.
	live := newLiveEvaluator(initial)
	startSanctionsWatcher(ctx, live)

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
		// Screening stops being screening once the list is old enough,
		// and this is where that becomes a decision rather than a
		// silently weaker check. Deliberately before evaluate(): a
		// deny produced here must not be confused with one produced by
		// the policies, because they mean opposite things to an
		// operator -- "this transaction is not allowed" versus "this
		// platform can no longer tell you whether it is".
		eval := live.get()
		if st := eval.sanctions.status(time.Now()); !st.OK {
			log.Printf("refusing to evaluate: %s", st.Reason)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(&PolicyDecision{
				Approved: false,
				Denials:  []string{"sanctions_list_stale"},
				Reason:   "sanctions screening unavailable: " + st.Reason,
			})
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
		// Reports the screening list's age whether or not it is a
		// problem yet. A control whose freshness can only be discovered
		// by it failing is one nobody notices decaying.
		status := "ok"
		eval := live.get()
		st := eval.sanctions.status(time.Now())
		if !st.OK {
			status = "degraded"
		}
		w.Header().Set("Content-Type", "application/json")
		if !st.OK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    status,
			"sanctions": eval.sanctions.describe(time.Now()),
		})
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

	tlsConfig, mtlsEnabled, err := serverTLSConfigFromEnv()
	if err != nil {
		log.Fatalf("mTLS configuration error: %v", err)
	}
	if mtlsEnabled {
		srv.TLSConfig = tlsConfig
		log.Printf("policy-service listening on :%s (mTLS: client certs required)", port)
		log.Fatal(srv.ListenAndServeTLS("", "")) // certs already loaded into TLSConfig
	}

	log.Printf("policy-service listening on :%s (mTLS disabled: %s/%s/%s not all set)",
		port, envMTLSCertFile, envMTLSKeyFile, envMTLSCAFile)
	log.Fatal(srv.ListenAndServe())
}
