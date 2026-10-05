// A self-playing walkthrough of the platform. Scenes are plain functions that
// build DOM with textContent only (the gateway's CSP forbids inline script, and
// nothing here needs innerHTML); CSS keyframes do the motion, this file only
// sequences scenes, drives the progress bar and handles play/pause/seek.
(function () {
  'use strict';

  var SVGNS = 'http://www.w3.org/2000/svg';

  function h(tag, cls, text, kids, delay) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    if (delay != null) e.style.setProperty('--d', delay + 'ms');
    (kids || []).forEach(function (k) { if (k) e.appendChild(k); });
    return e;
  }
  function s(tag, attrs, text, delay) {
    var e = document.createElementNS(SVGNS, tag);
    Object.keys(attrs || {}).forEach(function (k) { e.setAttribute(k, attrs[k]); });
    if (text != null) e.textContent = text;
    if (delay != null) e.style.setProperty('--d', delay + 'ms');
    return e;
  }
  function head(eyebrow, title, sub) {
    var t = h('h1', 'h a');
    title.forEach(function (part) { t.appendChild(part.em ? h('em', null, part.em) : document.createTextNode(part)); });
    return [h('div', 'eyebrow fade', eyebrow), t, sub ? h('p', 'sub a', sub, null, 250) : null];
  }
  function scene(parts, body) {
    var root = h('div', 'scene', null, parts);
    if (body) root.appendChild(body);
    return root;
  }

  // ---- scenes -------------------------------------------------------------

  function sceneOpen() {
    var chips = ['Bitcoin', 'Ethereum', 'Polygon', 'Solana', 'Cosmos'].map(function (c, i) { return h('span', 'chip pop', c, null, 900 + i * 120); });
    return scene(head('OpenFireblocks', ['Custody you ', { em: 'run yourself.' }],
      'Threshold-signed digital-asset custody, policy, approvals and settlement, deployed in your own cloud under your own controls.'),
      h('div', 'chips', null, chips));
  }

  function sceneMpc(o) {
    o = o || {};
    var svg = s('svg', { viewBox: '0 0 160 70', preserveAspectRatio: 'xMidYMid meet' });
    var P = { a: [80, 12], b: [34, 54], c: [126, 54] }, C = [80, 38];
    ['a', 'b', 'c'].forEach(function (k) { svg.appendChild(s('line', { 'class': 'link', x1: P[k][0], y1: P[k][1], x2: C[0], y2: C[1] })); });
    // parties A and B sign; C stays offline and the transfer still completes
    ['a', 'b'].forEach(function (k, i) {
      svg.appendChild(s('line', { 'class': 'pulse', x1: P[k][0], y1: P[k][1], x2: C[0], y2: C[1] }, null, 900 + i * 300));
    });
    (o.labels || [['a', 'Party A', 'host 1'], ['b', 'Party B', 'host 2'], ['c', 'Party C', 'offline']]).forEach(function (n) {
      var g = s('g', { 'class': 'pop' }, null, 200);
      g.appendChild(s('circle', { 'class': 'node' + (n[0] === 'c' ? ' off' : ''), cx: P[n[0]][0], cy: P[n[0]][1], r: 7.5 }));
      g.appendChild(s('text', { 'class': 'svgt', x: P[n[0]][0], y: P[n[0]][1] + .8 }, n[1]));
      g.appendChild(s('text', { 'class': 'svgs', x: P[n[0]][0], y: P[n[0]][1] + 11.5 }, n[2]));
      svg.appendChild(g);
    });
    var sig = s('g', { 'class': 'pop' }, null, 2200);
    sig.appendChild(s('rect', { x: 62, y: 33.5, width: 36, height: 9, rx: 1.4, fill: '#0A0A0A' }));
    sig.appendChild(s('text', { 'class': 'svgt', x: 80, y: 39.3, fill: '#F4F2EC', style: 'fill:#F4F2EC' }, 'signature'));
    svg.appendChild(sig);
    return scene(head(o.eyebrow || 'Keys', o.title || ['No single key. ', { em: 'Ever.' }],
      o.sub || '2-of-3 threshold signing: any two parties sign, the full private key is never assembled in one place, and one host down does not stop a transfer.'),
      h('div', 'body', null, [h('div', 'mpc', null, [svg])]));
  }

  // A request waiting on named people. `o.approvers` signed it off in order;
  // `o.required` pips fill as each does. The requester is shown refused first:
  // that is the point of segregation of duties.
  function sceneApproval(o) {
    o = o || {};
    var approvers = o.approvers || ['Alice', 'Ada'];
    var required = o.required || approvers.length;
    var left = h('div', 'card a', null, [
      h('div', 'k', 'Transfer request'),
      h('div', 'v', o.amount || '42.5 ETH', null),
      h('div', 'sm', o.to || 'to 0xd8dA…6045 · Sepolia'),
      h('div', 'row', null, [h('span', 'tag warn', o.why || 'Over 10 ETH'), h('span', 'sm', 'policy requires approval')]),
      o.requester ? h('div', 'row fade', null, [h('span', 'tag bad', 'Refused'), h('span', 'sm', o.requester + ' asked for it, so cannot approve it')], 1800) : null,
    ], 700);
    var pips = [];
    for (var i = 0; i < required; i++) pips.push(h('span', 'pip on', null, null, 2400 + i * 1000));
    var rows = approvers.map(function (n, i) {
      return h('div', 'row fade', null, [h('span', 'check', '✓'), h('span', 'sm', n + ' approved · ' + (o.stepUp || 'one-time code'))], 2400 + i * 1000);
    });
    var done = 2400 + approvers.length * 1000 + 300;
    var right = h('div', 'card a', null, [
      h('div', 'k', 'Approvals (' + required + ' required)'),
      h('div', 'meter', null, pips),
    ].concat(rows, [h('div', 'row fade', null, [h('span', 'tag ok', 'Signed'), h('span', 'sm', o.after || 'the stored request runs once, exactly as asked')], done)]), 1300);
    return scene(head(o.eyebrow || 'Policy and approvals', o.title || ['Rules decide. ', { em: 'People sign off.' }],
      o.sub || 'Policy is evaluated on the transaction the gateway itself builds. Large moves need named approvers with step-up auth, and the DB refuses to rewrite the record.'),
      (function () { var g = h('div', 'body', null, [left, right]); g.style.cssText = 'display:grid;grid-template-columns:1fr 1.1fr;gap:2cqw;align-items:start'; return g; })());
  }

  function sceneAgents() {
    function bar(label, w, d, extra) {
      var b = h('b'); b.style.setProperty('--w', w); b.style.setProperty('--d', d + 'ms');
      return h('div', null, null, [h('div', 'row', null, [h('span', 'k', label), extra || null]), h('div', 'bud', null, [b])]);
    }
    var card = h('div', 'card a', null, [
      h('div', 'row', null, [h('span', 'v', 'Procurement bot'), h('span', 'tag ok', 'Active')]),
      bar('Spent · 24h  R1,500 of R10,000', '15%', 800),
      h('div', 'row fade', null, [h('span', 'tag ok', 'Released'), h('span', 'sm mono', 'R1,500 · ZARP')], 1500),
      h('div', 'row fade', null, [h('span', 'tag bad', 'Refused'), h('span', 'sm mono', 'R12,000 · over per-transfer limit (R5,000)')], 2600),
    ], 500);
    card.style.cssText = 'max-width:62cqw;display:flex;flex-direction:column;gap:1.6cqw';
    return scene(head('Agents', ['Automation with a ', { em: 'budget.' }],
      'Give software a scoped credential that can only pay, to allowed tokens and recipients, within per-transfer and 24-hour limits. Everything else is refused and recorded.'),
      h('div', 'body', null, [card]));
  }

  function sceneCompliance() {
    var code = h('pre', 'code', null, [
      h('span', null, '{ "originator": { "legalPerson": {'), document.createTextNode('\n'),
      h('span', 'type', '    "name": "Forge Treasury Co" } },', null, 600), document.createTextNode('\n'),
      h('span', 'type', '  "beneficiary": { "legalPerson": {', null, 1500), document.createTextNode('\n'),
      h('span', 'type', '    "name": "External Counterparty Ltd" } },', null, 2300), document.createTextNode('\n'),
      h('span', 'type', '  "beneficiaryUnhosted": true }', null, 3300),
    ]);
    var travel = h('div', 'card a', null, [h('div', 'k', 'Travel Rule · IVMS101'), code,
      h('div', 'row fade', null, [h('span', 'tag warn', 'Awaiting transmission')], 4300)], 500);
    var recon = h('div', 'card a', null, [
      h('div', 'k', 'Reconciliation · ledger vs chain'),
      h('div', 'row fade', null, [h('span', 'tag ok', 'Match'), h('span', 'sm mono', '0xfc61b0…  1.2 ETH')], 2200),
      h('div', 'row fade', null, [h('span', 'tag ok', 'Match'), h('span', 'sm mono', '0x221593…  25,000 ZARP')], 2800),
      h('div', 'row fade', null, [h('span', 'tag bad', 'Break'), h('span', 'sm mono', 'unsigned outbound on chain')], 3600),
    ], 1000);
    var grid = h('div', 'body', null, [travel, recon]);
    grid.style.cssText = 'display:grid;grid-template-columns:1.15fr 1fr;gap:2cqw;align-items:start';
    return scene(head('Compliance', ['Evidence ', { em: 'by default.' }],
      'Travel Rule payloads are built and held with each transfer. Reconciliation checks the ledger against the chain and the statements, and surfaces every break.'), grid);
  }

  function sceneConsole() {
    var rows = [['ETH', '1.2', 'Confirmed', '4:50 AM'], ['ZARP', '25,000', 'Confirmed', 'Oct 3'], ['USDC', '500', 'Failed', 'Oct 1']];
    var tbl = h('div', 'tbl', null, [h('div', null, null, ['Asset', 'Amount', 'Status', 'When'].map(function (t) { return h('span', null, t); }))]);
    rows.forEach(function (r, i) {
      var st = h('span', null, null, [h('span', 'tag ' + (r[2] === 'Failed' ? 'bad' : 'ok'), r[2])]);
      tbl.appendChild(h('div', 'fade', null, [h('span', null, r[0]), h('span', null, r[1]), st, h('span', null, r[3])], 1500 + i * 400));
    });
    var stats = h('div', 'stats', null, [['Keys', '2'], ['Active', '1'], ['Pending approvals', '4'], ['Failed', '0']].map(function (x, i) {
      return h('div', 'a', null, [h('div', 'k', x[0]), h('div', 'v', x[1])], 700 + i * 120);
    }));
    var side = h('div', 'side', null, ['Overview', 'Keys', 'Transactions', 'Agents', 'Approvals', 'Policy', 'People', 'Travel Rule', 'Reconciliation', 'Compliance', 'Webhooks'].map(function (t, i) { return h('div', i === 0 ? 'on' : null, t); }));
    var shell = h('div', 'shell a', null, [side, h('div', 'main', null, [stats, tbl])], 400);
    shell.style.height = '100%';
    return scene(head('One console', ['Everything a role allows, ', { em: 'one sign-in.' }]), h('div', 'body', null, [shell]));
  }

  function sceneDeploy() {
    var cols = [
      ['Your cloud', 'Gateway, policy, signer parties, database and workflow engine run in your accounts and clusters. Helm charts and Terraform ship in the repo.'],
      ['Your keys', 'Signing parties on separate hosts. Optional PKCS#11 HSM mode keeps key material inside hardware.'],
      ['Your controls', 'SSO, named roles, segregation of duties, append-only audit trail, signed webhooks.'],
    ].map(function (c, i) { return h('div', 'card a', null, [h('div', 'big', c[0]), h('div', 'sm', c[1])], 400 + i * 250); });
    return scene(head('Deployment', ['Self-hosted ', { em: 'by design.' }]), h('div', 'body', null, [h('div', 'three', null, cols)]));
  }

  function sceneClose() {
    return scene(head('OpenFireblocks', ['Move value. ', { em: 'Prove everything.' }],
      'Start with a scoped pilot: one chain, one policy, your approvers, your cloud.'));
  }

  // ---- building blocks for the customer flows -----------------------------

  // A terminal: each line types in after the one before it.
  function terminal(lines, title) {
    var pre = h('pre', 'code');
    lines.forEach(function (l, i) {
      var t = typeof l === 'string' ? { text: l } : l;
      var span = h('span', t.dim ? 'type dim' : 'type', t.text, null, 400 + i * 900);
      span.style.setProperty('--cw', (t.text.length + 1) + 'ch');
      pre.appendChild(span);
      pre.appendChild(document.createTextNode('\n'));
    });
    return h('div', 'card a term', null, [title ? h('div', 'k', title) : null, pre], 300);
  }

  // The path one transfer takes, a step at a time. state: ok | hold | bad | idle.
  function pipeline(steps) {
    var row = h('div', 'pipe');
    steps.forEach(function (st, i) {
      row.appendChild(h('div', 'step pop ' + (st.state || 'ok'), null, [h('div', 'k', st.k), h('div', 'big2', st.title), h('div', 'sm', st.sub)], 500 + i * 800));
    });
    return row;
  }

  function twoCol(left, right, cols) {
    var g = h('div', 'body', null, [left, right]);
    g.style.cssText = 'display:grid;grid-template-columns:' + (cols || '1fr 1fr') + ';gap:2cqw;align-items:start';
    return g;
  }

  function list(title, items, delay) {
    return h('div', 'card a', null, [h('div', 'k', title)].concat(items.map(function (it, i) {
      return h('div', 'row fade', null, [h('span', 'check', it.mark || '•'), h('span', 'sm', it.text)], 700 + i * 450);
    })), delay || 400);
  }

  // Who the customer is, and that the story is illustrative.
  function sceneIntro(o) {
    return scene(head(o.eyebrow, o.title, o.sub), h('div', 'body', null, [
      h('div', 'chips', null, o.chips.map(function (c, i) { return h('span', 'chip pop', c, null, 700 + i * 140); })),
      h('p', 'fine fade', o.note || 'Illustrative scenario. The company is fictional; the steps are what the platform does.', null, 1800),
    ]));
  }

  // Honest close: what the pilot is, and what stands between it and production.
  function scenePilot(o) {
    var inc = list('The pilot', o.pilot.map(function (t) { return { text: t, mark: '✓' }; }), 400);
    var notyet = list('Before production money', o.before.map(function (t) { return { text: t, mark: '○' }; }), 900);
    return scene(head(o.eyebrow, o.title, o.sub), twoCol(inc, notyet));
  }

  // ---- fintech --------------------------------------------------------------

  function fintechConnect() {
    return scene(head('1 · Connect', ['One API key. ', { em: 'Your own keys.' }],
      'The app talks to the gateway over HTTPS. Creating a key starts a threshold key ceremony between the signing parties.'),
      h('div', 'body', null, [terminal([
        'curl -X POST $GATEWAY/keys \\',
        '  -H "x-api-key: $OFB_API_KEY" \\',
        '  -d \'{"blockchain":"ethereum","threshold":2,"total_parties":3}\'',
        { text: '→ { "id": "9f1c…", "status": "pending" }   // ceremony running', dim: true },
        { text: '→ status: "active"   // address is the group key, no party holds it', dim: true },
      ], 'Create a key')]));
  }

  function fintechPolicy() {
    return scene(head('2 · Set the rules', ['Limits live ', { em: 'in policy.' }],
      'Policy runs on the transaction the gateway builds, not on what the caller claims it is.'),
      twoCol(list('Organisation policy', [
        { text: 'Sanctioned addresses refused (OFAC list, fails closed if stale)', mark: '✓' },
        { text: 'Over 10 coins in one transfer needs approval', mark: '✓' },
        { text: 'Only registered stablecoins can be moved as tokens', mark: '✓' },
        { text: 'Unknown contract calls refused unless switched on', mark: '✓' },
      ]), list('Per-agent budget', [
        { text: 'Procurement bot: ZARP and USDC only', mark: '✓' },
        { text: 'R5,000 per transfer, R10,000 per 24 hours', mark: '✓' },
        { text: 'Allowed recipients only', mark: '✓' },
      ], 900)));
  }

  function fintechSmall() {
    return scene(head('3 · A routine payment', ['Under the limits, ', { em: 'it just goes.' }],
      'A supplier payment from the app. Nothing waits on a person, and nothing skips a check.'),
      h('div', 'body', null, [pipeline([
        { k: 'Request', title: '25,000 ZARP', sub: 'POST /keys/:id/token-transfers' },
        { k: 'Policy', title: 'Allowed', sub: 'registered token, recipient allowed' },
        { k: 'Sign', title: '2 of 3 parties', sub: 'ceremony; no key assembled' },
        { k: 'Broadcast', title: 'On chain', sub: 'transaction hash returned' },
        { k: 'Notify', title: 'Webhook', sub: 'signed with HMAC' },
      ])]));
  }

  function fintechLarge() {
    return scene(head('4 · A large payment', ['Same call. ', { em: 'Held, not signed.' }],
      'Over the threshold the gateway answers 202 and nothing is signed. The request is stored as asked; people decide, then it runs once.'),
      h('div', 'body', null, [pipeline([
        { k: 'Request', title: '15 SOL', sub: 'POST /keys/:id/transfers' },
        { k: 'Policy', title: 'Needs approval', sub: 'high-value rule', state: 'hold' },
        { k: 'Held', title: '202 pending', sub: 'approval id and expiry returned', state: 'hold' },
        { k: 'Approve', title: '2 people', sub: 'not the requester', state: 'ok' },
        { k: 'Execute', title: 'Once', sub: 'idempotent on the approval id' },
      ])]));
  }

  function fintechEvidence() {
    return scene(head('5 · Prove it', ['Match the ledger ', { em: 'to the chain.' }],
      'Reconciliation compares what the platform signed with what the chain shows, and flags anything signed elsewhere.'),
      twoCol(h('div', 'card a', null, [h('div', 'k', 'Reconciliation · ledger vs chain'),
        h('div', 'row fade', null, [h('span', 'tag ok', 'Match'), h('span', 'sm mono', '0xfc61b0…  25,000 ZARP')], 900),
        h('div', 'row fade', null, [h('span', 'tag ok', 'Match'), h('span', 'sm mono', '5xK2…  15 SOL')], 1500),
        h('div', 'row fade', null, [h('span', 'tag bad', 'Break'), h('span', 'sm mono', 'outbound the platform never signed')], 2200)], 400),
      list('Where it runs', [
        { text: 'EVM and Bitcoin: checked against the chain', mark: '✓' },
        { text: 'Solana and Cosmos: checked, protocol-tested only', mark: '○' },
      ], 900)));
  }

  // ---- bank -----------------------------------------------------------------

  function bankConnect() {
    var login = h('div', 'card a', null, [
      h('div', 'k', 'Console sign-in'),
      h('div', 'row', null, [h('span', 'btnlike', 'Continue with single sign-on')]),
      h('div', 'row fade', null, [h('span', 'check', '✓'), h('span', 'sm', 'Identity from the bank\'s own provider (OIDC)')], 1200),
      h('div', 'row fade', null, [h('span', 'check', '✓'), h('span', 'sm', 'Approving needs a fresh step-up, never a saved session')], 1900),
    ], 400);
    var roles = list('Roles, one set of rules', [
      { text: 'Admin: people, policy, keys', mark: '·' },
      { text: 'Operator: starts transfers', mark: '·' },
      { text: 'Approver: decides, cannot start', mark: '·' },
      { text: 'Auditor / viewer: read only', mark: '·' },
      { text: 'Billing admin: pays, sees no keys', mark: '·' },
    ], 1000);
    return scene(head('1 · Connect', ['Named people, ', { em: 'not shared keys.' }],
      'The desk signs in through its own identity provider. Every action carries a person.'), twoCol(login, roles));
  }

  function bankPolicy() {
    return scene(head('2 · Set the controls', ['Dual control ', { em: 'by default.' }],
      'The approval policy is part of the organisation, and the database refuses a quorum nobody can reach.'),
      twoCol(list('Approval policy', [
        { text: '2 approvals, within a 60-minute window', mark: '✓' },
        { text: 'Undecided requests expire and never run', mark: '✓' },
        { text: 'Cannot require more approvers than exist', mark: '✓' },
      ]), list('Compliance controls', [
        { text: 'Travel Rule details required above the threshold', mark: '✓' },
        { text: 'Sanctions screening on the destination', mark: '✓' },
        { text: 'Daily totals against reporting thresholds', mark: '✓' },
      ], 900)));
  }

  function bankRequest() {
    var card = h('div', 'card a', null, [
      h('div', 'k', 'Review this transfer'),
      h('div', 'v', '15 SOL'),
      h('div', 'sm mono', 'to 9WzDXw…AWWM'),
      h('div', 'sm', 'From: Treasury hot wallet · requested by Oscar (operator)'),
      h('div', 'row fade', null, [h('span', 'tag warn', 'Not sent yet'), h('span', 'sm', 'needs approval. Nothing has been signed.')], 1400),
    ], 500);
    card.style.maxWidth = '62cqw';
    return scene(head('3 · The operator asks', ['Held for ', { em: 'someone else.' }],
      'The console shows the request is held and links to the approval. The operator cannot approve their own.'), h('div', 'body', null, [card]));
  }

  function bankApproval() {
    return sceneApproval({
      eyebrow: '4 · The approvers decide', amount: '15 SOL', to: 'to 9WzDXw…AWWM · Solana', why: 'High value', requester: 'Oscar',
      approvers: ['Alice', 'Bob'], required: 2, stepUp: 'single sign-on step-up or one-time code',
      title: ['Two others. ', { em: 'Recorded.' }],
      sub: 'Each decision is stored with who, when and how they were verified. A rejection needs a reason. The record cannot be edited.',
      after: 'signed by the threshold parties and sent',
    });
  }

  function bankAudit() {
    var trail = h('div', 'card a', null, [h('div', 'k', 'Audit trail · append-only')].concat([
      ['Oscar', 'requested 15 SOL'], ['Alice', 'approved (one-time code)'], ['Bob', 'approved (one-time code)'],
      ['Platform', 'signed 2 of 3, relayed'], ['Chain check', 'matches the ledger'],
    ].map(function (r, i) {
      return h('div', 'row fade', null, [h('span', 'tag ok', r[0]), h('span', 'sm', r[1])], 600 + i * 600);
    })), 300);
    return scene(head('5 · Evidence for the audit committee', ['Everything ', { em: 'is on the record.' }],
      'Travel Rule payloads are held with the transfer, and reconciliation checks the ledger against the chain and statements.'),
      twoCol(trail, list('Travel Rule · IVMS101', [
        { text: 'Originator and beneficiary captured before signing', mark: '✓' },
        { text: 'Missing details refuse the transfer, naming the gap', mark: '✓' },
        { text: 'Transmission to the counterparty: not automated yet', mark: '○' },
      ], 1000)));
  }

  // ---- government -----------------------------------------------------------

  function govDeploy() {
    var hosts = [['Ministry network', 'Gateway, policy, database, workflow engine'], ['Agency A host', 'Signing party 1'], ['Agency B host', 'Signing party 2'], ['Treasury host', 'Signing party 3']];
    var cards = hosts.map(function (x, i) { return h('div', 'card a', null, [h('div', 'big', x[0]), h('div', 'sm', x[1])], 400 + i * 300); });
    var g = h('div', 'three4', null, cards);
    return scene(head('1 · Stand it up', ['Inside your ', { em: 'own boundary.' }],
      'Helm and Terraform deploy into the government\'s cluster. No vendor service sits in the signing path, and the three signing parties run under three owners.'),
      h('div', 'body', null, [g]));
  }

  function govControls() {
    return scene(head('2 · Write the controls', ['Rules a ', { em: 'committee can read.' }],
      'Policy is code in a repository the customer reviews. The approval quorum is theirs to set.'),
      twoCol(list('Approval policy', [
        { text: '3 approvals from named officials (any number 1 to 10)', mark: '✓' },
        { text: 'Window set by the customer; lapsed requests never run', mark: '✓' },
        { text: 'Requester excluded by the database, not by convention', mark: '✓' },
      ]), list('Hardware', [
        { text: 'Optional PKCS#11 mode keeps key material in an HSM', mark: '✓' },
        { text: 'Proven on a software HSM; a physical device is part of the proof of concept', mark: '○' },
      ], 900)));
  }

  function govApproval() {
    return sceneApproval({
      eyebrow: '3 · A disbursement', amount: '1,200 ETH', to: 'to 0x71C7…976F · Ethereum', why: 'Over 10 ETH', requester: 'Finance clerk',
      approvers: ['Director A', 'Director B', 'Auditor-General'], required: 3,
      title: ['Three officials. ', { em: 'Then it runs.' }],
      sub: 'The request is stored as asked. It cannot be changed after it is parked, and it runs once when the third approval lands.',
      after: 'two of three parties sign; the third may be offline',
    });
  }

  function govEvidence() {
    return scene(head('4 · Evidence', ['A record ', { em: 'that stands up.' }],
      'Append-only decisions and a ledger-versus-chain check give an auditor something to test.'),
      twoCol(list('What an auditor can test', [
        { text: 'Who asked, who approved, how each was verified', mark: '✓' },
        { text: 'Decisions and requests cannot be altered or deleted', mark: '✓' },
        { text: 'Anything on chain the platform did not sign is flagged', mark: '✓' },
      ]), list('Not claimed', [
        { text: 'Independent cryptographic audit: not done', mark: '○' },
        { text: 'Penetration test and SOC 2 Type II: not done', mark: '○' },
      ], 900)));
  }

  var FLOWS = {
    overview: { label: 'Platform', scenes: [
      { name: 'Open', ms: 5500, build: sceneOpen },
      { name: 'Keys', ms: 7500, build: sceneMpc },
      { name: 'Approvals', ms: 8500, build: sceneApproval },
      { name: 'Agents', ms: 7500, build: sceneAgents },
      { name: 'Compliance', ms: 8500, build: sceneCompliance },
      { name: 'Console', ms: 7000, build: sceneConsole },
      { name: 'Deploy', ms: 7000, build: sceneDeploy },
      { name: 'Close', ms: 5000, build: sceneClose },
    ] },
    fintech: { label: 'Fintech', scenes: [
      { name: 'Who', ms: 5500, build: function () { return sceneIntro({ eyebrow: 'Fintech', title: ['A payments app ', { em: 'moves stablecoins.' }], sub: 'Northwind Pay pays suppliers and customers in ZARP and USDC from custody it controls, through an API.', chips: ['API key', 'Agents', 'Stablecoins', 'Webhooks'] }); } },
      { name: 'Connect', ms: 8500, build: fintechConnect },
      { name: 'Keys', ms: 7500, build: function () { return sceneMpc({ eyebrow: '1 · Keys', sub: 'The ceremony runs across three signing parties. Any two sign; no one party, and not the vendor, can spend alone.' }); } },
      { name: 'Rules', ms: 8000, build: fintechPolicy },
      { name: 'Payment', ms: 8000, build: fintechSmall },
      { name: 'Agents', ms: 7500, build: sceneAgents },
      { name: 'Large', ms: 8500, build: fintechLarge },
      { name: 'Approve', ms: 8500, build: function () { return sceneApproval({ eyebrow: '4 · The approvers', amount: '15 SOL', to: 'to 9WzDXw…AWWM · Solana', why: 'High value', approvers: ['Alice', 'Ada'], required: 2 }); } },
      { name: 'Prove', ms: 7500, build: fintechEvidence },
      { name: 'Pilot', ms: 9000, build: function () { return scenePilot({ eyebrow: 'Starting', title: ['Pilot on ', { em: 'testnet or capped funds.' }], sub: 'A fintech can start in days, self-serve, with its own cloud.',
        pilot: ['Testnet first, then small capped balances', 'EVM and Bitcoin verified on a dev chain and regtest', 'Your policy, your approvers, your webhooks'],
        before: ['Solana and Cosmos accepted by a real network', 'Independent cryptographic audit and pen test', 'SOC 2 Type II if your customers ask for it'] }); } },
    ] },
    bank: { label: 'Bank', scenes: [
      { name: 'Who', ms: 5500, build: function () { return sceneIntro({ eyebrow: 'Bank', title: ['A digital-asset desk, ', { em: 'under bank controls.' }], sub: 'Meridian Bank\'s treasury desk holds and moves assets with the controls its risk committee requires.', chips: ['SSO', 'Dual control', 'Travel Rule', 'Audit trail'] }); } },
      { name: 'Connect', ms: 8500, build: bankConnect },
      { name: 'Keys', ms: 7500, build: function () { return sceneMpc({ eyebrow: '1 · Keys', title: ['Custody ', { em: 'without a single point.' }], sub: 'Three signing parties on separate hosts. Two sign. Losing one host does not freeze the desk or hand anyone the key.', labels: [['a', 'Party A', 'bank DC 1'], ['b', 'Party B', 'bank DC 2'], ['c', 'Party C', 'offline']] }); } },
      { name: 'Controls', ms: 8000, build: bankPolicy },
      { name: 'Request', ms: 8000, build: bankRequest },
      { name: 'Approve', ms: 9000, build: bankApproval },
      { name: 'Evidence', ms: 8500, build: bankAudit },
      { name: 'Pilot', ms: 9500, build: function () { return scenePilot({ eyebrow: 'Starting', title: ['A pilot with ', { em: 'milestones.' }], sub: 'Offered as a bounded pilot while the assurance work completes.',
        pilot: ['One chain, one policy, the desk\'s own approvers', 'Testnet or capped balances, in the bank\'s environment', 'Milestones agreed up front; exit at any point'],
        before: ['SOC 2 Type II: not yet complete', 'Independent cryptographic audit and penetration test', 'Hardware HSM proven; licensing and insurance reviewed'] }); } },
    ] },
    government: { label: 'Government', scenes: [
      { name: 'Who', ms: 5500, build: function () { return sceneIntro({ eyebrow: 'Government', title: ['A treasury that ', { em: 'keeps its own keys.' }], sub: 'A national treasury holds digital assets without depending on a vendor to operate or to sign.', chips: ['Self-hosted', 'Three owners', 'Quorum of officials', 'Auditable'] }); } },
      { name: 'Stand up', ms: 8500, build: govDeploy },
      { name: 'Keys', ms: 7500, build: function () { return sceneMpc({ eyebrow: '2 · Keys', title: ['Three owners. ', { em: 'No one alone.' }], sub: 'Each signing party runs under a different owner. Two of three can sign, so one can be offline, and none can spend alone.', labels: [['a', 'Agency A', 'own host'], ['b', 'Agency B', 'own host'], ['c', 'Treasury', 'offline']] }); } },
      { name: 'Controls', ms: 8500, build: govControls },
      { name: 'Disburse', ms: 10000, build: govApproval },
      { name: 'Evidence', ms: 8500, build: govEvidence },
      { name: 'Proof of concept', ms: 9500, build: function () { return scenePilot({ eyebrow: 'Starting', title: ['A bounded ', { em: 'proof of concept.' }], sub: 'Time-boxed, on testnet or capped funds, with exit criteria written first.',
        pilot: ['Deployed into the customer\'s cluster', 'Their officials as approvers, their policy repo', 'Handover of runbooks and the evidence trail'],
        before: ['Independent audit, pen test and SOC 2 Type II', 'Physical HSM and separate-owner hosting proven', 'Procurement, legal and licensing steps'] }); } },
    ] },
  };
  var FLOW_ORDER = ['overview', 'fintech', 'bank', 'government'];
  var SCENES = [];

  // ---- player -------------------------------------------------------------

  var stage = document.getElementById('stage');
  var segsEl = document.getElementById('segs');
  var flowsEl = document.getElementById('flows');
  var playBtn = document.getElementById('play');
  var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var idx = 0, elapsed = 0, playing = !reduced, last = 0, raf = 0;
  var flow = 'overview';
  var segs = [];
  var flowBtns = {};

  FLOW_ORDER.forEach(function (key) {
    var b = document.createElement('button');
    b.type = 'button'; b.textContent = FLOWS[key].label;
    b.addEventListener('click', function () { setFlow(key); });
    flowsEl.appendChild(b);
    flowBtns[key] = b;
  });

  function setFlow(key) {
    if (!FLOWS[key]) key = 'overview';
    flow = key;
    SCENES = FLOWS[key].scenes;
    FLOW_ORDER.forEach(function (k) { flowBtns[k].classList.toggle('on', k === key); flowBtns[k].setAttribute('aria-pressed', String(k === key)); });
    segsEl.replaceChildren();
    segs = SCENES.map(function (sc, i) {
      var b = document.createElement('button');
      b.type = 'button'; b.className = 'seg'; b.setAttribute('aria-label', sc.name); b.title = sc.name;
      b.appendChild(document.createElement('i'));
      b.addEventListener('click', function () { go(i); });
      segsEl.appendChild(b);
      return b;
    });
    try { history.replaceState(null, '', '?flow=' + key); } catch (e) { /* the player works without it */ }
    go(0);
    if (!playing && !reduced) setPlaying(true);
  }

  function go(i) {
    idx = (i + SCENES.length) % SCENES.length;
    elapsed = 0;
    stage.replaceChildren(SCENES[idx].build());
    segs.forEach(function (b, n) { b.classList.toggle('done', n < idx); b.firstChild.style.width = n < idx ? '100%' : '0'; });
  }
  function setPlaying(p) {
    playing = p; playBtn.textContent = p ? 'Pause' : 'Play';
    cancelAnimationFrame(raf);
    if (p) { last = performance.now(); raf = requestAnimationFrame(tick); }
  }
  function tick(now) {
    if (!playing) return;
    elapsed += now - last; last = now;
    var sc = SCENES[idx];
    segs[idx].firstChild.style.width = Math.min(100, elapsed / sc.ms * 100) + '%';
    if (elapsed >= sc.ms) {
      // A customer flow plays once and stops on its last frame, so a recording
      // ends where the story does; the overview loops as it always has.
      if (idx === SCENES.length - 1 && flow !== 'overview') { elapsed = sc.ms; playing = false; playBtn.textContent = 'Replay'; document.body.setAttribute('data-ended', flow); return; }
      go(idx + 1);
    }
    raf = requestAnimationFrame(tick);
  }

  playBtn.addEventListener('click', function () {
    if (!playing && document.body.hasAttribute('data-ended')) { document.body.removeAttribute('data-ended'); go(0); }
    setPlaying(!playing);
  });
  document.getElementById('restart').addEventListener('click', function () { document.body.removeAttribute('data-ended'); go(0); if (!playing) setPlaying(true); });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowRight') go(idx + 1);
    else if (e.key === 'ArrowLeft') go(idx - 1);
    else if (e.key === ' ') { e.preventDefault(); playBtn.click(); }
  });

  var params = new URLSearchParams(window.location.search);
  // ?record=1 hides the controls so a screen recording is only the story.
  if (params.has('record')) document.body.setAttribute('data-recording', '');
  var wanted = params.get('flow');
  setFlow(FLOWS[wanted] ? wanted : 'overview');
  setPlaying(playing);
})();
