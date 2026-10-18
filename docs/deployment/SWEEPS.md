# Deposit sweeps

Move what has accumulated on a deposit key to a treasury address, on a rule an administrator
sets. `POST /organisations/:id/sweeps` with `{ name, keyId, chainId?, destination, minAmount,
reserve?, intervalSeconds?, travelRule? }`; `POST .../sweeps/:ruleId/run` runs one by hand;
`GET .../sweeps` and `.../sweeps/runs` list rules and what each run did.

* A run sweeps `balance - reserve` when that is at least `minAmount` (base units). The
  reserve stays behind for fees.
* **A sweep is not a privileged path.** Each run is submitted as an ordinary transfer, so the
  freeze, address whitelist, spending policy and approval quorum apply: a large sweep waits
  for approvers like any large transfer, and a frozen organisation sweeps nothing.
* **A rule is fixed once made** (database-enforced): destination, amounts and key cannot be
  edited, only switched off or deleted, so a standing sweep cannot be quietly redirected. The
  destination must already be allowed by the whitelist when the rule is created.
* Outcomes are recorded, never thrown away: `completed`, `pending_approval` (with the approval
  id), `skipped` (and why), `refused` (a freeze, whitelist or policy decision) and `failed`
  (with an alert).
* The scheduler is opt-in: `SWEEPS_SCHEDULER_SECONDS=60` makes each gateway replica check for
  due rules; a rule is claimed with a conditional update so replicas cannot run it twice.
  Left at 0, rules run only when someone calls the run endpoint.

**Not supported:** tokens (native asset only), Bitcoin (the gateway has no balance read for
it), and sweeping from several keys under one rule (one rule, one key).
