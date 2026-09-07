# Running on a real Kubernetes cluster

What happened the first time this platform was deployed to real
Kubernetes, what it proved, what it broke, and what is still unproven.

Reproduce it with `infrastructure/kind/up.sh` followed by
`infrastructure/kind/smoke-test.sh`.

---

## 1. What was run

| | |
|---|---|
| Kubernetes | 1.29.14, four nodes (kind): one control plane, three workers |
| Container runtime | containerd 2.0.2 |
| Deployed by | `infrastructure/helm/openfireblocks` via `helm upgrade --install --wait` |
| Workloads | api-gateway, 3 × mpc-party (one per worker node), mpc-signer ×3, policy-service ×2, temporal-worker ×2 |
| Internal transport | mTLS, certificates issued per pod at startup by Vault PKI via Kubernetes auth |
| Dependencies | PostgreSQL 16, Vault 1.17 (dev mode), Temporal 1.25.2 — `infrastructure/kind/dependencies.yaml` |
| Schema | all 16 migrations, applied by a Job as the `app` role |

Before this, no container image in the repository had ever been built and
nothing had ever run on Kubernetes. Both statements were true right up
until this deployment; the chart had only ever been validated as YAML
(`helm lint`, `kubeconform` strict), which proves the API server would
*accept* the manifests and nothing about whether the system runs.

## 2. What it proved

The full customer path, exercised through the real API against the real
cluster (`infrastructure/kind/smoke-test.sh`):

1. `POST /admin/customers` creates a tenant and returns an API key.
2. `POST /keys` (authenticated with that key) starts a Temporal
   `ProvisionKeyWorkflow`.
3. That workflow runs a real 2-of-3 DKG ceremony across three separate
   `mpc-party` pods, each an independent process on its own pod network.
4. All three parties independently derive **the same** Ethereum address and
   each seals its own distinct share into Vault under
   `secret/openfireblocks/mpc-party/party-N/<ceremony-id>`.
5. The key is activated with that address and public key.
6. `POST /keys/:keyId/sign` runs a threshold signing ceremony with **two**
   of the three parties and returns a signature — gated by the same
   fail-closed policy evaluation as every other signing path.
7. That signature recovers, via `crypto.SigToPub`, to exactly the address
   the ceremony derived.

Step 7 is the one that matters. Steps 1–6 can all report success while the
pieces belong to different keys; recovering the signer from the signature
is what ties them together.

Separately, `POST /keys/:keyId/transactions` — where the gateway builds and
hashes the transaction rather than accepting a digest — was verified the
same way, and independently: the returned raw transaction bytes were parsed
back with `ethers` outside the service, and

- the sender recovered from them is the DKG-derived address, and
- the transaction's own `unsignedHash` is byte-identical to the digest the
  signing ceremony was asked to sign.

That second equality is the whole claim of the endpoint: what policy
evaluated and what got signed are provably the same transaction.

And finally, `infrastructure/kind/chain-test.sh` closes the loop against a
real node (`infrastructure/kind/geth-dev.yaml`). Recovering a signature
correctly is necessary and not sufficient: an encoding mistake -- a wrong
EIP-155 `v`, a mis-serialised field, an off-by-one in what gets hashed --
still yields a signature that recovers to the right address while being
rejected by every node on the network. So the raw bytes are handed to geth:

- the node **accepts** them, and computes the same transaction hash the
  service predicted,
- the transaction is **mined** with status success,
- the recipient's balance actually changes, and
- the node's own `eth_getTransactionByHash` attributes the transaction to
  the DKG-derived address.

Also checked, because a signing route is only as good as what it refuses:
an over-limit request is denied 403 with the specific policy reason, a
different tenant asking to sign with the same key gets 404 (row-level
security holding all the way from the API to the row), and a malformed
digest is rejected 400 before anything is started.

Measured:

| | |
|---|---|
| DKG end to end, cold pre-params pool | **101s** |
| DKG end to end, warm pre-params pool | **26s** |
| Threshold signature via `POST /keys/:keyId/sign` | **1.06s** |
| Signed transaction via `POST /keys/:keyId/transactions` | **2.17s** |
| DKG end to end, mTLS on, parties on three separate nodes | **118s** |
| Threshold-signed transfer accepted and mined by geth | block 93, status success |
| Migrations, fresh database | 16 applied |
| Migrations, second run | 0 applied, 16 skipped |
| Tenant isolation | tenant A sees 1 of 2 rows; no tenant context sees 0 |

The cold/warm difference is the pre-params fix in section 3.1, not noise.

## 3. What broke, and why only here

Three bugs. None was reachable when the services ran as processes on one
host, which is the entire argument for keeping this deployment runnable.

### 3.1 Every DKG ceremony failed

`services/mpc-party` answers a relayed protocol message with 503 while it
is still constructing its `LocalParty`; the sender retries, then aborts the
ceremony. The retry budget was 60s. But `runKeygen` called
`GeneratePreParams(2 * time.Minute)` on the ceremony's critical path — so a
party was allowed to take **twice as long to become ready as its peers
would wait for it**. Any party that used its allowance was guaranteed to be
abandoned mid-ceremony.

Locally this never lost: three unthrottled processes on one host finish
safe-prime generation in seconds. Under the chart's own CPU limits
(`requests: 250m`) it lost every time.

Fixed on both sides: generation moved off the critical path into a
background pool (`services/mpc-party/preparams.go`), and the two timeouts
became named constants with the correct ordering plus a test that fails if
they are ever inverted again. They live in different files and nothing in
the type system relates them, so the test is the only thing holding them
together.

### 3.2 Row-level security could never be enforced

Migration 011's premise is `ALTER ROLE app NOSUPERUSER`, because superusers
bypass RLS unconditionally. PostgreSQL refuses to let the *bootstrap* role
give up SUPERUSER — "The bootstrap user must have the SUPERUSER attribute"
— so on any cluster provisioned the obvious way (`POSTGRES_USER=app`) the
migration fails outright and there is no configuration in which those
policies take effect.

The migration now demotes only when necessary and raises a specific,
actionable error when demotion is impossible, rather than being capable of
reporting "migrations applied" on a database where every tenant can read
every other tenant's rows. `infrastructure/kind/dependencies.yaml`
provisions the roles the way that actually works: bootstrap as `postgres`,
create `app` as an ordinary LOGIN role, and give it ownership of schema
`public` so it owns the tables the migrations create — ownership is
load-bearing, because 011 turns on FORCE ROW LEVEL SECURITY and grants
table privileges only to `app_admin`.

### 3.3 A failed key provisioning was unrecoverable

`KeysService.createKey` inserts the `key_pairs` and `dkg_ceremonies` rows
and then starts the workflow. On a start failure it marked the ceremony
failed but left the key at `pending_dkg` with no workflow behind it, and
`UNIQUE (customer_id, name)` meant the dead row consumed the name forever.
Retrying the same request returned a raw 500 carrying the Postgres
constraint text — the failure mode most likely to follow a transient outage
was itself unrecoverable.

Migration 016 adds a terminal `failed` status and narrows uniqueness to
exclude it, so the audit trail keeps the attempt while the name is
released; `createKey` marks the key failed as well as the ceremony and maps
`unique_violation` to 409.

### 3.4 Turning mTLS on made every party pod permanently unrunnable

Found by turning it on. The chart's liveness and readiness probes are plain
`httpGet` against the party's main port. With mTLS that port is HTTPS with
`RequireAndVerifyClientCert`, and kubelet speaks plaintext and presents no
client certificate, so every probe failed the handshake (`client sent an
HTTP request to an HTTPS server`), liveness killed the container, and the
pods crash-looped forever.

Probing over HTTPS would not have helped: kubelet has no client certificate
to offer, and requiring one is the property worth keeping. So `mpc-party`
now serves a separate plaintext listener carrying **only** `GET /health`
(`serveHealthPlaintext`), and the chart points the probes at it when mTLS is
on. No ceremony endpoint, no `/info`, no `/metrics` — it is not a way around
mTLS for anything that matters.

A security control that guarantees an outage does not get switched on, so
this was load-bearing for mTLS being usable at all.

### 3.5 With mTLS on, the orchestrator still dialled the parties over plain HTTP

`MPC_PARTY_ENDPOINT_TEMPLATE` hardcoded `http://party-{id}:7000`. Every DKG
ceremony failed with `unexpected status 400: Client sent an HTTP request to
an HTTPS server`. The chart now emits `https://` when mTLS is enabled.

That alone was not enough. The template also had to change to the fully
qualified Service name, because certificates were being issued for
`party-N.internal` — the service *identity* — while peers dial
`party-N.<namespace>.svc.cluster.local`, and TLS verifies the name that was
dialled. `vault-pki-init` had no way to request additional SANs at all, so
it gained `ALT_NAMES`, and the chart now asks for the in-cluster DNS name as
a SAN alongside the identity in the CN. Without it, hostname verification
fails and the error reads like a broken certificate rather than a naming
mismatch.

### 3.6 A rolling update deadlocks under strict anti-affinity

With `spreadAcrossNodes: required` and exactly one node per party, a
`RollingUpdate` stands the replacement up before retiring the old pod — and
there is nowhere to put it. The new pod stays `Pending` and the rollout
never completes.

The party Deployments are now `Recreate`. A party is a singleton with a
fixed identity, not one of an interchangeable pool, so surging was never
meaningful for it; and the brief gap while one restarts is precisely what
the threshold exists to absorb — a 2-of-3 committee keeps signing with the
other two.

### 3.7 The pre-params pool starved the ceremonies it was meant to help

The fix in 3.1 moved safe-prime generation into a background pool of two.
The filler refills as soon as a slot frees, so each party kept two
searches running — six across a three-party committee — and safe-prime
generation is entirely CPU-bound. On a cluster where each party is capped
at one CPU, that background load starved the *inline* generation a new
ceremony depends on, and ceremonies began failing outright with `timeout or
error while generating the safe primes`. The deeper pool made the cold path
slower, not faster.

Pool size is now one, and both timeouts were raised on measurement rather
than guess: ninety seconds was not enough for the search to converge under
a 1-CPU limit. Safe-prime search is a randomised search with a heavy tail,
so the budget has to cover the tail, not the median. The ordering invariant
(`preParamsGenTimeout` < `PeerReadyTimeout`) still holds and is still
pinned by a test.

### 3.8 Also fixed along the way

- **No migration ledger.** Applying migrations was all-or-nothing from an
  empty database; a second run died on 001 with `relation "customers"
  already exists`, which made adding a migration to a deployed environment
  impossible. The Job now keeps `schema_migrations`, with each migration
  and its ledger row committing in one transaction.
- **No image could be built.** See the `build:` commit — the api-gateway
  Dockerfile compiled Go for a TypeScript service, four Go services pinned
  `golang:1.21` against `go 1.24` modules, `vault-pki-init` had no
  Dockerfile at all despite the chart mounting it as an initContainer, and
  every runtime stage placed its binary somewhere a uid-1000 pod could not
  read it.

## 4. What is still not proven

Do not read section 2 as more than it is.

1. **Still one host.** Four nodes, three of them workers, each running one
   MPC party — so the scheduling constraint is real, satisfied, and the
   parties reach each other across node boundaries under mTLS rather than
   over loopback. But they are all containers on one machine. This is not a
   multi-machine deployment and the parties do not have independent failure
   domains in any sense that would survive that machine dying.
2. **One of the two signing routes still signs an opaque digest.**
   `POST /keys/:keyId/transactions` builds and hashes the transaction
   itself, so policy governs exactly what gets signed -- that is the route
   to use, and it is verified below. `POST /keys/:keyId/sign` remains, and
   it takes a caller-supplied digest: policy there can only evaluate what
   the caller *claims* the digest commits to. It is useful for signing
   things that are not Ethereum transactions, and it is the weaker of the
   two; a deployment that does not need it should not expose it. Neither
   route constrains calldata semantics -- policy evaluates
   `to`/`value`/`chainId`, not what a contract call does.
   `POST /sign` is a third thing entirely: it routes to `mpc-signer`, the
   single-key non-threshold service.
3. **The chain is `geth --dev`, not a network.** A threshold-signed
   transaction from this cluster is accepted and mined (see above), which
   settles transaction encoding and node acceptance. It settles nothing
   about mainnet: `geth --dev` is a single-signer instant-seal chain with
   no consensus, no competing mempool, no reorgs and no fee market. Also,
   `POST /keys/:keyId/transactions` returns the raw bytes and does not
   broadcast them -- `chain-test.sh` does that step itself. Nonce
   management is the caller's problem.
4. **Nothing was drained or killed.** The parties are on separate nodes and
   the constraint that puts them there is enforced, but no node was
   cordoned, drained or failed to see whether a 2-of-3 committee really
   keeps signing through it. The design says it should; that is an
   argument, not evidence.
5. **The certificate lifecycle is untested past issuance.** Certs are
   issued per pod at startup with a 24h TTL and nothing renews them: a pod
   older than its certificate has no path back to a valid one except
   restarting. That is survivable given short-lived pods and is exactly the
   kind of thing that bites at 3am on day two.
6. **Dev-grade dependencies.** Vault in dev mode (in-memory,
   auto-unsealed, a root CA generated in place with no offline backup), one
   Postgres with no replica, well-known passwords. Nothing about HA,
   auto-unseal, or failover was exercised by this.
7. **No load.** All timings are single-request numbers on a shared host,
   and the pre-params contention in 3.7 is a direct demonstration that
   this system's behaviour under CPU pressure differs from its behaviour
   when idle. Do not read any of these numbers as capacity data.
8. **The extra services are built but not deployed.** `policyApi`,
   `settlement`, `billing`, `webhooks`, `marketplace` and `compliance` now
   have working images, but are still disabled in `values-kind.yaml` and
   have not been run. `ceremonyOrchestrator` has no image and does not
   compile; its responsibilities are already covered by `temporal-worker`'s
   `DKGCeremonyWorkflow`.

## 5. Running it in a sandboxed environment

Two things bite in nested-container environments (CI sandboxes, dev
containers) and neither is a problem with this repository:

- **`runc` cannot lower `oom_score_adj`.** Kubelet asks static
  control-plane pods for `oomScoreAdj: -998`; lowering it needs
  `CAP_SYS_RESOURCE` in the *initial* user namespace, which a nested
  container does not have. Every pod sandbox then fails with
  `can't get final child's PID from pipe: EOF`. Workaround: a `runc`
  wrapper in the kind node image that strips `process.oomScoreAdj` from the
  OCI bundle. It only changes which process the kernel OOM killer would
  pick on a throwaway dev node. Never do this on a real node.
- **Registry egress.** If the Docker Hub blob CDN
  (`production.cloudfront.docker.com`) is blocked, `mirror.gcr.io` is a
  pull-through mirror that serves its own blobs: `dockerd
  --registry-mirror=https://mirror.gcr.io`. `up.sh` pulls dependency images
  on the host and pushes them into the node, so the node itself never needs
  registry egress.
