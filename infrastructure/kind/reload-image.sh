#!/usr/bin/env bash
#
# Rebuild ONE service image and load it into the kind cluster's nodes, then
# restart its deployment -- for when one image changed and up.sh's full rebuild
# (all 14 images, 15-30 minutes) is more than is needed.
#
#   infrastructure/kind/reload-image.sh backup ofb-openfireblocks-backup
#
# Arguments: the service name (a directory under services/) and the name of the
# deployment to restart. Environment: CLUSTER (default ofb), K8S_NAMESPACE
# (default openfireblocks), EXTRA_CA_CERT_FILE and BUILD_NETWORK as in
# scripts/build-images.sh.
set -euo pipefail

SVC="${1:?usage: reload-image.sh <service> <deployment>}"
DEPLOY="${2:?usage: reload-image.sh <service> <deployment>}"
CLUSTER="${CLUSTER:-ofb}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="openfireblocks/${SVC}:latest"

[ -d "${ROOT}/services/${SVC}" ] || { echo "no such service: ${SVC}" >&2; exit 1; }

build_args=()
if [ -n "${EXTRA_CA_CERT_FILE:-}" ]; then
  build_args+=(--secret "id=egress_ca,src=${EXTRA_CA_CERT_FILE}")
fi
if [ -n "${BUILD_NETWORK:-}" ]; then
  build_args+=(--network "${BUILD_NETWORK}")
fi

echo "==> building ${IMAGE}"
DOCKER_BUILDKIT=1 docker build "${build_args[@]}" -t "${IMAGE}" "${ROOT}/services/${SVC}"

case "$(uname -m)" in
  aarch64|arm64) arch=arm64 ;;
  *) arch=amd64 ;;
esac

archive="$(mktemp)"
trap 'rm -f "${archive}"' EXIT
docker save "${IMAGE}" -o "${archive}"

# Every node that can run pods, not just one: with imagePullPolicy
# IfNotPresent, a node without the image cannot start the pod.
echo "==> loading into the cluster's nodes"
for node in $(kind get nodes --name "${CLUSTER}" | grep -v control-plane); do
  docker exec -i "${node}" ctr -n k8s.io images import --platform "linux/${arch}" - < "${archive}" >/dev/null
  echo "    ${node}"
done

echo "==> restarting ${DEPLOY}"
kubectl -n "${NS}" rollout restart "deploy/${DEPLOY}"
kubectl -n "${NS}" rollout status "deploy/${DEPLOY}" --timeout=300s
