package main

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

// Deciding whether a currency transaction report is owed.
//
// This service had no tests at all, which for the two functions below is
// the wrong place to have none: failing to file a CTR is a federal
// reporting violation, and filing one on a wrong aggregate produces a
// report a regulator can demonstrate is incorrect. Both are defects you
// would rather not learn about from an examiner.
//
// The arithmetic is exact on purpose. Native chain units are wei and
// satoshis -- integers far larger than a float64 can hold without loss --
// so the aggregate is big.Int and only the USD comparison is floating
// point. A test that used small round numbers would not notice if that
// stopped being true.

const oneETH = 1e18

func ethTx(id string, eth int64) NativeTransaction {
	amount := new(big.Int).Mul(big.NewInt(eth), big.NewInt(oneETH))
	return NativeTransaction{RequestID: id, Chain: "ethereum", Amount: amount, CreatedAt: time.Now()}
}

// $2,500/ETH, expressed as USD per wei, which is what the evaluation
// multiplies by.
func ratePerWei(usdPerETH float64) *big.Float {
	return new(big.Float).Quo(big.NewFloat(usdPerETH), big.NewFloat(oneETH))
}

var day = time.Date(2026, time.March, 3, 0, 0, 0, 0, time.UTC)

func TestADayUnderTheThresholdIsNotReportable(t *testing.T) {
	// 3 ETH at $2,500 = $7,500.
	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		ratePerWei(2_500), []NativeTransaction{ethTx("a", 1), ethTx("b", 2)})

	if !eval.Evaluated {
		t.Fatal("the evaluation did not complete despite a rate being available")
	}
	if eval.OverThreshold {
		t.Errorf("$%.2f was reported as over the $%.2f threshold",
			eval.AggregateAmountUSD, eval.ThresholdUSD)
	}
}

// The aggregate is the day's total, not the largest transaction. Someone
// moving $9,000 twice in a day is exactly the case the aggregation rule
// exists for, and treating each transaction separately would miss it.
func TestTransactionsAggregateAcrossTheDay(t *testing.T) {
	// 4 ETH at $2,500 = $10,000, reached only by adding them up.
	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		ratePerWei(2_500), []NativeTransaction{ethTx("a", 2), ethTx("b", 2)})

	if !eval.OverThreshold {
		t.Errorf("two $5,000 transfers aggregated to $%.2f and were not reported",
			eval.AggregateAmountUSD)
	}
	if len(eval.TransactionIDs) != 2 {
		t.Errorf("the filing would name %d transactions, not both", len(eval.TransactionIDs))
	}
}

// The boundary. Over-reporting is a filing; under-reporting is a
// violation, so exactly-at-threshold reports.
func TestExactlyAtTheThresholdIsReportable(t *testing.T) {
	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		ratePerWei(10_000), []NativeTransaction{ethTx("a", 1)})

	if eval.AggregateAmountUSD != DefaultCTRThresholdUSD {
		t.Fatalf("expected exactly $%.2f, computed $%.2f",
			DefaultCTRThresholdUSD, eval.AggregateAmountUSD)
	}
	if !eval.OverThreshold {
		t.Error("a day totalling exactly the threshold was not reported")
	}
}

// Without a price oracle the platform must not invent a rate. A CTR filed
// on a guessed conversion is a false statement to a regulator; declining to
// evaluate is a gap a compliance officer can see and act on.
func TestWithoutARateNothingIsDecided(t *testing.T) {
	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		nil, []NativeTransaction{ethTx("a", 100)})

	if eval.Evaluated {
		t.Error("an evaluation completed with no conversion rate available")
	}
	if eval.OverThreshold {
		t.Error("a threshold decision was reported without a rate to make it with")
	}
	if eval.AggregateAmountUSD != 0 {
		t.Errorf("a USD figure of $%.2f was produced from no rate", eval.AggregateAmountUSD)
	}
	if !strings.Contains(eval.Reason, "price oracle") {
		t.Errorf("the reason does not explain why: %q", eval.Reason)
	}
	// The native aggregate needs no external data, so it is still owed --
	// it is what a compliance officer converts by hand.
	want := new(big.Int).Mul(big.NewInt(100), big.NewInt(oneETH))
	if eval.AggregateAmountNative.Cmp(want) != 0 {
		t.Errorf("the native aggregate is %s, want %s", eval.AggregateAmountNative, want)
	}
}

// Native units exceed what a float64 can represent exactly, so the
// aggregate has to stay integral all the way through. 10,000 ETH is
// 1e22 wei -- well past float64's 2^53 exact-integer limit.
func TestLargeAggregatesStayExact(t *testing.T) {
	var txs []NativeTransaction
	for i := 0; i < 10_000; i++ {
		txs = append(txs, ethTx("tx", 1))
	}

	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		ratePerWei(2_500), txs)

	want := new(big.Int).Mul(big.NewInt(10_000), big.NewInt(oneETH))
	if eval.AggregateAmountNative.Cmp(want) != 0 {
		t.Errorf("10,000 ETH aggregated to %s wei, want %s -- precision was lost",
			eval.AggregateAmountNative, want)
	}
	if !eval.OverThreshold {
		t.Error("a $25,000,000 day was not reported")
	}
}

func TestADayWithNoTransactionsIsNotReportable(t *testing.T) {
	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		ratePerWei(2_500), nil)

	if eval.OverThreshold {
		t.Error("a day with no transactions was reported")
	}
	if eval.AggregateAmountNative.Sign() != 0 {
		t.Errorf("an empty day aggregated to %s", eval.AggregateAmountNative)
	}
	// Not nil: callers do arithmetic on this, and a nil big.Int here would
	// take down the daily run for every customer after this one.
	if eval.AggregateAmountNative == nil {
		t.Fatal("the aggregate is nil rather than zero")
	}
}

// A row with no parsed amount must not take down the evaluation. It should
// not happen -- the column is not null -- but a panic here would stop the
// daily CTR run for every customer, not just the one with the bad row.
func TestAMalformedTransactionDoesNotStopTheEvaluation(t *testing.T) {
	txs := []NativeTransaction{
		ethTx("good", 5),
		{RequestID: "bad", Chain: "ethereum", Amount: nil},
	}

	eval := EvaluateCTRFrom("cust-1", "ethereum", day, DefaultCTRThresholdUSD,
		ratePerWei(2_500), txs)

	want := new(big.Int).Mul(big.NewInt(5), big.NewInt(oneETH))
	if eval.AggregateAmountNative.Cmp(want) != 0 {
		t.Errorf("the aggregate is %s, want the one good transaction's %s",
			eval.AggregateAmountNative, want)
	}
	// Still named in the filing: an unparseable amount is something a
	// compliance officer needs to see, not something to quietly drop.
	if len(eval.TransactionIDs) != 2 {
		t.Errorf("the filing names %d transactions; the malformed one was dropped silently",
			len(eval.TransactionIDs))
	}
}

// The day is normalised to UTC midnight so that "one calendar day" means
// the same thing regardless of what time the evaluation runs.
func TestTheDayIsTheWholeUTCDay(t *testing.T) {
	afternoon := time.Date(2026, time.March, 3, 16, 45, 0, 0, time.UTC)

	eval := EvaluateCTRFrom("cust-1", "ethereum",
		time.Date(afternoon.Year(), afternoon.Month(), afternoon.Day(), 0, 0, 0, 0, time.UTC),
		DefaultCTRThresholdUSD, ratePerWei(2_500), []NativeTransaction{ethTx("a", 1)})

	if !eval.Day.Equal(day) {
		t.Errorf("the evaluation covers %s, want the day starting %s", eval.Day, day)
	}
}
