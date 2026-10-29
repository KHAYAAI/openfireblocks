package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// Golden decisions: the guard for OPA upgrades and Rego rewrites.
//
// Every policy decision this service can make is a function of a handful of
// inputs, and the dangerous changes are the quiet ones: an OPA release that
// parses a rule differently, or a syntax migration that turns a partial set
// into something else, leaves every service green and allows a transfer
// that used to be refused. This runs a grid of requests across the edges
// that matter (each tier on each side of each limit, sanctioned, whitelisted
// and blocked-country destinations, pegged, unpegged and unknown-currency
// tokens, allowances bounded and unlimited) and compares every decision,
// reasons included, with testdata/decisions.golden.json.
//
// A deliberate policy change regenerates the file with
//
//	go test -run TestPolicyDecisionsMatchGolden -update-golden
//
// and the diff of that file is the review of what the change does.

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/decisions.golden.json from the current policies")

const goldenPath = "testdata/decisions.golden.json"

func decisionCases() map[string]PolicyRequest {
	const (
		plain       = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
		other       = "0x1111111111111111111111111111111111111111"
		sanctioned  = "0x8589427373D6D84E98730D7795D8f6f8731FDA16"
		eth         = "000000000000000000" // 18 zeros
		maxUint256  = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
		usdcDecimal = 6
	)
	cases := map[string]PolicyRequest{}

	// Native transfers: each tier on both sides of each wei limit.
	values := map[string]string{
		"empty": "", "zero": "0", "1wei": "1",
		"10eth": "10" + eth, "10eth+1": "10" + eth[:len(eth)-1] + "1",
		"11eth": "11" + eth, "50eth": "50" + eth, "51eth": "51" + eth,
		"100eth": "100" + eth, "101eth": "101" + eth, "1000eth": "1000" + eth,
	}
	for _, tier := range []string{"free", "pro", "enterprise", ""} {
		for vname, v := range values {
			cases[fmt.Sprintf("native/%s/%s", orNone(tier), vname)] = PolicyRequest{
				CustomerTier: tier, To: plain, Value: v, ChainID: 1,
			}
		}
	}

	// Destinations: sanctions (any casing), whitelists, blocked countries.
	dest := map[string]PolicyRequest{
		"sanctioned":                  {To: sanctioned},
		"sanctioned-lowercase":        {To: strings.ToLower(sanctioned)},
		"sanctioned-uppercase-hex":    {To: "0x" + strings.ToUpper(sanctioned[2:])},
		"whitelist-nil":               {To: plain},
		"whitelist-empty":             {To: plain, Whitelist: []string{}},
		"whitelist-has-destination":   {To: plain, Whitelist: []string{strings.ToLower(plain)}},
		"whitelist-lacks-destination": {To: plain, Whitelist: []string{other}},
		"sanctioned-and-whitelisted":  {To: sanctioned, Whitelist: []string{sanctioned}},
		"country-blocked":             {To: plain, Country: "kp", BlockedCountries: []string{"KP"}},
		"country-not-blocked":         {To: plain, Country: "ZA", BlockedCountries: []string{"KP"}},
		"country-empty-blocklist":     {To: plain, Country: "KP", BlockedCountries: []string{}},
		"country-missing":             {To: plain, BlockedCountries: []string{"KP"}},
		"everything-wrong":            {To: sanctioned, Country: "KP", BlockedCountries: []string{"KP"}, Whitelist: []string{other}, Value: "1000" + eth},
	}
	for name, r := range dest {
		r.CustomerTier = "pro"
		r.ChainID = 1
		if r.Value == "" {
			r.Value = "1" + eth
		}
		cases["destination/"+name] = r
	}

	// Tokens: each peg currency on both sides of each limit, per tier.
	amounts := map[string]string{
		"1": "1", "9000": "9000", "9001": "9001", "10000": "10000", "10001": "10001",
		"90001": "90001", "100000": "100000", "100001": "100001", "180001": "180001",
		"900001": "900001", "1000001": "1000001", "1800001": "1800001", "18000001": "18000001",
	}
	for _, tier := range []string{"free", "pro", "enterprise"} {
		for _, peg := range []string{"USD", "ZAR", "EUR", "GBP"} {
			for aname, a := range amounts {
				cases[fmt.Sprintf("token/%s/%s/%s", tier, peg, aname)] = PolicyRequest{
					CustomerTier: tier, To: plain, Value: "0", ChainID: 1,
					Asset: "TOKEN-" + peg, AssetAmount: a + "000000", AssetDecimals: usdcDecimal, PegCurrency: peg,
				}
			}
		}
	}
	cases["token/unpegged"] = PolicyRequest{CustomerTier: "pro", To: plain, Value: "0", Asset: "WILD", AssetAmount: "5", AssetDecimals: 0}
	cases["token/unpegged-allowance"] = PolicyRequest{CustomerTier: "pro", To: plain, Value: "0", Asset: "WILD", AssetAmount: "5", AssetDecimals: 0, IsAllowance: true}
	cases["token/pegged-no-amount"] = PolicyRequest{CustomerTier: "pro", To: plain, Value: "0", Asset: "USDC", PegCurrency: "USD"}
	cases["token/sanctioned-destination"] = PolicyRequest{CustomerTier: "pro", To: sanctioned, Value: "0", Asset: "USDC", AssetAmount: "1000000", AssetDecimals: 6, PegCurrency: "USD"}
	cases["token/fractional-over-limit"] = PolicyRequest{CustomerTier: "free", To: plain, Value: "0", Asset: "USDC", AssetAmount: "10000000001", AssetDecimals: 6, PegCurrency: "USD"}
	cases["allowance/bounded"] = PolicyRequest{CustomerTier: "pro", To: other, Value: "0", Asset: "USDC", AssetAmount: "500000000", AssetDecimals: 6, PegCurrency: "USD", IsAllowance: true}
	cases["allowance/unlimited"] = PolicyRequest{CustomerTier: "pro", To: other, Value: "0", Asset: "USDC", AssetAmount: maxUint256, AssetDecimals: 6, PegCurrency: "USD", IsAllowance: true}
	cases["allowance/unlimited-18-decimals"] = PolicyRequest{CustomerTier: "pro", To: other, Value: "0", Asset: "DAI", AssetAmount: maxUint256, AssetDecimals: 18, PegCurrency: "USD", IsAllowance: true}

	// Inputs the service must refuse rather than decide on.
	cases["invalid/wei-not-a-number"] = PolicyRequest{CustomerTier: "pro", To: plain, Value: "12abc"}
	cases["invalid/negative-token-amount"] = PolicyRequest{CustomerTier: "pro", To: plain, Asset: "USDC", AssetAmount: "-5", AssetDecimals: 6, PegCurrency: "USD"}
	cases["invalid/implausible-decimals"] = PolicyRequest{CustomerTier: "pro", To: plain, Asset: "USDC", AssetAmount: "5", AssetDecimals: 99, PegCurrency: "USD"}
	return cases
}

func orNone(s string) string {
	if s == "" {
		return "no-tier"
	}
	return s
}

func TestPolicyDecisionsMatchGolden(t *testing.T) {
	ev, err := newEvaluator(context.Background())
	if err != nil {
		t.Fatalf("policies do not load: %v", err)
	}

	got := map[string]interface{}{}
	for name, req := range decisionCases() {
		req := req
		d, err := ev.evaluate(context.Background(), &req)
		if err != nil {
			got[name] = map[string]string{"error": err.Error()}
			continue
		}
		// Rule order inside a set is not a decision; sort so the file
		// changes only when a decision does.
		sort.Strings(d.Denials)
		sort.Strings(d.ApprovalReasons)
		got[name] = d
	}
	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	gotJSON = append(gotJSON, '\n')

	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, gotJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d decisions to %s", len(got), goldenPath)
		return
	}

	wantJSON, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v (generate it with -update-golden)", err)
	}
	var want map[string]json.RawMessage
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatal(err)
	}
	var gotRaw map[string]json.RawMessage
	_ = json.Unmarshal(gotJSON, &gotRaw)

	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	changed := 0
	for _, n := range names {
		g, ok := gotRaw[n]
		if !ok {
			t.Errorf("%s: case missing from the grid", n)
			changed++
			continue
		}
		if compact(g) != compact(want[n]) {
			t.Errorf("%s: decision changed\n  was: %s\n  now: %s", n, compact(want[n]), compact(g))
			changed++
		}
	}
	for n := range gotRaw {
		if _, ok := want[n]; !ok {
			t.Errorf("%s: new case not in the golden file (regenerate with -update-golden)", n)
		}
	}
	if changed == 0 {
		t.Logf("%d decisions unchanged", len(want))
	}
}

func compact(raw json.RawMessage) string {
	var v interface{}
	_ = json.Unmarshal(raw, &v)
	b, _ := json.Marshal(v)
	return string(b)
}
