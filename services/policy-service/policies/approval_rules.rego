package policies

# Approval, whitelist and geographic policies.

high_value_wei := 10000000000000000000 # 10 ETH

# Transactions above the high-value threshold require a human approval step.
# This is surfaced (not a hard deny) so the workflow can route to an approver.
require_approval[msg] {
	input.value_wei > high_value_wei
	msg := "high-value transaction (> 10 ETH) requires approval"
}

# Whitelist enforcement: when a tenant supplies a non-empty destination
# whitelist, deny any recipient not on it.
deny[msg] {
	count(input.whitelist) > 0
	not destination_whitelisted
	msg := sprintf("destination %s is not on the tenant whitelist", [input.to])
}

destination_whitelisted {
	some i
	lower(input.whitelist[i]) == lower(input.to)
}

# Geographic blocking: deny when the request's country is on the tenant's
# blocked list.
deny[msg] {
	some i
	lower(input.blocked_countries[i]) == lower(input.country)
	msg := sprintf("transactions from %s are blocked for this tenant", [input.country])
}
