package main

import (
	"context"
	"testing"
)

// Exercises the embedded policies against representative transactions.
func TestPolicyEvaluation(t *testing.T) {
	eval, err := newEvaluator(context.Background())
	if err != nil {
		t.Fatalf("newEvaluator: %v", err)
	}
	ctx := context.Background()

	tenEth := "10000000000000000000"
	sixtyEth := "60000000000000000000"
	hundredFiftyEth := "150000000000000000000"

	cases := []struct {
		name             string
		req              PolicyRequest
		wantApproved     bool
		wantApprovalFlag bool
	}{
		{
			name:         "small transfer approved",
			req:          PolicyRequest{CustomerTier: "free", Value: "1000", To: "0xabc"},
			wantApproved: true,
		},
		{
			// Denied on the tier limit, and also flagged for approval (> 10 ETH).
			name:         "free tier over 10 ETH denied",
			req:          PolicyRequest{CustomerTier: "free", Value: sixtyEth, To: "0xabc"},
			wantApproved: false, wantApprovalFlag: true,
		},
		{
			name:         "pro tier 60 ETH denied",
			req:          PolicyRequest{CustomerTier: "pro", Value: sixtyEth, To: "0xabc"},
			wantApproved: false, wantApprovalFlag: true,
		},
		{
			name:         "enterprise 60 ETH approved but needs approval",
			req:          PolicyRequest{CustomerTier: "enterprise", Value: sixtyEth, To: "0xabc"},
			wantApproved: true, wantApprovalFlag: true,
		},
		{
			name:         "enterprise over global limit denied",
			req:          PolicyRequest{CustomerTier: "enterprise", Value: hundredFiftyEth, To: "0xabc"},
			wantApproved: false, wantApprovalFlag: true,
		},
		{
			name: "whitelist miss denied",
			req: PolicyRequest{
				CustomerTier: "pro", Value: "1000", To: "0xBAD",
				Whitelist: []string{"0xgood"},
			},
			wantApproved: false,
		},
		{
			name: "whitelist hit approved (case-insensitive)",
			req: PolicyRequest{
				CustomerTier: "pro", Value: "1000", To: "0xGOOD",
				Whitelist: []string{"0xgood"},
			},
			wantApproved: true,
		},
		{
			name: "blocked country denied",
			req: PolicyRequest{
				CustomerTier: "pro", Value: "1000", To: "0xabc",
				Country: "KP", BlockedCountries: []string{"KP", "IR"},
			},
			wantApproved: false,
		},
		{
			name: "sanctioned destination denied (case-insensitive)",
			req: PolicyRequest{
				CustomerTier: "enterprise", Value: "1000",
				To: "0x8589427373d6d84e98730d7795d8f6f8731fda16",
			},
			wantApproved: false,
		},
		{
			name:             "high value over 10 ETH flags approval",
			req:              PolicyRequest{CustomerTier: "enterprise", Value: tenEth + "1", To: "0xabc"},
			wantApproved:     true,
			wantApprovalFlag: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := eval.evaluate(ctx, &tc.req)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if d.Approved != tc.wantApproved {
				t.Errorf("approved = %v, want %v (denials=%v)", d.Approved, tc.wantApproved, d.Denials)
			}
			if d.RequiresApproval != tc.wantApprovalFlag {
				t.Errorf("requiresApproval = %v, want %v", d.RequiresApproval, tc.wantApprovalFlag)
			}
		})
	}
}
