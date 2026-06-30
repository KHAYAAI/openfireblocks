package policies

# Amount-limit policies. `value_wei` is supplied as a JSON number (wei).
#
# Note on precision: wei values above 2^53 are not exactly representable as
# float64, so these threshold checks are accurate to within a few thousand wei
# of the boundary — fine for coarse policy limits. Hard, exact-boundary limits
# should be enforced with big-integer comparison in the calling service.

global_limit_wei := 100000000000000000000 # 100 ETH
free_limit_wei := 10000000000000000000 #  10 ETH
pro_limit_wei := 50000000000000000000 #  50 ETH

# Global ceiling applies to every tenant.
deny[msg] {
	input.value_wei > global_limit_wei
	msg := "amount exceeds global limit of 100 ETH"
}

# Free tier ceiling.
deny[msg] {
	input.customer_tier == "free"
	input.value_wei > free_limit_wei
	msg := "free tier limited to 10 ETH per transaction"
}

# Pro tier ceiling.
deny[msg] {
	input.customer_tier == "pro"
	input.value_wei > pro_limit_wei
	msg := "pro tier limited to 50 ETH per transaction"
}

# enterprise tier: only the global ceiling applies.
