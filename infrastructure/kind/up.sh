#!/usr/bin/env bash
#
# Brings the platform up on a local kind cluster, end to end: cluster,
# images, stateful dependencies, schema, chart.
#
# This is the script that first ran the system on real Kubernetes. Three
# bugs fell out of doing so -- see docs/deployment/CLUSTER-DEPLOYMENT.md --
# none of which were reachable when the services ran as processes on one
# host, so it is worth keeping this runnable rather than treating the
# cluster as a one-off.
#
#   ./infrastructure/kind/up.sh              # create and deploy
#   RESET_DB=1 ./infrastructure/kind/up.sh   # also wipe Postgres first
#   LITE=1 ./infrastructure/kind/up.sh       # laptop profile: signing path only
#   ADMIN_API_KEY=... JWT_SECRET=... ./infrastructure/kind/up.sh
#                                            # REQUIRED before exposing the gateway to any
#                                            # network: the defaults are published dev values
#
# Environment:
#   CLUSTER            kind cluster name (default: ofb)
#   NODE_IMAGE         kind node image (default: kindest/node:v1.29.14)
#   K8S_NAMESPACE      namespace to deploy into (default: openfireblocks)
#   EXTRA_CA_CERT_FILE PEM bundle for building behind a TLS-intercepting
#                      egress proxy; passed through to scripts/build-images.sh
#   BUILD_NETWORK      docker build --network value (e.g. host), likewise
#   SKIP_BUILD=1       reuse whatever openfireblocks/* images already exist
set -euo pipefail

CLUSTER="${CLUSTER:-ofb}"
NODE_IMAGE="${NODE_IMAGE:-kindest/node:v1.29.14}"
NS="${K8S_NAMESPACE:-openfireblocks}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"

# Asked of build-images.sh rather than restated here. This list used to be
# its own six-entry copy, and the chart had grown to deploy thirteen
# services -- so a cluster built from scratch had six deployments stuck in
# ImagePullBackOff, and the only reason it was not noticed is that the
# images happened to already exist on the machine where it was developed.
# while-read rather than mapfile: macOS ships bash 3.2, which has no mapfile.
SERVICES=()
while IFS= read -r line; do [ -n "$line" ] && SERVICES+=("$line"); done \
  < <("${ROOT}/scripts/build-images.sh" --list)
DEPENDENCY_IMAGES=(postgres:16-bookworm hashicorp/vault:1.17 temporalio/auto-setup:1.25.2)

need() { command -v "$1" >/dev/null || { echo "missing required tool: $1" >&2; exit 1; }; }
need docker; need kind; need kubectl; need helm

# Loads a local image into every node's containerd.
#
# Every node, not just the control plane: with three workers a pod can be
# scheduled anywhere, and imagePullPolicy IfNotPresent against images that
# exist only locally means a node without the image cannot start the pod.
#
# Not `kind load docker-image`: with Docker's containerd image store (the
# default from Docker 28) kind 0.24 reports "failed to detect containerd
# snapshotter" and refuses. Piping a saved archive straight into the node's
# own ctr sidesteps kind's detection entirely.
#
# --platform <node arch> rather than --all-platforms: a multi-arch image
# pulled for this host only has this platform's blobs locally, and
# --all-platforms fails on the missing ones.
# The nodes that can actually run workloads.
#
# kind taints the control plane NoSchedule whenever the cluster has at
# least one worker, so pushing images there costs a full copy of every
# image for nothing -- and with three workers that was enough to run the
# host out of disk mid-load. On a single-node cluster kind leaves the
# control plane schedulable, so it is included there.
schedulable_nodes() {
  local nodes workers
  nodes="$(kind get nodes --name "${CLUSTER}")"
  workers="$(echo "${nodes}" | grep -- '-worker' || true)"
  if [[ -n "${workers}" ]]; then
    echo "${workers}"
  else
    echo "${nodes}"
  fi
}

# The nodes' own architecture: amd64 on Intel hosts, arm64 on Apple Silicon.
# Hardcoding amd64 made the import fail on an M-series Mac ("no unpack
# platforms defined"), because the images built there are arm64.
node_arch() {
  case "$(docker exec "$(schedulable_nodes | head -n1)" uname -m)" in
    aarch64|arm64) echo arm64 ;;
    *) echo amd64 ;;
  esac
}

load_image() {
  local image="$1"
  local archive arch
  archive="$(mktemp)"
  arch="$(node_arch)"
  docker save "${image}" -o "${archive}"
  for node in $(schedulable_nodes); do
    docker exec -i "${node}" \
      ctr -n k8s.io images import --platform "linux/${arch}" - < "${archive}" >/dev/null
  done
  rm -f "${archive}"
}

if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  echo "==> creating cluster ${CLUSTER} (${NODE_IMAGE})"
  # cluster.yaml gives three workers so the MPC parties can be placed on
  # separate nodes, which the chart requires by default.
  kind create cluster --name "${CLUSTER}" --image "${NODE_IMAGE}" \
    --config "${HERE}/cluster.yaml" --wait 300s
else
  echo "==> cluster ${CLUSTER} already exists"
fi
kubectl config use-context "kind-${CLUSTER}" >/dev/null

if [[ -z "${SKIP_BUILD:-}" ]]; then
  echo "==> building service images"
  "${ROOT}/scripts/build-images.sh"
fi

echo "==> loading images into the schedulable nodes"
for svc in "${SERVICES[@]}"; do
  load_image "openfireblocks/${svc}:latest"
  echo "    openfireblocks/${svc}:latest"
done
for image in "${DEPENDENCY_IMAGES[@]}"; do
  # Pulled on the host, then pushed into the node, so the node never needs
  # registry egress of its own.
  docker image inspect "${image}" >/dev/null 2>&1 || docker pull "${image}"
  load_image "${image}"
  echo "    ${image}"
done

echo "==> stateful dependencies"
kubectl apply -f "${HERE}/dependencies.yaml"

if [[ -n "${RESET_DB:-}" ]]; then
  echo "==> RESET_DB: wiping Postgres"
  kubectl -n "${NS}" delete job ofb-migrate --ignore-not-found --wait=true
  kubectl -n "${NS}" delete deployment postgres --ignore-not-found --wait=true
  kubectl -n "${NS}" delete pvc postgres-data --ignore-not-found --wait=true
  kubectl apply -f "${HERE}/dependencies.yaml"
  # Temporal keeps its own schema in the same Postgres, so wiping the
  # volume takes Temporal's databases with it. Restart so auto-setup
  # recreates them rather than looping on "no usable database connection".
  kubectl -n "${NS}" rollout restart deployment/temporal-frontend
fi

echo "==> waiting for Postgres"
kubectl -n "${NS}" wait --for=condition=ready pod -l app=postgres --timeout=300s

echo "==> applying migrations"
# From the working tree rather than a checked-in copy, so the .sql files
# have exactly one source of truth.
kubectl create configmap ofb-migrations -n "${NS}" \
  --from-file="${ROOT}/infrastructure/database/migrations/" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "${NS}" delete job ofb-migrate --ignore-not-found --wait=true
kubectl apply -f "${HERE}/migrate-job.yaml"
kubectl -n "${NS}" wait --for=condition=complete job/ofb-migrate --timeout=600s
kubectl -n "${NS}" logs job/ofb-migrate | tail -3

echo "==> secrets"
# Development credentials matching dependencies.yaml. In a real
# environment these come from AWS Secrets Manager via the External Secrets
# Operator -- see templates/secret.yaml in the chart.
kubectl -n "${NS}" create secret generic openfireblocks-secrets \
  --from-literal=database-url='postgresql://app:app-dev-password@postgres:5432/openfireblocks?sslmode=disable' \
  --from-literal=database-admin-url='postgresql://app_admin:app-admin-dev-password@postgres:5432/openfireblocks?sslmode=disable' \
  --from-literal=admin-api-key="${ADMIN_API_KEY:-dev-admin-api-key}" \
  --from-literal=jwt-secret="${JWT_SECRET:-dev-jwt-secret-not-for-production}" \
  --from-literal=vault-token='dev-root-token' \
  --from-literal=bitcoin-rpc-password='ofb-regtest' \
  --dry-run=client -o yaml | kubectl apply -f -

echo "==> configuring Vault PKI + kubernetes auth"
# Must precede the chart install: mpc-party and temporal-worker pods run
# vault-pki-init as an init container, and it blocks pod startup until it
# has a certificate. Without the PKI mount, role and kubernetes auth
# backend in place, every one of those pods sits in Init:Error.
kubectl -n "${NS}" delete job vault-pki-bootstrap --ignore-not-found --wait=true
kubectl apply -f "${HERE}/vault-pki-bootstrap.yaml"
kubectl -n "${NS}" wait --for=condition=complete job/vault-pki-bootstrap --timeout=300s
kubectl -n "${NS}" logs job/vault-pki-bootstrap | tail -2

echo "==> waiting for Temporal"
# The frontend binds the pod IP, not loopback, so an in-pod `temporal`
# invocation needs --address. tcpSocket readiness passes before the server
# finishes schema setup on a first boot, so probe the API itself.
TPOD=$(kubectl -n "${NS}" get pod -l app=temporal-frontend \
  --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')
kubectl -n "${NS}" exec "${TPOD}" -- bash -c '
  for i in $(seq 1 120); do
    temporal operator namespace describe -n default --address temporal-frontend:7233 >/dev/null 2>&1 && exit 0
    sleep 5
  done
  echo "temporal did not become ready" >&2; exit 1'

echo "==> installing chart"
VALUES_ARGS=(-f "${HERE}/values-kind.yaml")
if [[ -n "${LITE:-}" ]]; then
  echo "==> LITE profile: signing path only, one replica each (see values-kind-lite.yaml)"
  VALUES_ARGS+=(-f "${HERE}/values-kind-lite.yaml")
  # The standby database only matters for the failover drill.
  kubectl -n "${NS}" scale deployment postgres-standby --replicas=0
fi
helm upgrade --install ofb "${ROOT}/infrastructure/helm/openfireblocks" \
  "${VALUES_ARGS[@]}" -n "${NS}" --wait --timeout 10m

echo
echo "Deployed. Reach the API with:"
echo "  kubectl -n ${NS} port-forward svc/ofb-openfireblocks-api-gateway 3000:3000"
echo
echo "Then exercise the full path:"
echo "  ./infrastructure/kind/smoke-test.sh"
echo
echo "To also prove a threshold-signed transaction is accepted by a real node:"
echo "  kubectl apply -f ${HERE}/geth-dev.yaml"
echo "  ./infrastructure/kind/chain-test.sh"
