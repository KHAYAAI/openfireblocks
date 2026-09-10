#!/usr/bin/env bash
#
# Answers, from the running deployment, how far apart the MPC parties
# actually are.
#
# A k-of-n threshold key is only as strong as the independence of the
# parties holding the shares. If two of three shares sit on the same
# machine, a 2-of-3 key is a 1-of-1 key wearing a costume: one root
# compromise, one disk image, one hypervisor escape, and the private key
# can be reconstructed. Nothing about the cryptography changes; the security
# claim does, entirely.
#
# This is worth checking mechanically rather than asserting in a README,
# because the failure is invisible. The pods have anti-affinity, the
# scheduler honours it, `kubectl get pods -o wide` shows three different
# node names, every drill passes -- and on a kind cluster those three nodes
# are three containers on one kernel. The system looks distributed from
# inside itself. Only the layer underneath knows.
#
#   ./infrastructure/kind/party-isolation-check.sh
#   REQUIRE=multi-region ./infrastructure/kind/party-isolation-check.sh
#
# Levels, weakest to strongest:
#
#   simulated       parties share a physical host (kind, minikube, one VM)
#   same-host       parties on distinct nodes that are the same machine
#   multi-node      distinct nodes, isolation below them unknown
#   multi-az        distinct availability zones
#   multi-region    distinct regions
#   multi-account   distinct cloud accounts or providers
#
# REQUIRE names the minimum acceptable level; the script exits non-zero
# below it. Custody of customer funds needs multi-account, because every
# level below it has an administrator who can reach every share.
set -euo pipefail

NS="${K8S_NAMESPACE:-openfireblocks}"
REQUIRE="${REQUIRE:-}"
PARTY_SELECTOR="${PARTY_SELECTOR:-app.kubernetes.io/component=mpc-party}"

fail() { echo "FAIL: $*" >&2; exit 1; }

command -v kubectl >/dev/null || fail "kubectl is required"

echo "==> finding the MPC parties"
mapfile -t PARTY_PODS < <(kubectl -n "${NS}" get pods -l "${PARTY_SELECTOR}" \
  --field-selector=status.phase=Running \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.nodeName}{"\n"}{end}' 2>/dev/null || true)

if [[ ${#PARTY_PODS[@]} -eq 0 ]]; then
  # Fall back to a name match: the label differs between the chart and the
  # standalone manifests, and a check that silently finds nothing would
  # report perfect isolation for a deployment that has no parties at all.
  mapfile -t PARTY_PODS < <(kubectl -n "${NS}" get pods --no-headers -o wide 2>/dev/null \
    | awk '/mpc-party/ && $3 == "Running" {print $1"\t"$7}' || true)
fi

[[ ${#PARTY_PODS[@]} -gt 0 ]] || fail "no running MPC parties found in namespace ${NS}"

declare -A NODE_OF
for row in "${PARTY_PODS[@]}"; do
  pod="${row%%$'\t'*}"
  node="${row##*$'\t'}"
  NODE_OF["${pod}"]="${node}"
  echo "    ${pod} on ${node}"
done

PARTY_COUNT=${#NODE_OF[@]}
mapfile -t NODES < <(printf '%s\n' "${NODE_OF[@]}" | sort -u)
NODE_COUNT=${#NODES[@]}

echo
echo "==> what is underneath those nodes"

# Every distinguishing fact Kubernetes will tell us about a node. Read
# together rather than trusting any one: a provider id is authoritative
# where it exists, a zone label is set by the cloud controller and can be
# forged by hand, and kind sets none of them.
declare -A ZONES REGIONS PROVIDERS KERNELS INSTANCE_IDS
for node in "${NODES[@]}"; do
  # Pipe-separated, not space-separated. Most of these labels are absent
  # on a local cluster, and with spaces the empty fields collapse -- the
  # kernel version ends up read into the zone, and every node then looks
  # like it has a distinct zone. IFS with an explicit delimiter keeps empty
  # fields empty.
  IFS='|' read -r zone region provider kernel <<<"$(kubectl get node "${node}" -o jsonpath='{.metadata.labels.topology\.kubernetes\.io/zone}{"|"}{.metadata.labels.topology\.kubernetes\.io/region}{"|"}{.spec.providerID}{"|"}{.status.nodeInfo.kernelVersion}' 2>/dev/null)"
  ZONES["${node}"]="${zone:-<none>}"
  REGIONS["${node}"]="${region:-<none>}"
  PROVIDERS["${node}"]="${provider:-<none>}"
  KERNELS["${node}"]="${kernel:-<unknown>}"
  printf '    %-28s zone=%-16s region=%-12s provider=%s\n' \
    "${node}" "${ZONES[$node]}" "${REGIONS[$node]}" "${PROVIDERS[$node]}"
done

# Counts how many distinct real values a map holds, ignoring the ones that
# were absent. `|| true` because grep exits non-zero when nothing matches,
# and under `set -e` inside a command substitution that ends the script --
# so a cluster where no node has a zone label would abort here instead of
# reporting that it has no zone labels, which is the answer.
distinct_values() {
  local -n map=$1
  printf '%s\n' "${map[@]}" | grep -v '^<none>$' | sort -u | wc -l || true
}

DISTINCT_ZONES=$(distinct_values ZONES)
DISTINCT_REGIONS=$(distinct_values REGIONS)
DISTINCT_PROVIDERS=$(distinct_values PROVIDERS)

# Cloud accounts, from the provider id. An AWS provider id looks like
# aws:///us-east-1a/i-0abc; the account is not in it, so this asks the
# instance's own metadata only where a tool is available. Absent that, a
# single provider prefix across all nodes means one account until proven
# otherwise -- the conservative reading, since claiming account separation
# that does not exist is the expensive direction to be wrong in.
DISTINCT_ACCOUNTS=0
if [[ -n "${OFB_PARTY_ACCOUNTS:-}" ]]; then
  # Explicitly declared by the operator: a comma-separated list of the
  # account (or subscription, or project) each party runs in. Trusted
  # because an operator asserting this in writing is exactly the sign-off
  # the check is trying to elicit.
  DISTINCT_ACCOUNTS=$(tr ',' '\n' <<<"${OFB_PARTY_ACCOUNTS}" | sed '/^$/d' | sort -u | wc -l)
fi

# kind and minikube put every "node" in a container on one kernel. There is
# no label that says so, so it is inferred: identical kernel versions across
# every node, and no provider id anywhere.
SAME_KERNEL=$(printf '%s\n' "${KERNELS[@]}" | sort -u | wc -l)
LOCAL_CLUSTER=false
if [[ "${DISTINCT_PROVIDERS}" -eq 0 && "${SAME_KERNEL}" -eq 1 ]]; then
  LOCAL_CLUSTER=true
fi
if kubectl get nodes -o jsonpath='{.items[*].metadata.labels}' 2>/dev/null | grep -q 'kind\.x-k8s\.io'; then
  LOCAL_CLUSTER=true
fi

echo
echo "==> verdict"

LEVEL="multi-node"
if [[ "${LOCAL_CLUSTER}" == true ]]; then
  LEVEL="simulated"
elif [[ "${NODE_COUNT}" -lt "${PARTY_COUNT}" ]]; then
  LEVEL="same-host"
elif [[ "${DISTINCT_ACCOUNTS}" -ge "${PARTY_COUNT}" ]]; then
  LEVEL="multi-account"
elif [[ "${DISTINCT_REGIONS}" -ge 2 ]]; then
  LEVEL="multi-region"
elif [[ "${DISTINCT_ZONES}" -ge 2 ]]; then
  LEVEL="multi-az"
fi

echo "    parties:  ${PARTY_COUNT}"
echo "    nodes:    ${NODE_COUNT}"
echo "    zones:    ${DISTINCT_ZONES}"
echo "    regions:  ${DISTINCT_REGIONS}"
echo "    accounts: ${DISTINCT_ACCOUNTS} (declared via OFB_PARTY_ACCOUNTS)"
echo "    level:    ${LEVEL}"
echo

case "${LEVEL}" in
  simulated)
    cat <<'EXPLAIN'
    The parties are on separate Kubernetes nodes that are containers on one
    machine. Anti-affinity is being honoured and it buys nothing here: one
    root shell, one disk image, or one hypervisor escape reaches every
    share, so the threshold key can be reconstructed by a single actor.

    This is correct for development and for the drills. It is not custody.
EXPLAIN
    ;;
  same-host)
    cat <<'EXPLAIN'
    Two or more parties share a node. Whatever the threshold says, the
    number of independent compromises needed to reconstruct the key is
    lower than it -- check the anti-affinity rules and whether the cluster
    has enough schedulable nodes to honour them.
EXPLAIN
    ;;
  multi-node)
    cat <<'EXPLAIN'
    The parties are on distinct nodes, and nothing below that is known:
    no zone, region or provider labels are set. If those nodes are virtual
    machines on one hypervisor, this is the simulated case with extra
    steps. Establish what the nodes actually are before relying on it.
EXPLAIN
    ;;
  multi-az)
    cat <<'EXPLAIN'
    Distinct availability zones. This survives a datacentre failure, which
    is an availability property. It is not an independence property: one
    cloud account's administrator, one compromised CI pipeline, and one
    provider-side incident still reach every share.
EXPLAIN
    ;;
  multi-region)
    cat <<'EXPLAIN'
    Distinct regions. Survives a regional failure and a regional
    misconfiguration. Still one account, one set of credentials, one
    administrator, and one provider -- so still one actor who can reach
    every share.
EXPLAIN
    ;;
  multi-account)
    cat <<'EXPLAIN'
    Distinct accounts. No single set of cloud credentials reaches every
    share, which is the first level at which the threshold means what it
    says against an insider or a compromised pipeline.

    The remaining question is not technical: whether the accounts are under
    genuinely separate operational control -- different people, different
    credentials, different on-call -- or whether one team administers all
    of them. Independent infrastructure under one administrator is one
    administrator.
EXPLAIN
    ;;
esac

if [[ -n "${REQUIRE}" ]]; then
  # Ordered weakest to strongest; a level at or above the requirement passes.
  LEVELS=(simulated same-host multi-node multi-az multi-region multi-account)
  rank_of() {
    local wanted="$1" i
    for i in "${!LEVELS[@]}"; do
      [[ "${LEVELS[$i]}" == "${wanted}" ]] && { echo "$i"; return; }
    done
    echo "-1"
  }
  have=$(rank_of "${LEVEL}")
  want=$(rank_of "${REQUIRE}")
  [[ "${want}" -ge 0 ]] || fail "unknown REQUIRE level '${REQUIRE}'"

  echo
  if [[ "${have}" -lt "${want}" ]]; then
    fail "isolation is '${LEVEL}', below the required '${REQUIRE}'"
  fi
  echo "PASS: isolation '${LEVEL}' meets the required '${REQUIRE}'"
fi
