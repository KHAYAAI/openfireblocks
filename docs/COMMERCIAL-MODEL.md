# How to sell OpenFireblocks

A packaging and pricing model for selling self-hosted licences now, while
keeping the option of running a hosted service later.

**These numbers are anchored, not researched.** They are built from
published competitor pricing, the economics of what a buyer is replacing,
and what the platform can actually meter. Test them on the first three
conversations and move them.

---

## 1. What you are actually selling

Not "MPC custody". Fireblocks sells that, has a nine-figure valuation, and
will win any comparison made on their terms.

You sell the one thing they structurally cannot:

> **The keys never leave your infrastructure. Not encrypted on our servers
> — not on our servers at all. The private key does not exist, anywhere,
> at any moment, including for us.**

That is true of this platform, it is provable by reading the source, and
it is exactly what a regulated institution's risk committee asks about
when a vendor proposes holding their keys.

The second thing you sell, which follows from the first: a South African
institution can custody and settle rand and dollar stablecoins without key
material or transaction data leaving the country, or leaving their own
cloud account. Data residency and exchange control make that a real
constraint for a real buyer, not a preference.

### The wedge

Lead with **self-hosted ZAR and USD stablecoin settlement**. Not
multi-chain custody, not MPC.

It is specific, it names an asset a South African fintech already wants to
move, and no incumbent sells it self-hosted. "Multi-chain MPC custody
platform" is a category with entrenched leaders; "the settlement rail your
auditors will actually sign off on" is a conversation.

---

## 2. Who buys it

In rough order of how quickly they close:

| Buyer | Why they buy | Deal shape |
|---|---|---|
| **Crypto-native fintechs and payment companies** | Already moving stablecoins, already paying a custodian bps, have engineers | Fastest. Annual licence, light integration |
| **Remittance and cross-border operators** | ZAR/USD settlement is the product; custody is a cost centre | Licence plus real integration work |
| **Exchanges and OTC desks** | Want treasury keys off a third party | Licence, high signature volume |
| **Banks and regulated institutions** | Data residency, no counterparty risk, board-level comfort | Slowest, largest, needs SOC 2 |
| **Other CASPs building a custody offering** | Buying the engine rather than building it | Watch the ELv2 non-compete boundary |

### Do not chase banks first

This is the sequencing mistake that costs a year, so it is worth stating
in full rather than as an aside.

A bank asks four questions on the first call, and today three of them have
bad answers:

| They ask | You say |
|---|---|
| "SOC 2 Type II?" | Not yet — the observation window has not started |
| "Who audited the cryptography?" | Nobody, yet |
| "What insurance?" | None — you hold the keys, your policy responds |
| "Show me dual control." | The policy engine flags it; no approver workflow |

None of those is fatal on its own. Together, on a first call, they end the
conversation — and worse, they end it in a way you cannot reopen. A bank
that has said no once does not re-evaluate for twelve to eighteen months,
because the internal cost of restarting a vendor review is higher than the
cost of waiting. **You get one first call per institution, and spending it
early is the most expensive mistake available to you.**

There is a second cost that is easy to miss. A bank pilot consumes an
enormous amount of engineering time in security questionnaires,
architecture reviews and procurement, all of it before any revenue. Three
months spent that way is three months not spent on the audit and the key
refresh that would have made the answers good.

**The order that works:**

1. **Crypto-native fintechs and payment companies** (now). They have
   engineers, they already pay a custodian, they can evaluate the source
   themselves, and they can sign in a quarter. They will not ask for SOC 2
   because they do not have it either.
2. **Larger fintechs and exchanges** (post-audit). The audit report is the
   artefact that makes this group possible. Reference customers from
   step 1 do the rest.
3. **Banks** (post-SOC 2, or with a fintech reference and a named
   sponsor). By then all four answers are good, and you arrive with
   production references rather than a pitch.

**The one exception worth taking:** a bank that approaches *you*, with a
named internal sponsor and a specific problem. Inbound interest with a
sponsor is a different conversation from outbound prospecting — the
sponsor absorbs the internal cost of the gaps. Even then, be the one to
volunteer the four answers on the first call. A gap you name yourself is a
roadmap item; the same gap found by their security team in month three is
a failed evaluation.

---

## 3. Packaging

Four things to sell, priced separately, because they are bought by
different budgets.

### Platform licence — annual, self-hosted

Metered on **keys and signatures**, which is what the billing service
already counts from `key_pairs` and `signing_requests`. Price on what the
software can already meter; a metric it cannot observe is a metric you
will argue about at renewal.

| | **Foundation** | **Institutional** | **Enterprise** |
|---|---|---|---|
| **Annual** | **$75,000** | **$180,000** | **from $400,000** |
| *in ZAR (~R18)* | *R1.35m* | *R3.2m* | *from R7.2m* |
| Production environments | 1 | 2 (prod + DR) | Unlimited |
| Threshold keys | 2,500 | 25,000 | Unlimited |
| Signatures / year | 250,000 | 2,500,000 | Unlimited |
| Chains | EVM + Bitcoin | All supported | All + roadmap input |
| Compliance module (FIC/FinCEN thresholds, per-currency aggregation) | — | Included | Included |
| Support | Business hours, 1 business day | 24/7, 4-hour P1 | 24/7, 1-hour P1, named engineer |
| Source escrow | — | Optional | Included |
| Overage | $0.10/signature, $20/key | $0.06/signature, $12/key | Negotiated |

**Why $75,000 is the floor and not $25,000.** Enterprise software priced
below about $50k does not get taken seriously by the buyers you want — it
lands under the threshold where procurement engages, which sounds
convenient and actually means no executive sponsor and no budget line. It
also anchors every renewal and every subsequent customer. You can always
discount a list price; you cannot raise one.

**The comparison to make in the room:** a custodian charging 20bps on $50m
of assets costs $100,000 a year, forever, and they hold the keys.
Foundation is $75,000, you hold the keys, and the fee does not scale with
your balance sheet. That framing wins on both axes a treasurer cares
about.

### Implementation — one-time

| | |
|---|---|
| Standard deployment (single region, one chain family) | **$30,000** |
| HA deployment, separated signing parties, HSM-backed Vault | **$60,000** |
| Custom integration (core banking, ledger, ERP) | **$90,000+**, scoped |

Charge for it. Free implementation teaches a customer that your
engineering time is worth nothing, and this deployment genuinely needs
three separated hosts and a Vault cluster — it is not a Helm install.

### Support and SLA — annual, on top of licence

Bundled above. Sell uplifts separately: a dedicated Slack channel, a
quarterly architecture review, a named TAM.

### Managed service — later

The ELv2 licence reserves this to you and nobody else. Price it at roughly
**2.5× the equivalent self-hosted tier** when you launch it, because you
are then carrying the infrastructure, the operational risk, and — the
expensive part — the regulatory posture of a custodian.

Do not launch it before you want to be a CASP. The moment you hold
customer keys you are in a licensing regime, and the whole self-hosted
argument you spent two years making now applies against you.

---

## 4. Sequencing, tied to what is actually built

### Now → Month 3: design partners

**Two or three, priced at R450,000 (~$25,000) for a six-month
engagement.** Includes implementation, a testnet deployment, and direct
access to you.

Not free. A free pilot has no internal champion, gets no engineering time
from the customer, and dies quietly. R450k is small enough to come out of
a line manager's budget and large enough that someone has to defend it,
which means someone has to make it work.

What you take in exchange for the discount, in the contract:

- A named public reference and a logo
- A written case study with real numbers
- Year-one production licence locked at **50% of list**, exercisable for
  twelve months
- Right to cite their compliance team's review in your own materials

**What you can honestly sell at this stage:** testnet, integration,
architecture review, a production plan. Nothing that holds mainnet funds.
Say so unprompted — it is the single fastest way to establish that you are
not overselling, and every serious buyer is testing for exactly that.

### Month 1 → Month 4: the cryptographic audit

Still the highest-leverage thing you can do, and it is now the only
remaining hard blocker you can buy your way past.

Budget **$40,000–$80,000** for a threshold-signature review from a firm
with MPC experience — Trail of Bits, Kudelski, NCC Group, Least Authority.
Commission it from the design-partner revenue.

It converts directly into price. "Independently audited" moves Foundation
from $75,000 to something closer to $120,000 and is the first question
every buyer after the first three will ask.

### Month 4 → Month 9: first production licences

Requires the audit, the three signing parties on genuinely separate
infrastructure (`party-isolation-check.sh` must report `multi-account`,
not `simulated`), and a dashboard.

**The dashboard was the quiet blocker, and it is now built.** A read-only
web console ships inside the API gateway — overview, keys with balances
and signing history, a transaction history showing decoded recipients and
amounts, per-currency threshold reporting, and webhook status. Server-
rendered, no build step, nothing extra for a customer to deploy.

It closes the disqualifying gap: the platform can now be evaluated by
someone who is not an engineer, which is most of the people who decide.
It does not close the competitive gap — Fireblocks' console is years of
work — and two things are still missing that a bank will ask for
specifically:

- **An approval workflow.** The policy engine emits `requiresApproval` and
  nothing consumes it. Dual control is the reason a control function signs
  off, and it is roughly three weeks including the console screens.
- **Mobile approvals.** Dual control is not real if the second approver
  has to open a laptop. Four weeks, and only worth it once the workflow
  above exists.

### Month 9 → Month 24: SOC 2 Type II

The observation window cannot start until evidence accumulates, which is
why this is measured in years rather than months.
`scripts/collect-soc2-evidence.sh` already collects it. Engage an auditor
at month nine so the window is running while you sell.

---

## 5. The contract

ELv2 grants the copyright licence and says nothing about any of this. A
bank will want all of it in a signed agreement:

- **Term and renewal.** Annual, auto-renew, 90 days' notice, uplift capped
  at CPI + 3%.
- **Liability cap.** Customarily 12 months' fees. Expect a custody buyer to
  push for a multiple; hold at 12 months and buy insurance rather than
  accepting uncapped liability on a product that moves money.
- **Warranty.** The software performs to documentation. Explicitly **not**
  a warranty that funds cannot be lost — they hold the keys and the
  infrastructure, which is the entire premise of the product, and the
  agreement must say so in terms.
- **Source escrow.** Offer it at Institutional and above. It costs little
  and removes a real objection about a small vendor.
- **Data processing.** You process none. Say it explicitly; it is a
  differentiator and it shortens the security review by weeks.
- **Audit rights.** They will want to audit your SDLC. Agree, scoped and
  annual.
- **Publicity.** Get the logo right in the first contract; it is very hard
  to add later.

---

## 6. What not to claim

Each of these is a claim a technical buyer can disprove in one question,
and one disproved claim costs the deal.

| Do not say | Say instead |
|---|---|
| "Bank-grade" / "military-grade" | "2-of-3 threshold ECDSA and Ed25519, audited by ‹firm›, and here is the report" |
| "SOC 2 compliant" | "SOC 2 readiness work is done, evidence collection is automated, Type II observation begins ‹date›" |
| "Fully multi-chain" | "Bitcoin and every EVM chain can transact today. Solana holds real MPC keys and cannot transact yet. Cosmos likewise." |
| "Production ready" | "Production ready for testnet today; mainnet after the audit and party separation, which are ‹dates›" |
| "Supports all stablecoins" | "Any ERC-20 on any EVM chain, once registered and verified against its own contract. Not SPL, not Tron." |

The honest version is a better sales asset than the inflated one. Everyone
in this market has been told "bank-grade" by six vendors; the one who
volunteers what does not work yet is the one whose other claims get
believed.

---

## 7. Realistic first-24-month picture

Assuming the audit lands and a dashboard ships:

| | Customers | Licence | Services | Total |
|---|---|---|---|---|
| **Year 1** | 3 design partners → 2 convert | ~$110k | ~$90k | **~$200k** |
| **Year 2** | 2 renew + 4 new | ~$600k | ~$180k | **~$780k** |

That is a services-heavy first year turning into a licence business in the
second, which is the normal shape for enterprise infrastructure and the
shape investors expect. It is also enough to fund the audit, a dashboard
and one more engineer without outside money — which matters, because the
alternative is raising against a product that has not yet sold anything.

---

## 8. The first call

Fifteen minutes, in this order:

1. **"Where do your stablecoin keys live today?"** Let them answer. It is
   usually an exchange account, a hardware wallet in a safe, or a
   custodian — and all three have a problem they already know about.
2. **"What does your custodian charge, and what happens if they fail?"**
   This is your price anchor and your risk argument, from their mouth.
3. **The demo.** Provision a 2-of-3 key, show three separate processes
   completing a DKG, send a stablecoin, show the transaction on chain,
   show the policy engine refusing one over the limit.
4. **"The private key is never assembled. Here is the code that proves
   it."** Offer source access under NDA. This is what ELv2 buys you and
   what a SaaS competitor cannot offer at all.
5. **What is not built.** Volunteered, not extracted.
6. **The design-partner offer**, with a date.

Do not send pricing before call two. Price is a conversation about value,
and you have not established the value until they have told you what they
pay now.
