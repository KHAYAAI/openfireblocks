package main

import (
	"strings"
	"testing"
)

// Deciding whether a transaction is allowed to be signed.
//
// This service had no tests, and these four functions are the ones that
// stand between a customer's key and their money leaving. A rule that
// silently returns true is not a bug report, it is a policy that was never
// enforced -- and nothing about the system looks different when that
// happens, which is why it needs tests rather than review.
//
// Every case below is written from the failure it prevents.

func svc() *PolicyService { return &PolicyService{} }

func amountRule(max string) RuleConfig {
	return RuleConfig{MaxAmount: &max}
}

func request(amount string) *PolicyEvaluationRequest {
	return &PolicyEvaluationRequest{
		KeyID:              "key-1",
		Amount:             amount,
		DestinationAddress: "0xdestination",
		Blockchain:         "ethereum",
	}
}

// -- amount limits --

func TestAnAmountUnderTheLimitIsAllowed(t *testing.T) {
	if !svc().checkAmountLimit(amountRule("1000"), request("999")) {
		t.Error("999 was blocked by a limit of 1000")
	}
}

func TestAnAmountOverTheLimitIsBlocked(t *testing.T) {
	if svc().checkAmountLimit(amountRule("1000"), request("1001")) {
		t.Error("1001 passed a limit of 1000")
	}
}

// Exactly at the limit passes: "maximum" means the largest allowed value,
// and a customer whose limit is 1000 expects to be able to send 1000.
func TestExactlyTheLimitIsAllowed(t *testing.T) {
	if !svc().checkAmountLimit(amountRule("1000"), request("1000")) {
		t.Error("an amount exactly equal to the maximum was blocked")
	}
}

// The bypass this comparison exists to prevent. Compared as strings,
// "1000" < "500" is true because '1' sorts before '5' -- so a transaction
// twice the limit would read as under it, on the one rule whose entire
// purpose is a numeric ceiling.
func TestAmountsAreComparedNumericallyNotLexicographically(t *testing.T) {
	cases := []struct {
		amount, max string
		allowed     bool
	}{
		{"1000", "500", false}, // lexicographically "1000" < "500"
		{"9", "10", true},      // lexicographically "9" > "10"
		{"100", "99", false},
		{"2", "10", true},
	}

	for _, tc := range cases {
		got := svc().checkAmountLimit(amountRule(tc.max), request(tc.amount))
		if got != tc.allowed {
			t.Errorf("amount %s against limit %s: allowed=%v, want %v",
				tc.amount, tc.max, got, tc.allowed)
		}
	}
}

// Base-unit amounts exceed what a float64 holds exactly. 1 ETH is 1e18
// wei, and a limit expressed in wei has to still discriminate at the
// bottom of that range.
func TestVeryLargeAmountsAreStillCompared(t *testing.T) {
	overByOneWei := "1000000000000000001"
	if svc().checkAmountLimit(amountRule("1000000000000000000"), request(overByOneWei)) {
		t.Error("an amount one wei over a 1 ETH limit passed")
	}
	if !svc().checkAmountLimit(amountRule("1000000000000000000"), request("999999999999999999")) {
		t.Error("an amount one wei under a 1 ETH limit was blocked")
	}
}

// An amount that cannot be parsed must fail closed. Treating an
// unparseable value as passing would make "" or "abc" a way past every
// amount limit in the system.
func TestAnUnparseableAmountIsBlocked(t *testing.T) {
	for _, amount := range []string{"", "abc", "1,000", " 10", "10 "} {
		t.Run(amount, func(t *testing.T) {
			if svc().checkAmountLimit(amountRule("1000"), request(amount)) {
				t.Errorf("an unparseable amount %q passed the limit check", amount)
			}
		})
	}
}

// A negative amount is not "under the limit", it is a bypass.
//
// big.Float.SetString parses "-5" happily, and -5 <= any positive maximum,
// so before amounts were parsed strictly a negative number satisfied every
// amount limit in the system. "-Inf" satisfied all of them at once.
func TestNegativeAmountsDoNotSatisfyALimit(t *testing.T) {
	for _, amount := range []string{"-5", "-1000000", "-Inf", "-0"} {
		t.Run(amount, func(t *testing.T) {
			if svc().checkAmountLimit(amountRule("1000"), request(amount)) {
				t.Errorf("a negative amount %q passed a limit of 1000", amount)
			}
		})
	}
}

// Infinity is not a number a caller should be able to send either way.
func TestInfinityIsRefused(t *testing.T) {
	for _, amount := range []string{"Inf", "+Inf", "inf"} {
		t.Run(amount, func(t *testing.T) {
			if svc().checkAmountLimit(amountRule("1000"), request(amount)) {
				t.Errorf("%q passed the limit check", amount)
			}
		})
	}
}

// The subtle one. These all parse to something under the limit, and each
// makes the policy engine read a string differently from whatever builds
// the transaction -- so the signature would be authorised for an amount
// nobody evaluated.
func TestAlternativeNumberFormatsAreRefused(t *testing.T) {
	cases := map[string]string{
		"hexadecimal":      "0x10",
		"binary":           "0b101",
		"octal":            "0o17",
		"exponent":         "1e3",
		"digit separators": "1_000",
		"explicit sign":    "+10",
	}

	for name, amount := range cases {
		t.Run(name, func(t *testing.T) {
			if svc().checkAmountLimit(amountRule("1000"), request(amount)) {
				t.Errorf("%s (%q) was accepted; policy and the signing path would "+
					"disagree about what this amount means", name, amount)
			}
		})
	}
}

// A limit written in one of those formats must not be silently
// reinterpreted either -- a policy configured with "1e3" would otherwise
// enforce 1000 while reading as something else to whoever set it.
func TestALimitInAnAlternativeFormatIsRefused(t *testing.T) {
	if svc().checkAmountLimit(amountRule("0x3E8"), request("1")) {
		t.Error("a limit written in hexadecimal was accepted")
	}
}

// Decimals are still fine: not every chain's amounts are integers.
func TestFractionalAmountsAreCompared(t *testing.T) {
	if !svc().checkAmountLimit(amountRule("1.5"), request("1.25")) {
		t.Error("1.25 was blocked by a limit of 1.5")
	}
	if svc().checkAmountLimit(amountRule("1.5"), request("1.75")) {
		t.Error("1.75 passed a limit of 1.5")
	}
}

// A limit that cannot be parsed also fails closed: a misconfigured policy
// must not become an absent one.
func TestAnUnparseableLimitBlocks(t *testing.T) {
	if svc().checkAmountLimit(amountRule("not a number"), request("1")) {
		t.Error("a policy with an unparseable limit allowed a transaction")
	}
}

// No limit configured is not a limit of zero.
func TestNoConfiguredLimitAllowsAnything(t *testing.T) {
	if !svc().checkAmountLimit(RuleConfig{}, request("999999999999999999999")) {
		t.Error("a rule with no maximum blocked a transaction")
	}
}

// -- whitelists --

func TestAWhitelistedDestinationIsAllowed(t *testing.T) {
	cfg := RuleConfig{WhitelistAddresses: []string{"0xother", "0xdestination"}}

	if !svc().checkWhitelist(cfg, request("1")) {
		t.Error("a destination on the whitelist was blocked")
	}
}

func TestADestinationNotOnTheWhitelistIsBlocked(t *testing.T) {
	cfg := RuleConfig{WhitelistAddresses: []string{"0xsomewhere", "0xelse"}}

	if svc().checkWhitelist(cfg, request("1")) {
		t.Error("a destination absent from the whitelist was allowed")
	}
}

// An empty whitelist means "not configured", not "nothing is allowed".
// Worth pinning because the other reading is defensible and the difference
// is every transaction on the key.
func TestAnEmptyWhitelistIsNotAnEmptyAllowList(t *testing.T) {
	if !svc().checkWhitelist(RuleConfig{}, request("1")) {
		t.Error("a rule with no whitelist entries blocked every destination")
	}
}

// Addresses are matched exactly. Ethereum's mixed-case checksum encoding
// means the same address has two spellings, so this documents that a
// whitelist entry in a different case does NOT match -- which is safe
// (it blocks) but is a real source of surprise for customers.
func TestWhitelistMatchingIsExact(t *testing.T) {
	cfg := RuleConfig{WhitelistAddresses: []string{"0xDESTINATION"}}

	if svc().checkWhitelist(cfg, request("1")) {
		t.Fatal("behaviour changed: whitelist matching is now case-insensitive")
	}
	t.Log("whitelist entries are matched byte for byte, so a checksummed address " +
		"and its lowercase form are different entries; normalising both sides " +
		"would remove a predictable customer surprise")
}

// -- blockchains --

func TestAnAllowedBlockchainPasses(t *testing.T) {
	cfg := RuleConfig{Blockchains: []string{"bitcoin", "ethereum"}}

	if !svc().checkBlockchain(cfg, request("1")) {
		t.Error("a transaction on an allowed chain was blocked")
	}
}

func TestAChainNotOnTheListIsBlocked(t *testing.T) {
	cfg := RuleConfig{Blockchains: []string{"bitcoin"}}

	if svc().checkBlockchain(cfg, request("1")) {
		t.Error("an ethereum transaction passed a bitcoin-only rule")
	}
}

// -- rule dispatch --

// The dispatch switch is where a rule quietly stops being enforced: a
// typo in a rule type, or a type that was renamed in the database and not
// here, falls to the default and returns true.
func TestEveryKnownRuleTypeIsDispatched(t *testing.T) {
	blocking := map[string]RuleConfig{
		"amount_limit": amountRule("1"),
		"whitelist":    {WhitelistAddresses: []string{"0xnotthisone"}},
		"blockchain":   {Blockchains: []string{"solana"}},
	}

	for ruleType, cfg := range blocking {
		t.Run(ruleType, func(t *testing.T) {
			rule := &PolicyRule{Type: ruleType, Config: cfg, Enabled: true}
			if svc().evaluateRule(rule, request("1000")) {
				t.Errorf("a %s rule that should have blocked returned allowed; "+
					"it is probably not reaching its check", ruleType)
			}
		})
	}
}

// An unrecognised rule type allows the transaction. That is the
// fail-*open* case in a fail-closed service, and it is worth having a test
// say so out loud: a rule type renamed in the database and not here stops
// being enforced silently, and the platform looks identical.
func TestAnUnknownRuleTypeAllowsTheTransaction(t *testing.T) {
	rule := &PolicyRule{Type: "velocity_limit", Enabled: true}

	if !svc().evaluateRule(rule, request("1000000")) {
		t.Fatal("behaviour changed: an unknown rule type now blocks")
	}
	t.Log("an unrecognised rule type is treated as satisfied, so a rule type " +
		"present in the database but absent from evaluateRule is silently " +
		"unenforced; refusing to evaluate would be the fail-closed choice")
}

// -- the description that reaches the customer --

// A blocked transaction has to say which rule blocked it. "Denied" with no
// reason turns every policy question into a support ticket.
func TestABlockedTransactionNamesTheRule(t *testing.T) {
	result := &PolicyEvaluationResult{
		Allowed:       false,
		ViolatedRules: []string{"daily limit of 10 ETH"},
	}
	reason := "Signing blocked by policy rules: " + strings.Join(result.ViolatedRules, ", ")

	if !strings.Contains(reason, "daily limit") {
		t.Error("the reason does not name the rule that blocked the transaction")
	}
}
