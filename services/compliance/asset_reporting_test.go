package main

import (
	"math/big"
	"testing"
	"time"
)

// Deciding whether a threshold report is owed, when the money is a
// stablecoin.
//
// The defect: EvaluateCTRFrom sums signing.transactions.amount, which is
// the native value attached to a transaction. For an ERC-20 transfer that
// is zero, because the money is in the calldata. A day in which a customer
// moved fifty million rand of ZARP therefore aggregated to nothing, the
// threshold was never crossed, and no filing was ever raised -- while the
// platform was under a statutory duty to raise one.
//
// Every test here is the failure it prevents, and the failures are
// regulatory rather than technical: a report that is not filed, a report
// filed on a wrong total, or a total that silently excludes transfers
// nobody could read.

var assetDay = time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)

func units(whole string, decimals int) *big.Int {
	v, _ := new(big.Int).SetString(whole, 10)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return v.Mul(v, scale)
}

func usdcTx(id, whole string) AssetTransaction {
	return AssetTransaction{
		RequestID: id, Chain: "ethereum", Symbol: "USDC",
		Amount: units(whole, 6), Decimals: 6, Peg: "USD", CreatedAt: assetDay,
	}
}

func zarpTx(id, whole string) AssetTransaction {
	return AssetTransaction{
		RequestID: id, Chain: "ethereum", Symbol: "ZARP",
		Amount: units(whole, 18), Decimals: 18, Peg: "ZAR", CreatedAt: assetDay,
	}
}

func currency(agg *AssetAggregation, code string) *CurrencyAggregate {
	for _, c := range agg.Currencies {
		if c.Currency == code {
			return c
		}
	}
	return nil
}

// The headline case. Under the native-only aggregate this day summed to
// zero and no report was owed.
func TestADayOfStablecoinTransfersCrossesTheThreshold(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		usdcTx("a", "4000"),
		usdcTx("b", "7000"),
	}, nil)

	usd := currency(agg, "USD")
	if usd == nil {
		t.Fatal("a day of USDC transfers produced no USD aggregate at all")
	}
	if usd.Total != 11000 {
		t.Errorf("11,000 USDC aggregated to %.2f USD", usd.Total)
	}
	if !usd.OverThreshold {
		t.Error("11,000 USD did not cross the 10,000 USD reporting threshold")
	}
}

// A pegged token needs no price oracle. One USDC is one dollar by the
// issuer's undertaking, which for a threshold is the conservative reading.
func TestAStablecoinIsValuedWithoutAPriceOracle(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{usdcTx("a", "15000")}, nil)

	usd := currency(agg, "USD")
	if !usd.OverThreshold {
		t.Error("a 15,000 USDC day was not reportable; the absence of a price oracle " +
			"must not stop a pegged asset from being valued")
	}
}

// -- South Africa --

func TestARandDayIsMeasuredAgainstTheRandThreshold(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		zarpTx("a", "30000"),
		zarpTx("b", "25000"),
	}, nil)

	zar := currency(agg, "ZAR")
	if zar == nil {
		t.Fatal("a day of ZARP transfers produced no ZAR aggregate")
	}
	if zar.Total != 55000 {
		t.Errorf("55,000 ZARP aggregated to %.2f ZAR", zar.Total)
	}
	if !zar.OverThreshold {
		t.Errorf("55,000 ZAR did not cross the %.2f ZAR threshold", zar.Threshold)
	}
}

func TestARandDayUnderTheRandThresholdIsNotReportable(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{zarpTx("a", "10000")}, nil)

	if currency(agg, "ZAR").OverThreshold {
		t.Error("a 10,000 ZAR day was flagged as reportable")
	}
}

// The mistake this prevents: applying the dollar threshold to rand. R10,000
// is a few hundred dollars, and a platform that reported it as a
// ten-thousand-unit crossing would file constantly and wrongly.
func TestTheDollarThresholdIsNotAppliedToRand(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{zarpTx("a", "12000")}, nil)

	zar := currency(agg, "ZAR")
	if zar.Threshold == DefaultCTRThresholdUSD {
		t.Fatal("the rand aggregate is being measured against the dollar threshold")
	}
	if zar.OverThreshold {
		t.Errorf("12,000 ZAR was reported against a %.2f ZAR threshold", zar.Threshold)
	}
}

// No FX. A day in two currencies produces two totals, each against its own
// threshold -- rather than one number built on a rate the platform does
// not have.
func TestTwoCurrenciesAreAggregatedSeparately(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		usdcTx("a", "9000"),
		zarpTx("b", "9000"),
	}, nil)

	if len(agg.Currencies) != 2 {
		t.Fatalf("expected a USD and a ZAR aggregate, got %d", len(agg.Currencies))
	}
	if currency(agg, "USD").OverThreshold {
		t.Error("$9,000 alone was reported as crossing the 10,000 USD threshold")
	}
	if currency(agg, "ZAR").OverThreshold {
		t.Error("R9,000 alone was reported as crossing the rand threshold")
	}
}

// Sorted, so two runs over the same day produce the same report.
func TestCurrenciesAreOrderedStably(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		zarpTx("a", "1"),
		usdcTx("b", "1"),
	}, nil)

	if agg.Currencies[0].Currency != "USD" || agg.Currencies[1].Currency != "ZAR" {
		t.Errorf("currencies are not in ISO order: %v", agg.Currencies)
	}
}

// -- what the total leaves out --

// A transfer nobody could decode must be visible, not dropped. A total
// that silently excludes activity is worse than no total, because it will
// be relied on.
func TestUndecodedTransfersAreCountedNotDropped(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		usdcTx("a", "1000"),
		{RequestID: "mystery", Chain: "ethereum", Symbol: "", Amount: nil, CreatedAt: assetDay},
	}, nil)

	if agg.UndecodedCount != 1 {
		t.Errorf("an undecodable transfer was not counted: %d", agg.UndecodedCount)
	}
	if len(agg.UndecodedIDs) != 1 || agg.UndecodedIDs[0] != "mystery" {
		t.Errorf("the undecodable transfer is not named: %v", agg.UndecodedIDs)
	}
	// And it did not silently become part of a currency total.
	if currency(agg, "USD").Total != 1000 {
		t.Errorf("the USD total absorbed an undecodable transfer: %.2f", currency(agg, "USD").Total)
	}
}

// An unpegged token cannot be valued, and saying so is different from
// saying it was worth nothing.
func TestAnUnpeggedTokenIsNamedRatherThanValuedAtZero(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "WEIRD",
			Amount: units("1000000", 18), Decimals: 18, Peg: "", CreatedAt: assetDay},
	}, nil)

	if len(agg.Currencies) != 0 {
		t.Error("an unpegged token was folded into a currency total")
	}
	if len(agg.UnvaluedAssets) != 1 || agg.UnvaluedAssets[0] != "WEIRD" {
		t.Errorf("the unvalued asset is not named: %v", agg.UnvaluedAssets)
	}
}

// Native activity with no price oracle is unvalued, not zero. A zero in a
// regulatory total has to mean "nothing moved", never "nobody could price
// it".
func TestNativeActivityWithNoOracleIsUnvaluedNotZero(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "NATIVE",
			Amount: units("100", 18), Decimals: 18, CreatedAt: assetDay},
	}, nil)

	if len(agg.UnvaluedAssets) != 1 || agg.UnvaluedAssets[0] != "NATIVE" {
		t.Errorf("100 ETH with no oracle was not reported as unvalued: %v", agg.UnvaluedAssets)
	}
}

func TestNativeActivityIsValuedWhenAnOracleIsAvailable(t *testing.T) {
	// $3,000 per ETH, expressed per wei.
	rate := new(big.Float).Quo(big.NewFloat(3000), new(big.Float).SetInt(units("1", 18)))

	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "NATIVE",
			Amount: units("5", 18), Decimals: 18, CreatedAt: assetDay},
	}, rate)

	usd := currency(agg, "USD")
	if usd == nil {
		t.Fatal("priced native activity produced no USD aggregate")
	}
	if usd.Total < 14999 || usd.Total > 15001 {
		t.Errorf("5 ETH at $3,000 aggregated to %.2f USD, want about 15,000", usd.Total)
	}
	if !usd.OverThreshold {
		t.Error("$15,000 of ether did not cross the threshold")
	}
}

// Native and stablecoin activity on the same day add up together, because
// both are dollars.
func TestNativeAndStablecoinDollarsAggregateTogether(t *testing.T) {
	rate := new(big.Float).Quo(big.NewFloat(3000), new(big.Float).SetInt(units("1", 18)))

	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "NATIVE",
			Amount: units("2", 18), Decimals: 18, CreatedAt: assetDay}, // $6,000
		usdcTx("b", "5000"),
	}, rate)

	usd := currency(agg, "USD")
	if usd.Total < 10999 || usd.Total > 11001 {
		t.Errorf("$6,000 of ether plus 5,000 USDC aggregated to %.2f", usd.Total)
	}
	if !usd.OverThreshold {
		t.Error("$11,000 across two assets did not cross a $10,000 threshold; " +
			"structuring across assets must not defeat the aggregate")
	}
}

// -- exactness --

// Per-asset totals go on a filing, so they are accumulated exactly rather
// than through a float. A total a regulator can show is wrong in its last
// digits is worse than no total.
func TestPerAssetTotalsAreExact(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "ZARP",
			Amount: big.NewInt(1), Decimals: 18, Peg: "ZAR", CreatedAt: assetDay},
		{RequestID: "b", Chain: "ethereum", Symbol: "ZARP",
			Amount: big.NewInt(2), Decimals: 18, Peg: "ZAR", CreatedAt: assetDay},
	}, nil)

	if got := currency(agg, "ZAR").ByAsset["ZARP"]; got != "0.000000000000000003" {
		t.Errorf("three wei-scale units of ZARP totalled to %q", got)
	}
}

func TestALargeRandTotalKeepsItsLowDigits(t *testing.T) {
	amount, _ := new(big.Int).SetString("17999999123456789012345678", 10)

	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "ZARP",
			Amount: amount, Decimals: 18, Peg: "ZAR", CreatedAt: assetDay},
	}, nil)

	if got := currency(agg, "ZAR").ByAsset["ZARP"]; got != "17999999.123456789012345678" {
		t.Errorf("a large rand amount lost precision: %q", got)
	}
}

// Decimals decide what a number means. The same integer is 5 USDC and
// 0.000000000005 DAI.
func TestDecimalsDecideTheValue(t *testing.T) {
	same, _ := new(big.Int).SetString("5000000", 10)

	six := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "USDC",
			Amount: same, Decimals: 6, Peg: "USD", CreatedAt: assetDay},
	}, nil)
	eighteen := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "DAI",
			Amount: same, Decimals: 18, Peg: "USD", CreatedAt: assetDay},
	}, nil)

	if currency(six, "USD").Total != 5 {
		t.Errorf("5,000,000 base units at 6 decimals valued at %.6f", currency(six, "USD").Total)
	}
	if currency(eighteen, "USD").Total > 0.001 {
		t.Errorf("5,000,000 base units at 18 decimals valued at %.6f", currency(eighteen, "USD").Total)
	}
}

// -- boundaries and configuration --

// At the threshold, not above it. Over-reporting is a filing;
// under-reporting is a violation.
func TestExactlyTheThresholdIsReportable(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{usdcTx("a", "10000")}, nil)

	if !currency(agg, "USD").OverThreshold {
		t.Error("a assetDay totalling exactly the threshold was not reportable")
	}
}

// A statutory threshold changes by directive. One compiled into a binary
// is one that silently goes stale.
func TestAThresholdCanBeReconfigured(t *testing.T) {
	t.Setenv("CTR_THRESHOLD_ZAR", "25000")

	agg := AggregateByCurrency([]AssetTransaction{zarpTx("a", "30000")}, nil)

	zar := currency(agg, "ZAR")
	if zar.Threshold != 25000 {
		t.Errorf("the configured threshold was ignored: %.2f", zar.Threshold)
	}
	if !zar.OverThreshold {
		t.Error("30,000 ZAR did not cross a configured 25,000 ZAR threshold")
	}
}

// A currency nobody has set a threshold for gets an aggregate and no
// verdict. "Nobody has decided" is different from "nobody crossed it", and
// reporting the second when the first is true is how an obligation is
// missed.
func TestACurrencyWithNoThresholdGetsNoVerdict(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		{RequestID: "a", Chain: "ethereum", Symbol: "EURC",
			Amount: units("1000000", 6), Decimals: 6, Peg: "EUR", CreatedAt: assetDay},
	}, nil)

	eur := currency(agg, "EUR")
	if eur == nil {
		t.Fatal("a euro-pegged transfer produced no aggregate")
	}
	if eur.HaveThreshold {
		t.Error("a threshold was invented for EUR")
	}
	if eur.OverThreshold {
		t.Error("a currency with no configured threshold was reported as over it")
	}
	if eur.Total != 1000000 {
		t.Errorf("the aggregate itself is still wrong: %.2f", eur.Total)
	}
}

func TestAnEmptyDayProducesNothingToReport(t *testing.T) {
	agg := AggregateByCurrency(nil, nil)

	if len(agg.Currencies) != 0 || agg.UndecodedCount != 0 {
		t.Errorf("an empty day produced %d currencies and %d undecoded",
			len(agg.Currencies), agg.UndecodedCount)
	}
}

// The narrative goes on the filing, so it has to name the amount, the
// currency and the assets behind it.
func TestTheNarrativeNamesTheAssetsBehindTheTotal(t *testing.T) {
	agg := AggregateByCurrency([]AssetTransaction{
		usdcTx("a", "6000"),
		{RequestID: "b", Chain: "ethereum", Symbol: "DAI",
			Amount: units("6000", 18), Decimals: 18, Peg: "USD", CreatedAt: assetDay},
	}, nil)

	narrative := currency(agg, "USD").Narrative("cust-1", "ethereum", assetDay)

	for _, want := range []string{"cust-1", "12000.00 USD", "USDC", "DAI", "2026-03-14"} {
		if !contains(narrative, want) {
			t.Errorf("the narrative does not mention %q: %s", want, narrative)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
