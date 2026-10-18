#!/usr/bin/env bash
# Assembles the evidence bundle to hand to an auditor, penetration tester or SOC 2 firm.
#
#   scripts/assurance-pack.sh                 writes dist/assurance/<commit>/ and a .tar.gz
#   OUT=/some/dir scripts/assurance-pack.sh
#
# A review is a statement about one commit, so everything here is tied to the commit it was
# built from, and refuses a dirty working tree (a bundle that does not match any commit is a
# bundle nobody can reproduce). It collects what exists; it does not run the test suites --
# scripts/verify-all.sh does that, and its output is included if you saved it.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [ -n "$(git status --porcelain --untracked-files=no)" ] && [ -z "${ALLOW_DIRTY:-}" ]; then
  echo "working tree has uncommitted changes; commit first (or ALLOW_DIRTY=1 for a draft)" >&2; exit 1
fi
COMMIT="$(git rev-parse HEAD)"; SHORT="$(git rev-parse --short HEAD)"
OUT="${OUT:-$ROOT/dist/assurance/$SHORT}"
rm -rf "$OUT"; mkdir -p "$OUT"/{dependencies,database,docs,ci}

{
  echo "commit:   $COMMIT"
  echo "branch:   $(git rev-parse --abbrev-ref HEAD)"
  echo "built:    $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "dirty:    $([ -n "$(git status --porcelain --untracked-files=no)" ] && echo yes || echo no)"
} > "$OUT/COMMIT.txt"

echo "== dependencies"
if [ -d services/api-gateway/node_modules ]; then
  (cd services/api-gateway && npm ls --omit=dev --all --json > "$OUT/dependencies/npm-gateway-prod.json" 2>/dev/null || true
   npm audit --omit=dev --json > "$OUT/dependencies/npm-gateway-audit.json" 2>/dev/null || true)
else echo "   (npm ci in services/api-gateway to include the npm lists)"; fi
for m in services/* sdks/sdk-go; do
  [ -f "$m/go.mod" ] || continue
  (cd "$m" && go list -m -json all > "$OUT/dependencies/go-$(echo "$m" | tr / -).json" 2>/dev/null) || true
done
cp services/api-gateway/package-lock.json "$OUT/dependencies/" 2>/dev/null || true

echo "== database"
ls infrastructure/database/migrations > "$OUT/database/migrations.txt"
(cd infrastructure/database/migrations && sha256sum *.sql) > "$OUT/database/migrations.sha256"
grep -l "ENABLE ROW LEVEL SECURITY" infrastructure/database/migrations/*.sql | xargs -n1 basename > "$OUT/database/files-enabling-rls.txt" || true

echo "== docs and CI"
cp docs/assurance/*.md "$OUT/docs/"
for f in docs/security/AUDIT-READINESS.md docs/security/threat-model.md docs/security/TSS-LIB-ADVISORY-REVIEW.md docs/security/SUPPLY-CHAIN.md \
         docs/deployment/KEY-RECOVERY.md docs/deployment/PARTY-ISOLATION.md docs/deployment/TRISA.md docs/deployment/TOKENISATION.md \
         docs/deployment/MULTI-CUSTODIAN.md docs/deployment/SWEEPS.md LAUNCH-CHECKLIST.md; do
  [ -f "$f" ] && cp "$f" "$OUT/docs/"
done
cp .github/workflows/*.y*ml "$OUT/ci/" 2>/dev/null || true
(cd services/api-gateway && node scripts/route-inventory.js > "$OUT/docs/route-inventory.generated.md")
git log --format='%H %an %ad %s' --date=short -n 200 -- services/mpc-party services/mpc-signer services/temporal-worker \
    services/api-gateway/src/approvals services/api-gateway/src/keys services/api-gateway/src/travel-rule/trisa contracts > "$OUT/signing-layer-history.txt"
[ -f "${VERIFY_LOG:-}" ] && cp "$VERIFY_LOG" "$OUT/verify-all.log" || echo "   (set VERIFY_LOG=<file> with the output of scripts/verify-all.sh to include it)"

( cd "$OUT" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS )
tar -C "$(dirname "$OUT")" -czf "$OUT.tar.gz" "$(basename "$OUT")"
echo "== $(find "$OUT" -type f | wc -l) files -> $OUT.tar.gz"
echo "   This bundle is a description of commit $SHORT. It is not an attestation of anything."
