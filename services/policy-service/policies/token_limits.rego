package policies

# Amount limits for tokens, denominated in the currency the token is
# pegged to.
#
# amount_limits.rego works in wei, which is the right unit for an ether
# transfer and a meaningless one for a stablecoin: 50,000 USDC is
# 50,000,000,000 base units, and compared against a hundred-ether ceiling
# written as 1e20 wei it passes by nine orders of magnitude. Before this
# file existed, every token amount rule in the platform was that
# comparison. The limit was present, it was evaluated, and it could not
# deny anything.
#
# `pegged_value` arrives already converted out of base units by the
# calling service, which does it in arbitrary precision -- dividing
# 10^18-scaled integers in float64 loses exactly the large amounts a limit
# is for. What arrives here is a quantity of currency.

# Ceilings per peg currency and tier.
#
# These are a statement of risk appetite, not an exchange rate. The rand
# figures are not the dollar figures converted -- they are what a South
# African settlement customer's ceiling should be, set independently, and
# an operator running this platform is expected to change them. They live
# in the policy bundle rather than the database deliberately: the deployed
# policy set is embedded in the binary and auditable, which is worth more
# for a limit than the convenience of editing it at runtime.
limits := {
	"USD": {"global": 1000000, "free": 10000, "pro": 100000},
	"ZAR": {"global": 18000000, "free": 180000, "pro": 1800000},
}

# The global ceiling applies to every tenant on every tier.
deny[msg] {
	input.has_pegged_value
	limit := limits[input.peg_currency].global
	input.pegged_value > limit
	msg := sprintf("amount exceeds the global limit of %v %s for %s", [limit, input.peg_currency, input.asset])
}

deny[msg] {
	input.has_pegged_value
	input.customer_tier == "free"
	limit := limits[input.peg_currency].free
	input.pegged_value > limit
	msg := sprintf("free tier limited to %v %s per transaction", [limit, input.peg_currency])
}

deny[msg] {
	input.has_pegged_value
	input.customer_tier == "pro"
	limit := limits[input.peg_currency].pro
	input.pegged_value > limit
	msg := sprintf("pro tier limited to %v %s per transaction", [limit, input.peg_currency])
}

# A pegged token in a currency this file has no limits for is denied.
#
# Fail closed. The alternative is that registering a token in a new
# currency silently creates an unlimited payment rail, and nothing about
# the system looks any different when it happens.
deny[msg] {
	input.has_pegged_value
	not limits[input.peg_currency]
	msg := sprintf("no amount limits are configured for %s; refusing to transact %s until they are", [input.peg_currency, input.asset])
}

# A token with no peg has no value this service can assert, so it cannot be
# held to a money limit. Routed to a human rather than denied: plenty of
# legitimate tokens are not stablecoins, and refusing all of them outright
# would be a bigger claim than the missing information justifies.
require_approval[msg] {
	input.is_token
	not input.has_pegged_value
	not input.is_allowance
	msg := sprintf("%s is not a pegged asset, so its value cannot be checked against a limit; approval required", [input.asset])
}

# Large transfers go to an approver even when they are within the ceiling.
high_value := {"USD": 100000, "ZAR": 1800000}

require_approval[msg] {
	input.has_pegged_value
	threshold := high_value[input.peg_currency]
	input.pegged_value > threshold
	msg := sprintf("high-value transfer (> %v %s) requires approval", [threshold, input.peg_currency])
}

# -- allowances --
#
# approve() moves nothing at the moment it is signed and hands the spender
# the right to move the amount whenever they like, possibly long after the
# policy that permitted it was changed. A control that reads an approval
# as a zero-value call is not a control, and an unlimited approval is the
# single most common way a token balance is emptied.

# The uint256 maximum, and anything near it, is an unlimited approval.
# Compared as a float against 1e70 rather than the exact 2^256-1: the
# value has already been divided by the token's scale, and no real
# allowance comes within sixty orders of magnitude of this.
deny[msg] {
	input.is_allowance
	input.pegged_value > 1e70
	msg := "refusing an unlimited token approval; approve a specific amount instead"
}

# Every other approval is allowed but escalated. The amount is exposure,
# not a payment, and somebody should look at it.
require_approval[msg] {
	input.is_allowance
	input.pegged_value <= 1e70
	msg := sprintf("approving %s to spend %s requires approval; an allowance can be drawn at any later time", [input.to, input.asset])
}
