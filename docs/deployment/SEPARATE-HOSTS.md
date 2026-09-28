# Running the signing parties on separate hosts

The procedure for moving from three parties in one cluster (`simulated`)
to three parties in three cloud accounts run by three different people
(`multi-account`). This is the step that stands between the platform and
real money; see [PARTY-ISOLATION.md](PARTY-ISOLATION.md) for why.

**Status: built, not yet applied.** The Terraform, the chart mode, the
share-retirement change and the isolation check are in the repository and
tested as far as this environment allows (below). Nobody has applied this
to real AWS accounts yet. The first time is a project with a checklist,
not a command.

---

## What is in the repository

| Piece | Where | What it does |
|---|---|---|
| Party host module | `infrastructure/terraform/modules/mpc-party-host` | One party, alone in one account: its own VPC, security group (ceremony port from named peers and the platform only; no SSH), encrypted volume, its own Vault auto-unsealed by this account's KMS key, instance role that can reach only this party's secrets, VPC flow logs, IMDSv2 only, `prevent_destroy` on the share volume |
| Per-party root | `infrastructure/terraform/party` | Applied **once per party, by that party's operator**, with its own state. Refuses to apply into any account but the one it was given. Requires ceremony co-signing |
| Vault bootstrap | `infrastructure/terraform/party/party-bootstrap.sh` | One-time, on the host: initialise the party's Vault (recovery keys PGP-encrypted to named holders), create a token that can touch only this party's shares, store it, revoke root |
| External mode in the chart | `mpcParty.external` | No parties in the cluster; the gateway addresses them at your DNS names. Refuses plaintext, refuses missing `{id}`, refuses running without worker mTLS |
| Share retirement by the party | `POST /tss/shares/retire` on mpc-party | Each party permanently destroys its own old share, on an authorised request. See "What changed in the platform" below |
| Isolation check | `infrastructure/kind/party-isolation-check.sh` with `OFB_PARTY_HOSTS` | Checks each party is a distinct, reachable address, and fails below `REQUIRE` |

## Why three separate applies, not one

One Terraform configuration that deploys all three parties needs one
person with credentials for all three accounts. That person is the single
administrator the separation exists to remove. So each party is its own
root, its own state bucket in its own account, and its own operator.
Operators exchange exactly one thing: each other's `public_ip`.

## The procedure

### 0. Decide who

Three operators, one per party. They should not hold each other's AWS
credentials, Vault recovery keys, or state buckets. Write the names down;
the auditor will ask.

### 1. Each operator: accounts and images

- An AWS account for this party, and an S3 bucket plus DynamoDB table in
  it for Terraform state (`backend.hcl.example`).
- The `mpc-party` image pushed to this account's ECR, **pinned by digest**.

### 2. Each operator: first apply (addresses)

Peers aren't known yet, so first apply with the platform's address only:

```bash
cd infrastructure/terraform/party
terraform init -backend-config=backend-party-2.hcl
terraform apply -var-file=party-2.tfvars -var='peer_party_cidrs=["192.0.2.1/32"]'
terraform output public_ip
```

(Any placeholder `/32` works for the first pass; it only admits traffic.)

### 3. Exchange addresses, re-apply

Each operator sends their `public_ip` to the other two and to the
platform. Each sets `peer_party_cidrs` to the other two and applies again.

### 4. DNS and certificates

- DNS: `party-1.custody.example.com` → party 1's `public_ip`, and so on.
- Each party needs an mTLS certificate for its name, from a CA the other
  parties and the platform's worker trust. Put it in the party's
  `tls_secret_arn` as `{"cert": "...", "key": "...", "ca": "..."}` —
  with the AWS CLI, **not** through Terraform, which would write it into
  state.

### 5. Each operator: initialise Vault

```bash
aws ssm start-session --target "$(terraform output -raw instance_id)"
sudo bash party-bootstrap.sh <vault_token_secret_arn> alice.asc,bob.asc,carol.asc
```

Recovery keys come out PGP-encrypted to the named holders. Copy
`/root/vault-recovery-keys.pgp.json` off the host and delete it.

### 6. Platform: point at the parties

```yaml
mpcParty:
  enabled: false
  external:
    enabled: true
    endpointTemplate: https://party-{id}.custody.example.com:7000
    healthTemplate: http://party-{id}.custody.example.com:7001/health
temporalWorker:
  mtls:
    enabled: true       # the worker's client certificate, from the same CA
external:
  partyIsolationLevel: multi-account
```

### 7. Verify, then drill

```bash
OFB_PARTY_HOSTS=party-1.custody.example.com,party-2.custody.example.com,party-3.custody.example.com \
OFB_PARTY_ACCOUNTS=<acct-1>,<acct-2>,<acct-3> \
REQUIRE=multi-account ./infrastructure/kind/party-isolation-check.sh
```

Then, on testnet: provision a key, sign, and run the recovery drill
(`docs/deployment/KEY-RECOVERY.md`) against these hosts. Measure ceremony
time across regions before promising anyone an SLA. Put the isolation
check in the go-live runbook with `REQUIRE=multi-account`.

## What changed in the platform

**Retiring old shares.** After a key rotation, the worker used to delete
every party's old share with its own Vault token. That worked only
because all parties shared one Vault, and it meant one platform credential
could reach every share. With separate hosts it would have silently done
nothing, leaving old shares in place.

Now each party retires its own: the worker sends `POST /tss/shares/retire`
to each party with a `retire` authorisation from the co-signer, and the
party destroys the share permanently (every version, not a recoverable
soft delete) and drops it from memory. Failures are reported per party.

Start a rotation with the old parties' endpoints so this path is used:

```json
{ "oldCeremonyId": "...", "oldPartyIds": [1, 2, 3],
  "oldPartyEndpoints": ["https://party-1.custody.example.com:7000", "..."] }
```

The shared-Vault path remains only for in-cluster deployments.

## What has and has not been proven

Proven here:
- Share retirement: 3 party tests (destroys its own share from Vault and
  memory; a restore afterwards fails; the other parties' shares are
  untouched; refused without a `retire` authorisation, with a `restore`
  one, or with one for another ceremony) and 4 worker tests (every party
  asked, failures reported per party, the signed authorisation carried).
- The chart: 7 new invariants, including that the default chart *does*
  run in-cluster parties, so the "none in the cluster" check can fail.
- The isolation check against three live endpoints: passes at
  `multi-account`, fails at two accounts, fails on a duplicated host, fails
  on an unreachable party.
- The host bootstrap: rendered through Terraform's own `templatefile` and
  both generated scripts pass `bash -n` and shellcheck.

Not proven:
- **Nothing has been applied to AWS.** No account credentials here.
- **`terraform validate` has not run here.** This environment's network
  policy blocks the Terraform registry, so providers cannot be fetched;
  `terraform fmt` passes, and CI (which can reach the registry) validates
  both roots.
- Cross-region ceremony latency, and the party's behaviour behind real
  internet paths.

## Costs

Per party: one `m6i.large` (about $70/month on demand), a 20 GB encrypted
volume, two KMS keys, flow logs. A few hundred dollars a month for three.
The real cost is three operators, three on-call rotations and three sets
of recovery-key holders — which is exactly what buys the property.
