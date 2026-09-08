#!/usr/bin/env bash
#
# Builds every container image the Helm chart references.
#
# The chart addresses images as <imageRegistry>/<image>:<tag> --
# see .Values.imageRegistry in infrastructure/helm/openfireblocks/values.yaml
# and the "ofb.image" helper -- so the defaults here match the chart's
# defaults and no values override is needed for a local build.
#
# Behind a TLS-intercepting egress proxy (corporate MITM, sandboxed CI) the
# build stages cannot verify proxy.golang.org or registry.npmjs.org against
# the base image's own trust store. Set EXTRA_CA_CERT_FILE to a PEM bundle
# and it is passed to the build stages as a BuildKit secret -- no size limit
# (a full system bundle overflows a build arg) and it never lands in an
# image layer. BUILD_NETWORK=host additionally lets the build stages reach a
# proxy that is only listening on the host's loopback.
#
#   REGISTRY=openfireblocks TAG=latest ./scripts/build-images.sh
#   EXTRA_CA_CERT_FILE=/root/.ccr/ca-bundle.crt BUILD_NETWORK=host \
#     ./scripts/build-images.sh
set -euo pipefail

REGISTRY="${REGISTRY:-openfireblocks}"
TAG="${TAG:-latest}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Every service the chart can deploy.
#
# Not built here: services/ceremony-orchestrator, which does not compile and
# whose responsibilities are already covered by temporal-worker's
# DKGCeremonyWorkflow; and services/backup, which is a scheduled job rather
# than a chart workload. See docs/deployment/CLUSTER-DEPLOYMENT.md.
SERVICES=(
  api-gateway
  mpc-party
  mpc-signer
  policy-service
  temporal-worker
  vault-pki-init
  vault-unseal
  billing
  webhooks
  marketplace
  settlement
  policy
  compliance
)

build_args=()
if [[ -n "${EXTRA_CA_CERT_FILE:-}" ]]; then
  if [[ ! -r "${EXTRA_CA_CERT_FILE}" ]]; then
    echo "EXTRA_CA_CERT_FILE=${EXTRA_CA_CERT_FILE} is not readable" >&2
    exit 1
  fi
  build_args+=(--secret "id=egress_ca,src=${EXTRA_CA_CERT_FILE}")
fi
# HTTP_PROXY/HTTPS_PROXY/NO_PROXY are predefined build args in Docker: they
# reach the build stages without the Dockerfile declaring them, and are not
# persisted into the resulting image's environment.
for v in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  [[ -n "${!v:-}" ]] && build_args+=(--build-arg "${v}=${!v}")
done
[[ -n "${BUILD_NETWORK:-}" ]] && build_args+=(--network "${BUILD_NETWORK}")

failed=()
for svc in "${SERVICES[@]}"; do
  image="${REGISTRY}/${svc}:${TAG}"
  echo "==> ${image}"
  if ! docker build "${build_args[@]}" -t "${image}" "${ROOT}/services/${svc}"; then
    failed+=("${svc}")
  fi
done

if (( ${#failed[@]} )); then
  echo "FAILED: ${failed[*]}" >&2
  exit 1
fi

echo
echo "Built ${#SERVICES[@]} images at ${REGISTRY}/*:${TAG}"
