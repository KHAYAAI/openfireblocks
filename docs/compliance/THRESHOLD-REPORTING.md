# Threshold reporting, and what the platform does not know

This document exists because the numbers in it are wrong eventually, and
somebody needs to know that before they rely on them.

## What was broken

`EvaluateCTRFrom` sums `signing.transactions.amount` — the native value
attached to a transaction — and multiplies by a native/USD rate.

An ERC-20 transfer carries its recipient and amount inside the calldata.
Its native value is zero. So a day in which a customer moved fifty million
rand of a stablecoin aggregated to nothing, the threshold was never
crossed, and no filing was ever raised. The platform would have been under
a statutory duty with no record that the duty arose.

Worse, there was a second gap underneath it. `signing.transactions` was
written only by the legacy single-key path (`POST /sign` → `mpc-signer`).
Everything signed through a real threshold ceremony — which is the entire
product — went into `signing_requests`, whose columns are a digest and a
blob: no recipient, no amount, no asset. The regulatory aggregate was
summing a table the platform's main signing route never wrote to.

Both are closed. `signing.transactions` now carries `asset_symbol`,
`asset_decimals`, `asset_peg`, `effective_to` and `effective_amount`,
written by the threshold path, and `AggregateByCurrency` sums those.

## How aggregation works now

Per customer, per chain, per calendar day (UTC), activity is totalled
**per currency** and each total is compared against that currency's own
threshold.

Per currency, not converted into one figure, because this codebase has no
FX source. A customer who moved $9,000 and R9,000 in a day has crossed
neither threshold. Saying so is correct; saying they crossed one because
both were summed through a guessed rate would not be.

A pegged token is valued at its peg. One USDC is one dollar, one ZARP is
one rand. That is an issuer's undertaking rather than a market price, and
for a *threshold* it is the conservative reading — a stablecoin trading at
0.97 would otherwise let a threshold be dodged by three percent, and
under-reporting is the failure that matters.

Native-coin activity needs a rate, and this platform has no price oracle.
Without one it is reported under `unvalued_assets` rather than counted as
zero. **A zero in a regulatory total must mean "nothing moved", never
"nobody could price it."**

Three things are surfaced at the top of every response for the same
reason:

| Field | Means |
|---|---|
| `undecoded_count` / `undecoded_ids` | Transfers whose calldata nobody could read. Not in any total. |
| `unvalued_assets` | Assets moved that have no peg and no rate. Not in any total. |
| `have_threshold: false` | A currency nobody has configured a threshold for. **Not judged**, as distinct from not over. |

A total is only sound if what it leaves out is visible.

## The numbers, and why you must check them

| Constant | Default | Source |
|---|---|---|
| `DefaultCTRThresholdUSD` | 10,000 USD | FinCEN currency transaction report |
| `DefaultCTRThresholdZAR` | 49,999.99 ZAR | FIC Act cash threshold report |
| `DefaultTravelRuleThresholdZAR` | 5,000 ZAR | FIC Directive 9 travel rule |

**These defaults have not been confirmed against the current directives by
anyone qualified to do so.** They are a starting point placed here so the
machinery has something to run against, and they are configurable —
`CTR_THRESHOLD_USD`, `CTR_THRESHOLD_ZAR` — precisely because a statutory
threshold is a number that changes by directive, and one compiled into a
binary is one that silently goes stale.

Before this platform is relied on for filing in any jurisdiction, a
compliance officer must confirm each threshold against the current source
and set it explicitly. Treat the defaults as placeholders that happen to
be plausible, not as advice.

## What the platform does not do

Stated plainly, because a compliance feature that is half-present is worse
than one that is absent — the absent one does not get relied upon.

- **It does not file anything.** It raises drafts. Submission to FinCEN or
  the FIC is manual and there is no integration with either.
- **It does not transmit travel-rule information.** The rand threshold is
  recorded and the aggregation can tell you a transfer crossed it. The
  platform does not attach originator or beneficiary details to a transfer
  or exchange them with a counterparty VASP.
- **It does not screen against sanctions lists in real time** beyond the
  static list embedded in `policy-service/sanctions.json`.
- **It has no price oracle**, so native-coin activity is unvalued unless a
  rate is supplied per call.
- **It does not know about jurisdictions it was not told about.** There is
  no logic that infers which regulator applies from a customer's address.
  Currency is inferred from the asset's peg, nothing more.

## South Africa specifically

The rand path is built because a South African settlement customer is the
clearest commercial case for this platform, not because the platform is
licensed for that market. Operating as a crypto asset service provider in
South Africa requires FSCA authorisation under FAIS and registration as an
accountable institution under the FIC Act. **Neither is a property of this
software.** The platform can produce the records such an institution needs;
it cannot make anyone into one.

## Where to look

| What | Where |
|---|---|
| Aggregation | `services/compliance/asset_reporting.go` |
| Its tests | `services/compliance/asset_reporting_test.go` |
| HTTP surface | `POST /v1/regulatory/thresholds/evaluate` |
| Schema | `infrastructure/database/migrations/021_token_registry.sql` |
| End-to-end proof | `infrastructure/kind/stablecoin-drill.sh` |
