// The approval console.
//
// A single static page, served by the gateway, that talks to the same
// JSON API everything else uses. No framework and no build step, for the
// same reason as the dashboard: this ships inside a self-hosted
// customer's gateway container and must add nothing to deploy.
//
// Rules this file keeps:
//   - Every value from the API reaches the page through textContent,
//     never innerHTML. The gateway's CSP already forbids inline script;
//     this makes injected markup impossible as well.
//   - The session token lives in sessionStorage: one tab, gone when the
//     tab closes. An approval console left open on a desk should not
//     outlive the tab.
//   - The console decides nothing. It shows what the API says and sends
//     what the person chose; the rules are enforced by the gateway and
//     the database, and an error from them is shown as-is.
(function () {
  'use strict';

  var TOKEN_KEY = 'ofb.console.token';
  var ORG_KEY = 'ofb.console.org';
  var REFRESH_MS = 15000;

  var state = { token: null, me: null, orgs: [], org: null, view: 'queue', tab: 'pending', timer: null };
  var app = document.getElementById('app');
  var who = document.getElementById('who');

  // ------------------------------------------------------------------ utils

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === null || v === undefined || v === false) return;
        if (k === 'text') node.textContent = v;
        else if (k === 'onclick' || k === 'onsubmit' || k === 'oninput') node[k] = v;
        else if (k === 'className') node.className = v;
        else node.setAttribute(k, v === true ? '' : String(v));
      });
    }
    (children || []).forEach(function (c) {
      if (c === null || c === undefined || c === false) return;
      node.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    });
    return node;
  }

  function show(nodes) {
    // Same rule as el(): empty slots are skipped, never rendered as text.
    app.replaceChildren.apply(app, nodes.filter(function (n) { return n !== null && n !== undefined && n !== false; }));
  }

  function notice(text, kind) {
    return el('div', { className: 'notice ' + (kind || ''), role: kind === 'error' ? 'alert' : null, text: text });
  }

  function storage(get, key, value) {
    try {
      if (get) return sessionStorage.getItem(key);
      if (value === null) sessionStorage.removeItem(key);
      else sessionStorage.setItem(key, value);
    } catch (e) {
      /* private mode: the session simply does not persist */
    }
    return null;
  }

  function api(method, path, body) {
    var headers = { 'content-type': 'application/json' };
    if (state.token) headers.authorization = 'Bearer ' + state.token;
    return fetch(path, { method: method, headers: headers, body: body ? JSON.stringify(body) : undefined })
      .then(function (res) {
        return res.text().then(function (text) {
          var data = null;
          try { data = text ? JSON.parse(text) : null; } catch (e) { data = { message: text }; }
          return { status: res.status, ok: res.ok, body: data };
        });
      });
  }

  function errorText(r) {
    var m = r.body && r.body.message;
    if (Array.isArray(m)) m = m.join('; ');
    return m || ('request failed (' + r.status + ')');
  }

  // Wei (a base-10 string) to ETH, exactly: BigInt, not floating point.
  // An approver is reading an amount of money; it must not be rounded.
  function formatWei(wei) {
    try {
      var v = BigInt(wei || '0');
      var unit = BigInt('1000000000000000000');
      var whole = v / unit;
      var frac = (v % unit).toString().padStart(18, '0').replace(/0+$/, '');
      return whole.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',') + (frac ? '.' + frac : '') + ' ETH';
    } catch (e) {
      return String(wei) + ' wei';
    }
  }

  var CHAINS = { 1: 'Ethereum mainnet', 11155111: 'Sepolia testnet', 17000: 'Holesky testnet' };
  function chainName(id) { return CHAINS[id] || ('chain ' + id); }

  function relative(iso) {
    var ms = new Date(iso).getTime() - Date.now();
    var abs = Math.abs(ms);
    var mins = Math.round(abs / 60000);
    var s = mins < 1 ? 'less than a minute' : mins < 90 ? mins + ' min' : Math.round(mins / 60) + ' h';
    return ms >= 0 ? 'in ' + s : s + ' ago';
  }

  function pill(status) { return el('span', { className: 'pill ' + status, text: status }); }

  function role() { return state.org ? state.org.role : null; }
  function canDecide() { return role() === 'admin' || role() === 'approver'; }
  function isAdmin() { return role() === 'admin'; }

  // ---------------------------------------------------------- signing in

  function renderHeader() {
    who.replaceChildren();
    if (!state.me) return;
    who.appendChild(document.createTextNode(state.me.email + (state.org ? ' · ' + state.org.role : '')));
    who.appendChild(el('button', { type: 'button', text: 'Sign out', onclick: signOut }));
  }

  function signOut() {
    if (state.timer) clearInterval(state.timer);
    state = { token: null, me: null, orgs: [], org: null, view: 'queue', tab: 'pending', timer: null };
    storage(false, TOKEN_KEY, null);
    storage(false, ORG_KEY, null);
    renderHeader();
    renderLogin();
  }

  function renderLogin(message) {
    var email = el('input', { type: 'email', autocomplete: 'username', required: true, id: 'email' });
    var password = el('input', { type: 'password', autocomplete: 'current-password', required: true, id: 'password' });
    var status = el('div');
    var ssoSlot = el('div');
    var form = el('form', {
      onsubmit: function (e) {
        e.preventDefault();
        status.replaceChildren(notice('Signing in…'));
        api('POST', '/v1/auth/login', { email: email.value, password: password.value }).then(function (r) {
          if (!r.ok) return status.replaceChildren(notice(errorText(r), 'error'));
          if (r.body.status === 'mfa_required') return renderMfa(email.value, r.body.challengeToken);
          signedIn(r.body.accessToken);
        });
      },
    }, [
      el('label', { for: 'email', text: 'Email' }), email,
      el('label', { for: 'password', text: 'Password' }), password,
      el('div', { className: 'actions' }, [el('button', { className: 'primary', type: 'submit', text: 'Sign in' })]),
    ]);
    show([
      el('h1', { text: 'Sign in to approve transfers' }),
      message ? notice(message, 'error') : null,
      el('div', { className: 'card' }, [form, status, ssoSlot]),
      el('p', { className: 'muted', text: 'Approving needs two-factor authentication, or single sign-on through your organisation.' }),
    ]);
    api('GET', '/auth/sso/status').then(function (r) {
      if (r.ok && r.body && r.body.enabled) {
        ssoSlot.replaceChildren(el('div', { className: 'actions' }, [
          el('button', { type: 'button', text: 'Sign in with your organisation (SSO)', onclick: function () {
            window.location.href = '/auth/sso/authorize';
          } }),
        ]));
      }
    });
  }

  function renderMfa(email, challengeToken) {
    var code = el('input', { inputmode: 'numeric', autocomplete: 'one-time-code', pattern: '[0-9]{6}', maxlength: '6', required: true, id: 'code' });
    var status = el('div');
    show([
      el('h1', { text: 'Two-factor code' }),
      el('div', { className: 'card' }, [
        el('form', {
          onsubmit: function (e) {
            e.preventDefault();
            api('POST', '/v1/auth/mfa/verify', { email: email, challengeToken: challengeToken, code: code.value }).then(function (r) {
              if (!r.ok) return status.replaceChildren(notice(errorText(r), 'error'));
              signedIn(r.body.accessToken);
            });
          },
        }, [
          el('label', { for: 'code', text: 'Code from your authenticator app' }), code,
          el('div', { className: 'actions' }, [el('button', { className: 'primary', type: 'submit', text: 'Continue' })]),
        ]),
        status,
      ]),
    ]);
    code.focus();
  }

  // SSO lands the browser here (set WORKOS_REDIRECT_URI to
  // https://<gateway>/console/sso-callback). The code is exchanged by the
  // gateway's own callback endpoint, which answers JSON; the token never
  // appears in a URL.
  function completeSso() {
    show([el('p', { className: 'muted', text: 'Completing single sign-on…' })]);
    return api('GET', '/auth/sso/callback' + window.location.search).then(function (r) {
      history.replaceState(null, '', '/console');
      if (!r.ok) return renderLogin(errorText(r));
      signedIn(r.body.accessToken);
    });
  }

  function signedIn(token) {
    state.token = token;
    storage(false, TOKEN_KEY, token);
    loadMe();
  }

  function loadMe() {
    api('GET', '/me/organisations').then(function (r) {
      if (r.status === 401) return signOut();
      if (!r.ok) return show([notice(errorText(r), 'error')]);
      state.me = r.body.user;
      state.orgs = r.body.organisations;
      var remembered = storage(true, ORG_KEY);
      state.org = state.orgs.filter(function (o) { return o.customerId === remembered; })[0] ||
        (state.orgs.length === 1 ? state.orgs[0] : null);
      renderHeader();
      if (!state.orgs.length) {
        return show([
          el('h1', { text: 'No organisations yet' }),
          notice('You are signed in, but no organisation has given you a role. Ask one of its admins to add ' + state.me.email + '.'),
        ]);
      }
      if (!state.org) return renderOrgPicker();
      openQueue();
    });
  }

  function renderOrgPicker() {
    show([el('h1', { text: 'Choose an organisation' })].concat(state.orgs.map(function (o) {
      return el('div', { className: 'card link', role: 'button', tabindex: '0', onclick: function () {
        state.org = o;
        storage(false, ORG_KEY, o.customerId);
        renderHeader();
        openQueue();
      } }, [el('div', { className: 'row' }, [el('strong', { text: o.name }), el('span', { className: 'muted', text: o.role })])]);
    })));
  }

  // ----------------------------------------------------------- the queue

  function orgPath(rest) { return '/organisations/' + encodeURIComponent(state.org.customerId) + rest; }

  function nav() {
    var items = [['queue', 'Approvals'], ['policy', 'Policy'], ['members', 'People']];
    return el('div', { className: 'tabs' }, items.map(function (it) {
      return el('button', { type: 'button', className: state.view === it[0] ? 'active' : '', text: it[1], onclick: function () {
        state.view = it[0];
        if (it[0] === 'queue') openQueue();
        else if (it[0] === 'policy') renderPolicy();
        else renderMembers();
      } });
    }).concat(state.orgs.length > 1 ? [el('button', { type: 'button', text: 'Switch organisation', onclick: renderOrgPicker })] : []));
  }

  function openQueue() {
    state.view = 'queue';
    if (state.timer) clearInterval(state.timer);
    renderQueue();
    state.timer = setInterval(function () { if (state.view === 'queue' && !document.hidden) renderQueue(true); }, REFRESH_MS);
  }

  function renderQueue(quiet) {
    var tabs = el('div', { className: 'tabs' }, ['pending', 'approved', 'rejected', 'expired'].map(function (t) {
      return el('button', { type: 'button', className: state.tab === t ? 'active' : '', text: t, onclick: function () {
        state.tab = t; renderQueue();
      } });
    }));
    if (!quiet) show([el('h1', { text: state.org.name }), nav(), tabs, el('p', { className: 'muted', text: 'Loading…' })]);
    api('GET', orgPath('/approvals?status=' + state.tab)).then(function (r) {
      if (r.status === 401) return signOut();
      if (state.view !== 'queue') return;
      var body = !r.ok ? [notice(errorText(r), 'error')]
        : r.body.length === 0 ? [el('p', { className: 'muted', text: state.tab === 'pending' ? 'Nothing is waiting for approval.' : 'None.' })]
        : r.body.map(summaryCard);
      show([el('h1', { text: state.org.name }), nav(), tabs].concat(body));
    });
  }

  function summaryCard(a) {
    var s = a.summary || {};
    return el('div', { className: 'card link', role: 'button', tabindex: '0', onclick: function () { renderDetail(a.approvalId); } }, [
      el('div', { className: 'row' }, [el('span', { className: 'amount', text: formatWei(s.valueWei) }), pill(a.status)]),
      el('div', { className: 'mono', text: 'to ' + (s.to || '?') }),
      el('div', { className: 'row muted' }, [
        el('span', { text: a.approvals + ' of ' + a.requiredApprovals + ' approvals · ' + chainName(s.chainId) }),
        el('span', { text: a.status === 'pending' ? 'expires ' + relative(a.expiresAt) : relative(a.decidedAt || a.createdAt) }),
      ]),
    ]);
  }

  // ------------------------------------------------------------- detail

  function renderDetail(approvalId, flash) {
    state.view = 'detail';
    api('GET', orgPath('/approvals/' + encodeURIComponent(approvalId))).then(function (r) {
      if (r.status === 401) return signOut();
      if (!r.ok) return show([nav(), notice(errorText(r), 'error')]);
      var a = r.body;
      var s = a.summary || {};
      var mine = (a.decisions || []).filter(function (d) { return d.userId === state.me.id; })[0];
      var initiatedByMe = a.initiatedByUserId && a.initiatedByUserId === state.me.id;

      var facts = el('dl', null, [
        el('dt', { text: 'Amount' }), el('dd', { className: 'amount', text: formatWei(s.valueWei) }),
        el('dt', { text: 'To' }), el('dd', { className: 'mono', text: s.to || '?' }),
        el('dt', { text: 'Network' }), el('dd', { text: chainName(s.chainId) }),
        el('dt', { text: 'Requested by' }), el('dd', { text: a.initiatedBy }),
        el('dt', { text: 'Requested' }), el('dd', { text: new Date(a.createdAt).toLocaleString() }),
        el('dt', { text: a.status === 'pending' ? 'Expires' : 'Closed' }),
        el('dd', { text: a.status === 'pending' ? relative(a.expiresAt) + ' (' + new Date(a.expiresAt).toLocaleString() + ')' : new Date(a.decidedAt || a.expiresAt).toLocaleString() }),
        s.data && s.data !== '0x' ? el('dt', { text: 'Contract call' }) : null,
        s.data && s.data !== '0x' ? el('dd', { className: 'mono', text: s.data }) : null,
      ]);

      var reasons = (s.reasons || []).length
        ? el('div', null, [el('h2', { text: 'Why this needs approval' }), el('ul', { className: 'reasons' }, s.reasons.map(function (x) { return el('li', { text: x }); }))])
        : null;

      var decisions = el('div', null, [el('h2', { text: 'Decisions (' + a.approvals + ' of ' + a.requiredApprovals + ' approvals)' })].concat(
        (a.decisions || []).length === 0 ? [el('p', { className: 'muted', text: 'No decisions yet.' })] :
        a.decisions.map(function (d) {
          return el('div', { className: 'card' }, [
            el('div', { className: 'row' }, [el('strong', { text: d.fullName + ' (' + d.email + ')' }), pill(d.decision === 'approve' ? 'approved' : 'rejected')]),
            el('div', { className: 'muted', text: new Date(d.createdAt).toLocaleString() + ' · verified by ' + (d.stepUp === 'sso' ? 'single sign-on' : 'one-time code') }),
            d.reason ? el('div', { text: d.reason }) : null,
          ]);
        })));

      show([
        nav(),
        el('div', { className: 'row' }, [el('h1', { text: 'Transfer approval' }), pill(a.status)]),
        flash || null,
        el('div', { className: 'card' }, [facts, reasons]),
        decisionPanel(a, mine, initiatedByMe),
        decisions,
        el('div', { className: 'actions' }, [el('button', { type: 'button', text: 'Back to approvals', onclick: openQueue })]),
      ]);
    });
  }

  function decisionPanel(a, mine, initiatedByMe) {
    if (a.status !== 'pending') return null;
    if (initiatedByMe) return notice('You requested this transfer, so you cannot approve or reject it. Someone else must.');
    if (mine) return notice('You ' + (mine.decision === 'approve' ? 'approved' : 'rejected') + ' this. Waiting for others.', 'ok');
    if (!canDecide()) return notice('Your role (' + role() + ') can see approvals but not decide on them.');
    var sso = state.me.authProvider === 'workos_sso';
    if (!sso && !state.me.mfaEnabled) return notice('Turn on two-factor authentication before approving or rejecting transfers.', 'error');

    var code = sso ? null : el('input', { inputmode: 'numeric', autocomplete: 'one-time-code', pattern: '[0-9]{6}', maxlength: '6', id: 'totp' });
    var reason = el('textarea', { rows: '2', id: 'reason', placeholder: 'Required to reject; optional to approve' });
    var status = el('div');
    var buttons = [];

    function send(decision) {
      if (decision === 'reject' && !reason.value.trim()) return status.replaceChildren(notice('Say why you are rejecting it.', 'error'));
      if (code && !/^\d{6}$/.test(code.value)) return status.replaceChildren(notice('Enter the six-digit code from your authenticator.', 'error'));
      buttons.forEach(function (b) { b.disabled = true; });
      status.replaceChildren(notice('Recording your decision…'));
      var body = { decision: decision };
      if (reason.value.trim()) body.reason = reason.value.trim();
      if (code) body.totpCode = code.value;
      api('POST', orgPath('/approvals/' + encodeURIComponent(a.approvalId) + '/decisions'), body).then(function (r) {
        if (r.ok) {
          return renderDetail(a.approvalId, notice(decision === 'approve'
            ? (r.body.status === 'approved' ? 'Approved. The transfer has its approvals and will now be signed.' : 'Your approval is recorded. It still needs ' + (r.body.requiredApprovals - r.body.approvals) + ' more.')
            : 'Rejected. The transfer will not be signed.', 'ok'));
        }
        buttons.forEach(function (b) { b.disabled = false; });
        if (code) code.value = '';
        status.replaceChildren(notice(errorText(r), 'error'));
      });
    }

    buttons = [
      el('button', { type: 'button', className: 'approve', text: 'Approve', onclick: function () { send('approve'); } }),
      el('button', { type: 'button', className: 'reject', text: 'Reject', onclick: function () { send('reject'); } }),
    ];
    return el('div', { className: 'card' }, [
      el('h2', { text: 'Your decision' }),
      el('label', { for: 'reason', text: 'Reason' }), reason,
      code ? el('label', { for: 'totp', text: 'Code from your authenticator app' }) : null, code,
      el('div', { className: 'actions' }, buttons),
      status,
    ]);
  }

  // ------------------------------------------------------------- policy

  function renderPolicy() {
    state.view = 'policy';
    api('GET', orgPath('/approval-policy')).then(function (r) {
      if (!r.ok) return show([nav(), notice(errorText(r), 'error')]);
      var p = r.body;
      var parts = [
        el('h1', { text: 'Approval policy' }), nav(),
        el('div', { className: 'card' }, [el('dl', null, [
          el('dt', { text: 'Approvals needed' }), el('dd', { text: String(p.requiredApprovals) + (p.requiredApprovals >= 2 ? ' (dual control)' : ' (single approver)') }),
          el('dt', { text: 'Time to decide' }), el('dd', { text: p.windowMinutes + ' minutes' }),
          el('dt', { text: 'Set' }), el('dd', { text: p.isDefault ? 'default' : new Date(p.updatedAt).toLocaleString() }),
        ])]),
        el('p', { className: 'muted', text: 'The person who requests a transfer can never approve it. Each person decides once. One rejection stops it. A change here applies to new requests, not ones already waiting.' }),
      ];
      if (isAdmin()) {
        var req = el('input', { type: 'number', min: '1', max: '10', value: String(p.requiredApprovals), id: 'req' });
        var win = el('input', { type: 'number', min: '5', max: '10080', value: String(p.windowMinutes), id: 'win' });
        var status = el('div');
        parts.push(el('div', { className: 'card' }, [
          el('h2', { text: 'Change' }),
          el('form', { onsubmit: function (e) {
            e.preventDefault();
            api('PUT', orgPath('/approval-policy'), { requiredApprovals: Number(req.value), windowMinutes: Number(win.value) }).then(function (r2) {
              status.replaceChildren(r2.ok ? notice('Saved.', 'ok') : notice(errorText(r2), 'error'));
              if (r2.ok) setTimeout(renderPolicy, 600);
            });
          } }, [
            el('label', { for: 'req', text: 'Approvals needed (1–10)' }), req,
            el('label', { for: 'win', text: 'Minutes to decide (5–10080)' }), win,
            el('div', { className: 'actions' }, [el('button', { type: 'submit', className: 'primary', text: 'Save policy' })]),
          ]),
          status,
        ]));
      }
      show(parts);
    });
  }

  // ------------------------------------------------------------- people

  var ROLES = ['admin', 'approver', 'operator', 'auditor', 'viewer', 'billing_admin'];

  function renderMembers() {
    state.view = 'members';
    api('GET', orgPath('/members')).then(function (r) {
      if (!r.ok) return show([nav(), notice(errorText(r), 'error')]);
      var rows = r.body.map(function (m) {
        return el('tr', null, [
          el('td', null, [el('div', { text: m.fullName }), el('div', { className: 'muted mono', text: m.email })]),
          el('td', { text: m.role }),
          el('td', { className: 'muted', text: m.authProvider === 'workos_sso' ? 'SSO' : (m.mfaEnabled ? '2FA on' : '2FA off') }),
        ]);
      });
      var parts = [
        el('h1', { text: 'People' }), nav(),
        el('div', { className: 'card' }, [el('table', null, [
          el('thead', null, [el('tr', null, [el('th', { text: 'Person' }), el('th', { text: 'Role' }), el('th', { text: 'Sign-in' })])]),
          el('tbody', null, rows),
        ])]),
        el('p', { className: 'muted', text: 'Approvers approve; operators request transfers; admins do both, but never approve their own. Auditors and viewers can only read.' }),
      ];
      if (isAdmin()) {
        var email = el('input', { type: 'email', id: 'm-email', required: true, placeholder: 'person@example.com' });
        var sel = el('select', { id: 'm-role' }, ROLES.map(function (x) { return el('option', { value: x, text: x }); }));
        sel.value = 'approver';
        var status = el('div');
        parts.push(el('div', { className: 'card' }, [
          el('h2', { text: 'Add a person or change a role' }),
          el('form', { className: 'inline', onsubmit: function (e) {
            e.preventDefault();
            api('PUT', orgPath('/members'), { email: email.value, role: sel.value }).then(function (r2) {
              status.replaceChildren(r2.ok ? notice(r2.body.email + ' is now ' + r2.body.role + '.', 'ok') : notice(errorText(r2), 'error'));
              if (r2.ok) setTimeout(renderMembers, 800);
            });
          } }, [
            el('div', null, [el('label', { for: 'm-email', text: 'Email' }), email]),
            el('div', null, [el('label', { for: 'm-role', text: 'Role' }), sel]),
            el('button', { type: 'submit', className: 'primary', text: 'Save' }),
          ]),
          el('p', { className: 'muted', text: 'The person must already have an account (registered, or signed in once with SSO).' }),
          status,
        ]));
      }
      show(parts);
    });
  }

  // --------------------------------------------------------------- boot

  if (window.location.pathname === '/console/sso-callback' && window.location.search.indexOf('code=') !== -1) {
    completeSso();
  } else {
    state.token = storage(true, TOKEN_KEY);
    if (state.token) loadMe();
    else renderLogin();
  }
})();
