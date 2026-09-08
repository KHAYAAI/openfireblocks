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

SERVICES=(api-gateway mpc-party mpc-signer policy-service temporal-worker vault-pki-init)
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
# --platform linux/amd64 rather than --all-platforms: a multi-arch image
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

load_image() {
  local image="$1"
  local archive
  archive="$(mktemp)"
  docker save "${image}" -o "${archive}"
  for node in $(schedulable_nodes); do
    docker exec -i "${node}" \
      ctr -n k8s.io images import --platform linux/amd64 - < "${archive}" >/dev/null
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
  --from-literal=admin-api-key='dev-admin-api-key' \
  --from-literal=jwt-secret='dev-jwt-secret-not-for-production' \
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
helm upgrade --install ofb "${ROOT}/infrastructure/helm/openfireblocks" \
  -f "${HERE}/values-kind.yaml" -n "${NS}" --wait --timeout 10m

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
