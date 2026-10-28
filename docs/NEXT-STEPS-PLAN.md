# Next steps: the plan from supervised pilot to real money

Written 2026-10-19. The current state is in [LAUNCH-READINESS.md](LAUNCH-READINESS.md); the evidence for every claim
is in [`evidence/`](evidence/). This document says what to do next, in what order, who does it, what it costs and how
we will know each step is done.

## Where we start

**Ready:** a supervised pilot on testnet or capped balances. Real 2-of-3 signing on four public test networks;
recovery from the off-site backup alone measured at 41 min 42 s; all 23 staging keys restored in one command; signed
and attested images on every build; two real signing bugs found by testing and fixed this week.

**Not ready:** uncapped balances, banks, governments. The one remaining engineering step is running the three signing
parties on separate hosts under separate operators. Everything else between here and there is assurance and
contracts, not code.

**Decided on 2026-10-19:** rehearse separate hosts alone (track A) first, then with three operators (track B).
Regions: party 1 `eu-west-1`, party 2 `eu-central-1`, party 3 `eu-west-2`.

---

## Phase 0: housekeeping (this week, about 2 hours in total)

Small items that make every later phase cleaner.

| # | Step | Who | Effort | Done when |
|---|---|---|---|---|
| 0.1 | Merge the open documentation pull request(s) so the default branch carries the current evidence | You | 5 min | Default branch shows the 2026-10-19 status |
| 0.2 | Decide what to do with the Deploy workflow's **Deploy to Staging** job, which fails on every run at its AWS credentials step because staging is a single server it cannot reach | Engineering, your decision | 30 min | The job is either removed, skipped with a clear reason, or wired to a role that works; the workflow run shows green when nothing is wrong |
| 0.3 | Re-authorise the AWS connector (`/mcp`) and re-run the AWS baseline check | You, then Engineering | 30 min | Baseline report for the current account committed to `evidence/` |
| 0.4 | Snyk token as a repository secret, or record the decision to rely on the other scanners | You | 5 min | The Snyk step either runs or the skip is a written decision |
| 0.5 | Prove a **restored Cosmos key signs** (the restore is proven; a signature from it is not) | You, with Engineering | 20 min | A signature from one of the two restored Cosmos keys, recorded |

**Progress, 2026-10-28:** 0.1 done (the documentation pull request is merged). 0.2 done: the job is skipped
unless the repository variable `STAGING_DEPLOY_ENABLED` is `true`, so it no longer fails every run; set the
variable once a staging cluster the workflow can reach exists. The gateway's `npm audit` step also started failing
on four advisories published after 2026-10-20; they are fixed. Ten low-risk dependency updates are merged
(#51, #53, #54, #55, #56, #59, #60, #61, #62, #67). 0.3 to 0.5 are open.

---

## Phase 1: separate hosts, rehearsal (track A) — 1 to 2 days, about $20-30

Follow [`deployment/SEPARATE-HOSTS-PLAYBOOK.md`](deployment/SEPARATE-HOSTS-PLAYBOOK.md). You play all three operators.

### 1.0 Before starting (you, 20 minutes plus waiting)
- **A domain.** Register a cheap one in Route 53 (for example a `.click` name, a few dollars a year), or use a subdomain
  of one you own. Registration can take a few hours.
- **Three AWS accounts** in AWS Organizations: `ofb-party-1`, `-2`, `-3`, each with its own email (Gmail `+` aliases
  work). Note the three account ids.
- **A throwaway platform instance** (the DR runbook's phases 2 to 4) with an Elastic IP: the existing staging must not be
  pointed at external parties, because that hides every key its in-cluster parties hold.

### 1.1 to 1.7 The playbook's phases

| Phase | What | Time | Checkpoint (must hold before moving on) |
|---|---|---|---|
| 1. Accounts, state, images | State bucket and lock table per account; `mpc-party` image pulled by digest, `cosign verify`, pushed to each account's ECR | 1 h | Each account shows a verified image digest in its own ECR, and its own state bucket |
| 2. Co-signer key | ed25519 key generated; public key written down; private key's custodian named (your laptop, for the rehearsal) | 20 min | Public key recorded (64 hex characters) |
| 3. CA and certificates | Ceremony CA created; each party makes its own key and a signing request; the CA signs them; the worker gets a client certificate | 1 h | `openssl verify` passes for all three; no private key left its owner |
| 4. Apply each party | First apply with a placeholder peer, exchange addresses, second apply; DNS A records | 1.5 h | Three instances in three accounts; `dig` resolves each name; each security group admits only the two peers and the platform |
| 5. Each party's Vault | TLS secret stored with the CLI; `party-bootstrap.sh` over SSM; recovery shares copied off and deleted from the host | 45 min each | Each party answers `/health`; root tokens revoked |
| 6. Point the platform at the parties | Chart's external-party mode, worker mTLS from the ceremony CA, co-signer on | 30 min | Worker and gateway running; no in-cluster parties |
| 7. Verify | Isolation check at `multi-account`; a brand-new key generated and signing; one party's host stopped and 2 of 3 still signs; restore; `restore-all-keys.sh` | 2 h | All pass; ceremony and recovery times measured |

### 1.8 Record and tear down
- Evidence file `evidence/YYYY-MM-DD-separate-hosts-rehearsal.md`: accounts (ids only), plan summaries, isolation-check
  output, first signature, timings, **every fault found and its fix**. Expect several: nothing here has run on real AWS.
- Fix the faults in the repository (Terraform, chart, playbook) before track B.
- Destroy everything: `terraform destroy` per account, delete images and state buckets, release Elastic IPs, terminate
  the throwaway platform, close the accounts, delete the rehearsal CA and co-signer keys.

**Phase 1 is done when** a key generated on three separate accounts signs, the isolation check passes at
`multi-account`, recovery works, and the evidence file lists every fault with its fix. It is recorded as
"multi-account, **single operator**": it proves the machinery, not the separation.

---

## Phase 2: pentest round 4 — 1 to 2 days

On the final shape of the system, so after phase 1's fixes are merged.

| Step | Detail |
|---|---|
| Scope | The routes added since round 3 (user wallets, SPL tokens, retention, agents and the MCP server), the restore and backup-fetch paths, and the external-party mode's network surface (from the rehearsal, before it is destroyed, if timing allows) |
| Tool | Shannon with the DeepSeek Anthropic-compatible endpoint; validate the model first (`--validate-model`); description under 500 characters; the brief in `security/PENTEST-ROUND-4-BRIEF.md` |
| Done when | Every finding is fixed or accepted in writing, redeployed, and re-tested; results in `security/` |

**Then commission the independent, human-led test** (a vendor, 2 to 4 weeks lead time). It is a prerequisite for banks.

---

## Phase 3: separate hosts with three operators (track B) — 1 to 2 weeks

| Step | Who | Detail |
|---|---|---|
| 3.1 Recruit two operators | You | People or organisations you trust who are not under one administrator: for example a co-founder's company, a law firm's IT, or a custody partner. Each needs an AWS account they alone control |
| 3.2 Agreement | You, with counsel | A short written agreement per operator: what they hold, on-call, patching, what they must never share, what happens if they leave |
| 3.3 Recovery-key holders | Each operator | At least three people per party, none holding another party's share; PGP keys exchanged |
| 3.4 Co-signer custody | You | Move the authorising key off any machine the platform administrator controls (a separate signer service or KMS-backed key held by someone else) |
| 3.5 Run the playbook | All four | Same phases as the rehearsal, each operator in their own account; the platform operator holds the CA and the co-signer arrangement |
| 3.6 Negative tests | All | Confirm in writing that no operator can read another's secrets, Vault or disk, and no single operator can sign |
| 3.7 Go-live record | You | Evidence file, isolation check at `multi-account` in the go-live runbook, operators and holders listed, renewal dates calendared |

**Phase 3 is done when** the platform signs only through parties in three accounts run by three named operators, the
negative tests are recorded, and the readiness verdict for uncapped balances can move.

---

## Phase 4: dependency reviews — 1 to 2 days, alongside the others

Ten Dependabot pull requests remain (as of 2026-10-28; Dependabot replaced several of the earlier ones). One at a
time, each with CI green on current code, in this order:

| Order | Pull request | Why it needs care |
|---|---|---|
| 1 | #11 Vault client | Used by the signer and the backup service; check the token and KV v2 paths still behave |
| 2 | #38 immudb | Audit log; check verification and the client's TLS behaviour |
| 3 | #50 Temporal SDK 1.50 (replaces #39) | Workflow determinism: replay existing workflow histories before merging |
| 4 | #68 OPA 1.x (major; replaces #43) | Rego v1 syntax can change policy meaning; run every policy test and the sanctions screening against known cases |
| 5 | #64, #65, #66 NestJS 12 (together) | Must move as a set; the gateway's guards, interceptors and validation pipes need a full test pass |
| 6 | #57 protobufjs 8 (major) | The gateway's gRPC link to TRISA (Travel Rule) uses it through @grpc/proto-loader; run the TRISA tests and one exchange |
| 7 | #63, #23 TypeScript 7 | A new compiler; may be left until it settles |

---

## Phase 5: assurance and business (decisions, not engineering)

Long lead times, so start these now in parallel.

| Item | Why | Lead time |
|---|---|---|
| Independent cryptographic review of how the platform drives `tss-lib` | Required before material balances | 4-8 weeks |
| Independent human-led pentest | Required by banks | 2-4 weeks to book |
| SOC 2 Type I, then Type II | Required by banks; Type II needs an observation window | Type I 1-2 months; Type II 6-12 months |
| Legal: licensing, terms, privacy and POPIA s69, custody regulation for each market | Required before any customer funds | Weeks |
| Named roles: ISMS owner, Information Officer (registered), incident lead | Compliance pack assumes them | Days |
| Custodian account, TRISA membership, KYC and sanctions vendor, insurance | Production operations | Weeks to months |
| CloudHSM | Deferred on cost (about $1,100 a month per HSM); only when a customer or auditor requires FIPS 140-2 Level 3 for the single-key path | When required |

---

## Phase 6: the pilot itself

| Stage | Gate to enter | Limits |
|---|---|---|
| Supervised testnet pilot | **Now** | Testnet only; a named contact at the customer; weekly review |
| Capped mainnet pilot | Phase 1 done and its faults fixed; phase 2 findings fixed; restore and recovery drills re-run on the deployed version; written runbook for incidents | A per-key and per-day cap agreed in writing (start small); approvals at 2 of N; daily reconciliation |
| Uncapped / banks | Phase 3 done; cryptographic review and human pentest closed; SOC 2 Type I at least; legal done | As contracted |

---

## Timeline (realistic)

| Week | Focus |
|---|---|
| 1 | Phase 0; domain and accounts; phase 1 rehearsal; book the human pentest and the cryptographic review |
| 2 | Fix rehearsal faults; phase 2 (pentest round 4); start recruiting operators; dependency reviews |
| 3-4 | Phase 3 with three operators; capped mainnet pilot can start once phases 1 and 2 are closed |
| 5+ | Assurance engagements running; SOC 2 observation window; uncapped only when phase 5's items close |

## Risks and how they are handled

| Risk | Handling |
|---|---|
| The rehearsal finds many faults | Expected; the reason for doing it alone first. Each fault goes into the evidence and is fixed before track B |
| Recruiting operators takes longer than planned | Capped mainnet does not wait on it; only uncapped balances do |
| Cross-region ceremonies are slow | Measured in phase 1 step 7 before any promise is made; regions can be moved closer |
| Losing the backup encryption key | Two copies kept apart (done); recorded where each lives |
| A dependency update changes behaviour | One pull request at a time, CI on current code, replay or policy tests for the risky ones |

## What to do today

1. Register the domain and create the three AWS accounts (phase 1.0).
2. Merge the documentation pull request (phase 0.1).
3. Send Engineering the domain and the three account ids, then start phase 1 together.
