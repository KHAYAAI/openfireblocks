# Competitive analysis

Component by component, against the vendors a buyer will actually name.

**On accuracy.** Everything about OpenFireblocks in this document was read
out of this repository and is checkable. Everything about a competitor is
assembled from public material and industry knowledge, is not always
current, and in the case of pricing is mostly not published at all.
Verify any specific claim before it goes in a deck or an RFP response —
a competitor detail that turns out to be wrong costs more credibility
than not making the claim.

---

## 1. The competitive set is not the one you think

The instinct is to benchmark against Fireblocks. That is the name a buyer
says, and it is the wrong primary comparison, because **Fireblocks does
not sell what you sell.**

Fireblocks is SaaS. Their MPC shares run in their infrastructure, in Intel
SGX enclaves, under their operational control. A bank that wants the key
material inside its own perimeter cannot buy Fireblocks and get that,
whatever the commercial terms.

The vendors who actually compete for a self-hosted deal:

| Vendor | Model | Position |
|---|---|---|
| **Metaco** (acquired by Ripple, ~$250M, 2023) | On-premises / customer-hosted orchestration | The incumbent for tier-1 banks. Citi, BNP Paribas, Société Générale, Zodia. **Your most direct competitor.** |
| **Taurus** (Swiss, ~$65M Series B led by Credit Suisse) | On-prem or hosted | Deutsche Bank, Arab Bank, Swissquote. Strong in European regulated institutions. |
| **Safeheron** | Open-source MPC, self-hostable | Open-source TSS. Closest thing to a free alternative to you. |
| **fystack/mpcium** | Open-source MPC node (Apache-2.0) | Same tss-lib core as you. Infrastructure, not a product. |
| **Fireblocks / Copper / BitGo / Anchorage** | SaaS or qualified custodian | Lose to them on features; they cannot follow you on-prem. |

**What this changes.** In a deal you are not arguing "we are a cheaper
Fireblocks" — a losing argument, because you are not. You are arguing
"Metaco costs seven figures and came with Ripple attached; Taurus is a
Swiss vendor with a Swiss support model; we are the same architecture at a
tenth the price, and you can read the source." That is winnable.

It also tells you where you lose: a buyer who is happy with SaaS is not
your buyer, and the sales cycle spent convincing them otherwise is wasted.

---

## 2. Cryptographic core

Where the deepest real gap is, and the one an auditor will find first.

| | **OpenFireblocks** | **Fireblocks** | **Metaco / Taurus** | **mpcium** |
|---|---|---|---|---|
| Library | bnb-chain/tss-lib v2.0.0 (MIT) | In-house | In-house / HSM-backed | bnb-chain/tss-lib |
| ECDSA protocol | GG18/GG20 family | **MPC-CMP** (CCS 2020, peer-reviewed) | Proprietary; Taurus publishes research | GG18/GG20 |
| EdDSA | tss-lib `eddsa` — **verified against `crypto/ed25519`** | Yes | Yes | Yes |
| Signing rounds | Multi-round | **One round** with preprocessing | Varies | Multi-round |
| **Proactive key refresh** | **No — `resharing` ships in tss-lib and is not wired** | **Yes** | Yes | Partial |
| Share isolation | OS processes, Vault-sealed | **Intel SGX enclaves** | **HSM** (often FIPS 140-2 L3) | Processes, age-encrypted Badger |
| Peer authentication | mTLS **+ certificate-bound sender** (new) | Internal | Internal | **Per-message Ed25519 signatures** |
| Independent audit | **None** | Multiple, published | Multiple; regulated audits | None published |

### The three findings that matter

**1. No proactive key refresh. This is the biggest technical gap.**

`ecdsa/resharing` and `eddsa/resharing` ship in tss-lib v2.0.0 and are used
nowhere in this repository. Without periodic resharing, share compromise is
*cumulative*: an attacker who obtains one share in January and another in
June has two shares, and at 2-of-3 that is the key. Proactive security —
re-randomising shares on a schedule so that shares from different epochs
cannot be combined — is precisely the property that defeats that adversary,
and it is a named feature of MPC-CMP.

A cryptographic auditor will raise this in week one. It is buildable:
tss-lib does the protocol, the work is the ceremony orchestration, which
already exists for DKG. **Estimate: 2–3 weeks.** Do it before the audit,
not after, so the report does not lead with it.

**2. GG18/GG20 has a disclosure history that MPC-CMP does not.**

The GG18/GG20 family has had real vulnerabilities found in real
implementations — the 2023 disclosures against multiple tss-lib-derived
libraries are the well-known examples. Being on tss-lib v2.0.0 is not by
itself a problem; being on it *without having checked which advisories
apply to that version* is.

**Before the audit:** confirm v2.0.0 carries the fixes for every published
tss-lib advisory, and write down which ones and where. A buyer's security
team will ask, and "we use Binance's library" is not the answer. This is a
day's work and it removes a whole category of objection.

**3. No hardware root of trust.**

Fireblocks runs shares in SGX; Metaco and Taurus lean on HSMs, often
FIPS 140-2 Level 3. OpenFireblocks holds shares in ordinary processes with
Vault sealing them at rest. A host-root compromise reads a share out of
memory.

This is what party isolation is *for* — three separated hosts mean root on
one host yields one share — which is why `party-isolation-check.sh`
reporting `simulated` is a commercial problem and not just a technical
one. **PKCS#11 support so shares can live in an HSM is the single
highest-value hardware item**, and it is what lets you answer the
FIPS question at all.

### What is genuinely strong

- **The Ed25519 path is verified against `crypto/ed25519`** — the same code
  a Solana validator runs — not merely against itself. Most vendors cannot
  show you that test.
- **Sender binding.** A relayed protocol message is now attributed to the
  common name of the client certificate the sender presented, so one party
  cannot speak as another. This was a real hole, found by reading mpcium,
  and it is the kind of thing an audit finds.
- **Determinism.** Signing is RFC 6979, and the encoding path is pinned
  byte-for-byte to golden vectors. Two signatures over the same authorised
  transaction are identical, so a duplicate is visible as a duplicate.

---

## 3. Key lifecycle

| | **OpenFireblocks** | **Fireblocks** | **Metaco / Taurus** |
|---|---|---|---|
| DKG | Real, over the network, Temporal-orchestrated | Yes | Yes |
| Threshold | Configurable k-of-n, 2-of-3 default | Configurable | Configurable |
| Share storage | Vault (sealed), Postgres metadata | SGX + their infra | HSM |
| **Key refresh** | **No** | Yes | Yes |
| **Backup / disaster recovery** | `services/backup` exists; **no documented customer-facing recovery drill** | Documented, tested | Documented, regulated |
| Key rotation / migration | Sweep workflow exists (`balance_migration`) | Yes | Yes |
| **Break-glass recovery** | **Not documented** | Yes | Yes |

**The gap that will kill a bank deal:** a bank's risk committee asks "if
your company disappears, how do we get our money out?" Fireblocks answers
with a documented recovery procedure and key escrow. Metaco answers that
the bank holds the HSMs.

You need a **documented, drilled, customer-executable recovery procedure**
that works with the vendor gone. You are better placed than anyone to
answer it — they hold the shares and the source — but it must be written
down and rehearsed, not implied. **Estimate: 1–2 weeks to write and drill.**
Cheap, and it converts your structural advantage into an answer.

---

## 4. Policy and transaction controls

Your strongest technical area relative to price.

| | **OpenFireblocks** | **Fireblocks (TAP)** | **Metaco / Taurus** |
|---|---|---|---|
| Engine | OPA/Rego, embedded in the binary | Proprietary rules | Proprietary |
| Fail mode | **Fail-closed, tested** | Fail-closed | Fail-closed |
| Amount limits | Per tier, per peg currency, arbitrary precision | Yes | Yes |
| Counterparty whitelist | Yes, on the **decoded** recipient | Yes | Yes |
| **Calldata decoding before policy** | **Yes — refuses what it cannot decode** | Yes | Varies |
| Allowance (`approve`) handling | Escalated; unlimited approval **denied** | Yes | Varies |
| Geographic / sanctions | Blocked countries, static sanctions list | **Live screening, Chainalysis/Elliptic integrated** | Integrated |
| **Multi-party approval workflow** | **Rules emit `requiresApproval`; no approver UI or quorum** | **Yes, with mobile approvals** | Yes |
| Travel Rule | Thresholds recorded; **no transmission** | **Notabene/Sygna integrated** | Integrated |

**What you can defend in a room:** the policy set is embedded in the
binary and auditable, calldata is decoded before evaluation and undecodable
calldata is refused, and an unlimited ERC-20 approval is denied outright.
That last one is a control most platforms do not have, and it is the
single most common way a token balance is drained.

**What you cannot:** there is no approval workflow. The policy engine
returns `requiresApproval` and nothing consumes it. For a bank, dual
control is not a feature, it is the reason the control function signs off.
**This is the second-most-valuable build after key refresh — roughly 3
weeks** for a quorum model, an approver role and the dashboard screens.

---

## 5. Compliance

| | **OpenFireblocks** | **Fireblocks** | **Metaco / Taurus** |
|---|---|---|---|
| Threshold aggregation | **Per currency, per day, USD + ZAR** | Via partners | Via partners |
| Stablecoin-aware | **Yes — pegged assets valued at peg** | Yes | Yes |
| Names what the totals exclude | **Yes — undecoded and unvalued surfaced** | Unclear | Unclear |
| **South African FIC** | **Built in** | Not specifically | Not specifically |
| Filing submission | **Drafts only, manual** | Partner | Partner |
| Travel Rule transmission | **No** | Yes | Yes |
| Live AML screening | Static list | **Chainalysis / Elliptic** | Integrated |

**This is your sharpest differentiator in South Africa and your weakest
area everywhere else.** Nobody else has built FIC thresholds and rand
aggregation. Equally, a European or US buyer will expect Chainalysis in
the box, and you have a static JSON file.

The right move is not to build screening. It is to **integrate
Chainalysis or Elliptic** — both sell to platforms, both are what buyers
already expect — and keep building the rand-specific logic nobody else
will. **Estimate: 2 weeks for a screening integration.**

---

## 6. Chain coverage

The comparison you lose most badly, and the one that matters least for
your wedge.

| | **OpenFireblocks** | **Fireblocks** | **BitGo** |
|---|---|---|---|
| Chains that can transact | **2 families** (Bitcoin, EVM) | 100+ | 100+ |
| Tokens | Any registered ERC-20, **verified on-chain** | Thousands | Thousands |
| Solana | **MPC keys only — cannot transact** | Full | Full |
| Cosmos | Keys only | Yes | Yes |
| Staking | **No** | Yes | Yes |
| DeFi / WalletConnect | **No** | Yes | Limited |
| NFTs | **No** | Yes | Yes |

**Do not fight this.** "We support two chains properly" loses to "we
support a hundred" in every feature-matrix comparison — so refuse the
feature matrix. A stablecoin settlement customer needs USDC and USDT on
one or two EVM chains, and possibly rand tokens. That is the conversation
to have.

Where it does hurt: **Tron carries a large share of real USDT settlement
volume**, particularly in emerging markets, and it is not in the codebase
at all. For a cross-border payments buyer that is a live objection.
**Estimate: 2–3 weeks.** It is the one chain worth adding for the wedge.

---

## 7. Operations and assurance

| | **OpenFireblocks** | **Fireblocks** | **Metaco / Taurus** |
|---|---|---|---|
| SOC 2 Type II | **No** — evidence collection automated | Yes | Yes |
| ISO 27001 | No | Yes | Yes |
| CCSS | No | Level 3 | — |
| **Cryptographic audit** | **None** | Multiple, published | Multiple |
| Penetration test | No | Yes | Yes |
| **Insurance** | **None** | Up to ~$30M+ (varies) | Varies |
| Qualified custodian status | N/A (you are not a custodian) | N/A | Some entities |
| Uptime SLA | None offered | 99.9%+ | Contractual |
| HA / DR | Postgres replication, Vault Raft, 4-node kind | Multi-region | Multi-site |
| Observability | Prometheus metrics, audit trail (immudb + Postgres) | Full | Full |
| **E2E drills in CI** | **Yes — on a real cluster, real chains** | Internal | Internal |

**The insurance line is worth understanding rather than fixing.** Fireblocks
carries insurance because they hold keys — the policy covers *their*
failure. Self-hosted, the customer holds the keys, so their own crime and
cyber policy is what responds. That is not a weakness to apologise for; it
is a different risk allocation, and for many institutions a better one,
because it removes a counterparty rather than insuring one.

**Continuous drills on a real cluster are a genuine differentiator** and
almost nobody demos them. Fireblocks cannot show a prospect their internal
test suite. You can show CI standing up a four-node cluster, running a real
DKG across three processes, spending real Bitcoin on regtest, deploying a
real ERC-20 and proving the policy engine refuses an over-limit transfer.
**Put this in the demo.** It is the most credible thing you have while the
audit is pending.

---

## 8. Developer and user surface

| | **OpenFireblocks** | **Fireblocks** | **Turnkey / Dfns** |
|---|---|---|---|
| REST API | Yes, OpenAPI at `/docs` | Yes, mature | Yes |
| SDKs | JS, Go, Python (thin) | Many, maintained | Many |
| **Web console** | **Yes (new)** — read-only, 5 pages | Full, mature | Yes |
| Mobile approvals | **No** | **Yes** | Varies |
| Webhooks | Yes, signed, retried | Yes | Yes |
| Sandbox / testnet | Yes | Yes | Yes |
| Docs | In-repo, thorough; **no public site** | Extensive public | Good |

The dashboard closes the disqualifying gap. It does not close the
*competitive* gap — Fireblocks' console is years of work — but it moves you
from "cannot be evaluated by a non-engineer" to "can".

**Mobile approval is the remaining piece a bank will ask for**, and it
pairs with the approval workflow above: dual control is not real if the
second approver has to open a laptop.

---

## 9. Commercial comparison

Pricing is largely unpublished. Treat these as order-of-magnitude.

| | **OpenFireblocks** | **Fireblocks** | **Metaco / Taurus** | **BitGo** |
|---|---|---|---|---|
| Model | Annual licence, self-hosted | SaaS subscription + volume | Licence + services | bps on AUC |
| Entry | **$75k** | ~$30–100k | **High six / seven figures** | Minimums + bps |
| Institutional | $180k | $250k–$1M+ | Seven figures | bps |
| Metering | Keys + signatures | Workspaces, volume, AUC | Negotiated | AUC |
| Implementation | $30–90k | Included / partner | **Six figures, months** | Onboarding |
| Time to production | Weeks | Days | **6–18 months** | Weeks |

### Where you win commercially

1. **Price against Metaco, not Fireblocks.** You are roughly a tenth of a
   Metaco deal for the same architectural promise. That is the arbitrage.
2. **Deployment speed.** A Metaco or Taurus implementation is a
   multi-quarter programme. Weeks is a real differentiator to a buyer with
   a deadline.
3. **No AUC coupling.** A custodian's bps scale with the customer's balance
   sheet forever. A licence does not. At $50m of assets, 20bps is $100k a
   year — more than Foundation, permanently, and they hold the keys.
4. **Source access.** For a custody platform whose entire claim is that the
   private key never exists, "read the code and check" is a sales asset no
   SaaS vendor can match.

### Where you lose commercially

1. **No audit, no SOC 2, no insurance.** Three questions, three bad answers.
   The audit fixes one of them for $40–80k.
2. **Vendor risk.** A small company holding up a bank's custody
   infrastructure. Source escrow and a documented recovery procedure are
   the answers, and both are cheap.
3. **No reference customers.** Solved only by the first three.
4. **Support model.** A named engineer against a 24/7 global desk. Price
   accordingly and do not over-promise an SLA you cannot staff.

---

## 10. What mpcium teaches

`fystack/mpcium` (Apache-2.0) shares your cryptographic core — tss-lib,
ECDSA plus EdDSA, self-hosted. It is infrastructure rather than a product:
no policy engine, no compliance, no billing, no dashboard, no multi-tenancy.
You are not competing with it; you are a platform and it is a node.

Its architecture is better than yours in four specific places:

| What it does | What you do | Verdict |
|---|---|---|
| **Per-message Ed25519 signatures** between nodes | mTLS + certificate-bound sender | **Now closed**, and theirs is still stronger — message signatures survive a terminating proxy |
| **NATS** message bus | HTTP relay | Theirs is better under partition and retry. Not urgent |
| **Consul** service discovery and health | Endpoint template + HTTP probes | Theirs handles dynamic membership. Not urgent |
| **Optional authorizer signatures** (Ed25519/P256, **AWS KMS**) gating high-risk operations | Policy engine only | **Worth stealing.** A KMS-held key that must co-sign a ceremony is a hardware second factor at the MPC layer, without an HSM |
| **age-encrypted share backups** with a defined restore path | Vault sealing, no documented restore | **Worth stealing** — see the recovery gap above |

**The one to take now** is the authorizer concept. It gives you a hardware
root of trust story using cloud KMS rather than an HSM, which is a
materially better answer to "what stops a compromised host signing" than
you have today, and it is weeks rather than quarters.

---

## 11. The honest scorecard

| Dimension | Score | Note |
|---|---|---|
| Self-hosted architecture | **9/10** | Genuinely differentiated; the reason to buy |
| Policy engine | **8/10** | Embedded, auditable, calldata-aware. Missing approvals |
| Stablecoin support | **8/10** | Registry with on-chain verification is better than most |
| ZAR / FIC compliance | **9/10** | Nobody else has it |
| Test and proof culture | **9/10** | Real drills on real chains; almost nobody demos this |
| Cryptographic core | **5/10** | tss-lib is sound; **no refresh, no audit, no hardware** |
| Key lifecycle | **4/10** | No refresh, no documented recovery |
| Chain coverage | **3/10** | Two families. No Tron, no Solana transactions |
| Approval workflow | **2/10** | Signalled, not implemented |
| Console | **5/10** | Exists now, read-only |
| Assurance | **1/10** | No audit, no SOC 2, no insurance |
| **Overall vs Metaco/Taurus** | **~5/10 at 1/10 the price** | A real deal for the right buyer |
| **Overall vs Fireblocks** | **Not comparable** | Different product; refuse the comparison |

---

## 12. What to build, in order

Ranked by deal impact per week of work.

| # | Item | Effort | Why |
|---|---|---|---|
| 1 | **Commission the cryptographic audit** | $40–80k, 4–8 wks | Not code. Longest lead time. Blocks everything |
| 2 | **Proactive key refresh** (tss-lib resharing) | 2–3 wks | Biggest technical gap; an auditor leads with it |
| 3 | **Documented, drilled recovery procedure** | 1–2 wks | Answers the question that kills bank deals |
| 4 | **Approval workflow + quorum** | ~3 wks | Dual control is why a control function signs off |
| 5 | **Party isolation to `multi-account`** | 2 wks | Makes the threshold claim true, not just asserted |
| 6 | **tss-lib advisory review, written down** | 1 day | Removes a whole objection category |
| 7 | **Chainalysis or Elliptic integration** | 2 wks | Table stakes outside South Africa |
| 8 | **Authorizer co-signing (KMS)** — from mpcium | 2 wks | Hardware root of trust without an HSM |
| 9 | **Tron** | 2–3 wks | Where real USDT settlement volume is |
| 10 | **Solana transaction route** | 1 wk | Finishes a claim already half-made |
| 11 | PKCS#11 / HSM support | 4+ wks | Needed for FIPS conversations, not before |
| 12 | Mobile approvals | 4 wks | Pairs with #4 for a bank |

Items 1–6 are roughly one quarter and move you from "interesting" to
"defensible in a security review". That is the whole game.

---

## 13. The positioning, in one paragraph

> OpenFireblocks is self-hosted MPC custody and stablecoin settlement. The
> private key is never assembled — not on our servers, because there are no
> our servers. It is the Metaco architecture at a tenth of the price and a
> tenth of the implementation time, with source access, and with South
> African rand stablecoin settlement and FIC threshold reporting that no
> other vendor has built. It is not audited yet, it does not hold your
> keys, and it is not insured — because you hold the keys, and your own
> policy responds.

Every clause there is defensible today. Do not add one that is not.
