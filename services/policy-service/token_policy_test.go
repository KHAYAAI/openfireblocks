package main

import (
	"context"
	"strings"
	"testing"
)

// Governing what a stablecoin transfer actually moves.
//
// The bug these tests exist against: an ERC-20 transfer carries its
// recipient and amount inside the calldata, so the transaction's own `to`
// is the token contract and its `value` is zero. Every rule in this
// service read those two fields. The consequence was not that limits were
// loose -- it was that they could not deny anything at all. A fifty
// million rand transfer and a one rand transfer produced identical policy
// input, and both were approved.

func evaluateOne(t *testing.T, req PolicyRequest) *PolicyDecision {
	t.Helper()
	eval, err := newEvaluator(context.Background())
	if err != nil {
		t.Fatalf("newEvaluator: %v", err)
	}
	decision, err := eval.evaluate(context.Background(), &req)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return decision
}

func usdc(amount string) PolicyRequest {
	return PolicyRequest{
		CustomerTier:  "enterprise",
		To:            "0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
		Value:         "0", // as it really is on the wire for a token transfer
		Asset:         "USDC",
		AssetAmount:   amount,
		AssetDecimals: 6,
		PegCurrency:   "USD",
	}
}

func zarp(amount string) PolicyRequest {
	return PolicyRequest{
		CustomerTier:  "enterprise",
		To:            "0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
		Value:         "0",
		Asset:         "ZARP",
		AssetAmount:   amount,
		AssetDecimals: 18,
		PegCurrency:   "ZAR",
	}
}

// The headline case. Two million dollars of USDC, with a native value of
// zero, against an enterprise ceiling of one million.
//
// Before the asset fields existed this request was indistinguishable from
// a transfer of nothing.
func TestALargeStablecoinTransferIsDeniedDespiteAZeroNativeValue(t *testing.T) {
	decision := evaluateOne(t, usdc("2000000000000")) // 2,000,000 USDC at 6 dp

	if decision.Approved {
		t.Fatal("a 2,000,000 USDC transfer was approved; the amount limit is reading the " +
			"native value field, which is 0 for every token transfer")
	}
	if !strings.Contains(strings.Join(decision.Denials, "; "), "global limit") {
		t.Errorf("denied, but not by the amount limit: %v", decision.Denials)
	}
}

func TestAStablecoinTransferUnderTheLimitIsApproved(t *testing.T) {
	decision := evaluateOne(t, usdc("5000000")) // 5 USDC

	if !decision.Approved {
		t.Fatalf("a 5 USDC transfer was denied: %v", decision.Denials)
	}
}

// Decimals are the difference between a limit and a rounding error. The
// same integer is 5 USDC at six decimals and 5e-12 DAI at eighteen; a
// registry that records the wrong one makes every limit a million million
// times too generous.
func TestTheSameIntegerIsJudgedDifferentlyAtDifferentDecimals(t *testing.T) {
	// 2,000,000,000,000 base units.
	atSix := usdc("2000000000000")
	atEighteen := usdc("2000000000000")
	atEighteen.AssetDecimals = 18

	if evaluateOne(t, atSix).Approved {
		t.Error("2,000,000 USDC (6 dp) was approved against a 1,000,000 ceiling")
	}
	if !evaluateOne(t, atEighteen).Approved {
		t.Error("the same integer at 18 decimals is 0.000002 and should be approved")
	}
}

// Tier ceilings apply to tokens as they do to ether.
func TestTierCeilingsApplyToStablecoins(t *testing.T) {
	cases := []struct {
		tier         string
		amount       string // USDC base units
		wantApproved bool
	}{
		{"free", "9000000000", true},       // 9,000 USDC, under the 10,000 free ceiling
		{"free", "20000000000", false},     // 20,000 USDC, over it
		{"pro", "20000000000", true},       // fine on pro
		{"pro", "200000000000", false},     // 200,000 USDC, over the pro ceiling
		{"enterprise", "200000000000", true},
	}

	for _, tc := range cases {
		t.Run(tc.tier+"/"+tc.amount, func(t *testing.T) {
			req := usdc(tc.amount)
			req.CustomerTier = tc.tier
			decision := evaluateOne(t, req)
			if decision.Approved != tc.wantApproved {
				t.Errorf("%s sending %s base units: approved=%v, want %v (%v)",
					tc.tier, tc.amount, decision.Approved, tc.wantApproved, decision.Denials)
			}
		})
	}
}

// -- rand --

// A rand ceiling is not a dollar ceiling. 1,000,000 ZAR is a little over
// fifty thousand dollars, and a platform that applied the dollar number to
// rand would be roughly eighteen times stricter than intended -- which
// looks, to the customer, like the product not working.
func TestRandLimitsAreDenominatedInRand(t *testing.T) {
	// 1,000,000 ZARP: over the USD global ceiling numerically, well under
	// the ZAR one.
	decision := evaluateOne(t, zarp("1000000000000000000000000"))

	if !decision.Approved {
		t.Fatalf("a 1,000,000 ZAR transfer was denied: %v", decision.Denials)
	}
}

func TestARandTransferOverTheRandCeilingIsDenied(t *testing.T) {
	// 20,000,000 ZARP against an 18,000,000 global ceiling.
	decision := evaluateOne(t, zarp("20000000000000000000000000"))

	if decision.Approved {
		t.Fatal("a 20,000,000 ZAR transfer passed an 18,000,000 ZAR ceiling")
	}
}

func TestARandTransferIsFlaggedForApprovalAtTheRandThreshold(t *testing.T) {
	// 2,000,000 ZARP, over the 1,800,000 high-value threshold.
	decision := evaluateOne(t, zarp("2000000000000000000000000"))

	if !decision.Approved {
		t.Fatalf("a 2,000,000 ZAR transfer was denied outright: %v", decision.Denials)
	}
	if !decision.RequiresApproval {
		t.Error("a 2,000,000 ZAR transfer was not routed to an approver")
	}
}

// -- fail-closed cases --

// Registering a token in a currency with no configured limits must not
// quietly create an unlimited payment rail.
func TestATokenInAnUnconfiguredCurrencyIsDenied(t *testing.T) {
	req := usdc("1")
	req.Asset = "EURC"
	req.PegCurrency = "EUR"

	decision := evaluateOne(t, req)

	if decision.Approved {
		t.Fatal("a EUR-pegged token was transacted with no EUR limits configured; " +
			"registering a token in a new currency must not create an unlimited rail")
	}
	if !strings.Contains(strings.Join(decision.Denials, "; "), "EUR") {
		t.Errorf("the denial does not say which currency is unconfigured: %v", decision.Denials)
	}
}

// An unpegged token has no value this service can assert, so the amount check
// cannot apply, so it goes to a human rather than through unexamined.
func TestAnUnpeggedTokenIsRoutedToAnApprover(t *testing.T) {
	req := usdc("1000000")
	req.Asset = "WEIRD"
	req.PegCurrency = ""

	decision := evaluateOne(t, req)

	if !decision.RequiresApproval {
		t.Error("an unpegged token passed with no approval step and no limit applied")
	}
}

// An amount that does not parse must not evaluate to zero. Zero passes
// every limit in the file, which would make a malformed amount the easiest
// bypass in the system.
func TestAnUnparseableAmountIsAnErrorNotAZero(t *testing.T) {
	eval, err := newEvaluator(context.Background())
	if err != nil {
		t.Fatalf("newEvaluator: %v", err)
	}
	req := usdc("not-a-number")

	if _, err := eval.evaluate(context.Background(), &req); err == nil {
		t.Fatal("an unparseable token amount was evaluated rather than refused; " +
			"it would have been treated as zero and passed every limit")
	}
}

func TestANegativeAmountIsRefused(t *testing.T) {
	eval, _ := newEvaluator(context.Background())
	req := usdc("-1000000000000")

	if _, err := eval.evaluate(context.Background(), &req); err == nil {
		t.Fatal("a negative token amount was accepted")
	}
}

// -- allowances --

// approve(spender, 2^256-1) moves nothing today and lets the spender take
// everything tomorrow. It is the most common way a token balance is
// emptied and it must not read as a zero-value call.
func TestAnUnlimitedApprovalIsDenied(t *testing.T) {
	req := usdc("115792089237316195423570985008687907853269984665640564039457584007913129639935")
	req.IsAllowance = true

	decision := evaluateOne(t, req)

	if decision.Approved {
		t.Fatal("an unlimited token approval was approved")
	}
	if !strings.Contains(strings.Join(decision.Denials, "; "), "unlimited") {
		t.Errorf("denied, but not as an unlimited approval: %v", decision.Denials)
	}
}

func TestABoundedApprovalIsAllowedButEscalated(t *testing.T) {
	req := usdc("1000000") // 1 USDC
	req.IsAllowance = true

	decision := evaluateOne(t, req)

	if !decision.Approved {
		t.Fatalf("a bounded approval was denied outright: %v", decision.Denials)
	}
	if !decision.RequiresApproval {
		t.Error("an approval was granted with no human step; an allowance can be drawn " +
			"at any later time, including after the policy that permitted it changed")
	}
}

// -- the whitelist, which is the other half of the blindness --

// The counterparty whitelist reads input.to. For a token transfer that
// used to be the token contract, which is identical for every transfer of
// that token -- so a whitelist either allowed all of them or none.
func TestTheWhitelistSeesTheRealRecipientNotTheTokenContract(t *testing.T) {
	const usdcContract = "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
	const stranger = "0x1111111111111111111111111111111111111111"

	req := usdc("1000000")
	req.To = stranger
	// The customer's approved counterparties. The token contract is on the
	// list only to make the point: if `to` were still the contract, this
	// transfer to a stranger would pass.
	req.Whitelist = []string{usdcContract, "0x2222222222222222222222222222222222222222"}

	decision := evaluateOne(t, req)

	if decision.Approved {
		t.Fatal("a USDC transfer to an address not on the whitelist was approved")
	}
}

func TestAWhitelistedRecipientIsStillAllowed(t *testing.T) {
	const friend = "0x2222222222222222222222222222222222222222"

	req := usdc("1000000")
	req.To = friend
	req.Whitelist = []string{friend}

	if decision := evaluateOne(t, req); !decision.Approved {
		t.Fatalf("a transfer to a whitelisted recipient was denied: %v", decision.Denials)
	}
}

// -- precision --

// Base units are exact integers and 10^18 is past what a float64 holds
// exactly. Converting before dividing loses the low digits of exactly the
// large amounts a limit is for.
func TestALargeRandAmountConvertsWithoutLosingItsMagnitude(t *testing.T) {
	// 17,999,999 ZAR: just under the 18,000,000 ceiling, at 18 decimals,
	// with non-zero digits all the way down.
	req := zarp("17999999123456789012345678")

	if decision := evaluateOne(t, req); !decision.Approved {
		t.Fatalf("17,999,999.12 ZAR was denied against an 18,000,000 ceiling: %v", decision.Denials)
	}

	// And one rand over the line is caught.
	over := zarp("18000001000000000000000000")
	if decision := evaluateOne(t, over); decision.Approved {
		t.Error("18,000,001 ZAR passed an 18,000,000 ZAR ceiling")
	}
}

// A native transfer keeps working exactly as before. The asset fields are
// absent on that path and must not change any existing decision.
func TestNativeTransfersAreUnaffected(t *testing.T) {
	req := PolicyRequest{
		CustomerTier: "free",
		To:           "0x742d35Cc6634C0532925a3b844Bc454e4438f44e",
		Value:        "1000000000000000000", // 1 ETH
	}

	decision := evaluateOne(t, req)

	if !decision.Approved {
		t.Fatalf("a 1 ETH native transfer was denied: %v", decision.Denials)
	}
	if decision.RequiresApproval {
		t.Error("a 1 ETH native transfer was escalated; the token rules are firing on native transfers")
	}
}
