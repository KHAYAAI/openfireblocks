# Launch thesis and readiness

What this company is betting on, who it is for, and — separately and
without flattery — what is actually built.

Two halves deliberately kept apart. A thesis is an argument and can be
wrong; a readiness assessment is a set of claims that are either true in
the repository or not. Mixing them is how a founder talks themselves into
selling something that does not exist yet.

**Everything in section 2 was read out of this repository and is
checkable.** Where a claim rests on something outside it — a market, a
competitor, a buyer's behaviour — it is marked as judgement.

---

## Part 1 — The thesis

### 1.1 The one-sentence version

Every institution that wants to hold digital assets today must choose
between a custodian who holds their keys and a build project they cannot
staff; OpenFireblocks is the third option, and the thing that makes it
saleable is not the cryptography — it is that the customer can prove they
get their money back without us.

### 1.2 The bet

**Self-hosting is the product, not the deployment model.**

Fireblocks is excellent and cannot sell this. Not "does not" — *cannot*.
Their architecture puts a share in their cloud and their SGX enclaves;
that is the security model they market, and it is also the thing that
makes "you hold everything, we hold nothing" unavailable to them. Metaco
and Taurus can, which is why they are the real competitors, and both are
priced and staffed for tier-one banks.

The gap is the institution that is too big to trust a retail exchange and
too small to be interesting to Metaco. A South African bank, a regional
payments processor, a stablecoin issuer, a fintech holding float. They are
told to pick between a US custodian and a spreadsheet.

**Why this is a real gap and not wishful thinking:** the objection that
kills a small vendor in a custody deal is always the same one — *what
happens to our assets if you disappear?* A SaaS custodian answers it with
a balance sheet and an insurance policy. Self-hosting answers it with a
procedure the customer can execute themselves, and that answer is
strictly stronger from a vendor nobody has heard of. The smaller you are,
the better this argument works. That inversion is the whole thesis.

*(Judgement, not repository fact: the competitor characterisations above
are public material plus industry knowledge. Verify any specific claim
before it goes in a deck — a wrong one costs more credibility than not
making it.)*

### 1.3 Why now

- Stablecoin settlement is becoming an operational requirement rather than
  a crypto-desk experiment, and the institutions adopting it are exactly
  the ones with no custody answer.
- Regulators increasingly ask *where are the keys*, and "a vendor's cloud
  in another jurisdiction" is a worse answer every year.
- The cryptography is no longer the moat. tss-lib is open, audited in
  parts, and free. What is scarce is the surrounding system: policy,
  orchestration, audit, recovery, and the evidence that all of it works.

### 1.4 What we are actually selling

Not MPC. MPC is a library anyone can `go get`.

We sell **the part that makes a control function sign off**: fail-closed
policy on decoded transaction content, durable orchestration that cannot
lose a transfer halfway, an audit trail someone can subpoena, a dashboard
a compliance officer will actually use, and a recovery procedure that is
executed on every change rather than described in a PDF.

The licence (Elastic 2.0) means they get the source, can read it, can
escrow it, and cannot resell it as a competing service. That is the
correct shape for this: the source is the reassurance, not the giveaway.

### 1.5 The sequencing bet

**Do not chase banks first.** A bank's first call asks for SOC 2 Type II
and the honest answer is a year away. Burning six months in a procurement
process you cannot pass teaches you nothing and costs the runway.

The order that works:

1. **Design partners** (now) — two or three, paid, testnet or small
   balances, priced low enough to be a decision one person can make.
   They are buying influence over the roadmap and they are giving you the
   thing you cannot buy: a reference and a list of what actually breaks.
2. **Fintechs and payment processors** (months 3–9) — have a platform
   team, feel the custody problem acutely, buy on capability rather than
   certification.
3. **Stablecoin issuers and regional banks** (months 9+) — after the
   cryptographic audit is done and SOC 2 is underway.

Pricing is in `docs/COMMERCIAL-MODEL.md`. The short version: the floor is
$75k/year, and it is a floor rather than a starting point because
enterprise software priced under about $50k is not taken seriously by the
buyers worth having.

### 1.6 What would falsify this thesis

Stated plainly, so it can be checked rather than defended:

- **If design partners will not pay for a self-hosted deployment**, the
  thesis is wrong and the answer is a managed service, which is a
  different and worse business.
- **If the cryptographic audit finds a structural problem** in how tss-lib
  is driven, the timeline moves by months, not weeks.
- **If buyers consistently ask for SOC 2 before a pilot** rather than
  before production, the sequencing above is wrong and the company needs
  compliance spend far earlier than planned.

---

## Part 2 — Readiness

Scored against the only question that matters: **would this be negligent
to run?** Not "is it impressive".

### 2.1 The stages, and where the line is

| Stage | What it means | Status |
|---|---|---|
| **Demo** | Runs, does the thing, in front of someone | **Cleared** |
| **Testnet pilot** | A design partner drives real traffic against a testnet | **Cleared** |
| **Production, small balances** | Real money, amounts the customer can afford to lose | **Blocked on one item** — see 2.4 |
| **Production, material balances** | Real money at institutional size | **Blocked** — audit, SOC 2, hardware isolation |
| **Regulated custody** | Holding client assets under a licence | **Not in scope this year** |

### 2.2 What is built and proven

Evidence column names the thing that would go red if the claim stopped
being true.

| Capability | Evidence |
|---|---|
| Threshold ECDSA and EdDSA, key never reconstructed | 85 Go tests in `services/mpc-party`; real DKG over HTTP between separate processes |
| **Proactive key refresh** | `tss_resharing.go`; refresh, then sign, then verify against the *original* address |
| **Recovery from total loss** | `infrastructure/local/recovery-drill-local.sh` — 3 real processes, real Vault, `kill -9`, restore, sign, verify. Run 6/6 green |
| Recovery against a deployed cluster | `infrastructure/kind/recovery-drill.sh`, wired into `.github/workflows/e2e.yml` |
| Shares survive a restart usefully | Ceremony context sealed with the share; `POST /tss/keygen/restore` |
| Sender binding between parties | `peer_identity.go`; a party cannot address a message as another |
| mTLS between parties, per-pod certs from Vault PKI | `infrastructure/kind/values-kind.yaml`, `mtls.go` |
| **Ceremony co-signing**, end to end | `mpc-party/authorizer.go` verifies; `temporal-worker/.../ceremony_authorization.go` signs; one golden vector pins the wire format across both modules; **on in `values-kind.yaml`, so every e2e drill exercises it** |
| **Live OFAC sanctions feed** | `policy-service/cmd/ofac-sync`; the service refuses to evaluate once the list passes `denyAfter`, and warns before that |
| **Payment collection** | `billing/collect.go`; invoices were raised on a schedule and never charged. Idempotent, bounded, and every attempt leaves a row |
| Fail-closed policy on *decoded* ERC-20 content | `erc20.ts` + `token_limits.rego`; 18 Go tests in `policy-service` |
| Durable settlement orchestration | 92 Go tests in `services/temporal-worker` |
| Signing and broadcast, legacy and EIP-1559 | 128 Go tests in `services/mpc-signer` |
| Multi-tenant API, RLS, audit trail | 214 specs in `services/api-gateway` |
| Compliance dashboard | `src/dashboard/*` — the thing that makes this usable by someone who will not use curl |
| No copyleft dependency | `go mod why` gate in CI across four modules |
| Licensed to sell | Root `LICENSE` (Elastic 2.0), `NOTICE`, `docs/LICENSING.md` |
| 9 named drills plus smoke and isolation checks | `infrastructure/kind/*.sh` (13 scripts), `infrastructure/local/*.sh` |

### 2.3 What was found by building the drills

Worth its own section, because it is the argument for drills in general
and because none of it was visible from reading the code.

- **A refresh made a key unrecoverable.** Re-sealing after a proactive
  refresh wrote the share without its ceremony context, stripping what the
  DKG had sealed. The key became unrecoverable at the exact moment it was
  made more secure. Found by checking one sentence in the recovery doc.
- **`LoadKeyShare` returned a silently empty share**, and had since shares
  were first tagged by curve. Its only test needed a real Vault, so it
  skipped everywhere and had never once run.
- **A protocol message arriving before its ceremony was started got a 500,
  which the sender does not retry.** The orchestrator starts parties in
  sequence, so the first party's round-1 message can beat the last party's
  start request — and the ceremony then hangs until it times out. Present
  on all three paths: keygen, signing and refresh. Found by running the
  local drill repeatedly and watching it fail about one run in three.

Three real defects, all in the recovery and liveness path, all found in one
sitting by insisting the claims be executed rather than described.

And one about the drill itself, recorded because it cuts the other way. The
drill's first verifier was a hand-written Ed25519 implementation, and it
**rejected roughly one valid signature in five** — reporting that recovery
had failed when it had not. A test that is wrong in the failing direction
is the more expensive kind: it costs an investigation every time, and it
trains people to re-run rather than look. The verifier is now
`infrastructure/local/verify`, which is twenty lines around
`crypto/ed25519`.

### 2.4 What blocks production with real money

**One item, and it is not cryptography:**

1. **Party isolation is simulated.** `party-isolation-check.sh` reports the
   level and on kind it always says `simulated` — the parties share a
   kernel. A threshold is only worth what the independence of its parties
   is worth, and three processes on one host is a 1-of-1 key wearing a
   costume. This needs separate hosts, and for custody, separate cloud
   accounts. It is deployment work, not research.

### 2.5 What blocks material balances

2. **No external cryptographic review.** `docs/security/TSS-LIB-ADVISORY-REVIEW.md`
   answers six of seven published weakness classes with file and line out
   of the pinned tree — which establishes the defences are present and
   invoked, and *not* that they are correct or that nothing has been
   disclosed since v2.0.0. That is a cryptographer's job. Budget
   $40k–$80k; it has the longest lead time of anything on this list and
   should be commissioned before it is needed.
3. **No hardware isolation.** Shares live in process memory and are sealed
   in Vault at rest. Host root reads a live share. This is what PKCS#11
   support is eventually for.
4. **SOC 2 Type II.** A year from first control, and the reason banks are
   third in the sequence rather than first.

### 2.6 What is half-built, stated as such

- **The kind recovery drill has never executed.** Its first real run will
  be in CI. Attempting it locally got as far as a four-node cluster
  image and then failed on `runc`: this build environment mounts
  `/sys/fs/cgroup` as tmpfs rather than cgroup2, so no nested container
  runtime can start. That is a property of the sandbox, not of the
  drill. What *is* proven: the same recovery procedure passes repeatedly
  against real processes and a real Vault
  (`infrastructure/local/recovery-drill-local.sh`), the authorisation the
  drill sends is accepted by the party's real verifier, the request body
  it builds decodes into the handler's type, and every drill script is
  shellcheck-clean — which is where two of the last three drill bugs
  were.
- **The OFAC sync has never reached Treasury.** `ofac.treas.gov` is
  denied by this environment's egress policy, and going around a policy
  denial to fetch the same data from a mirror would be the wrong kind of
  resourceful. The parser is proven against a fixture in the real SDN
  schema, and the tool is proven end-to-end over HTTP against a local
  server. `ofac-sync --check --file SDN.XML` is the one-second check an
  operator runs against a real download, in any environment, with no
  credentials and nothing written.
- **Payment collection has never charged a real card.** Three layers now,
  and it is worth being precise about which answers what. The decision
  logic — what gets marked paid, what gets retried, what a human must
  chase — is covered by unit tests. Every parameter we send is checked
  against **Stripe's own published OpenAPI spec** in CI, which catches the
  failure mode that otherwise reaches a customer: Stripe ignores unknown
  parameters rather than rejecting them, so a one-character typo in
  `currency` silently charges in the account default. Whether Stripe
  *accepts a charge* still needs `stripe_live_test.go` and a test-mode
  key.
- **`docs/PHASE3-BACKUP-RECOVERY-PROCEDURES.md` is partly a target
  design**, and says so in its own banner. Key recovery is real and
  drilled; the surrounding infrastructure backup story is not all built.

### 2.7 The honest sentence

> Real threshold signing on two curves, with proactive refresh, sender
> binding, mutual TLS, fail-closed policy over decoded transaction
> content, durable orchestration, a full audit trail, and a recovery
> procedure that is executed against real processes and a real Vault
> rather than described. Suitable today for testnet pilots and paid design
> partnerships. Not yet cleared to hold material customer funds: the
> parties are not yet on isolated hosts, and the cryptographic review has
> not been commissioned.

Every clause is checkable in this repository. Do not add one that is not.

### 2.8 The next three things, in order

1. **Put the three parties on separate hosts.** It is the only thing
   between here and real money, it is deployment work rather than
   research, and it converts "simulated" into a claim that can be
   defended.
2. **Commission the cryptographic review.** Longest lead time on the list,
   costs nothing to start, and the first question a serious buyer asks.
3. **Sign two design partners.** Not for the revenue. For the reference
   and for the list of things that break when someone who did not build it
   tries to use it.

The cheap items that used to sit above these — the authoriser wired into
the chart, the OFAC feed, payment collection — are done. What they cost
was a week; what they bought was that three of the sentences in section
2.2 stopped being aspirational. Note the pattern: each one turned up a
defect that only appeared when the thing was actually connected (a Secret
mode the container could not read, an invoice marked paid on a payment
that had not settled, a body shape the party answered 400 to). None of
them were visible from reading the code.
