#!/usr/bin/env bash
# Hardware check for the enclave signing party. Run on an enclave-enabled EC2
# instance (for example m6i.xlarge with --enclave-options Enabled=true), with
# nitro-cli, docker, jq, go and the AWS CLI installed, the enclave allocator
# configured for at least 2 vCPUs and 2048 MiB, and an instance role.
#
#   REGION=af-south-1 ./infrastructure/nitro/spike.sh
#
# What it shows, each step ending PASS or FAIL:
#
#   1. The image builds into an EIF, and its PCRs are printed.
#   2. A KMS key whose policy names that PCR0 seals and opens a secret for the
#      enclave (the enclave's self-test, reported to the host over vsock).
#   3. The same key refuses the host: Decrypt of the self-test's data key
#      without an attestation document is denied.
#   4. A rebuild with one changed byte has a new PCR0, and KMS refuses it.
#   5. The original image in debug mode (zeroed PCRs) refuses to start.
#
# It creates one KMS key (scheduled for deletion at the end unless KEEP_KEY=1)
# and writes docs/evidence/<date>-nitro-spike.md for you to review and commit.
# It never prints a secret: the self-test reports only a SHA-256 of one.
set -euo pipefail

REGION="${REGION:?set REGION, e.g. af-south-1}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="${WORK:-$(mktemp -d)}"
CID=16
PARTY=1
LOG="$WORK/host.log"
EVIDENCE="$ROOT/docs/evidence/$(date -u +%Y-%m-%d)-nitro-spike.md"
results=()

pass() { results+=("PASS  $1"); echo "PASS  $1"; }
fail() { results+=("FAIL  $1"); echo "FAIL  $1"; }

cleanup() {
  nitro-cli terminate-enclave --all >/dev/null 2>&1 || true
  [ -n "${HOST_PID:-}" ] && kill "$HOST_PID" 2>/dev/null || true
}
trap cleanup EXIT

ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
ROLE_ARN="$(aws sts get-caller-identity --query Arn --output text |
  sed -E 's#arn:aws:sts::([0-9]+):assumed-role/([^/]+)/.*#arn:aws:iam::\1:role/\2#')"
INSTANCE_TYPE="$(curl -s -H "X-aws-ec2-metadata-token: $(curl -s -X PUT http://169.254.169.254/latest/api/token \
  -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')" http://169.254.169.254/latest/meta-data/instance-type || echo unknown)"
echo "account $ACCOUNT, role $ROLE_ARN, instance $INSTANCE_TYPE, work dir $WORK"

# A throwaway co-signer key: the spike runs no ceremony, but enclave mode
# refuses to start without one.
AUTHZ="$(openssl rand -hex 32)"

# ---- host relay ----
(cd "$ROOT/services/mpc-party" && CGO_ENABLED=0 go build -o "$WORK/mpc-party-host" ./cmd/mpc-party-host)

# ---- KMS key, created before the image so its ARN can be baked in ----
KEY_ID="${KEY_ID:-}"
if [ -z "$KEY_ID" ]; then
  # Created with a placeholder policy that already denies cryptography
  # without attestation; the real PCR0 is written in after the build.
  sed -e "s#ACCOUNT_ID#$ACCOUNT#g" -e "s#PARTY_ROLE_ARN#$ROLE_ARN#g" \
      -e "s#PCR0_HEX#$(printf '0%.0s' $(seq 96))#g" -e "s#PARTY_NUMBER#$PARTY#g" \
      "$ROOT/infrastructure/nitro/kms-key-policy.json" > "$WORK/policy.json"
  KEY_ID="$(aws kms create-key --region "$REGION" --description "FORGE nitro spike (delete me)" \
    --policy "file://$WORK/policy.json" --query KeyMetadata.Arn --output text)"
fi
echo "KMS key $KEY_ID"

build() { # $1 tag, $2 marker
  docker build -q -f "$ROOT/infrastructure/nitro/Dockerfile.enclave" \
    --build-arg ENCLAVE_KMS_REGION="$REGION" \
    --build-arg ENCLAVE_KMS_KEYS="$PARTY=$KEY_ID" \
    --build-arg ENCLAVE_REQUIRE_PARTY_PIN=0 \
    --build-arg CEREMONY_AUTHORIZER_PUBKEY="$AUTHZ" \
    --build-arg ENCLAVE_SELFTEST=1 \
    --build-arg SPIKE_MARKER="$2" \
    -t "$1" "$ROOT/services/mpc-party" >/dev/null
  nitro-cli build-enclave --docker-uri "$1:latest" --output-file "$WORK/$1.eif" > "$WORK/$1.json"
  jq -r .Measurements.PCR0 "$WORK/$1.json"
}

start_host() {
  cat > "$WORK/runtime.json" <<EOF
{"PARTY_ID":"$PARTY","ENCLAVE_EGRESS":"kms.$REGION.amazonaws.com:443=8443"}
EOF
  : > "$LOG"
  ENCLAVE_CID=$CID HOST_EGRESS="8443=kms.$REGION.amazonaws.com:443" HOST_RUNTIME_CONFIG="$WORK/runtime.json" \
    HOST_LISTEN=127.0.0.1:7000 HOST_HEALTH_LISTEN=127.0.0.1:8081 "$WORK/mpc-party-host" >"$LOG" 2>&1 &
  HOST_PID=$!
  sleep 1
}

# wait_report <event>: prints the first enclave report of that event.
wait_report() {
  for _ in $(seq 120); do
    line="$(grep -m1 "enclave report: .*\"event\":\"$1\"" "$LOG" || true)"
    if [ -n "$line" ]; then echo "${line#*enclave report: }"; return 0; fi
    sleep 1
  done
  return 1
}

# ---- 1. build and measure ----
PCR0="$(build forge-mpc-party-enclave spike-a)"
echo "PCR0 $PCR0"
jq . "$WORK/forge-mpc-party-enclave.json"
[ -n "$PCR0" ] && pass "image builds; PCR0 $PCR0" || fail "image build"

sed -e "s#ACCOUNT_ID#$ACCOUNT#g" -e "s#PARTY_ROLE_ARN#$ROLE_ARN#g" -e "s#PCR0_HEX#$PCR0#g" \
    -e "s#PARTY_NUMBER#$PARTY#g" "$ROOT/infrastructure/nitro/kms-key-policy.json" > "$WORK/policy.json"
aws kms put-key-policy --region "$REGION" --key-id "$KEY_ID" --policy-name default --policy "file://$WORK/policy.json"

# ---- 2. self-test in the measured enclave ----
start_host
nitro-cli run-enclave --eif-path "$WORK/forge-mpc-party-enclave.eif" --cpu-count 2 --memory 2048 --enclave-cid $CID >/dev/null
SELF="$(wait_report selftest || echo '{}')"
echo "$SELF" | jq 'del(.encrypted_key)'
if [ "$(echo "$SELF" | jq -r .ok)" = "true" ]; then pass "KMS sealed and opened a secret for PCR0 $PCR0"; else fail "self-test: $(echo "$SELF" | jq -r .detail)"; fi
ROLE_PCR3="$(echo "$SELF" | jq -r '.role_pcr3 // empty')"
curl -sf http://127.0.0.1:8081/health >/dev/null && pass "host health reaches the enclave over vsock" || fail "host health"

# ---- 3. the host cannot decrypt the data key itself ----
echo "$SELF" | jq -r .encrypted_key | base64 -d > "$WORK/dk.bin" 2>/dev/null || true
if [ -s "$WORK/dk.bin" ]; then
  if out="$(aws kms decrypt --region "$REGION" --key-id "$KEY_ID" --ciphertext-blob "fileb://$WORK/dk.bin" \
      --encryption-context "forge:purpose=selftest,forge:party=party-$PARTY" 2>&1)"; then
    fail "Decrypt without attestation SUCCEEDED for the host"
  else
    echo "$out" | grep -q AccessDenied && pass "Decrypt without attestation refused (AccessDenied)" || fail "Decrypt without attestation: unexpected $out"
  fi
else
  fail "no encrypted data key in the self-test report"
fi
nitro-cli terminate-enclave --all >/dev/null; kill "$HOST_PID"; wait "$HOST_PID" 2>/dev/null || true

# ---- 4. a rebuilt image is refused ----
PCR0_B="$(build forge-mpc-party-enclave-b spike-b)"
[ "$PCR0_B" != "$PCR0" ] && pass "one changed byte gives a new PCR0 ($PCR0_B)" || fail "PCR0 did not change"
start_host
nitro-cli run-enclave --eif-path "$WORK/forge-mpc-party-enclave-b.eif" --cpu-count 2 --memory 2048 --enclave-cid $CID >/dev/null
REB="$(wait_report selftest || echo '{}')"
if [ "$(echo "$REB" | jq -r .ok)" = "false" ] && echo "$REB" | jq -r .detail | grep -q AccessDenied; then
  pass "KMS refused the rebuilt image"
else
  fail "rebuilt image: $(echo "$REB" | jq -c 'del(.encrypted_key)')"
fi
nitro-cli terminate-enclave --all >/dev/null; kill "$HOST_PID"; wait "$HOST_PID" 2>/dev/null || true

# ---- 5. debug mode refuses to start ----
start_host
nitro-cli run-enclave --eif-path "$WORK/forge-mpc-party-enclave.eif" --cpu-count 2 --memory 2048 --enclave-cid $CID --debug-mode >/dev/null
DBG="$(wait_report failed || echo '{}')"
echo "$DBG" | jq -r .detail | grep -q "debug mode" && pass "debug-mode enclave refused to start" || fail "debug mode: $(echo "$DBG" | jq -c .)"
nitro-cli terminate-enclave --all >/dev/null

# ---- evidence ----
mkdir -p "$(dirname "$EVIDENCE")"
{
  echo "# Nitro enclave spike, $(date -u +%Y-%m-%d)"
  echo
  echo "- Instance type: $INSTANCE_TYPE; region: $REGION"
  echo "- Commit: $(git -C "$ROOT" rev-parse HEAD)"
  echo "- PCR0 (image): \`$PCR0\`"
  echo "- PCR0 (rebuilt, one byte changed): \`$PCR0_B\`"
  echo "- PCR3 (this instance's role, to pin as ENCLAVE_PARTY_PCR3): \`${ROLE_PCR3:-not reported}\`"
  echo "- KMS key: \`$KEY_ID\`"
  echo
  echo "## Measurements"
  echo
  echo '```json'
  jq .Measurements "$WORK/forge-mpc-party-enclave.json"
  echo '```'
  echo
  echo "## Results"
  echo
  for r in "${results[@]}"; do echo "- $r"; done
  echo
  echo "## Cost and teardown"
  echo
  echo "- Instance hours: (fill in)"
  echo "- KMS key: $([ "${KEEP_KEY:-0}" = 1 ] && echo kept || echo 'scheduled for deletion in 7 days')"
  echo "- Terminate the instance when done."
} > "$EVIDENCE"

if [ "${KEEP_KEY:-0}" != 1 ]; then
  aws kms schedule-key-deletion --region "$REGION" --key-id "$KEY_ID" --pending-window-in-days 7 >/dev/null
fi

echo
printf '%s\n' "${results[@]}"
echo "Evidence written to $EVIDENCE"
! printf '%s\n' "${results[@]}" | grep -q '^FAIL'
