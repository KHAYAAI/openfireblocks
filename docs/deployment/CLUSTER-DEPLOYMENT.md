# Running on a real Kubernetes cluster

What happened the first time this platform was deployed to real
Kubernetes, what it proved, what it broke, and what is still unproven.

Reproduce it with `infrastructure/kind/up.sh` followed by
`infrastructure/kind/smoke-test.sh`.

---

## 1. What was run

| | |
|---|---|
| Kubernetes | 1.29.14, single node (kind) |
| Container runtime | containerd 2.0.2 |
| Deployed by | `infrastructure/helm/openfireblocks` via `helm upgrade --install --wait` |
| Workloads | api-gateway, 3 × mpc-party, mpc-signer ×3, policy-service ×2, temporal-worker ×2 |
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
6. `ThresholdSigningWorkflow` with **two** of the three parties produces a
   signature.
7. That signature recovers, via `crypto.SigToPub`, to exactly the address
   the ceremony derived.

Step 7 is the one that matters. Steps 1–6 can all report success while the
pieces belong to different keys; recovering the signer from the signature
is what ties them together.

Measured:

| | |
|---|---|
| DKG end to end, cold pre-params pool | **101s** |
| DKG end to end, warm pre-params pool | **26s** |
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

### 3.4 Also fixed along the way

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

1. **No customer-facing route signs with a provisioned key.** `POST /sign`
   goes to `mpc-signer`, which is the separate **single-key, non-threshold**
   path. `ThresholdSigningWorkflow` is reachable only from Temporal — the
   smoke test starts it with the `temporal` CLI, and before that it was
   started only from a test file. A customer can create a threshold key
   through the API and has no API with which to use it. This is the largest
   remaining functional gap and it is squarely on the critical path to
   launch.
2. **Single node.** Everything ran on one kubelet. Nothing here exercises
   scheduling across nodes, pod anti-affinity (the chart declares it for
   api-gateway), rolling updates under load, or a node failure. Three
   `mpc-party` pods on one node is not three independent failure domains,
   which is the entire security argument for threshold signing.
3. **No chain.** `ethereumRpcSepolia` is empty in this deployment, so
   nothing was broadcast from the cluster. Chain behaviour is covered
   separately by the `live`-tagged tests against a real geth node.
4. **mTLS is off here.** `mpcParty.mtls.enabled: false`. Turning it on
   additionally requires Vault's Kubernetes auth method to be configured,
   which remains unexercised — `vault-pki-init`'s Kubernetes path has still
   never run.
5. **Dev-grade dependencies.** Vault in dev mode (in-memory,
   auto-unsealed), one Postgres with no replica, well-known passwords.
   Nothing about HA, auto-unseal, or failover was exercised by this.
6. **No load.** All timings are single-node, single-request numbers.
7. **The extra services are not deployed.** `policyApi`, `settlement`,
   `billing`, `webhooks`, `marketplace`, `compliance` and
   `ceremonyOrchestrator` are disabled in `values-kind.yaml` — they have no
   images built. `ceremony-orchestrator` in particular does not compile and
   its responsibilities are already covered by `temporal-worker`'s
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
