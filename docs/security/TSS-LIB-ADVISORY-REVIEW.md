# tss-lib: dependency review

The threshold signing library this platform's security rests on, what
version is pinned, and which classes of published weakness a reviewer must
confirm it carries fixes for.

**Status: partly complete, and the boundary is stated rather than blurred.**
Everything here was read out of the pinned module cache and is checkable
line by line — including section 5, where six of seven classes of known
weakness are answered with the file and line that answers them.

What this is **not** is a security review. Reading a tree establishes that
a defence is present and invoked. It does not establish that the defence
is correct, and it cannot establish anything about weaknesses disclosed
after v2.0.0 was cut, because that information is not in the tree. Those
two gaps are named in section 5 and they are what the cryptographic audit
is for.

Why it exists at all: "we use Binance's library" is not an answer to a
buyer's security team, and an auditor who has to establish the contents of
section 5 themselves will bill for it and lead their report with it.

---

## 1. What is pinned

| | |
|---|---|
| Module | `github.com/bnb-chain/tss-lib/v2` |
| Version | **v2.0.0** |
| Licence | MIT |
| `go.sum` (module) | `h1:VE2X5eWmHSH4u0UI7z87oI/99IJbfevtm3OYDZM48Eg=` |
| `go.sum` (go.mod) | `h1:7Uai3xfLjJPD2gbd0+/1gHsfqa9PKxONdwDtnt6ZYxc=` |

The hashes matter: they are what makes "v2.0.0" mean one specific tree
rather than a tag someone could move. Quote them in a security
questionnaire.

## 2. What is actually used

Nine packages, and the list is short enough to be worth stating because a
reviewer's first question is scope:

```
tss-lib/v2/common            party ids, message routing, sorted committees
tss-lib/v2/crypto            EC points, commitments
tss-lib/v2/tss               parameters, peer contexts, the Party interface
tss-lib/v2/ecdsa/keygen      secp256k1 DKG
tss-lib/v2/ecdsa/signing     secp256k1 threshold signing
tss-lib/v2/ecdsa/resharing   secp256k1 proactive refresh
tss-lib/v2/eddsa/keygen      Ed25519 DKG
tss-lib/v2/eddsa/signing     Ed25519 threshold signing
tss-lib/v2/eddsa/resharing   Ed25519 proactive refresh
```

No forks, no patches, no vendored copy. The module cache is the upstream
tree.

## 3. Protocol family

The ECDSA packages implement the **GG18/GG20 family** (Gennaro–Goldfeder).
The signing package has nine rounds, consistent with the GG20 shape.

This matters commercially as well as technically. Fireblocks' MPC-CMP is a
different protocol with a different disclosure history, and a buyer who has
read Fireblocks' marketing will ask why this platform is on GG20. The
honest answers are: it is the protocol the best-maintained open
implementation offers, it is widely deployed, and the published weaknesses
in it are implementation weaknesses with known fixes — which is exactly why
this document has to be filled in rather than waved away.

## 4. What is present in the pinned tree

Verified by reading the vendored source. These are the defences whose
*absence* is what the published attacks exploited:

| Defence | Present in v2.0.0 |
|---|---|
| `crypto/mta/range_proof.go` — range proofs on MtA | **Yes** |
| `crypto/mta/proofs.go` — Bob's proofs | **Yes** |
| `crypto/modproof` — Paillier-Blum modulus proof | **Yes** |
| `crypto/facproof` — no-small-factor proof | **Yes** |
| `ValidateBasic` on keygen messages | **Yes** — 4 implementations in `ecdsa/keygen/messages.go` |
| `ValidateBasic` on signing messages | **Yes** — 10 implementations in `ecdsa/signing/messages.go` |
| Point-on-curve checks | **Yes** — enforced in `crypto.NewECPoint`, which 25 non-test call sites construct received points through |

That the machinery is present is necessary and **not sufficient**. The
published attacks in this family turned on whether particular proofs were
*checked*, whether parameters were bound to the session, and whether
specific values were validated before use — not on whether a file existed.
Confirming that is the review below.

## 5. What a reviewer must confirm

Each row is a class of weakness publicly disclosed against threshold-ECDSA
implementations in this family. Where the answer is visible in the pinned
source, it is given with the file and line so a reviewer can check it in
minutes rather than rediscovering it.

| # | Class | Finding in v2.0.0 | Status |
|---|---|---|---|
| 1 | **Missing or unchecked MtA range proofs** | Both directions are verified, and both abort on failure. Bob verifies Alice's range proof before responding (`crypto/mta/share_protocol.go`, `BobMid`: `if !pf.Verify(...) { return error }`); Alice verifies Bob's proof before decrypting (`AliceEnd`: `if !pf.Verify(...) { return error }`). `ecdsa/signing/round_3.go` routes that error to the round's error channel, which fails the ceremony | **Present** |
| 2 | **Paillier modulus validation** | `ecdsa/keygen/round_2.go:55` rejects a peer whose Paillier modulus is not exactly `paillierBitsLen`; line 61 applies the same check to `NTilde`. v2.0.0 also carries `crypto/modproof` (Paillier-Blum) and `crypto/facproof` (no-small-factor), both invoked from `round_2.go:119-143` | **Present** |
| 3 | **Ring-Pedersen / NTilde parameter soundness** | Proven in both directions: `round_2.go:77` and `:83` call `VerifyDLNProof1`/`VerifyDLNProof2` on a peer's `h1`/`h2` over its `NTilde`, and `:58` rejects `h1 == h2` outright — the degenerate case that makes the proofs trivial | **Present** |
| 4 | **Nonce / k-value leakage through aborts** | The GG20 type-5 check is implemented: rounds 5–9 commit, decommit and then require `U == T` (`ecdsa/signing/round_9.go`, "U doesn't equal T"). A party whose values are inconsistent fails the ceremony rather than yielding a signature | **Present, not independently analysed** |
| 5 | **Session binding** | Every proof is bound to a session id. `getSSID()` (`ecdsa/keygen/rounds.go:102`, `ecdsa/signing/rounds.go:130`) hashes the curve parameters, the sorted party keys, the round number and a per-ceremony nonce; the result is concatenated with the party index into the `ContextI`/`ContextJ` passed to every MtA proof and every Schnorr proof. A proof from one ceremony does not verify in another | **Present** |
| 6 | **Small-subgroup and point validation** | `crypto.NewECPoint` refuses a point that is not on the curve (`crypto/ecpoint.go`), and the rounds construct received points through it rather than by struct literal — so validation is on the constructor, not on each call site | **Present** |
| 7 | **Zero / identity share handling** | Keygen rejects `h1 == h2` and out-of-size moduli (row 3, row 2), and the derived public key is checked on the curve (`ecdsa/keygen/round_3.go:208`). A systematic sweep for zero and identity values across all message fields is **not** something reading the tree establishes | **Partially** |

### One thing worth recording, which is not a finding

`ecdsa/signing/round_9.go:36` guards the decommitment with
`if !ok && len(values) != 4`, where the equivalent check in
`round_7.go:41` uses `||`. That reads like a bug and is worth a reviewer's
attention, but it is **not exploitable in this tree**: `DeCommit()`
returns `(false, nil)` on failure (`crypto/commitments/commitment.go:64`),
so a failed decommitment always satisfies both operands and is rejected.
It is a latent robustness defect that depends on a contract two files
away, not a hole.

Recorded because a reviewer will find it and should not have to work out
whether it matters twice — and because stating it accurately is the
difference between this document and one that inflates what it found.

### What still requires the upstream advisory history

Rows 1–6 above were established by reading the pinned tree, and are
checkable by anyone with the module cache. What reading the tree **cannot**
establish:

1. Whether a weakness was disclosed after v2.0.0 was cut and fixed in a
   later release. The defences above being present says nothing about
   defences added since.
2. Whether any of these implementations is *correct*, as opposed to
   present and invoked. Row 4 in particular is marked "not independently
   analysed" deliberately: the type-5 check is there, and whether it
   closes the attack it is meant to close is a cryptographer's judgement,
   not a reader's.

So: read the upstream release notes and advisories from v2.0.0 to today,
name the release that addresses anything found, and where v2.0.0 predates
a fix, **that is the finding** — record the upgrade path and test it
against the drills in `.github/workflows/e2e.yml`. Then record the result
here with a date and a name, because a buyer's question is "who checked,
and when", and this section does not yet answer it.

## 6. What this platform adds on top

Relevant because some published attacks assume an adversary who can speak
as another party, and that assumption no longer holds here:

- **Sender binding** (`services/mpc-party/peer_identity.go`). A relayed
  protocol message is attributed to the common name of the client
  certificate the sender presented. One party cannot address a message as
  another. This was a real hole until recently; several attacks in this
  family become materially harder without it.
- **Mutual TLS** between parties, with per-pod certificates from Vault's
  PKI.
- **Ceremony authorisation** (`services/mpc-party/authorizer.go`). An
  optional second signature from a key the platform's hosts cannot reach,
  required before a party will join a ceremony at all.
- **Proactive refresh** (`services/mpc-party/tss_resharing.go`). Share
  compromise is no longer cumulative across refresh intervals.
- **Drilled recovery** (`docs/deployment/KEY-RECOVERY.md`). Every party is
  destroyed and rebuilt from sealed material in CI on every change. Not a
  protocol property, but the one a risk committee asks about first.

None of these fix a protocol weakness. They narrow who can attempt one.

## 7. What is still missing

- **No hardware isolation.** Shares are held in ordinary process memory and
  sealed in Vault at rest. A host-root compromise reads a live share. This
  is what party isolation and, eventually, PKCS#11 support are for.
- **No independent review of the integration.** tss-lib being sound does
  not make this platform's use of it sound. The way a library is driven —
  committee construction, message routing, save-data subsetting — is
  where integration bugs live, and this codebase has already found several
  of its own.

## 8. Restating the honest position

What can be written today, and is true:

> Threshold signing uses bnb-chain/tss-lib v2.0.0 (MIT), pinned by module
> hash. We have read the pinned tree against seven classes of weakness
> published against the GG18/GG20 family and record, with file and line,
> that the corresponding defences are present and invoked — MtA range
> proofs verified in both directions with abort on failure, Paillier and
> ring-Pedersen parameters validated and proven at keygen, and every proof
> bound to a per-ceremony session identifier. Party-to-party traffic is
> mutually authenticated and each protocol message is bound to the
> sender's certificate. Shares are refreshed proactively and recovery is
> drilled in CI.

What may **not** be written until someone has done the work in section 5:

> ...has been reviewed against the published advisory history.

Those are different claims. The first says the defences are in the code
and points at them. The second says someone competent checked that they
are sufficient and that nothing has been disclosed since — which is the
cryptographic audit, and it has not happened.

A buyer's security team will accept the first and will catch the second.
Say the first.
