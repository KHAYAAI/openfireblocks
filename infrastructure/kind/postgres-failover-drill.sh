#!/usr/bin/env bash
#
# Proves the Postgres standby is a real replica and a usable recovery path.
#
# Two claims, and they need separate evidence:
#
#   1. It is genuinely streaming. A base backup that has silently stopped
#      replicating still looks like a healthy Postgres, still answers
#      queries, and still holds a plausible copy of the data -- it is just
#      quietly hours stale. So this writes a row on the primary and waits to
#      read it back from the standby.
#
#   2. Promotion works. A replica you have never promoted is a backup you
#      have never restored: you find out whether it works on the day you
#      need it. This promotes it for real, then checks it accepts writes --
#      which a standby refuses, so it is a genuine state change and not a
#      hopeful read.
#
# This is NOT automatic failover. Nothing here elects a leader; promotion is
# deliberate. What the drill measures is how long the recovery takes and
# whether the data survived it.
set -euo pipefail

NS="${K8S_NAMESPACE:-openfireblocks}"
PRIMARY_DEPLOY="deploy/postgres"
STANDBY_DEPLOY="deploy/postgres-standby"

fail() { echo "FAIL: $*" >&2; exit 1; }

psql_primary() {
  kubectl -n "${NS}" exec "${PRIMARY_DEPLOY}" -- psql -U postgres -d openfireblocks -tAc "$1" 2>/dev/null
}
psql_standby() {
  kubectl -n "${NS}" exec "${STANDBY_DEPLOY}" -c postgres -- psql -U postgres -d openfireblocks -tAc "$1" 2>/dev/null
}

echo "==> checking the standby is actually in recovery"
in_recovery="$(psql_standby 'SELECT pg_is_in_recovery();' | tr -d '[:space:]')"
[[ "${in_recovery}" == "t" ]] \
  || fail "the standby reports pg_is_in_recovery=${in_recovery:-<none>}; it is not a replica, it is a second independent database"
echo "    pg_is_in_recovery = t"

echo "==> checking the primary sees a connected walsender"
senders="$(psql_primary "SELECT count(*) FROM pg_stat_replication WHERE application_name IS NOT NULL;" | tr -d '[:space:]')"
[[ "${senders:-0}" -ge 1 ]] \
  || fail "the primary has no replication connections; the standby is not streaming"
state="$(psql_primary "SELECT state FROM pg_stat_replication LIMIT 1;" | tr -d '[:space:]')"
echo "    ${senders} walsender(s), state=${state}"

echo "==> writing a row on the primary and reading it back from the standby"
CANARY="failover-drill-$(date +%s)"
psql_primary "CREATE TABLE IF NOT EXISTS failover_canary (id TEXT PRIMARY KEY, at TIMESTAMPTZ DEFAULT NOW());" >/dev/null
psql_primary "INSERT INTO failover_canary (id) VALUES ('${CANARY}');" >/dev/null

replicated=""
started=$(date +%s%3N)
for _ in $(seq 1 60); do
  got="$(psql_standby "SELECT id FROM failover_canary WHERE id = '${CANARY}';" 2>/dev/null | tr -d '[:space:]' || true)"
  if [[ "${got}" == "${CANARY}" ]]; then
    replicated="yes"
    break
  fi
  sleep 0.5
done
lag_ms=$(( $(date +%s%3N) - started ))
[[ -n "${replicated}" ]] \
  || fail "the canary row never reached the standby; replication is not working"
echo "    replicated in ${lag_ms}ms"

# The row must be there *before* promotion, or the rest of the drill would
# be measuring a promotion that lost the most recent write.
echo "==> promoting the standby"
promote_started=$(date +%s%3N)
# pg_promote() rather than `pg_ctl promote`: kubectl exec lands as root,
# and pg_ctl refuses to run as root by design. pg_promote() is SQL, runs as
# the superuser the connection already is, and is the documented promotion
# path since PostgreSQL 12.
promote_result="$(psql_standby 'SELECT pg_promote(wait => true, wait_seconds => 60);' | tr -d '[:space:]')"
[[ "${promote_result}" == "t" ]] \
  || fail "pg_promote() returned ${promote_result:-<nothing>}"

promoted=""
for _ in $(seq 1 60); do
  r="$(psql_standby 'SELECT pg_is_in_recovery();' 2>/dev/null | tr -d '[:space:]' || true)"
  if [[ "${r}" == "f" ]]; then
    promoted="yes"
    break
  fi
  sleep 0.5
done
promote_ms=$(( $(date +%s%3N) - promote_started ))
[[ -n "${promoted}" ]] || fail "the standby never left recovery after promote"
echo "    left recovery in ${promote_ms}ms"

echo "==> confirming the promoted node accepts writes"
# A standby refuses writes outright, so this is the check that the promotion
# was a real state change rather than a read that happened to succeed.
psql_standby "INSERT INTO failover_canary (id) VALUES ('${CANARY}-postpromote');" >/dev/null \
  || fail "the promoted node still refuses writes"
echo "    write accepted"

echo "==> confirming the pre-failover data survived"
survived="$(psql_standby "SELECT id FROM failover_canary WHERE id = '${CANARY}';" | tr -d '[:space:]')"
[[ "${survived}" == "${CANARY}" ]] \
  || fail "the row written before promotion is missing; the promotion lost data"
rows="$(psql_standby 'SELECT count(*) FROM key_pairs;' | tr -d '[:space:]' || echo 0)"
echo "    canary intact, ${rows} key_pairs rows present"

echo
echo "PASS: the standby was streaming (${lag_ms}ms for a round trip), promoted"
echo "      out of recovery in ${promote_ms}ms, accepts writes, and kept the"
echo "      data written before the promotion."
echo
echo "NOTE: this promoted node is now a diverged primary. It is not wired"
echo "      into the platform -- nothing routes to postgres-standby -- and"
echo "      the drill deliberately leaves it promoted so the state can be"
echo "      inspected. Re-run the standby's base backup to rebuild it:"
echo "        kubectl -n ${NS} delete pvc postgres-standby-data"
echo "        kubectl -n ${NS} rollout restart ${STANDBY_DEPLOY}"
