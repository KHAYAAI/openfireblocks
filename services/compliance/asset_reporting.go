package main

import (
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"time"
)

// Threshold reporting when the money is a stablecoin.
//
// EvaluateCTRFrom sums signing.transactions.amount -- the native value
// attached to a transaction -- and multiplies by a native/USD rate. For an
// ERC-20 transfer that field is zero, because the money is in the
// calldata. So a day in which a customer moved fifty million rand of ZARP
// aggregated to nothing, the threshold was never crossed, and no filing
// was ever owed. That is not a product gap; it is a reporting failure, and
// the platform would have been under a statutory duty with no record that
// the duty arose.
//
// Two things are different here from the native path.
//
// First, a pegged token needs no price oracle. One USDC is one dollar by
// the issuer's own undertaking, and one ZARP is one rand. That is a claim
// rather than a market price, but for a *threshold* the claim is the
// conservative reading: a depegged stablecoin trading at 0.97 would
// otherwise let a filing threshold be dodged by three percent, and
// under-reporting is the failure that matters.
//
// Second, a day's activity can be in more than one currency, and this
// codebase has no FX source. Rather than invent a rate to produce one
// number, activity is aggregated per currency and each total is checked
// against that currency's own threshold. A customer who moved $9,000 and
// R9,000 in a day has crossed neither; saying so is correct, and saying
// they crossed one because both were summed through a guessed rate would
// not be.

// Reporting thresholds.
//
// The US figure is the well-known FinCEN currency-transaction default.
// The South African figure is the Financial Intelligence Centre Act cash
// threshold report.
//
// Both are configurable, and that is deliberate rather than lazy: a
// statutory threshold is a number that changes by directive, and one
// compiled into a binary is one that silently goes stale. The defaults
// here are a starting point that a compliance officer is expected to
// confirm against the current directive before the platform is relied on
// for filing -- see docs/compliance/THRESHOLD-REPORTING.md, which says so
// in the place somebody will actually read it.
const (
	DefaultCTRThresholdZAR = 49999.99

	// FIC Directive 9 travel-rule threshold: transfers at or above this
	// require originator and beneficiary information to travel with them.
	// Recorded here because the same aggregation answers it; the platform
	// does not yet transmit that information, which the documentation
	// states plainly rather than implying coverage it does not have.
	DefaultTravelRuleThresholdZAR = 5000.00
)

// thresholdsByCurrency returns the reporting threshold for a currency.
//
// A currency with no configured threshold returns ok=false, and the
// caller reports the aggregate without a verdict rather than assuming
// there is nothing to report. An absent threshold means nobody has
// decided, which is different from a threshold nobody crossed.
func thresholdFor(currency string) (float64, bool) {
	if env := os.Getenv("CTR_THRESHOLD_" + currency); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			return v, true
		}
	}
	switch currency {
	case "USD":
		return DefaultCTRThresholdUSD, true
	case "ZAR":
		return DefaultCTRThresholdZAR, true
	default:
		return 0, false
	}
}

// AssetTransaction is one transfer, as signing.transactions now records
// it: what actually moved, rather than the envelope it moved in.
type AssetTransaction struct {
	RequestID string
	Chain     string

	// Symbol is "NATIVE" for an ordinary transfer, otherwise a registry
	// symbol. Empty means the row predates asset recording or carried
	// calldata nobody decoded -- see Undecoded below.
	Symbol string

	// Amount in the asset's own base units. Nil when the platform could
	// not determine what moved, which is recorded truthfully rather than
	// as a zero.
	Amount   *big.Int
	Decimals int

	// Peg is the ISO code the asset tracked at the time of the transfer,
	// empty when unpegged. Read from the transaction row rather than the
	// registry so a later registry edit cannot rewrite a historical
	// aggregate.
	Peg string

	CreatedAt time.Time
}

// Undecoded reports whether the platform could not tell what this
// transaction moved.
//
// These are counted and surfaced, never dropped. A transfer missing from
// a regulatory aggregate because nobody could read it is the same defect
// as one missing because of a bug -- the difference is only that this one
// is knowable, so a report that carries the count lets a compliance
// officer see the gap instead of trusting a total that quietly excludes
// it.
func (t AssetTransaction) Undecoded() bool {
	return t.Amount == nil || t.Symbol == ""
}

// CurrencyAggregate is one day's activity in one currency.
type CurrencyAggregate struct {
	Currency string
	// Total in whole currency units.
	Total float64
	// Per-asset totals, in each asset's own units, so a filing can say
	// "R2,000,000 of ZARP" rather than only a currency figure.
	ByAsset        map[string]string
	TransactionIDs []string

	Threshold     float64
	HaveThreshold bool
	OverThreshold bool
}

// AssetAggregation is the whole picture for one customer, one chain, one
// day.
type AssetAggregation struct {
	CustomerID string
	Chain      string
	Day        time.Time

	// One entry per currency that saw activity, sorted by ISO code so a
	// report is stable between runs.
	Currencies []*CurrencyAggregate

	// Transfers the platform could not value, and why that matters.
	UndecodedCount int
	UndecodedIDs   []string

	// Unpegged tokens moved that day. Not valued -- there is no price
	// source -- but named, because "no filing was due" is only a sound
	// conclusion if the things left out of the total are visible.
	UnvaluedAssets []string
}

// AggregateByCurrency turns a day of transfers into per-currency totals.
//
// usdPerNativeUnit converts native-coin transfers into USD, and may be nil
// when no price oracle is configured -- in which case native activity is
// listed among UnvaluedAssets rather than assumed to be worth nothing.
// That distinction is the whole point: a zero in a regulatory total has to
// mean "nothing moved", never "nobody could price it".
func AggregateByCurrency(
	txs []AssetTransaction,
	usdPerNativeUnit *big.Float,
) *AssetAggregation {
	agg := &AssetAggregation{}
	byCurrency := map[string]*CurrencyAggregate{}
	unvalued := map[string]bool{}

	get := func(currency string) *CurrencyAggregate {
		if c, ok := byCurrency[currency]; ok {
			return c
		}
		threshold, have := thresholdFor(currency)
		c := &CurrencyAggregate{
			Currency:      currency,
			ByAsset:       map[string]string{},
			Threshold:     threshold,
			HaveThreshold: have,
		}
		byCurrency[currency] = c
		return c
	}

	for _, tx := range txs {
		if tx.Undecoded() {
			agg.UndecodedCount++
			agg.UndecodedIDs = append(agg.UndecodedIDs, tx.RequestID)
			continue
		}

		if tx.Symbol == "NATIVE" {
			if usdPerNativeUnit == nil {
				unvalued["NATIVE"] = true
				continue
			}
			value, _ := new(big.Float).Mul(
				new(big.Float).SetPrec(256).SetInt(tx.Amount),
				usdPerNativeUnit,
			).Float64()
			c := get("USD")
			c.Total += value
			c.ByAsset["NATIVE"] = addDecimal(c.ByAsset["NATIVE"], tx.Amount, tx.Decimals)
			c.TransactionIDs = append(c.TransactionIDs, tx.RequestID)
			continue
		}

		if tx.Peg == "" {
			// An unpegged token. There is no price source, so it cannot
			// join a currency total -- but it is named, so a reader can
			// see what the total leaves out.
			unvalued[tx.Symbol] = true
			continue
		}

		c := get(tx.Peg)
		c.Total += toUnits(tx.Amount, tx.Decimals)
		c.ByAsset[tx.Symbol] = addDecimal(c.ByAsset[tx.Symbol], tx.Amount, tx.Decimals)
		c.TransactionIDs = append(c.TransactionIDs, tx.RequestID)
	}

	for _, c := range byCurrency {
		// At or above, not above: the same reading EvaluateCTRFrom uses.
		// Over-reporting is a filing, under-reporting is a violation.
		c.OverThreshold = c.HaveThreshold && c.Total >= c.Threshold
		agg.Currencies = append(agg.Currencies, c)
	}
	sort.Slice(agg.Currencies, func(i, j int) bool {
		return agg.Currencies[i].Currency < agg.Currencies[j].Currency
	})

	for symbol := range unvalued {
		agg.UnvaluedAssets = append(agg.UnvaluedAssets, symbol)
	}
	sort.Strings(agg.UnvaluedAssets)

	return agg
}

// toUnits converts base units to whole currency units.
//
// Arbitrary precision throughout, narrowed to a float64 only at the end.
// A token amount is an exact integer and 10^18 is well past what a float64
// holds exactly, so dividing after converting loses the low digits of
// every large amount -- which is precisely the range a reporting threshold
// cares about. Once divided, the value is a quantity of currency, and a
// float64 is exact to the cent far beyond any threshold in this file.
func toUnits(amount *big.Int, decimals int) float64 {
	if amount == nil {
		return 0
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	v, _ := new(big.Float).SetPrec(256).Quo(
		new(big.Float).SetPrec(256).SetInt(amount),
		new(big.Float).SetPrec(256).SetInt(scale),
	).Float64()
	return v
}

// addDecimal accumulates a per-asset total as an exact decimal string.
//
// Kept exact rather than as a float, because this is the figure that goes
// on a filing. A total a regulator can show is wrong in its last digits is
// a worse outcome than no total at all.
func addDecimal(existing string, amount *big.Int, decimals int) string {
	running := new(big.Int)
	if existing != "" {
		if parsed, ok := new(big.Int).SetString(stripDecimalPoint(existing, decimals), 10); ok {
			running = parsed
		}
	}
	running.Add(running, amount)
	return formatUnits(running, decimals)
}

func stripDecimalPoint(s string, decimals int) string {
	whole, frac := s, ""
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			whole, frac = s[:i], s[i+1:]
			break
		}
	}
	for len(frac) < decimals {
		frac += "0"
	}
	return whole + frac
}

// formatUnits renders base units as a decimal string, without floating
// point.
func formatUnits(amount *big.Int, decimals int) string {
	if decimals == 0 {
		return amount.String()
	}
	s := amount.String()
	negative := false
	if len(s) > 0 && s[0] == '-' {
		negative, s = true, s[1:]
	}
	for len(s) <= decimals {
		s = "0" + s
	}
	whole, frac := s[:len(s)-decimals], s[len(s)-decimals:]
	for len(frac) > 1 && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	if frac == "0" {
		if negative {
			return "-" + whole
		}
		return whole
	}
	if negative {
		return "-" + whole + "." + frac
	}
	return whole + "." + frac
}

// Narrative renders one currency's aggregate as the sentence a filing
// needs. Built here rather than at the call site so every filing the
// platform produces describes its own arithmetic the same way.
func (c *CurrencyAggregate) Narrative(customerID, chain string, day time.Time) string {
	assets := make([]string, 0, len(c.ByAsset))
	for symbol, total := range c.ByAsset {
		assets = append(assets, fmt.Sprintf("%s %s", total, symbol))
	}
	sort.Strings(assets)

	return fmt.Sprintf(
		"Customer %s moved %.2f %s on %s across %d transaction(s) on %s (%v), "+
			"at or above the %.2f %s reporting threshold.",
		customerID, c.Total, c.Currency, day.Format("2006-01-02"),
		len(c.TransactionIDs), chain, assets, c.Threshold, c.Currency,
	)
}
