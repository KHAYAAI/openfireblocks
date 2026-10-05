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

  function sceneMpc() {
    var svg = s('svg', { viewBox: '0 0 160 70', preserveAspectRatio: 'xMidYMid meet' });
    var P = { a: [80, 12], b: [34, 54], c: [126, 54] }, C = [80, 38];
    ['a', 'b', 'c'].forEach(function (k) { svg.appendChild(s('line', { 'class': 'link', x1: P[k][0], y1: P[k][1], x2: C[0], y2: C[1] })); });
    // parties A and B sign; C stays offline and the transfer still completes
    ['a', 'b'].forEach(function (k, i) {
      svg.appendChild(s('line', { 'class': 'pulse', x1: P[k][0], y1: P[k][1], x2: C[0], y2: C[1] }, null, 900 + i * 300));
    });
    [['a', 'Party A', 'host 1'], ['b', 'Party B', 'host 2'], ['c', 'Party C', 'offline']].forEach(function (n) {
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
    return scene(head('Keys', ['No single key. ', { em: 'Ever.' }],
      '2-of-3 threshold signing: any two parties sign, the full private key is never assembled in one place, and one host down does not stop a transfer.'),
      h('div', 'body', null, [h('div', 'mpc', null, [svg])]));
  }

  function sceneApproval() {
    var left = h('div', 'card a', null, [
      h('div', 'k', 'Transfer request'),
      h('div', 'v', '42.5 ETH', null),
      h('div', 'sm', 'to 0xd8dA…6045 · Sepolia'),
      h('div', 'row', null, [h('span', 'tag warn', 'Over 10 ETH'), h('span', 'sm', 'policy requires approval')]),
    ], 700);
        var right = h('div', 'card a', null, [
      h('div', 'k', 'Approvals (2 required)'),
      h('div', 'meter', null, [h('span', 'pip on', null, null, 1700), h('span', 'pip on', null, null, 3000)]),
      h('div', 'row', null, [h('span', 'check', '✓'), h('span', 'sm', 'Alice approved · one-time code')]),
      h('div', 'row fade', null, [h('span', 'check', '✓'), h('span', 'sm', 'Ada approved · one-time code')], 3000),
      h('div', 'row fade', null, [h('span', 'tag ok', 'Signed'), h('span', 'sm', 'requester can never approve their own transfer')], 3600),
    ], 1300);
        return scene(head('Policy and approvals', ['Rules decide. ', { em: 'People sign off.' }],
      'Policy is evaluated on the transaction the gateway itself builds. Large moves need named approvers with step-up auth, and the DB refuses to rewrite the record.'),
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

  var SCENES = [
    { name: 'Open', ms: 5500, build: sceneOpen },
    { name: 'Keys', ms: 7500, build: sceneMpc },
    { name: 'Approvals', ms: 8500, build: sceneApproval },
    { name: 'Agents', ms: 7500, build: sceneAgents },
    { name: 'Compliance', ms: 8500, build: sceneCompliance },
    { name: 'Console', ms: 7000, build: sceneConsole },
    { name: 'Deploy', ms: 7000, build: sceneDeploy },
    { name: 'Close', ms: 5000, build: sceneClose },
  ];

  // ---- player -------------------------------------------------------------

  var stage = document.getElementById('stage');
  var segsEl = document.getElementById('segs');
  var playBtn = document.getElementById('play');
  var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var idx = 0, elapsed = 0, playing = !reduced, last = 0, raf = 0;
  var segs = SCENES.map(function (sc, i) {
    var b = document.createElement('button');
    b.type = 'button'; b.className = 'seg'; b.setAttribute('aria-label', sc.name);
    b.appendChild(document.createElement('i'));
    b.addEventListener('click', function () { go(i); });
    segsEl.appendChild(b);
    return b;
  });

  function go(i) {
    idx = (i + SCENES.length) % SCENES.length;
    elapsed = 0;
    stage.replaceChildren(SCENES[idx].build());
    segs.forEach(function (b, n) { b.classList.toggle('done', n < idx); b.firstChild.style.width = n < idx ? '100%' : '0'; });
  }
  function setPlaying(p) {
    playing = p; playBtn.textContent = p ? 'Pause' : 'Play';
    if (p) { last = performance.now(); raf = requestAnimationFrame(tick); } else cancelAnimationFrame(raf);
  }
  function tick(now) {
    if (!playing) return;
    elapsed += now - last; last = now;
    var sc = SCENES[idx];
    segs[idx].firstChild.style.width = Math.min(100, elapsed / sc.ms * 100) + '%';
    if (elapsed >= sc.ms) go(idx + 1);
    raf = requestAnimationFrame(tick);
  }

  playBtn.addEventListener('click', function () { setPlaying(!playing); });
  document.getElementById('restart').addEventListener('click', function () { go(0); if (!playing) setPlaying(true); });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowRight') go(idx + 1);
    else if (e.key === 'ArrowLeft') go(idx - 1);
    else if (e.key === ' ') { e.preventDefault(); setPlaying(!playing); }
  });

  go(0);
  setPlaying(playing);
})();
