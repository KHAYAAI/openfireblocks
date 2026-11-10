#!/usr/bin/env bash
# Rebuilds the enclave image from this checkout and checks its measurements.
#
#   ./infrastructure/nitro/reproduce.sh                      # build twice, compare
#   EXPECTED_PCR0=<hex> ./infrastructure/nitro/reproduce.sh  # also compare with a published PCR0
#
# PCR0 only means something to a reviewer who can rebuild it. A party's
# peers, its KMS key and the orchestrator all trust "the image whose PCR0 is
# X". That is a statement about source code only if anyone can take the
# commit, build it, and get X. This script is that check: two builds from
# nothing (no Docker cache), the same build arguments, and the PCRs compared.
#
# Build arguments are read from BUILD_ARGS_FILE (default
# infrastructure/nitro/build-args.env, one KEY=value per line), because the
# image bakes them in and they are part of the measurement. Use the file the
# release was built from.
#
# Needs docker (with BuildKit) and nitro-cli (the aws-nitro-enclaves-cli
# package, or built from github.com/aws/aws-nitro-enclaves-cli with
# NITRO_CLI_BLOBS pointing at its blobs/). No enclave hardware is needed: an
# EIF can be built and measured anywhere.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="${WORK:-$(mktemp -d)}"
BUILD_ARGS_FILE="${BUILD_ARGS_FILE:-$ROOT/infrastructure/nitro/build-args.env}"
NITRO_CLI="${NITRO_CLI:-nitro-cli}"
export NITRO_CLI_ARTIFACTS="${NITRO_CLI_ARTIFACTS:-$WORK/artifacts}"
mkdir -p "$NITRO_CLI_ARTIFACTS"

[ -f "$BUILD_ARGS_FILE" ] || { echo "no build arguments at $BUILD_ARGS_FILE"; exit 2; }
args=()
while IFS= read -r line; do
  case "$line" in ''|'#'*) continue ;; esac
  args+=(--build-arg "$line")
done < "$BUILD_ARGS_FILE"

# DOCKER_BUILD_FLAGS passes extra flags to docker build, for example
# "--network=host --secret id=egress_ca,src=ca.pem" behind a proxy. They do
# not change the image.
read -r -a extra <<< "${DOCKER_BUILD_FLAGS:-}"

build() { # $1 name; prints the PCRs or fails
  docker build --no-cache -q "${extra[@]}" -f "$ROOT/infrastructure/nitro/Dockerfile.enclave" "${args[@]}" \
    -t "forge-repro-$1" "$ROOT/services/mpc-party" >/dev/null \
    || { echo "docker build $1 failed" >&2; return 1; }
  "$NITRO_CLI" build-enclave --docker-uri "forge-repro-$1:latest" --output-file "$WORK/$1.eif" > "$WORK/$1.json" \
    || { echo "nitro-cli build-enclave $1 failed" >&2; return 1; }
  local m
  m=$(jq -ce '.Measurements | {PCR0, PCR1, PCR2}' "$WORK/$1.json") || { echo "no measurements for $1" >&2; return 1; }
  # A measurement that is empty or all zeros proves nothing; never compare two of them.
  if ! echo "$m" | jq -e '[.PCR0, .PCR1, .PCR2] | all(type == "string" and test("^[0-9a-fA-F]{96}$") and (test("^0+$") | not))' >/dev/null; then
    echo "build $1 produced no usable measurement: $m" >&2
    return 1
  fi
  echo "$m"
}

# PCR1 (and so PCR0) also depends on the kernel and init that nitro-cli
# wraps around the image, which ship with nitro-cli. A reproducer must use
# the release's nitro-cli version and blobs; record them with the PCRs.
blobs="${NITRO_CLI_BLOBS:-/usr/share/nitro_enclaves/blobs}"
{
  echo "nitro-cli: $("$NITRO_CLI" --version 2>/dev/null || echo unknown)"
  echo "docker:    $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown)"
  (cd "$blobs" 2>/dev/null && sha256sum ./* 2>/dev/null) | sed 's/^/blob:      /'
} | tee "$WORK/toolchain.txt"

echo "==> build 1"
A=$(build a) || { echo "FAIL  build 1"; exit 1; }
echo "    $A"
echo "==> build 2, from nothing again"
B=$(build b) || { echo "FAIL  build 2"; exit 1; }
echo "    $B"

status=0
if [ "$A" = "$B" ]; then
  echo "PASS  two clean builds give the same PCR0, PCR1 and PCR2"
else
  echo "FAIL  the builds differ; the measurement cannot be checked against the source"
  status=1
fi
if [ -n "${EXPECTED_PCR0:-}" ]; then
  got=$(echo "$A" | jq -r .PCR0)
  if [ "$(echo "$got" | tr 'A-F' 'a-f')" = "$(echo "$EXPECTED_PCR0" | tr 'A-F' 'a-f')" ]; then
    echo "PASS  PCR0 matches the published value"
  else
    echo "FAIL  PCR0 $got is not the published $EXPECTED_PCR0"
    status=1
  fi
fi
echo "EIFs, measurements and the toolchain record (toolchain.txt) in $WORK"
exit $status
