# Procurement plan: cryptographic audit, penetration test, SOC 2

**Status as of 2026-10-06: nothing is engaged.** Selecting a firm, getting quotes, signing a
statement of work and paying are actions a person with authority takes. What could be prepared
without them is prepared; this is the plan for the rest, in the order that matters.

## What is ready

| For | Package |
|---|---|
| Cryptographic review | `docs/security/AUDIT-READINESS.md` (the brief, scope, evidence) plus [AUDIT-SCOPE-ADDENDUM.md](AUDIT-SCOPE-ADDENDUM.md) for everything built since, with sizes and the questions each area raises |
| Penetration test | [PENTEST-SCOPE.md](PENTEST-SCOPE.md) and the generated, CI-checked [ROUTE-INVENTORY.md](ROUTE-INVENTORY.md) |
| SOC 2 | [SOC2-CONTROL-MATRIX.md](SOC2-CONTROL-MATRIX.md) (every criterion: what exists, the evidence, what a person must still do) and `scripts/collect-soc2-evidence.sh` |
| Handing evidence over | `scripts/assurance-pack.sh` builds a single dated bundle with the pinned commit, dependency lists, migrations, route inventory, workflows and checksums |

## What only a person can do, in order

### Week 0 (decisions; no spend required)

1. **Name an owner** for the programme and for each **P** row of the SOC 2 matrix. Without
   named owners nothing below moves.
2. **Decide the SOC 2 boundary**: which service organisation, which system, which period. The
   cleanest is the self-hosted product plus the vendor's own build and release pipeline; if
   the vendor never holds customer keys or data, say so, because it shrinks the boundary.
3. **Decide whether the first customer pilot waits for any of these.** The honest position
   for banks and governments is a bounded pilot while these run; that is a commercial decision
   with a security consequence, and it should be made explicitly.
4. **Pin the commit** the cryptographic review will be a statement about, and decide whether a
   re-review window is part of the engagement (cheaper to include than to add later).

### Weeks 1 to 2 (outreach; each is one email)

5. **Cryptographic review: ask three firms the screening question first** (below). Send the
   brief and the addendum only to those who answer it well.
6. **Penetration test: request three quotes** against PENTEST-SCOPE.md. The scope names a
   deployed environment as a prerequisite; say it will exist by the start date (item 11).
7. **SOC 2: speak to two or three CPA firms licensed to issue SOC 2 reports**, and decide
   whether to use a compliance-automation platform to collect evidence. Ask each firm how it
   treats a self-hosted product, and whether it will do a Type I first.
8. **Smart-contract audit**: the token contract is separate from the cryptographic review and
   needs a smart-contract auditor. Ask the cryptographic firms whether they also do this.

### Weeks 2 to 6 (engineering that unblocks the above; this is the part code can do)

9. **Start the evidence collector on a schedule** (`scripts/collect-soc2-evidence.sh`) into a
   durable, access-controlled location. The observation window cannot begin before it runs.
10. **Install the chart on a real cluster** (never done) and run the isolation measurement in
    `docs/deployment/PARTY-ISOLATION.md`. This closes several **X** rows at once.
11. **Stand up the staging environment the penetration test needs**, with signing parties on
    separate hosts and owners (`docs/deployment/SEPARATE-HOSTS.md`), test chains only, and
    `METRICS_TOKEN` set.
12. **Run a real OIDC acceptance** against the customer's IdP (Keycloak, Entra ID or Okta).

### From week 6

13. Cryptographic review runs against the pinned commit. Fix findings on a branch and agree
    which are re-reviewed.
14. Penetration test runs against staging; retest of every high and critical finding.
15. SOC 2: Type I (if used) at the end of readiness; the Type II observation window opens when
    controls are operating and evidence exists.

## The screening question for cryptographic firms

> Name a threshold-signature or MPC implementation you have reviewed, and one class of
> protocol-level finding you have made in one.

A general application-security answer (web vulnerabilities) means the wrong firm for the
cryptographic review and a fine firm for the penetration test. Compare what each proposes to
*do* (a code read is a different product from a protocol analysis), not what each costs; get
three quotes. No figure is given here on purpose: a number in a repository is read as a budget.

## Outreach template (cryptographic review)

> Subject: Threshold-signature custody: request for a review quote
>
> We build self-hosted MPC custody infrastructure (2-of-3 threshold ECDSA and EdDSA, built on
> bnb-chain/tss-lib v2). We are seeking an independent review of the signing layer, with a
> pinned commit, and a separate review of a ~140-line permissioned ERC-20.
>
> Before we send the brief: could you name a threshold-signature implementation you have
> reviewed, and a class of protocol-level finding you made in one? We will then send the
> scope (AUDIT-READINESS.md and an addendum), repository access under NDA, and the evidence we
> have already produced, and ask for a quote, an estimated start date and duration, whether a
> re-review window is included, and a sample redacted report.

## What would change the order

* A customer or regulator names a specific attestation as a condition (then that comes first).
* The cryptographic review finds a protocol-level defect (then the penetration test waits for
  the fix, since it would test code that is about to change).
* The cluster install (item 10) uncovers topology problems (then item 11 slips, and so does the
  penetration test).

## Not a substitute for each other

A SOC 2 report says controls operated as described. A penetration test says what an attacker
found in a window. The cryptographic review says whether the signing is sound. A customer who
asks for one should not be given another.
