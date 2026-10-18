#!/usr/bin/env bash
# Tests scripts/govulncheck-gate.py against a fake govulncheck that emits the documented JSON
# stream, because the real tool cannot always run where this is tested (its database is
# fetched from vuln.go.dev). Checks the three decisions the gate exists to make: fail on an
# unaccepted called vulnerability, pass on an accepted one, fail on an expired acceptance.
set -uo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export GOVULNCHECK="$PWD/scripts/testdata/fake-govulncheck"
ACC=docs/security/accepted-vulnerabilities.json; BAK="$(mktemp)"; cp "$ACC" "$BAK"; trap 'cp "$BAK" "$ACC"; rm -f "$BAK"' EXIT
fail=0; check() { if [ "$2" != "$3" ]; then echo "FAIL $1: expected exit $2, got $3"; fail=1; else echo "ok   $1"; fi; }
python3 scripts/govulncheck-gate.py services/mpc-signer services/webhooks >/dev/null; check "accepted and uncalled findings pass" 0 $?
python3 scripts/govulncheck-gate.py services/backup >/dev/null; check "an unaccepted called finding fails" 1 $?
sed -i 's/"expires": "[0-9-]*"/"expires": "2020-01-01"/' "$ACC"
python3 scripts/govulncheck-gate.py services/mpc-signer >/dev/null; check "an expired acceptance fails" 1 $?
exit $fail
