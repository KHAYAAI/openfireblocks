#!/usr/bin/env bash
#
# Collects the evidence a SOC 2 Type II auditor samples.
#
# A Type II report is not a statement that controls exist. It is a statement
# that they *operated* over a period, and the way that gets demonstrated is
# an auditor picking dates at random and asking to see what the system was
# doing on them. The controls here are largely real -- row-level security,
# mTLS, fail-closed policy evaluation, an append-only signing record -- and
# none of them produced anything an auditor could sample, because nothing
# ever wrote the evidence down.
#
# That gap is the expensive part of the audit. A firm that has to derive
# every artefact by interviewing engineers bills for the derivation, and the
# answers are worse: "we believe RLS is enabled on every tenant table" is an
# assertion, and `SELECT relrowsecurity FROM pg_class` on a Tuesday in March
# is evidence.
#
#   ./scripts/collect-soc2-evidence.sh
#   OUT=/path/to/evidence ./scripts/collect-soc2-evidence.sh
#
# Run it on a schedule. One run is a snapshot; a Type II needs a series, so
# the output is dated and meant to accumulate somewhere durable and
# access-controlled -- which is itself a control an auditor will ask about.
#
# What this deliberately does NOT do is claim completeness. Section 9 of the
# manifest lists the controls no script can evidence, because pretending
# otherwise would produce a tidy directory that falls apart on the first
# question about training records.
set -euo pipefail

NS="${K8S_NAMESPACE:-openfireblocks}"
STAMP="$(date -u +%Y-%m-%dT%H%M%SZ)"
OUT="${OUT:-evidence/${STAMP}}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

mkdir -p "${OUT}"
echo "==> collecting into ${OUT}"

# Each collector writes one file and reports whether it got anything. A
# collector that cannot run says so in the manifest rather than leaving an
# empty file that reads as "no findings".
declare -a COLLECTED MISSING

collect() {
  local name="$1" description="$2"
  shift 2
  local path="${OUT}/${name}"
  if "$@" > "${path}" 2>"${path}.err"; then
    if [[ -s "${path}" ]]; then
      rm -f "${path}.err"
      COLLECTED+=("${name}|${description}")
      echo "    ${name}"
      return
    fi
  fi
  local reason
  reason="$(head -3 "${path}.err" 2>/dev/null | tr '\n' ' ' || true)"
  rm -f "${path}" "${path}.err"
  MISSING+=("${name}|${description}|${reason:-produced no output}")
  echo "    ${name} -- unavailable"
}

psql_query() {
  kubectl -n "${NS}" exec deploy/postgres -- \
    psql -U postgres -d openfireblocks -A -F$'\t' -c "$1" 2>/dev/null
}

# -- CC6.1 / CC6.6: logical access, tenant isolation --

# Which tables enforce row-level security. This is the control that keeps
# one customer's keys invisible to another, and "it is enabled" is exactly
# the kind of claim that quietly stops being true when somebody adds a
# table.
collect rls-enabled-tables.tsv \
  "CC6.1 — row-level security is enabled on tenant-scoped tables" \
  psql_query "SELECT schemaname, tablename, rowsecurity FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY rowsecurity, tablename;"

# The policies themselves, not just the flag. A table with RLS enabled and
# no policy denies everything; a table with a permissive policy enforces
# nothing. Both look identical to the flag above.
collect rls-policies.tsv \
  "CC6.1 — the row-level security policies in force, per table" \
  psql_query "SELECT schemaname, tablename, policyname, permissive, roles::text, qual FROM pg_policies ORDER BY tablename, policyname;"

# Which database roles exist and what they can bypass. app_admin holds
# BYPASSRLS deliberately and a small number of services use it; an auditor
# will want the list and the reason.
collect database-roles.tsv \
  "CC6.1 — database roles and their privileges, including BYPASSRLS" \
  psql_query "SELECT rolname, rolsuper, rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname NOT LIKE 'pg\\_%' ORDER BY rolname;"

# -- CC6.7: transmission and disposal --

collect service-tls-config.txt \
  "CC6.7 — which internal service links require mTLS" \
  bash -c "kubectl -n '${NS}' get deploy -o json 2>/dev/null | python3 -c \"
import sys, json
data = json.load(sys.stdin)
for item in data.get('items', []):
    name = item['metadata']['name']
    for container in item['spec']['template']['spec']['containers']:
        env = {e['name']: e.get('value', '<from secret>') for e in container.get('env', [])}
        mtls = [k for k in env if k.startswith('MTLS_')]
        print(f\\\"{name}\\t{'mtls: ' + ','.join(sorted(mtls)) if mtls else 'mtls: not configured'}\\\")
\""

# -- CC7.2: monitoring --

collect alert-rules.yaml \
  "CC7.2 — the alerting rules defined for this system" \
  bash -c "cat '${ROOT}'/infrastructure/monitoring/*alert* 2>/dev/null || cat '${ROOT}'/infrastructure/monitoring/**/*.yaml 2>/dev/null"

# -- CC8.1: change management --

# Every change to the signing layer in the period, with author and date.
# This is the control an auditor tests by picking a commit and asking who
# reviewed it.
collect signing-layer-changes.txt \
  "CC8.1 — changes to the threshold signing layer, with author and date" \
  git -C "${ROOT}" log --since="1 year ago" --date=iso \
    --pretty=format:'%H%x09%ad%x09%an%x09%s' -- services/mpc-party services/mpc-signer

collect ci-workflows.txt \
  "CC8.1 — the automated checks every change must pass" \
  bash -c "ls -1 '${ROOT}'/.github/workflows/ && echo '---' && grep -h '^  [a-z-]*:' '${ROOT}'/.github/workflows/*.yml | sort -u"

# -- A1.2: availability, recovery --

# The drills, and the fact that they are enforced rather than optional. An
# auditor asking "how do you know failover works" gets a script they can
# read and a workflow that runs it.
collect recovery-drills.txt \
  "A1.2 — the recovery and failure drills, and where they run" \
  bash -c "ls -1 '${ROOT}'/infrastructure/kind/*drill*.sh '${ROOT}'/infrastructure/kind/*test*.sh 2>/dev/null && echo '---' && echo 'enforced by:' && grep -l 'drill' '${ROOT}'/.github/workflows/*.yml 2>/dev/null"

# -- CC6.1 / custody: the isolation claim --

# The one that decides whether the threshold means anything. Recorded as
# evidence precisely because the honest answer today is "simulated", and an
# evidence pack that omitted it would be misleading by selection.
collect party-isolation.txt \
  "CC6.1 — measured isolation between the MPC parties holding key shares" \
  bash -c "'${ROOT}'/infrastructure/kind/party-isolation-check.sh 2>&1"

# -- the signing record itself --

# Not a control document: the actual operational record. Counts only --
# the contents are customer data and do not belong in an evidence pack.
collect signing-activity-summary.tsv \
  "CC7.2 — signing requests recorded per status, demonstrating the audit trail is written" \
  psql_query "SELECT status, COUNT(*), MIN(created_at), MAX(created_at) FROM signing_requests GROUP BY status ORDER BY status;"

collect signing-committee-coverage.tsv \
  "CC6.1 — proportion of signatures whose committee was recorded" \
  psql_query "SELECT (signing_parties IS NOT NULL) AS committee_recorded, COUNT(*) FROM signing_requests WHERE status = 'completed' GROUP BY 1;"

# -- the manifest --

{
  cat <<HEADER
# SOC 2 evidence pack

Collected: ${STAMP}
Namespace: ${NS}
Commit:    $(git -C "${ROOT}" rev-parse HEAD 2>/dev/null || echo "unknown")

One run is a snapshot. A Type II report is about controls operating over a
period, so this is meant to be run on a schedule and accumulated -- a single
directory here evidences a single day.

## Collected

| File | Control | What it shows |
|---|---|---|
HEADER

  for entry in "${COLLECTED[@]}"; do
    name="${entry%%|*}"
    desc="${entry#*|}"
    control="${desc%% —*}"
    printf '| `%s` | %s | %s |\n' "${name}" "${control}" "${desc#*— }"
  done

  if [[ ${#MISSING[@]} -gt 0 ]]; then
    cat <<'MISSING_HEADER'

## Not collected on this run

Listed rather than omitted. An evidence pack that silently drops what it
could not gather is worse than one with gaps, because the gaps are what an
auditor most needs to know about.

| File | Control | Why not |
|---|---|---|
MISSING_HEADER
    for entry in "${MISSING[@]}"; do
      IFS='|' read -r name desc reason <<<"${entry}"
      printf '| `%s` | %s | %s |\n' "${name}" "${desc%% —*}" "${reason}"
    done
  fi

  cat <<'FOOTER'

## What no script can evidence

These are controls in `docs/PHASE3-SOC2-COMPLIANCE.md` that this collector
deliberately does not attempt. Each needs a human to produce a record, and
each is a common reason a Type II slips:

| Control | What is needed | Who produces it |
|---|---|---|
| CC1.1–CC1.4 competence, oversight | Org chart, role definitions, board or advisor minutes | Management |
| CC1.4 training | Security training completion records, dated and signed | Whoever runs onboarding |
| CC2.x communication | Evidence that policies were distributed and acknowledged | Management |
| CC9.2 vendor management | Assessments of Stripe, the cloud provider, WorkOS, any custodial dependency | Whoever owns the contracts |
| CC4.1 risk assessment | A dated risk register with owners and remediation status | Management |
| CC7.4 incident response | Records of actual incidents, or a documented tabletop exercise | On-call |
| Access reviews | Periodic evidence that access lists were reviewed and stale access removed | Whoever administers IAM |

The technical controls in the table above are the half that can be
automated. This half is the half that determines whether the audit
completes, and it starts with somebody being made accountable for it.

## An honest note on scope

A SOC 2 report covers a defined system boundary over a defined period, with
a named service organisation. None of those three things is decided yet.
This pack is the raw material; it is not a substitute for engaging an
auditor to scope the report, and the scoping conversation is what
determines which of the criteria above are even in scope.
FOOTER
} > "${OUT}/MANIFEST.md"

echo
echo "==> ${#COLLECTED[@]} artefacts collected, ${#MISSING[@]} unavailable"
echo "    manifest: ${OUT}/MANIFEST.md"
