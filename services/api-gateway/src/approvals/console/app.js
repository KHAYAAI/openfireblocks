// The OpenFireblocks console: one sign-in, one shell, everything a
// person with a role in an organisation can see or do.
//
// Replaces two separate surfaces (the read-only /dashboard, keyed off a
// tenant-wide API key, and the earlier approvals-only /console, keyed off
// a person's JWT) with one. The API underneath is unchanged for either:
// this file only talks to the JWT-authenticated, role-checked
// /organisations/:customerId/... routes, which read through the exact
// same services /dashboard always has -- so there is one truth about a
// customer's keys and balances, not two code paths that can disagree.
//
// Rules this file keeps, carried over from the approvals-only console:
//   - Every value from the API reaches the page through textContent or
//     el()'s `text` attribute, never innerHTML. The gateway's CSP forbids
//     inline script; this makes injected markup impossible as well.
//   - The session token lives in sessionStorage: one tab, gone when the
//     tab closes.
//   - This console decides nothing. It shows what the API says and sends
//     what the person chose; the rules are enforced by the gateway and
//     the database, and an error from them is shown as-is.
//
// No framework, no build step: this ships inside the gateway container a
// self-hosted customer is already running, same as /dashboard always has.
// Split across two files for one reason, the pattern the uploaded brand
// kit itself uses -- plain <script> tags sharing state through one global
// rather than a bundler: this file (app.js) is the shell, router, API
// client and formatters; views.js is the page renderers. This file loads
// first and defines window.OFB; views.js reads it and defines
// window.OFBViews, which this file's router reads in turn. index.html
// must load them in that order -- views.js reads window.OFB once, at
// parse time, not lazily, so loading it first leaves it permanently
// holding undefined.
(function () {
  'use strict';

  var TOKEN_KEY = 'ofb.console.token';
  var ORG_KEY = 'ofb.console.org';
  var BADGE_REFRESH_MS = 20000;

  var OFB = window.OFB = {
    state: {
      token: null,
      me: null,
      orgs: [],
      org: null,
      route: { view: null, id: null },
      pendingCount: null,
    },
  };

  var root = document.getElementById('root');

  // ------------------------------------------------------------ el/dom

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === null || v === undefined || v === false) return;
        if (k === 'text') node.textContent = v;
        else if (k === 'html_UNSAFE_NEVER_USE') throw new Error('innerHTML is not available in this console');
        else if (/^on[a-z]/.test(k)) node[k] = v;
        else if (k === 'className') node.className = v;
        else node.setAttribute(k, v === true ? '' : String(v));
      });
    }
    (children || []).forEach(function (c) {
      if (c === null || c === undefined || c === false) return;
      node.appendChild(typeof c === 'string' || typeof c === 'number' ? document.createTextNode(String(c)) : c);
    });
    return node;
  }
  OFB.el = el;

  function frag(nodes) {
    var f = document.createDocumentFragment();
    (nodes || []).forEach(function (n) { if (n !== null && n !== undefined && n !== false) f.appendChild(n); });
    return f;
  }
  OFB.frag = frag;

  function replace(container, nodes) {
    container.replaceChildren();
    container.appendChild(frag(nodes));
  }
  OFB.replace = replace;

  function notice(text, kind) {
    return el('div', { className: 'notice ' + (kind || ''), role: kind === 'error' ? 'alert' : null, text: text });
  }
  OFB.notice = notice;

  function banner(title, text, kind) {
    return el('div', { className: 'banner ' + (kind || '') }, [
      el('div', null, [el('strong', { text: title }), el('span', { text: text })]),
    ]);
  }
  OFB.banner = banner;

  function skeleton() {
    return el('div', { className: 'stack' }, [
      el('div', { className: 'skel', style: 'height:84px' }),
      el('div', { className: 'skel', style: 'height:180px' }),
    ]);
  }
  OFB.skeleton = skeleton;

  // ------------------------------------------------------------ storage

  function storage(get, key, value) {
    try {
      if (get) return sessionStorage.getItem(key);
      if (value === null) sessionStorage.removeItem(key);
      else sessionStorage.setItem(key, value);
    } catch (e) { /* private mode: the session simply does not persist */ }
    return null;
  }

  // ------------------------------------------------------------------ api

  function api(method, path, body) {
    var headers = { 'content-type': 'application/json' };
    if (OFB.state.token) headers.authorization = 'Bearer ' + OFB.state.token;
    return fetch(path, { method: method, headers: headers, body: body ? JSON.stringify(body) : undefined })
      .then(function (res) {
        return res.text().then(function (text) {
          var data = null;
          try { data = text ? JSON.parse(text) : null; } catch (e) { data = { message: text }; }
          return { status: res.status, ok: res.ok, body: data };
        });
      });
  }
  OFB.api = api;

  function errorText(r) {
    var m = r.body && r.body.message;
    if (Array.isArray(m)) m = m.join('; ');
    return m || ('request failed (' + r.status + ')');
  }
  OFB.errorText = errorText;

  function orgPath(rest) {
    return '/organisations/' + encodeURIComponent(OFB.state.org.customerId) + rest;
  }
  OFB.orgPath = orgPath;

  // --------------------------------------------------------- formatters

  function fmtBaseUnits(raw, decimals, opts) {
    opts = opts || {};
    if (raw === null || raw === undefined) return '—';
    try {
      var neg = String(raw).charAt(0) === '-';
      var digits = (neg ? String(raw).slice(1) : String(raw)).padStart((decimals || 0) + 1, '0');
      var whole = digits.slice(0, digits.length - (decimals || 0)) || '0';
      var frac = decimals ? digits.slice(digits.length - decimals).replace(/0+$/, '') : '';
      whole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
      return (neg ? '-' : '') + whole + (frac ? '.' + frac : '') + (opts.suffix ? ' ' + opts.suffix : '');
    } catch (e) { return String(raw); }
  }
  OFB.fmtBaseUnits = fmtBaseUnits;

  function fmtWei(wei) { return fmtBaseUnits(wei || '0', 18, { suffix: 'ETH' }); }
  OFB.fmtWei = fmtWei;

  function fmtZar(n) {
    if (n === null || n === undefined) return '—';
    var num = Number(n);
    return 'R' + num.toLocaleString('en-ZA', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }
  OFB.fmtZar = fmtZar;

  var CHAINS = { 1: 'Ethereum mainnet', 11155111: 'Sepolia testnet', 17000: 'Holesky testnet' };
  function chainName(id) { return CHAINS[id] || (id ? 'chain ' + id : '—'); }
  OFB.chainName = chainName;

  function short(hex, n) {
    n = n || 6;
    if (!hex) return '—';
    var s = String(hex);
    return s.length <= n * 2 + 2 ? s : s.slice(0, n + 2) + '…' + s.slice(-n);
  }
  OFB.short = short;

  function relative(iso) {
    if (!iso) return '—';
    var ms = new Date(iso).getTime() - Date.now();
    var abs = Math.abs(ms);
    var mins = Math.round(abs / 60000);
    var s = mins < 1 ? 'less than a minute' : mins < 90 ? mins + ' min' : Math.round(mins / 60) + ' h';
    return ms >= 0 ? 'in ' + s : s + ' ago';
  }
  OFB.relative = relative;

  function fmtDate(iso) { return iso ? new Date(iso).toLocaleString() : '—'; }
  OFB.fmtDate = fmtDate;

  function tag(status, label) {
    return el('span', { className: 'tag ' + String(status || '').toLowerCase(), text: (label || status || '').replace(/_/g, ' ') });
  }
  OFB.tag = tag;

  // ------------------------------------------------------------- roles

  function role() { return OFB.state.org ? OFB.state.org.role : null; }
  OFB.role = role;
  function isAdmin() { return role() === 'admin'; }
  OFB.isAdmin = isAdmin;
  function canDecide() { return role() === 'admin' || role() === 'approver'; }
  OFB.canDecide = canDecide;
  function canInitiate() { return role() === 'admin' || role() === 'operator' || role() === 'user'; }
  OFB.canInitiate = canInitiate;

  // -------------------------------------------------------------- nav

  var NAV = [
    { items: [{ id: 'overview', label: 'Overview' }] },
    { label: 'Treasury', items: [
      { id: 'keys', label: 'Keys' },
      { id: 'transactions', label: 'Transactions' },
      { id: 'agents', label: 'Agents' },
    ] },
    { label: 'Controls', items: [
      { id: 'approvals', label: 'Approvals', badge: 'pendingCount' },
      { id: 'policy', label: 'Approval policy' },
      { id: 'people', label: 'People' },
    ] },
    { label: 'Compliance', items: [
      { id: 'travel-rule', label: 'Travel Rule' },
      { id: 'reconciliation', label: 'Reconciliation' },
      { id: 'compliance', label: 'Thresholds' },
    ] },
    { label: 'Integration', items: [{ id: 'webhooks', label: 'Webhooks' }] },
  ];
  var TITLES = { 'travel-rule': 'Travel Rule', reconciliation: 'Reconciliation', compliance: 'Thresholds', webhooks: 'Webhooks' };
  function titleFor(view) {
    if (TITLES[view]) return TITLES[view];
    for (var g = 0; g < NAV.length; g++) for (var i = 0; i < NAV[g].items.length; i++) {
      if (NAV[g].items[i].id === view) return NAV[g].items[i].label;
    }
    return 'OpenFireblocks';
  }

  // ------------------------------------------------------------- route

  function parseHash() {
    var h = (window.location.hash || '#/overview').replace(/^#\/?/, '');
    var parts = h.split('/').filter(Boolean).map(decodeURIComponent);
    return { view: parts[0] || 'overview', id: parts[1] || null };
  }

  function navigate(view, id) {
    window.location.hash = '#/' + view + (id ? '/' + encodeURIComponent(id) : '');
  }
  OFB.navigate = navigate;

  function onRouteChange() {
    if (!OFB.state.org) return;
    OFB.state.route = parseHash();
    renderRoute();
  }

  function renderRoute() {
    var r = OFB.state.route;
    var content = document.getElementById('content');
    var topTitle = document.getElementById('topTitle');
    var known = ['overview', 'keys', 'transactions', 'agents', 'approvals', 'policy', 'people', 'travel-rule', 'reconciliation', 'compliance', 'webhooks'];
    if (known.indexOf(r.view) === -1) { navigate('overview'); return; }
    topTitle.textContent = titleFor(r.view);
    document.querySelectorAll('.navitem').forEach(function (b) {
      b.classList.toggle('active', b.getAttribute('data-nav') === r.view);
    });
    closeSidebar();
    replace(content, [skeleton()]);
    var key = r.view.replace(/-([a-z])/g, function (_, c) { return c.toUpperCase(); });
    var fn = window.OFBViews && window.OFBViews[key];
    if (!fn) { replace(content, [notice('This view is not implemented.', 'error')]); return; }
    fn(r.id, content);
  }
  OFB.rerender = renderRoute;

  window.addEventListener('hashchange', onRouteChange);

  // ------------------------------------------------------------ badges

  function refreshBadges() {
    if (!OFB.state.org) return;
    api('GET', orgPath('/approvals?status=pending')).then(function (r) {
      if (!r.ok) return;
      OFB.state.pendingCount = r.body.length;
      document.querySelectorAll('[data-badge="pendingCount"]').forEach(function (b) {
        if (r.body.length > 0) { b.textContent = String(r.body.length); b.style.display = ''; }
        else b.style.display = 'none';
      });
    });
  }
  OFB.refreshBadges = refreshBadges;
  setInterval(function () { if (OFB.state.org && !document.hidden) refreshBadges(); }, BADGE_REFRESH_MS);

  // -------------------------------------------------------------- sidebar (mobile)

  function closeSidebar() { var s = document.querySelector('.sidebar'); if (s) s.classList.remove('open'); }
  function toggleSidebar() { var s = document.querySelector('.sidebar'); if (s) s.classList.toggle('open'); }

  // -------------------------------------------------------------- shell

  function brandMark() { return el('span', { className: 'brand-mark', 'aria-hidden': 'true' }); }

  function renderShell() {
    var sidebarNav = NAV.map(function (group) {
      var items = group.items.map(function (it) {
        return el('button', {
          type: 'button', className: 'navitem', 'data-nav': it.id,
          onclick: function () { navigate(it.id); },
        }, [
          el('span', { text: it.label }),
          it.badge ? el('span', { className: 'count', 'data-badge': it.badge, style: 'display:none' }) : null,
        ]);
      });
      return el('div', { className: 'navgroup' }, [
        group.label ? el('div', { className: 'eyebrow navlabel', text: group.label }) : null,
      ].concat(items));
    });

    var sidebar = el('div', { className: 'sidebar' }, [
      el('div', { className: 'brand' }, [brandMark(), el('div', null, [
        el('div', { className: 'brand-word', text: 'OpenFireblocks' }),
      ])]),
      el('button', {
        type: 'button', className: 'org-switch', title: 'Switch organisation',
        onclick: function () { OFB.state.org = null; storage(false, ORG_KEY, null); renderOrgPicker(); },
      }, [
        el('div', { className: 'name', text: OFB.state.org.name }),
        el('div', { className: 'role', text: role() }),
      ]),
      el('nav', null, sidebarNav),
      el('div', { className: 'sidebar-foot' }, [
        el('div', { className: 'whoami' }, [
          el('div', { text: OFB.state.me.email }),
          el('div', { className: 'role', text: OFB.state.me.authProvider === 'workos_sso' ? 'SSO' : (OFB.state.me.mfaEnabled ? '2FA on' : '2FA off') }),
        ]),
        el('button', { type: 'button', className: 'ghost sm signout', text: 'Sign out', onclick: signOut }),
      ]),
    ]);

    var main = el('div', { className: 'main' }, [
      el('div', { className: 'topbar' }, [
        el('div', { className: 'row', style: 'gap:10px' }, [
          el('button', { type: 'button', className: 'ghost sm menu-toggle', text: '☰', 'aria-label': 'Menu', onclick: toggleSidebar }),
          el('h1', { id: 'topTitle', text: 'Overview' }),
        ]),
        el('div', { className: 'topbar-actions' }),
      ]),
      el('div', { className: 'content', id: 'content' }),
    ]);

    replace(root, [el('div', { className: 'shell' }, [sidebar, main])]);
    refreshBadges();
    onRouteChange();
  }

  // ---------------------------------------------------------- signing in

  function signOut() {
    OFB.state = { token: null, me: null, orgs: [], org: null, route: { view: null, id: null }, pendingCount: null };
    storage(false, TOKEN_KEY, null);
    storage(false, ORG_KEY, null);
    window.location.hash = '';
    renderLogin();
  }

  function authCard(children) {
    return el('div', { className: 'auth-shell' }, [
      el('div', { className: 'auth-card' }, [
        el('div', { className: 'auth-brand' }, [brandMark(), el('span', { className: 'brand-word', text: 'OpenFireblocks' })]),
      ].concat(children)),
    ]);
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
      el('div', { className: 'actions' }, [el('button', { className: 'accent block', type: 'submit', text: 'Sign in' })]),
    ]);
    replace(root, [authCard([
      message ? notice(message, 'error') : null,
      el('div', { className: 'card' }, [form, status, ssoSlot]),
      el('p', { className: 'muted', style: 'text-align:center;font-size:12.5px', text: 'Deciding on a transfer needs two-factor authentication, or single sign-on.' }),
    ])]);
    api('GET', '/auth/sso/status').then(function (r) {
      if (r.ok && r.body && r.body.enabled) {
        ssoSlot.replaceChildren(el('div', { className: 'actions' }, [
          el('button', { type: 'button', className: 'block', text: 'Continue with single sign-on', onclick: function () {
            window.location.href = '/auth/sso/authorize';
          } }),
        ]));
      }
    });
    email.focus();
  }

  function renderMfa(email, challengeToken) {
    var code = el('input', { inputmode: 'numeric', autocomplete: 'one-time-code', pattern: '[0-9]{6}', maxlength: '6', required: true, id: 'code' });
    var status = el('div');
    replace(root, [authCard([
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
          el('div', { className: 'actions' }, [el('button', { className: 'accent block', type: 'submit', text: 'Continue' })]),
        ]),
        status,
      ]),
    ])]);
    code.focus();
  }

  function completeSso() {
    replace(root, [el('div', { className: 'auth-shell' }, [el('p', { className: 'muted', text: 'Completing single sign-on…' })])]);
    return api('GET', '/auth/sso/callback' + window.location.search).then(function (r) {
      history.replaceState(null, '', '/console');
      if (!r.ok) return renderLogin(errorText(r));
      signedIn(r.body.accessToken);
    });
  }

  function signedIn(token) {
    OFB.state.token = token;
    storage(false, TOKEN_KEY, token);
    loadMe();
  }

  function loadMe() {
    api('GET', '/me/organisations').then(function (r) {
      if (r.status === 401) return signOut();
      if (!r.ok) return replace(root, [el('div', { className: 'auth-shell' }, [notice(errorText(r), 'error')])]);
      OFB.state.me = r.body.user;
      OFB.state.orgs = r.body.organisations;
      var remembered = storage(true, ORG_KEY);
      OFB.state.org = OFB.state.orgs.filter(function (o) { return o.customerId === remembered; })[0] ||
        (OFB.state.orgs.length === 1 ? OFB.state.orgs[0] : null);
      if (!OFB.state.orgs.length) {
        return replace(root, [el('div', { className: 'auth-shell' }, [el('div', { className: 'auth-card' }, [
          el('div', { className: 'auth-brand' }, [brandMark(), el('span', { className: 'brand-word', text: 'OpenFireblocks' })]),
          notice('You are signed in, but no organisation has given ' + OFB.state.me.email + ' a role yet. Ask an admin to add you.'),
          el('div', { className: 'actions' }, [el('button', { type: 'button', text: 'Sign out', onclick: signOut })]),
        ])])]);
      }
      if (!OFB.state.org) return renderOrgPicker();
      renderShell();
    });
  }

  function renderOrgPicker() {
    replace(root, [el('div', { className: 'auth-shell' }, [el('div', { className: 'auth-card', style: 'max-width:440px' }, [
      el('div', { className: 'auth-brand' }, [brandMark(), el('span', { className: 'brand-word', text: 'OpenFireblocks' })]),
      el('h2', { style: 'text-align:center;margin-bottom:14px;font-size:15px', text: 'Choose an organisation' }),
      el('div', { className: 'org-pick-list' }, OFB.state.orgs.map(function (o) {
        return el('div', { className: 'card link', role: 'button', tabindex: '0', onclick: function () {
          OFB.state.org = o;
          storage(false, ORG_KEY, o.customerId);
          renderShell();
        } }, [el('div', { className: 'row' }, [el('strong', { text: o.name }), el('span', { className: 'tag info', text: o.role })])]);
      })),
    ])])]);
  }

  // --------------------------------------------------------------- boot

  if (window.location.pathname === '/console/sso-callback' && window.location.search.indexOf('code=') !== -1) {
    completeSso();
  } else {
    OFB.state.token = storage(true, TOKEN_KEY);
    if (OFB.state.token) loadMe();
    else renderLogin();
  }
})();
