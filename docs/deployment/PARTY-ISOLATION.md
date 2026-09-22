# Making the threshold mean something

How to get from "three parties on one machine" to three parties that a
single actor cannot reach — which is the difference between a demonstration
of MPC and custody of somebody's money.

Run `infrastructure/kind/party-isolation-check.sh` against any deployment to
see where it currently sits.

---

## The problem, stated precisely

A 2-of-3 threshold key resists one compromise. That is the entire security
claim, and it is a claim about the *parties*, not about the cryptography.
The cryptography is the same whether the three shares live in three
datacentres or three directories on one laptop.

Today the parties run as three pods with anti-affinity across four
Kubernetes nodes. The scheduler honours it. `kubectl get pods -o wide` shows
three distinct node names. Every drill passes. And on a kind cluster those
nodes are containers sharing one kernel, so:

- one root shell reaches all three shares
- one disk snapshot captures all three
- one hypervisor escape, one compromised CI runner with cluster credentials,
  one `kubectl exec`, and the key is reconstructable

The system looks distributed from inside itself. This is why the check
script exists: the only layer that knows is the one underneath, and it has
to be interrogated deliberately.

---

## Levels, and what each actually buys

| Level | Survives | Does **not** survive |
|---|---|---|
| `simulated` | nothing | one root shell |
| `same-host` | a pod crash | one host compromise |
| `multi-node` | one node failing | one hypervisor, if the nodes are VMs on it |
| `multi-az` | a datacentre outage | one cloud account's administrator |
| `multi-region` | a regional outage or misconfiguration | one set of cloud credentials |
| `multi-account` | one account compromise, one leaked credential set, one compromised pipeline | collusion, or one administrator who holds all three accounts |

The jump that matters is `multi-region` → `multi-account`. Everything below
it is an *availability* property: it keeps the service up. Only account
separation is an *independence* property: it changes the number of distinct
compromises needed to steal the key from one to two.

A note on the last row, because it is the one people skip. Three AWS
accounts administered by the same two engineers, with the same SSO, is one
compromise away from all three. Independent infrastructure under one
administrator is one administrator. Genuine independence means different
credentials, different people, and ideally different providers — and at the
strongest end, different legal entities, which is how the established
custodians do it and why they can make the claim they make.

---

## Getting to multi-account

### What each party needs

A party is small: one service, one Vault, one secret store, and a public
endpoint the other parties can reach over mTLS. It does not need the
platform. The gateway, Temporal, Postgres and the chain nodes all stay in
the primary environment; the parties are deliberately the only thing that
moves.

Per party:

- its own cloud account (or a different provider entirely)
- its own Vault, sealed with that account's KMS — **not** a shared Vault,
  which would put all three shares behind one unseal key and undo the whole
  exercise
- a public DNS name and a certificate the other parties trust
- its own credentials, held by whoever operates that party
- egress to the other two parties, and to nothing else

### What changes in the platform

Less than expected, because the parties already speak over the network. The
chart addresses them through `MPC_PARTY_ENDPOINT_TEMPLATE`, which is
already an environment variable:

```
MPC_PARTY_ENDPOINT_TEMPLATE=https://party-{id}.example-custody.net
```

Set that, give each party its own mTLS identity, and the ceremony code is
unchanged. This is the payoff from having built the transport as real HTTP
between processes rather than as function calls in one binary — the
topology is configuration, not a rewrite.

What does need attention:

1. **Latency.** A DKG ceremony is several rounds of all-to-all messaging.
   Across regions that is tens of milliseconds per round rather than
   sub-millisecond, so ceremonies take longer. Measure before promising an
   SLA; `node-failure-drill.sh` reports ceremony duration.
2. **Party liveness.** The gateway picks a committee from whichever parties
   answer a health probe. Across the public internet that probe now fails
   for reasons that are not "the party is down" — measure and tune the
   timeout, or a transient network event silently narrows the committee.
3. **Vault per party.** Three Vaults means three unseal procedures, three
   backup regimes, three key-rotation schedules. This is real operational
   cost and it is the cost that buys the security property.

### Verifying it

```
OFB_PARTY_ACCOUNTS=111122223333,444455556666,777788889999 \
  REQUIRE=multi-account ./infrastructure/kind/party-isolation-check.sh
```

The account list is declared by the operator rather than discovered,
because Kubernetes cannot see the account a node's cloud credentials belong
to. That is deliberate: writing it down is a person asserting the fact, and
that assertion is what an auditor will ask for anyway.

Add the check to the go-live runbook with `REQUIRE=multi-account`, so a
deployment that has quietly collapsed back onto one account fails a gate
rather than passing unnoticed.

---

## What this costs

Rough, and worth pricing properly before committing:

- three small always-on instances plus three Vaults: on the order of a few
  hundred dollars a month, which is not the constraint
- three sets of cloud accounts to administer, with separate credentials,
  separate patching, and separate on-call
- if the strongest form is wanted — separate legal entities or a third-party
  co-signer — that is a corporate arrangement with legal and commercial
  work attached, measured in months rather than sprints

The engineering is the small part. That is worth being clear about:
whoever owns this decision is buying an operational commitment, not a
sprint.

---

## What this does not fix

Party isolation makes the *key* hard to steal. It does nothing about:

- **the policy engine**, which decides what gets signed. An attacker who
  cannot reconstruct the key but can make the parties sign a transaction of
  their choosing does not need to.
- **the gateway's authentication**, for the same reason.
- **a malicious insider at the platform operator**, who may not need the
  key if they can drive a signature.

These are what the external audit and penetration test are for
(`docs/security/AUDIT-READINESS.md`). Isolation and audit are complements:
neither is sufficient, and the audit is the one that finds the paths nobody
here has thought of.
