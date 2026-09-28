package main

import (
	"strings"
	"testing"
)

// How much money to ask somebody for.
//
// The rest of billing has room to be approximately right. This does not:
// an off-by-one here is not a bug report, it is a customer disputing their
// invoice, and the platform having to prove which of them is correct.

func plan() *Plan {
	return &Plan{
		Name:                "Growth",
		BillingCycle:        "monthly",
		Price:               50_000, // $500.00
		Currency:            "usd",
		SigningLimit:        1_000,
		KeyLimit:            25,
		OverageSigningCents: 2,
		OverageKeyCents:     500,
	}
}

func TestUsageWithinTheLimitsIsChargedTheBaseRate(t *testing.T) {
	items := InvoiceLineItems(plan(), UsageCounts{SigningRequests: 900, KeyOperations: 10})

	if len(items) != 1 {
		t.Fatalf("got %d line items, want just the plan: %+v", len(items), items)
	}
	if TotalOf(items) != 50_000 {
		t.Errorf("charged %d cents, want the 50000 base rate", TotalOf(items))
	}
}

// The boundary. A customer who used exactly their allowance has not
// exceeded it, and billing them for one unit of overage is the single most
// likely way to have this argument.
func TestUsageExactlyAtTheLimitIsNotOverage(t *testing.T) {
	items := InvoiceLineItems(plan(), UsageCounts{SigningRequests: 1_000, KeyOperations: 25})

	if len(items) != 1 {
		t.Fatalf("charged overage for usage exactly at the limit: %+v", items)
	}
}

func TestOneOverTheLimitIsChargedForExactlyOne(t *testing.T) {
	items := InvoiceLineItems(plan(), UsageCounts{SigningRequests: 1_001, KeyOperations: 25})

	if len(items) != 2 {
		t.Fatalf("got %d line items, want the plan plus one overage line: %+v", len(items), items)
	}
	if items[1].Quantity != 1 {
		t.Errorf("charged for %d signatures over, want 1", items[1].Quantity)
	}
	if TotalOf(items) != 50_002 {
		t.Errorf("charged %d cents, want 50000 + 2", TotalOf(items))
	}
}

func TestSigningAndKeyOverageAreChargedSeparately(t *testing.T) {
	items := InvoiceLineItems(plan(), UsageCounts{SigningRequests: 1_500, KeyOperations: 30})

	if len(items) != 3 {
		t.Fatalf("got %d line items, want the plan plus two overage lines: %+v", len(items), items)
	}
	// 500 signatures at 2c, 5 keys at 500c.
	want := 50_000 + 500*2 + 5*500
	if TotalOf(items) != want {
		t.Errorf("charged %d cents, want %d", TotalOf(items), want)
	}

	// The two units cost very different amounts to serve, so folding them
	// into one line would hide the thing a customer most wants to check.
	if items[1].UnitPrice == items[2].UnitPrice {
		t.Error("signatures and keys were charged at the same rate")
	}
}

// Every line has to explain itself. A customer who cannot check an invoice
// will dispute it, and the platform then has to prove the number.
func TestEveryLineItemIsSelfExplanatory(t *testing.T) {
	items := InvoiceLineItems(plan(), UsageCounts{SigningRequests: 1_500, KeyOperations: 30})

	for i, item := range items {
		if strings.TrimSpace(item.Description) == "" {
			t.Errorf("line %d has no description", i)
		}
		if item.Amount != item.Quantity*item.UnitPrice {
			t.Errorf("line %d: %d x %d does not equal %d",
				i, item.Quantity, item.UnitPrice, item.Amount)
		}
	}
}

// Plans sold before overage rates existed agreed to no overage charge.
// Inventing one retroactively bills people for something they never agreed
// to, which is worse than under-charging.
func TestAPlanWithNoOverageRateNeverChargesOverage(t *testing.T) {
	p := plan()
	p.OverageSigningCents = 0
	p.OverageKeyCents = 0

	items := InvoiceLineItems(p, UsageCounts{SigningRequests: 100_000, KeyOperations: 900})

	if len(items) != 1 {
		t.Fatalf("charged overage on a plan with no overage rate: %+v", items)
	}
	if TotalOf(items) != p.Price {
		t.Errorf("charged %d cents, want the %d base rate", TotalOf(items), p.Price)
	}
}

// An unlimited plan is expressed as a limit of zero, and must not be read
// as "everything is overage".
func TestAnUnlimitedPlanIsNotBilledAsEntirelyOverage(t *testing.T) {
	p := plan()
	p.SigningLimit = 0
	p.KeyLimit = 0
	p.OverageSigningCents = 0
	p.OverageKeyCents = 0

	items := InvoiceLineItems(p, UsageCounts{SigningRequests: 50_000, KeyOperations: 400})

	if TotalOf(items) != p.Price {
		t.Errorf("an unlimited plan charged %d cents for 50000 signatures", TotalOf(items))
	}
}

// A remaining allowance shown as a negative number reads as a bug, and a
// dashboard is where customers look before they ask why they were charged.
func TestRemainingAllowanceNeverGoesNegative(t *testing.T) {
	cases := []struct {
		limit, used, want int
	}{
		{1_000, 0, 1_000},
		{1_000, 400, 600},
		{1_000, 1_000, 0},
		{1_000, 1_400, 0},
		{0, 900, 0}, // unlimited: no allowance to report
	}
	for _, tc := range cases {
		if got := remaining(tc.limit, tc.used); got != tc.want {
			t.Errorf("remaining(%d, %d) = %d, want %d", tc.limit, tc.used, got, tc.want)
		}
	}
}
