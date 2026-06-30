package policies

# Sanctions screening. Denies any transaction whose destination is on the
# sanctions list. The list is provided as data (data.sanctions.addresses),
# loaded from sanctions.json at startup, so it can be refreshed from the
# official OFAC SDN feed without changing policy code.

deny[msg] {
	addrs := data.sanctions.addresses
	some i
	lower(addrs[i]) == lower(input.to)
	msg := sprintf("destination %s is on the sanctions list", [input.to])
}
