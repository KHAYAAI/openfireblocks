# Assurance

Everything an auditor, penetration tester or SOC 2 firm needs, and the plan for engaging them.
**Nothing here is an attestation; no firm is engaged; no report exists.**

* [PROCUREMENT-PLAN.md](PROCUREMENT-PLAN.md): what is ready, what only a person can do, in order
* [AUDIT-SCOPE-ADDENDUM.md](AUDIT-SCOPE-ADDENDUM.md): what to add to `docs/security/AUDIT-READINESS.md`
* [PENTEST-SCOPE.md](PENTEST-SCOPE.md): targets, attack paths, rules of engagement
* [SOC2-CONTROL-MATRIX.md](SOC2-CONTROL-MATRIX.md): each criterion, its evidence, its gap
* [ROUTE-INVENTORY.md](ROUTE-INVENTORY.md): every HTTP route with its guards and roles (generated)

`scripts/assurance-pack.sh` bundles the evidence for a firm; `scripts/collect-soc2-evidence.sh`
snapshots operational evidence on a schedule.
