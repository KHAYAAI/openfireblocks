#!/usr/bin/env bash
#
# Starts a Bitcoin Core testnet4 node in the cluster and points the platform at it.
# Run ON THE SERVER, once. Re-running is safe.
#
#   ./infrastructure/kind/bitcoin-testnet4-setup.sh            # start the node and switch the platform to testnet4
#   ./infrastructure/kind/bitcoin-testnet4-setup.sh status     # sync progress
#   ./infrastructure/kind/bitcoin-testnet4-setup.sh regtest    # switch the platform back to the private regtest node
#
# Switching the platform to testnet4 means the regtest drills (bitcoin-drill.sh, bitcoin-api-drill.sh) will not
# work until you switch back, because BITCOIN_NETWORK and the node URL are cluster-wide.
#
# Optional: IMAGE (default lncm/bitcoind:v28.0 -- needs Bitcoin Core 28+ for testnet4; verify the tag exists).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
NS="${K8S_NAMESPACE:-openfireblocks}"
IMAGE="${IMAGE:-lncm/bitcoind:v28.0}"
CHART="${ROOT}/infrastructure/helm/openfireblocks"
MODE="${1:-start}"

cli() { kubectl -n "${NS}" exec deploy/bitcoind-testnet4 -- sh -c 'bitcoin-cli -testnet4 -rpcuser=ofb -rpcpassword="$RPC_PASSWORD" "$@"' sh "$@"; }

restart_platform() {
  kubectl -n "${NS}" rollout restart deployment/ofb-openfireblocks-mpc-signer deployment/ofb-openfireblocks-api-gateway
  kubectl -n "${NS}" rollout status deployment/ofb-openfireblocks-mpc-signer --timeout=240s
  kubectl -n "${NS}" rollout status deployment/ofb-openfireblocks-api-gateway --timeout=240s
  sudo systemctl restart ofb-forward 2>/dev/null || true
}

case "${MODE}" in
  status)
    cli getblockchaininfo | python3 -c "
import sys,json
d=json.load(sys.stdin)
print('chain', d['chain'], 'blocks', d['blocks'], '/', d['headers'], 'progress %.4f' % d['verificationprogress'], 'initial_download', d['initialblockdownload'])"
    echo "peers: $(cli getconnectioncount)"
    ;;
  regtest)
    helm upgrade ofb "${CHART}" -n "${NS}" --reuse-values \
      --set external.bitcoinNetwork=regtest \
      --set-string mpcSigner.bitcoin.rpcUrl=http://bitcoind:18443 --wait --timeout 10m
    restart_platform
    echo "switched back to the private regtest node"
    ;;
  start)
    pw="$(kubectl -n "${NS}" get secret openfireblocks-secrets -o jsonpath='{.data.bitcoin-rpc-password}' | base64 -d)"
    [[ -n "${pw}" ]] || { echo "FAIL: no bitcoin-rpc-password in openfireblocks-secrets" >&2; exit 1; }
    kubectl -n "${NS}" create secret generic bitcoind-testnet4-rpc --from-literal=password="${pw}" \
      --dry-run=client -o yaml | kubectl apply -f -
    sed "s#__IMAGE__#${IMAGE}#" "${HERE}/bitcoind-testnet4.yaml" | kubectl apply -f -
    kubectl -n "${NS}" rollout status deployment/bitcoind-testnet4 --timeout=300s
    helm upgrade ofb "${CHART}" -n "${NS}" --reuse-values \
      --set external.bitcoinNetwork=testnet \
      --set-string mpcSigner.bitcoin.rpcUrl=http://bitcoind-testnet4:48332 \
      --set-string mpcSigner.bitcoin.rpcUser=ofb --wait --timeout 10m
    restart_platform
    echo
    echo "Node started and the platform now targets testnet4. The first sync takes a while; check with:"
    echo "  ./infrastructure/kind/bitcoin-testnet4-setup.sh status"
    ;;
  *) echo "usage: $0 [start|status|regtest]" >&2; exit 2 ;;
esac
