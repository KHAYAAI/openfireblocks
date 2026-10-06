#!/usr/bin/env bash
#
# Everything that can be checked on one machine, in one command, with an honest
# summary of what ran, what failed and what was skipped (and why).
#
#   ./scripts/verify-all.sh            # everything available
#   ./scripts/verify-all.sh --quick    # builds, vet, unit tests, chart; no databases, no e2e
#
# What this is not: a launch decision. It cannot tell you about a real cluster,
# a real chain, Stripe, an independent audit, a penetration test or SOC 2. Those
# are listed in LAUNCH-CHECKLIST.md section 1.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
QUICK=0; [ "${1:-}" = "--quick" ] && QUICK=1
LOGDIR="$(mktemp -d)"
declare -a NAMES STATUS NOTES
record() { NAMES+=("$1"); STATUS+=("$2"); NOTES+=("${3:-}"); printf '%-6s %s %s\n' "$2" "$1" "${3:+($3)}"; }
run() { # name, command...
  local name="$1"; shift
  if "$@" > "$LOGDIR/$(echo "$name" | tr -c 'A-Za-z0-9' _).log" 2>&1; then record "$name" PASS; else record "$name" FAIL "log: $LOGDIR/$(echo "$name" | tr -c 'A-Za-z0-9' _).log"; fi
}
skip() { record "$1" SKIP "$2"; }
have() { command -v "$1" >/dev/null 2>&1; }

echo "== Go services: build, vet, tests"
for d in "$ROOT"/services/*/; do
  m="$(basename "$d")"; [ -f "$d/go.mod" ] || continue
  run "go build $m" bash -c "cd '$d' && go build ./..."
  # mpc-party has one known vet finding (copylocks on a tss-lib type, which CI also exempts)
  if [ "$m" = "mpc-party" ]; then run "go vet $m (copylocks exempt)" bash -c "cd '$d' && go vet -copylocks=false ./..."; else run "go vet $m" bash -c "cd '$d' && go vet ./..."; fi
  if [ "$m" = "mpc-party" ]; then
    run "go test $m (short; real-DKG tests run in CI and the drills)" bash -c "cd '$d' && go test -short -count=1 ./..."
  else
    run "go test $m" bash -c "cd '$d' && go test -short -count=1 ./..."
  fi
done

echo "== API gateway"
GW="$ROOT/services/api-gateway"
if [ -d "$GW/node_modules" ]; then
  run "gateway typecheck" bash -c "cd '$GW' && npx --no-install tsc --noEmit"
  run "gateway build" bash -c "cd '$GW' && npx --no-install nest build"
  run "gateway npm audit (production, high+)" bash -c "cd '$GW' && npm audit --omit=dev --audit-level=high"
  if [ "$QUICK" = 1 ]; then skip "gateway tests (with live Postgres)" "--quick"
  elif have psql && { have pg_ctl || [ -d /usr/lib/postgresql ]; }; then
    export PGLOCAL_PORT=55497 PGLOCAL_DIR="${TMPDIR:-/tmp}/ofb-verify-pg-$$"
    if eval "$("$ROOT/infrastructure/local/postgres-local.sh" start 2>"$LOGDIR/pg.log")"; then
      run "gateway tests (live Postgres, every migration)" bash -c "cd '$GW' && REQUIRE_LIVE_DB=1 npx --no-install jest"
      "$ROOT/infrastructure/local/postgres-local.sh" stop >/dev/null 2>&1; rm -rf "$PGLOCAL_DIR"
    else record "gateway tests (live Postgres)" FAIL "could not start Postgres: $LOGDIR/pg.log"; fi
  else skip "gateway tests (with live Postgres)" "no PostgreSQL server binaries"; fi
else skip "gateway" "run npm ci in services/api-gateway"; fi

echo "== Kubernetes chart"
if have helm; then
  run "helm lint" helm lint "$ROOT/infrastructure/helm/openfireblocks"
  run "helm template (defaults)" bash -c "helm template ci '$ROOT/infrastructure/helm/openfireblocks' >/dev/null"
  ALL=(--set mpcParty.mtls.enabled=true --set mpcParty.mtls.autoIssue.enabled=true --set temporalWorker.mtls.enabled=true --set temporalWorker.mtls.autoIssue.enabled=true
       --set oidc.enabled=true --set oidc.issuer=https://id.example/realms/t --set oidc.clientId=ofb --set oidc.redirectUri=https://c.example/console/sso-callback
       --set billing.consolePublicUrl=https://c.example --set billing.returnHosts=c.example --set networkPolicy.enabled=true --set 'networkPolicy.gatewayIngressNamespaces={ingress-nginx}'
       --set serviceMonitor.enabled=true --set ingress.enabled=true --set backup.enabled=true --set backup.offsite.enabled=true --set backup.offsite.bucket=b --set backup.offsite.keySecret=k)
  run "helm template (every optional feature)" bash -c "helm template ci '$ROOT/infrastructure/helm/openfireblocks' ${ALL[*]@Q} >/dev/null"
  KC="$(command -v kubeconform || echo "$(go env GOPATH 2>/dev/null)/bin/kubeconform")"
  if [ -x "$KC" ]; then run "kubeconform strict (every optional feature)" bash -c "helm template ci '$ROOT/infrastructure/helm/openfireblocks' ${ALL[*]@Q} | '$KC' -strict -ignore-missing-schemas -summary -kubernetes-version 1.29.0"
  else skip "kubeconform" "not installed (go install github.com/yannh/kubeconform/cmd/kubeconform@latest)"; fi
  have python3 && python3 -c "import yaml" 2>/dev/null && run "chart invariants" python3 "$ROOT/infrastructure/helm/test-chart-invariants.py" || skip "chart invariants" "needs python3 with pyyaml"
else skip "chart checks" "helm not installed"; fi

echo "== Whole-platform runs (real processes)"
if [ "$QUICK" = 1 ]; then skip "full-stack end to end" "--quick"; skip "OIDC against an independent provider" "--quick"; skip "recovery drill (Vault)" "--quick"; skip "recovery drill (file store)" "--quick"
else
  TBIN="${TEMPORAL_BIN:-$(command -v temporal || true)}"
  if [ -n "$TBIN" ] && [ -d "$GW/node_modules" ]; then run "full-stack end to end (real DKG, threshold signing, approvals, freeze, console)" env TEMPORAL_BIN="$TBIN" "$ROOT/infrastructure/local/e2e-fullstack-local.sh"
  else skip "full-stack end to end" "needs the temporal CLI (TEMPORAL_BIN) and npm ci"; fi
  if [ -d "$GW/node_modules" ] && have npm; then run "OIDC against an independent provider" "$ROOT/infrastructure/local/e2e-oidc-local.sh"; else skip "OIDC against an independent provider" "needs npm and npm ci"; fi
  if have vault; then run "recovery drill (Vault)" "$ROOT/infrastructure/local/recovery-drill-local.sh"; else skip "recovery drill (Vault)" "no vault binary"; fi
  run "recovery drill (file store, no Vault)" env SEAL=file "$ROOT/infrastructure/local/recovery-drill-local.sh"
fi

echo
echo "== Summary"
p=0; f=0; s=0
for i in "${!NAMES[@]}"; do case "${STATUS[$i]}" in PASS) p=$((p+1));; FAIL) f=$((f+1));; SKIP) s=$((s+1));; esac; done
printf 'passed %d, failed %d, skipped %d\n' "$p" "$f" "$s"
if [ "$s" -gt 0 ]; then echo "Skipped checks did not run; they are not passes:"; for i in "${!NAMES[@]}"; do [ "${STATUS[$i]}" = SKIP ] && echo "  - ${NAMES[$i]}: ${NOTES[$i]}"; done; fi
[ "$f" -eq 0 ]
