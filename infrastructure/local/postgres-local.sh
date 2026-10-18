#!/usr/bin/env bash
# A throwaway PostgreSQL with this repository's real schema, for tests that
# need the database's own rules to run -- row-level security, triggers,
# constraints -- rather than a mock that agrees with whatever the code
# under test assumes.
#
#   eval "$(infrastructure/local/postgres-local.sh start)"   # exports DATABASE_URL, DATABASE_ADMIN_URL
#   infrastructure/local/postgres-local.sh stop
#
# Provisioned the way infrastructure/kind/dependencies.yaml provisions the
# real one: bootstrap role `postgres`, an ordinary `app` role that owns
# schema public (so FORCE ROW LEVEL SECURITY binds it), and `app_admin`
# with BYPASSRLS. Every migration is applied as `app`, in order, exactly
# as a deployment applies them. A migration that fails here fails the
# script.
set -euo pipefail

PORT="${PGLOCAL_PORT:-55432}"
DIR="${PGLOCAL_DIR:-${TMPDIR:-/tmp}/ofb-postgres-local}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MIGRATIONS="$ROOT/infrastructure/database/migrations"

bindir() {
  if command -v pg_ctl >/dev/null 2>&1; then
    dirname "$(command -v pg_ctl)"
    return
  fi
  local d
  d="$(find /usr/lib/postgresql -maxdepth 2 -name bin -type d 2>/dev/null | sort -V | tail -1)"
  [ -n "$d" ] || { echo "PostgreSQL server binaries not found" >&2; exit 1; }
  echo "$d"
}

# initdb refuses to run as root; run the server as the postgres user when
# we are root, and as ourselves otherwise.
as_owner() {
  if [ "$(id -u)" = "0" ]; then
    runuser -u postgres -- "$@"
  else
    "$@"
  fi
}

start() {
  local bin
  bin="$(bindir)"
  rm -rf "$DIR"
  mkdir -p "$DIR"
  if [ "$(id -u)" = "0" ]; then chown postgres "$DIR"; fi
  as_owner "$bin/initdb" -D "$DIR/data" -U postgres --auth=trust >/dev/null
  as_owner "$bin/pg_ctl" -D "$DIR/data" -l "$DIR/log" -w \
    -o "-p $PORT -k $DIR -c listen_addresses=127.0.0.1" start >/dev/null

  local psql=(psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$PORT")
  "${psql[@]}" -U postgres -d postgres -c "CREATE DATABASE openfireblocks" >/dev/null
  "${psql[@]}" -U postgres -d openfireblocks >/dev/null <<'SQL'
CREATE ROLE app LOGIN PASSWORD 'dev-only';
CREATE ROLE app_admin LOGIN PASSWORD 'dev-only' BYPASSRLS;
ALTER DATABASE openfireblocks OWNER TO app;
ALTER SCHEMA public OWNER TO app;
GRANT ALL ON SCHEMA public TO app;
GRANT USAGE ON SCHEMA public TO app_admin;
-- Extensions need a superuser; the migrations' CREATE EXTENSION IF NOT
-- EXISTS then finds them already present.
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
SQL

  local f
  for f in "$MIGRATIONS"/*.sql; do
    if ! "${psql[@]}" -U app -d openfireblocks -f "$f" >/dev/null 2>"$DIR/migration.err"; then
      echo "migration failed: $(basename "$f")" >&2
      cat "$DIR/migration.err" >&2
      exit 1
    fi
  done

  echo "export DATABASE_URL='postgres://app:dev-only@127.0.0.1:$PORT/openfireblocks?sslmode=disable'"
  echo "export DATABASE_ADMIN_URL='postgres://app_admin:dev-only@127.0.0.1:$PORT/openfireblocks?sslmode=disable'"
}

stop() {
  local bin
  bin="$(bindir)"
  if [ -d "$DIR/data" ]; then
    as_owner "$bin/pg_ctl" -D "$DIR/data" -m fast stop >/dev/null || true
  fi
  rm -rf "$DIR"
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
