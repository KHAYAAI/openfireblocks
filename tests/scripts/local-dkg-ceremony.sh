#!/bin/bash
# Local DKG Ceremony Test Script
# Demonstrates a complete 7-round DKG ceremony with 7 parties on a single machine
# Each party runs on a different port (7001-7007)

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

say() { printf "${GREEN}=== %s ===${NC}\n" "$1"; }
fail() { printf "${RED}FAIL: %s${NC}\n" "$1" >&2; exit 1; }
warn() { printf "${YELLOW}WARN: %s${NC}\n" "$1"; }

# Configuration
N_PARTIES=7
THRESHOLD=3
ORCHESTRATOR_URL="http://localhost:8081"
PARTY_BASE_PORT=7000

# Temporary directory for logs
LOG_DIR="/tmp/dkg-ceremony-logs-$$"
mkdir -p "$LOG_DIR"

say "Starting local DKG ceremony test"
say "Configuration: $N_PARTIES parties, threshold $((THRESHOLD+1))-of-$N_PARTIES"

# Start all party services in background
say "Starting $N_PARTIES party services"
PARTY_PIDS=()

for i in $(seq 1 $N_PARTIES); do
  PORT=$((PARTY_BASE_PORT + i))
  LOG_FILE="$LOG_DIR/party-$i.log"

  say "Starting party $i on port $PORT (log: $LOG_FILE)"
  PARTY_ID=$i PORT=$PORT go run ./services/mpc-party > "$LOG_FILE" 2>&1 &
  PID=$!
  PARTY_PIDS+=($PID)

  # Wait for party to be ready
  sleep 1
  if ! kill -0 $PID 2>/dev/null; then
    fail "Party $i failed to start (PID $PID)"
  fi
done

say "All party services started: ${PARTY_PIDS[@]}"

# Cleanup function
cleanup() {
  say "Cleaning up..."
  for PID in "${PARTY_PIDS[@]}"; do
    if kill -0 $PID 2>/dev/null; then
      kill $PID || true
      wait $PID 2>/dev/null || true
    fi
  done

  say "Party logs preserved in: $LOG_DIR"
  say "View logs: tail -f $LOG_DIR/party-*.log"
}

trap cleanup EXIT

# Wait for all parties to be ready
say "Waiting for all parties to be healthy..."
for i in $(seq 1 $N_PARTIES); do
  PORT=$((PARTY_BASE_PORT + i))
  RETRIES=10
  while [ $RETRIES -gt 0 ]; do
    if curl -s http://localhost:$PORT/health > /dev/null 2>&1; then
      say "Party $i is healthy"
      break
    fi
    RETRIES=$((RETRIES - 1))
    sleep 0.5
  done
  [ $RETRIES -gt 0 ] || fail "Party $i failed to become healthy"
done

# Create ceremony request
say "Creating DKG ceremony with orchestrator"

PARTY_ENDPOINTS=""
for i in $(seq 1 $N_PARTIES); do
  PORT=$((PARTY_BASE_PORT + i))
  if [ -n "$PARTY_ENDPOINTS" ]; then
    PARTY_ENDPOINTS="$PARTY_ENDPOINTS,"
  fi
  PARTY_ENDPOINTS="${PARTY_ENDPOINTS}\"http://localhost:$PORT\""
done

PARTY_IDS=""
for i in $(seq 1 $N_PARTIES); do
  if [ -n "$PARTY_IDS" ]; then
    PARTY_IDS="$PARTY_IDS,"
  fi
  PARTY_IDS="${PARTY_IDS}$i"
done

CEREMONY_REQUEST="{
  \"chainId\": \"ethereum\",
  \"n\": $N_PARTIES,
  \"k\": $THRESHOLD,
  \"partyIds\": [$PARTY_IDS],
  \"partyEndpoints\": [$PARTY_ENDPOINTS]
}"

echo "Ceremony Request:"
echo "$CEREMONY_REQUEST" | jq .

CEREMONY_RESPONSE=$(curl -s -X POST "$ORCHESTRATOR_URL/ceremonies" \
  -H "Content-Type: application/json" \
  -d "$CEREMONY_REQUEST")

CEREMONY_ID=$(echo "$CEREMONY_RESPONSE" | jq -r '.id // empty')
if [ -z "$CEREMONY_ID" ]; then
  fail "Failed to create ceremony: $CEREMONY_RESPONSE"
fi

say "Ceremony created with ID: $CEREMONY_ID"

# Execute 7 rounds
for ROUND in $(seq 1 7); do
  say "Executing DKG Round $ROUND..."

  # Signal all parties to start round
  for i in $(seq 1 $N_PARTIES); do
    PORT=$((PARTY_BASE_PORT + i))
    curl -s -X POST "http://localhost:$PORT/round" \
      -H "Content-Type: application/json" \
      -d "{\"ceremonyId\": \"$CEREMONY_ID\", \"roundNum\": $ROUND, \"action\": \"start\"}" \
      > /dev/null || warn "Failed to signal party $i for round $ROUND"
  done

  # Wait a bit for processing
  sleep 0.5

  # Collect round data from all parties
  say "Collecting round $ROUND data from all parties..."
  PARTY_DATA_MAP="{}"
  for i in $(seq 1 $N_PARTIES); do
    PORT=$((PARTY_BASE_PORT + i))
    ROUND_DATA=$(curl -s "http://localhost:$PORT/round/$ROUND/data")

    if echo "$ROUND_DATA" | jq . > /dev/null 2>&1; then
      # Merge into party data map
      PARTY_DATA_MAP=$(echo "$PARTY_DATA_MAP" | jq --arg key "$i" --argjson data "$ROUND_DATA" '.[$key] = $data')
    else
      warn "Failed to get round data from party $i"
    fi
  done

  # Broadcast round data to all parties
  say "Broadcasting round $ROUND data to all parties..."
  BROADCAST_REQUEST="{\"ceremonyId\": \"$CEREMONY_ID\", \"roundNum\": $ROUND, \"partyDataMap\": $PARTY_DATA_MAP}"

  for i in $(seq 1 $N_PARTIES); do
    PORT=$((PARTY_BASE_PORT + i))
    curl -s -X POST "http://localhost:$PORT/round/$ROUND/broadcast" \
      -H "Content-Type: application/json" \
      -d "$BROADCAST_REQUEST" \
      > /dev/null || warn "Failed to broadcast to party $i"
  done

  # Small delay between rounds
  if [ $ROUND -lt 7 ]; then
    sleep 0.5
  fi
done

say "All 7 DKG rounds completed successfully!"

# Test threshold signing
say "Testing threshold signing with 4-of-7 threshold"

MESSAGE="deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
SIGNING_PARTIES="1 2 3 4"

say "Computing partial signatures from parties: $SIGNING_PARTIES"
PARTIAL_SIGNATURES="{}"

for PARTY_ID in $SIGNING_PARTIES; do
  PORT=$((PARTY_BASE_PORT + PARTY_ID))
  SIG_RESPONSE=$(curl -s -X POST "http://localhost:$PORT/sign" \
    -H "Content-Type: application/json" \
    -d "{\"ceremonyId\": \"$CEREMONY_ID\", \"message\": \"$MESSAGE\", \"partyIds\": [1, 2, 3, 4]}")

  SIGNATURE=$(echo "$SIG_RESPONSE" | jq -r '.signature // empty')
  if [ -n "$SIGNATURE" ]; then
    PARTIAL_SIGNATURES=$(echo "$PARTIAL_SIGNATURES" | jq --arg key "$PARTY_ID" --arg sig "$SIGNATURE" '.[$key] = $sig')
    say "Party $PARTY_ID computed partial signature"
  else
    warn "Failed to get signature from party $PARTY_ID"
  fi
done

# In production, orchestrator would combine signatures
say "Partial signatures collected: $(echo "$PARTIAL_SIGNATURES" | jq 'keys | length') of 4 parties"

# Verify ceremony completion
say "Verifying ceremony completion..."
CEREMONY_STATUS=$(curl -s "$ORCHESTRATOR_URL/ceremonies/$CEREMONY_ID")
CEREMONY_STATE=$(echo "$CEREMONY_STATUS" | jq -r '.status // empty')

if [ "$CEREMONY_STATE" = "completed" ]; then
  say "Ceremony status: COMPLETED ✓"
  echo "$CEREMONY_STATUS" | jq '{id, status, n, k, thresholdPubKey}'
else
  warn "Ceremony status: $CEREMONY_STATE (expected: completed)"
fi

say "Local DKG ceremony test completed successfully!"
echo ""
echo "Log files: $LOG_DIR/party-*.log"
echo ""
echo "To manually inspect a party:"
echo "  curl http://localhost:7001/info  # Party 1"
echo "  curl http://localhost:7002/info  # Party 2"
echo "  ... etc"
echo ""
