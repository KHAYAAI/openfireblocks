# Customer flow videos

Three self-playing walkthroughs of how an application uses the platform, one
each for a fintech, a bank and a government. They are the same scenes the
gateway serves at `/console/showcase?flow=fintech|bank|government` (and
`flow=overview` for the platform tour), recorded at 1280x720.

| File | Length | Story |
|---|---|---|
| `openfireblocks-fintech-flow.webm` | ~80 s | API key, keys, rules, routine payment, agent budget, held large payment, approvals, reconciliation, pilot |
| `openfireblocks-bank-flow.webm` | ~65 s | SSO and roles, keys, dual control, operator request held, approvers, audit trail and Travel Rule, pilot with milestones |
| `openfireblocks-government-flow.webm` | ~60 s | self-hosted deployment, three owners, controls, three-official disbursement, evidence, bounded proof of concept |

What they are: an illustration of real platform behaviour. The companies are
fictional, the figures are examples, and the scenes are animations, not a
recording of a live system. Each one ends on what a pilot is and what still
stands between a pilot and production money, because those are the claims we
can and cannot make today (see `LAUNCH-CHECKLIST.md`).

Re-record with the gateway running:

    GATEWAY_URL=http://localhost:3000 node scripts/record-showcase.js

(needs `playwright` and its Chromium).
