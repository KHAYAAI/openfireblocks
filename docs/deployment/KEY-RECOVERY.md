# Key recovery

How to get money out of this platform without the platform, without the
vendor, and without any single machine that is still running.

**Read this before you buy, not after.** A bank's risk committee asks one
question about a small vendor holding custody infrastructure: *what happens
to our assets if you disappear?* Self-hosting is the strongest possible
answer to that question — you hold the shares, you hold the source, and
nothing in this system ever contacts the vendor — but an answer that is
implied rather than written down and rehearsed is not an answer a control
function can accept.

This document is the procedure, and two drills execute it rather than
describe it. `infrastructure/local/recovery-drill-local.sh` runs on every
push against real processes and a real Vault;
`infrastructure/kind/recovery-drill.sh` runs the same procedure against a
deployed cluster. Both destroy every party and require the recovered
committee to sign for the original address.

---

## 1. What you are recovering from

Four scenarios, in increasing order of how bad the day is. Only the last
one needs this document.

| Scenario | What to do |
|---|---|
| One party is down | Nothing. A 2-of-3 key signs with the other two. Rebuild the party and let it rejoin |
| A party's host is lost permanently | Provision a replacement, restore its share from Vault, verify it can sign |
| The whole cluster is lost, Vault survives | Redeploy from the chart; shares are sealed in Vault and come back with the pods |
| **Vault is lost, or the vendor is gone, or both** | **This document** |

The first three are operations. The fourth is the one a risk committee
cares about, and the one most vendors answer with "contact support".

## 2. What you must be holding before you need it

If you have not done this, the procedure below does not work. Do it on day
one of the deployment, not on the day you need it.

| Artefact | Where it must live | Why |
|---|---|---|
| **Vault unseal keys / recovery keys** | Split across officers, offline, geographically separated | Without these the sealed shares are ciphertext |
| **Vault's storage backend** | Backed up on your own schedule, to your own storage | Raft snapshots. `services/backup` produces them; you must retain them |
| **A copy of the source at the deployed version** | Your own git remote or an escrow agent | ELv2 grants you this. Exercise it |
| **The chart values you deployed with** | Configuration management | Party count, threshold, chain ids, token registry |
| **The key inventory** | Postgres backup, or exported | Which key ids exist, their addresses, their chains |

**None of these come from the vendor.** That is the point. Check annually
that you still hold all five and that the people who hold the unseal keys
still work there.

## 3. The procedure

### Step 0 — establish what you have

You need, at minimum, **`threshold + 1` of the sealed key shares** for the
key you are recovering, plus the means to decrypt them. For a 2-of-3 key
that is two shares. A single share is useless by construction, which is
the same property that protects you on a normal day.

### Step 1 — restore Vault

```bash
# From your own Raft snapshot.
vault operator raft snapshot restore backup.snap
# Unseal with the officer-held keys.
vault operator unseal   # repeated to the configured threshold
```

If Vault is unrecoverable, you need the shares from wherever else you
replicated them. If you replicated them nowhere, recovery ends here — which
is why section 2 is not optional.

### Step 2 — read the shares out

Shares are sealed per party, keyed by ceremony id, at the path
`<mount>/openfireblocks/mpc-party/party-<N>/<ceremony-id>` — the layout is
built in `vaultShareConfigFromEnv` in `services/mpc-party/vault_seal.go`,
and `VAULT_KV_MOUNT` and `VAULT_KEY_SHARE_PATH` move it.

```bash
vault kv get -format=json \
  "secret/openfireblocks/mpc-party/party-1/$CEREMONY_ID"
```

Each entry holds two things, and you need both:

- `save_data` — the share itself, a tss-lib `LocalPartySaveData`, tagged
  with its curve. It is **not** a private key and cannot be turned into one
  by itself.
- `ceremony_context` — the threshold, the party count, the curve, the
  refresh epoch and the expected address. Without it the share is a blob
  that cannot be loaded into a signing party, which is the failure the
  last row of section 5 describes.

If `ceremony_context` is absent, the share was sealed by a build that
predates it, and the key cannot be restored by Route A below. Refresh the
key (section 4) while the parties are still running — a refresh re-seals
every share with its context — and take a fresh backup afterwards. Do that
before you need it, because a refresh needs a working committee and a
recovery is what you have instead of one.

### Step 3 — reconstruct, or re-sign

Two routes, and the choice matters.

**Route A — stand the parties back up (preferred).** Redeploy from the
chart, restore each share to its party, and use the platform normally. The
private key is never assembled, so the security property you bought is
preserved through the recovery itself. This is the route the drill
exercises.

**Route B — offline reconstruction (last resort).** With `threshold + 1`
shares in one place, the private key *can* be reconstructed via Lagrange
interpolation. This is the break-glass path: it produces a single private
key, on one machine, in memory.

Understand what Route B costs. The moment it completes, the guarantee that
no private key exists anywhere is gone, permanently, for that key. Anyone
who has ever had root on that machine is in scope. So:

- Do it on an air-gapped machine you will destroy afterwards.
- Do it with two officers present and a written record.
- **Sweep the funds to a newly generated key immediately.** The old key
  must be treated as compromised from the moment it was assembled, because
  it was.
- Never return the reconstructed key to normal operations.

Route B exists because a risk committee needs to know it exists. It should
never be used.

### Step 4 — verify before you rely on it

Recovery is not complete when the shares load. It is complete when they
sign.

There are two drills, and they prove different halves.

```bash
# Real processes, real Vault, real SIGKILL, no cluster needed. Runs on
# every push (.github/workflows/ci.yml). Proves the recovery logic and the
# sealed material.
./infrastructure/local/recovery-drill-local.sh

# The same procedure against a deployed cluster: real pods, mTLS, Vault
# in Raft. Runs in the end-to-end workflow. Proves the deployment.
./infrastructure/kind/recovery-drill.sh

# Or rehearse against a key that already exists.
./infrastructure/kind/recovery-drill.sh \
  --ceremony-id "$CEREMONY_ID" --key-id "$KEY_ID" --api-key "$API_KEY"
```

Both destroy every party without warning -- `--force --grace-period=0` for
pods, `kill -9` for processes -- wait for the replacements, **require that
signing now fails**, restore each party from Vault, and prove the restored
committee produces a signature that verifies against the *original*
address.

The middle step is the one that matters. A drill that only showed the
parties coming back and signing would pass in the case where nothing was
ever destroyed — which is precisely the case it exists to catch. And a
recovery that produces a different address has recovered nothing.

The restore is an explicit operator action, not something a party does on
startup: `POST /tss/keygen/restore` with the ceremony id and the peer map.
A party that restarts does not silently rearm itself with a key, because
deciding that a key should be live again is a decision, not a side effect
of a pod rescheduling.

## 4. Recovering after a proactive refresh

Shares carry a refresh epoch (`services/mpc-party/tss_resharing.go`). A
refresh re-evaluates the secret at new coordinates, so **shares from
different epochs cannot be combined** — that is the security property, and
it is also a recovery hazard.

Recover `threshold + 1` shares **from the same epoch**. A backup taken
before a refresh and one taken after are not interchangeable, and mixing
them produces an interpolation that yields a key controlling nothing.

Where the platform helps, and where it does not. A refresh re-seals each
share at the same path **with its new epoch in the ceremony context**, so
Vault's current state is always internally consistent: restore from it and
every party comes back on the same epoch. That is covered by
`TestAKeyCanStillBeRecoveredAfterARefresh`, which refreshes a key,
destroys every party, restores from what the refresh wrote, and requires
the signature to verify against the original address.

What the platform cannot fix is your *snapshots*. A Raft snapshot is a
point in time, and two snapshots taken either side of a refresh hold
different epochs. Restoring party 1 from one and party 2 from the other
gives you two shares that do not interpolate, and the symptom is the first
row of section 5 — a different address, and nothing to say why.

Practically: take a fresh backup immediately after every refresh, and
retain the previous one until the new backup is verified. The refresh
schedule and the backup schedule are the same schedule.

## 5. What can go wrong, and what it means

| Symptom | Cause | What to do |
|---|---|---|
| Restored shares produce a different address | Mixed epochs, or shares from different ceremonies | Re-read section 4. Do not broadcast anything |
| Fewer than `threshold + 1` shares recoverable | Backups were not independent | The key is unrecoverable. Funds at that address are lost |
| Shares decrypt but signing fails | Committee constructed with the wrong party ids | Check the epoch; see `committeeKey` |
| Vault unseals but shares are absent | Ceremony completed without `VAULT_ADDR` set | The shares were only ever in memory. Check your deployment |

The second row is worth dwelling on. Backups of three shares taken by one
process to one bucket are **one** backup. Independence is the property that
makes `threshold + 1` mean something, and it is the easiest one to lose
while believing you have it.

## 6. What to tell a risk committee

> The shares live on our infrastructure, sealed in our Vault, backed up on
> our schedule to our storage. We hold the source under a perpetual
> licence. The recovery procedure is documented, it is executed by scripts
> we can run on demand, and those scripts run in the vendor's CI on every
> change — one against real processes and a real Vault, one against a
> deployed cluster. Each destroys every signing party and requires the
> recovered committee to produce a signature that verifies against the
> original address. No step of it contacts the vendor. We have rehearsed
> it ourselves on ‹date›.

Every clause is checkable and none of it depends on the vendor existing.
That is the argument self-hosting lets you make and a SaaS custodian
cannot.

**Rehearse it annually.** An untested recovery procedure is a document, not
a control, and the first time you find out which of section 2's five
artefacts you are missing should not be the day you need all five.
