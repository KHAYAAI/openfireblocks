// Page renderers for the OpenFireblocks console.
//
// Loaded before app.js, which calls into window.OFBViews[view](id, content)
// on every navigation. Each function owns one area of #content; app.js
// owns the sidebar, topbar and auth, which do not change between routes.
//
// Every value that came from the API reaches the page through el()'s
// `text` or a textContent assignment -- never a string built with user or
// chain data concatenated into markup. That is what makes the CSP's
// script-src 'self' (no inline script) a real defence rather than a
// formality: there is nowhere for injected markup to come from.
(function () {
  'use strict';
  var O = window.OFB;
  var el = O.el, api = O.api, errorText = O.errorText, notice = O.notice, banner = O.banner, tag = O.tag;
  var replace = O.replace, orgPath = O.orgPath, short = O.short, relative = O.relative, fmtDate = O.fmtDate;
  var fmtBaseUnits = O.fmtBaseUnits, fmtZar = O.fmtZar, chainName = O.chainName;

  function loading(content) { replace(content, [O.skeleton()]); }
  function failed(content, r) { replace(content, [notice(errorText(r), 'error')]); }
  function empty(text) { return el('div', { className: 'empty', text: text }); }

  function statGrid(stats) {
    return el('div', { className: 'grid-stats' }, stats.map(function (s) {
      return el('div', { className: 'stat' }, [
        el('div', { className: 'eyebrow', text: s.label }),
        el('div', { className: 'value' + (s.tone ? ' ' + s.tone : ''), text: s.value }),
        s.sub ? el('div', { className: 'sub', text: s.sub }) : null,
      ]);
    }));
  }

  function sheet(headers, rows, opts) {
    opts = opts || {};
    return el('table', { className: 'sheet' }, [
      el('thead', null, [el('tr', null, headers.map(function (h) {
        return el('th', { className: (h && h.num) ? 'num' : null, text: h && h.label !== undefined ? h.label : h });
      }))]),
      el('tbody', null, rows.length ? rows : [el('tr', null, [el('td', { colspan: String(headers.length) }, [empty(opts.emptyText || 'Nothing here yet.')])])]),
    ]);
  }

  // --------------------------------------------------------- transactions

  function amountOf(tx) {
    var raw = tx.effective_amount !== null && tx.effective_amount !== undefined ? tx.effective_amount : tx.amount;
    if (raw === null || raw === undefined) return '—';
    var decimals = tx.asset_symbol === 'NATIVE' || !tx.asset_symbol ? 18 : (tx.asset_decimals || 0);
    return fmtBaseUnits(raw, decimals);
  }

  function assetOf(tx) {
    if (!tx.asset_symbol) return tag('warn', 'undecoded');
    if (tx.asset_symbol === 'NATIVE') return el('span', { className: 'mono', text: 'native' });
    return el('span', { className: 'mono', text: tx.asset_symbol });
  }

  function txRow(tx) {
    return el('tr', null, [
      el('td', { className: 'muted hide-sm', text: fmtDate(tx.created_at) }),
      el('td', null, [assetOf(tx)]),
      el('td', { className: 'num' }, [el('span', { text: amountOf(tx) })]),
      el('td', { className: 'mono trunc hide-sm' }, [el('span', { text: short(tx.effective_to || tx.to_address, 6) })]),
      el('td', { className: 'muted', text: tx.chain }),
      el('td', null, [tag(tx.status)]),
      el('td', { className: 'mono trunc hide-sm' }, [el('span', { text: short(tx.tx_hash, 6) })]),
    ]);
  }

  function txTable(rows) {
    return sheet(
      [{ label: 'When', num: false }, 'Asset', { label: 'Amount', num: true }, 'Recipient', 'Chain', 'Status', 'Tx hash'],
      rows.map(txRow),
      { emptyText: 'No transactions yet.' },
    );
  }

  // --------------------------------------------------------------- overview

  function overview(id, content) {
    loading(content);
    api('GET', orgPath('/overview')).then(function (r) {
      if (!r.ok) return failed(content, r);
      var d = r.body;
      var parts = [];
      if (d.isolation && d.isolation !== 'multi-account') {
        parts.push(banner(
          'Party isolation: ' + (d.isolation || 'unknown') + '.',
          'The signing parties are not yet on separate infrastructure, so the threshold does not protect against a single host compromise. This is a deployment state, not a code one.',
          'warn',
        ));
      }
      parts.push(statGrid([
        { label: 'Keys', value: String(d.keyCount) },
        { label: 'Active', value: String(d.activeKeys), tone: 'accent' },
        { label: 'Pending DKG', value: String(d.pendingKeys) },
        { label: 'Failed', value: String(d.failedKeys), tone: d.failedKeys ? 'critical' : null },
        { label: 'Signatures · 30d', value: String(d.signaturesThisMonth) },
      ]));
      parts.push(el('div', { className: 'card-head' }, [el('h2', { text: 'Recent activity' }), el('button', { type: 'button', className: 'ghost sm', text: 'View all →', onclick: function () { O.navigate('transactions'); } })]));
      parts.push(el('div', { className: 'card' }, [txTable(d.recentTransactions || [])]));
      replace(content, parts);
    });
  }

  // -------------------------------------------------------------------- keys

  function keyRow(k) {
    return el('tr', { className: 'link', onclick: function () { O.navigate('keys', k.key_id); } }, [
      el('td', null, [el('div', { text: k.name || '(unnamed)' }), el('div', { className: 'muted mono trunc', style: 'font-size:11.5px', text: k.key_id })]),
      el('td', { className: 'muted', text: k.blockchain }),
      el('td', { className: 'mono num', text: k.threshold + '-of-' + k.total_parties }),
      el('td', { className: 'mono trunc hide-sm', text: short(k.address, 6) }),
      el('td', null, [tag(k.status)]),
      el('td', { className: 'muted hide-sm', text: fmtDate(k.created_at) }),
    ]);
  }

  function keysList(content) {
    loading(content);
    api('GET', orgPath('/keys')).then(function (r) {
      if (!r.ok) return failed(content, r);
      var parts = [];
      if (O.isAdmin()) parts.push(el('div', { className: 'actions', style: 'margin-bottom:14px' }, [el('button', { type: 'button', className: 'accent', text: '+ New key', onclick: function () { keyCreateForm(content); } })]));
      parts.push(el('div', { className: 'card' }, [
        sheet(['Name', 'Chain', 'Threshold', 'Address', 'Status', 'Created'], r.body.map(keyRow), { emptyText: 'No keys provisioned yet.' }),
      ]));
      replace(content, parts);
    });
  }

  function keyDetail(keyId, content) {
    loading(content);
    api('GET', orgPath('/keys/' + encodeURIComponent(keyId))).then(function (r) {
      if (r.status === 404) return replace(content, [empty('No such key.')]);
      if (!r.ok) return failed(content, r);
      var d = r.body, k = d.key;

      var addressRows = d.addresses
        ? Object.keys(d.addresses).map(function (name) {
          return el('div', { className: 'row', style: 'padding:6px 0;border-bottom:1px solid var(--line)' }, [
            el('span', { className: 'muted', text: name }),
            el('span', { className: 'mono', text: d.addresses[name] }),
          ]);
        })
        : [el('p', { className: 'muted', text: 'No derivable addresses for this chain.' })];

      var balanceBody;
      if (d.balanceError) {
        balanceBody = [notice('Balances could not be read: ' + d.balanceError, 'error')];
      } else if (d.balances) {
        var rows = [el('tr', null, [el('td', { className: 'mono' }, [el('span', { text: 'native' })]), el('td', { className: 'num mono', text: fmtBaseUnits(d.balances.native, 18) }), el('td', null, [tag('ok', chainName(d.balances.chainId))])])]
          .concat(d.balances.tokens.map(function (t) {
            return el('tr', null, [
              el('td', { className: 'mono' }, [el('span', { text: t.symbol })]),
              el('td', { className: 'num mono' }, [el('span', { text: t.balance === null ? '—' : t.balance })]),
              el('td', null, [t.error ? tag('critical', 'unreadable') : (t.peg_currency ? tag('info', t.peg_currency + ' peg') : null)]),
            ]);
          }));
        balanceBody = [sheet(['Asset', { label: 'Balance', num: true }, ''], rows)];
      } else {
        balanceBody = [el('p', { className: 'muted', text: 'No balance source configured for this chain.' })];
      }

      var signingRows = (d.signings || []).map(function (s) {
        return el('tr', null, [
          el('td', { className: 'muted', text: fmtDate(s.created_at) }),
          el('td', null, [tag(s.status)]),
          el('td', { className: 'muted', text: s.blockchain }),
          el('td', { className: 'num', text: s.latency_ms !== null ? s.latency_ms + ' ms' : '—' }),
          el('td', { className: 'mono trunc hide-sm' }, [el('span', { text: short(s.transaction_hash, 6) })]),
        ]);
      });
      var ceremonyRows = (d.ceremonies || []).map(function (c) {
        return el('tr', null, [
          el('td', { className: 'muted', text: fmtDate(c.started_at) }),
          el('td', null, [tag(c.status)]),
          el('td', { className: 'muted', text: c.current_round !== null ? 'round ' + c.current_round : '—' }),
          el('td', { className: 'muted hide-sm', text: c.completed_at ? fmtDate(c.completed_at) : '—' }),
        ]);
      });

      replace(content, [
        el('button', { type: 'button', className: 'ghost sm', text: '← All keys', onclick: function () { O.navigate('keys'); } }),
        el('div', { className: 'row', style: 'margin:12px 0 16px' }, [el('h1', { style: 'font-size:19px', text: k.name || '(unnamed key)' }), tag(k.status)]),
        statGrid([
          { label: 'Blockchain', value: k.blockchain },
          { label: 'Threshold', value: k.threshold + '-of-' + k.total_parties },
          { label: 'Signatures', value: String(signingRows.length) + '+' },
        ]),
        el('div', { className: 'card' }, [el('div', { className: 'card-head' }, [el('h2', { text: 'Addresses' })])].concat(addressRows)),
        sendCard(k, content, function () { keyDetail(keyId, content); }),
        el('div', { className: 'card' }, [el('div', { className: 'card-head' }, [el('h2', { text: 'Balances' })])].concat(balanceBody)),
        el('div', { className: 'card' }, [el('div', { className: 'card-head' }, [el('h2', { text: 'Recent signing requests' })]), sheet(['When', 'Status', 'Chain', { label: 'Latency', num: true }, 'Tx hash'], signingRows, { emptyText: 'No signing requests yet.' })]),
        el('div', { className: 'card' }, [el('div', { className: 'card-head' }, [el('h2', { text: 'Ceremonies' })]), sheet(['Started', 'Status', 'Progress', 'Completed'], ceremonyRows, { emptyText: 'No ceremonies recorded.' })]),
        el('details', null, [el('summary', { className: 'muted', style: 'cursor:pointer;font-size:12px', text: 'Public key' }), el('p', { className: 'mono trunc', style: 'margin-top:8px', text: k.public_key || '—' })]),
      ]);
    });
  }


  // ------------------------------------------------- creating keys, sending

  var SEND_CHAINS = {
    bitcoin: { unit: 'BTC', decimals: 8, path: '/bitcoin-transactions', placeholder: 'bc1q… (tb1q… on testnet)' },
    solana: { unit: 'SOL', decimals: 9, path: '/solana-transactions', placeholder: 'base58 address' },
    cosmos: { unit: 'ATOM', decimals: 6, path: '/cosmos-transactions', placeholder: 'cosmos1…', memo: true },
  };

  // A decimal amount in whole coins to a base-10 string of base units, or
  // null. Refuses more fractional digits than the coin has rather than
  // rounding: the amount a person typed must be the amount that is sent.
  function toBaseUnits(text, decimals) {
    var t = String(text).trim();
    if (!/^[0-9]+(\.[0-9]+)?$/.test(t)) return null;
    var parts = t.split('.');
    var frac = parts[1] || '';
    if (frac.length > decimals) return null;
    var digits = (parts[0] + frac + new Array(decimals - frac.length + 1).join('0')).replace(/^0+/, '');
    return digits === '' ? null : digits;
  }

  function newRequestId() {
    if (window.crypto && window.crypto.randomUUID) return window.crypto.randomUUID();
    return 'req-' + Date.now() + '-' + Math.random().toString(16).slice(2);
  }

  function field(id, label, input) { return [el('label', { for: id, text: label }), input]; }

  function keyCreateForm(content) {
    var chain = el('select', { id: 'k-chain' }, [
      ['ethereum', 'Ethereum'], ['polygon', 'Polygon'], ['bitcoin', 'Bitcoin'], ['solana', 'Solana'], ['cosmos', 'Cosmos'],
    ].map(function (c) { return el('option', { value: c[0], text: c[1] }); }));
    var name = el('input', { id: 'k-name', placeholder: 'Treasury hot wallet' });
    var threshold = el('input', { id: 'k-threshold', type: 'number', min: '2', max: '10', value: '2' });
    var parties = el('input', { id: 'k-parties', type: 'number', min: '2', max: '10', value: '3' });
    var status = el('div');
    var form = el('form', {
      onsubmit: function (e) {
        e.preventDefault();
        var body = { blockchain: chain.value, name: name.value.trim() || undefined, threshold: Number(threshold.value), total_parties: Number(parties.value) };
        status.replaceChildren(notice('Starting the key ceremony…'));
        api('POST', orgPath('/keys'), body).then(function (r) {
          if (!r.ok) return status.replaceChildren(notice(errorText(r), 'error'));
          replace(content, [
            notice('Key created. The signing parties are generating it now; it becomes usable when its status turns active.', 'ok'),
            el('div', { className: 'card' }, [el('dl', { className: 'facts' }, [
              el('dt', { text: 'Key' }), el('dd', { className: 'mono', text: r.body.id }),
              el('dt', { text: 'Chain' }), el('dd', { text: r.body.blockchain }),
              el('dt', { text: 'Threshold' }), el('dd', { text: r.body.threshold + '-of-' + r.body.total_parties }),
              el('dt', { text: 'Status' }), el('dd', null, [tag(r.body.status)]),
            ])]),
            el('div', { className: 'actions' }, [el('button', { type: 'button', className: 'primary', text: 'Back to keys', onclick: function () { keysList(content); } })]),
          ]);
        });
      },
    }, [].concat(
      field('k-chain', 'Blockchain', chain), field('k-name', 'Name', name),
      [el('div', { className: 'field-row' }, [
        el('div', null, field('k-threshold', 'Signatures needed', threshold)),
        el('div', null, field('k-parties', 'Signing parties', parties)),
      ])],
      [el('p', { className: 'muted', text: 'Any two parties sign; no single party can spend. A threshold of 1 is not offered for a multi-party key.' })],
      [el('div', { className: 'actions' }, [
        el('button', { type: 'submit', className: 'accent', text: 'Create key' }),
        el('button', { type: 'button', className: 'ghost', text: 'Cancel', onclick: function () { keysList(content); } }),
      ]), status]
    ));
    replace(content, [el('div', { className: 'card' }, [el('h2', { text: 'New key' }), form])]);
  }

  // Optional IVMS101 details. Sent only if something is filled in; a transfer
  // that needs them and does not have them is refused by the server, which
  // says exactly what is missing.
  function travelRuleFields() {
    var orgName = (O.state.org && O.state.org.name) || '';
    var oName = el('input', { id: 'tr-oname', value: orgName });
    var oAddr = el('input', { id: 'tr-oaddr', placeholder: 'Street, city' });
    var oCountry = el('input', { id: 'tr-ocountry', maxlength: '2', placeholder: 'ZA', style: 'text-transform:uppercase' });
    var bKind = el('select', { id: 'tr-bkind' }, [el('option', { value: 'company', text: 'Company' }), el('option', { value: 'person', text: 'Person' })]);
    var bName = el('input', { id: 'tr-bname', placeholder: 'Company name, or surname' });
    var bGiven = el('input', { id: 'tr-bgiven', placeholder: 'Given names (person only)' });
    var bHolder = el('select', { id: 'tr-bholder' }, [el('option', { value: 'self', text: 'Holds their own wallet' }), el('option', { value: 'vasp', text: 'Held by an exchange or custodian' })]);
    var bVasp = el('input', { id: 'tr-bvasp', placeholder: 'Exchange or custodian name' });
    var node = el('details', { className: 'card tight' }, [
      el('summary', { className: 'muted', style: 'cursor:pointer', text: 'Travel Rule details (required above the reporting threshold)' }),
      el('div', { style: 'margin-top:10px' }, [].concat(
        [el('div', { className: 'eyebrow', text: 'Sender' })], field('tr-oname', 'Name', oName), field('tr-oaddr', 'Address', oAddr), field('tr-ocountry', 'Country (2 letters)', oCountry),
        [el('div', { className: 'eyebrow', style: 'margin-top:12px', text: 'Recipient' })], field('tr-bkind', 'Recipient is a', bKind), field('tr-bname', 'Name', bName), field('tr-bgiven', 'Given names', bGiven),
        field('tr-bholder', 'Wallet', bHolder), field('tr-bvasp', 'Provider', bVasp)
      )),
    ]);
    function build() {
      if (!bName.value.trim() && !oAddr.value.trim()) return undefined;
      var addr = oAddr.value.trim() ? { addressLine: [oAddr.value.trim()], country: oCountry.value.trim().toUpperCase() } : undefined;
      var originator = { legalPerson: { name: oName.value.trim(), geographicAddress: addr } };
      var beneficiary = bKind.value === 'person'
        ? { naturalPerson: { name: { primaryIdentifier: bName.value.trim(), secondaryIdentifier: bGiven.value.trim() || undefined } } }
        : { legalPerson: { name: bName.value.trim() } };
      var tr = { originator: originator, beneficiary: beneficiary };
      if (bHolder.value === 'self') tr.beneficiaryUnhosted = true; else tr.beneficiaryVasp = { name: bVasp.value.trim() };
      return tr;
    }
    return { node: node, build: build };
  }

  function sendForm(k, content, back) {
    var cfg = SEND_CHAINS[k.blockchain];
    var dest = el('input', { id: 's-dest', required: true, placeholder: cfg.placeholder, autocomplete: 'off', spellcheck: 'false' });
    var amt = el('input', { id: 's-amt', required: true, inputmode: 'decimal', placeholder: '0.00' });
    var memo = cfg.memo ? el('input', { id: 's-memo', maxlength: '256', placeholder: 'Optional' }) : null;
    var tr = travelRuleFields();
    var status = el('div');
    var form = el('form', {
      onsubmit: function (e) {
        e.preventDefault();
        var base = toBaseUnits(amt.value, cfg.decimals);
        if (!base) return status.replaceChildren(notice('Enter a positive amount with at most ' + cfg.decimals + ' decimal places.', 'error'));
        var body = { destination: dest.value.trim(), amount: base };
        if (memo && memo.value.trim()) body.memo = memo.value.trim();
        var t = tr.build();
        if (t) body.travelRule = t;
        review(k, cfg, body, amt.value.trim(), content, function () { sendForm(k, content, back); }, back);
      },
    }, [].concat(
      field('s-dest', 'To', dest), field('s-amt', 'Amount (' + cfg.unit + ')', amt), memo ? field('s-memo', 'Memo', memo) : [],
      [tr.node],
      [el('div', { className: 'actions' }, [
        el('button', { type: 'submit', className: 'accent', text: 'Review' }),
        el('button', { type: 'button', className: 'ghost', text: 'Cancel', onclick: back }),
      ]), status]
    ));
    replace(content, [el('div', { className: 'card' }, [el('h2', { text: 'Send ' + cfg.unit + ' from ' + (k.name || 'this key') }), form])]);
  }

  // The last look before money moves. The request id is made here, once, so a
  // double click or a retry after a timeout replays the same transfer instead
  // of sending a second one.
  function review(k, cfg, body, shown, content, edit, back) {
    var requestId = newRequestId();
    var status = el('div');
    var send = el('button', { type: 'button', className: 'accent', text: 'Confirm and send' });
    send.onclick = function () {
      send.disabled = true;
      status.replaceChildren(notice('Signing… this runs a threshold ceremony and can take a few seconds.'));
      var payload = Object.assign({ idempotencyKey: requestId }, body);
      api('POST', orgPath('/keys/' + encodeURIComponent(k.key_id) + cfg.path), payload).then(function (r) {
        if (!r.ok) {
          send.disabled = false;
          return status.replaceChildren(notice(errorText(r), 'error'));
        }
        var b = r.body;
        replace(content, [
          notice(b.broadcast === false ? 'Signed, not broadcast.' : 'Sent. It is on its way to the network.', 'ok'),
          el('div', { className: 'card' }, [el('dl', { className: 'facts' }, [
            el('dt', { text: 'Transaction' }), el('dd', { className: 'mono', style: 'word-break:break-all', text: b.txid || b.signature || b.txhash }),
            el('dt', { text: 'To' }), el('dd', { className: 'mono', style: 'word-break:break-all', text: body.destination }),
            el('dt', { text: 'Amount' }), el('dd', { text: shown + ' ' + cfg.unit }),
            el('dt', { text: 'Network fee' }), el('dd', { className: 'mono', text: String(b.fee) + ' (base units)' }),
          ])]),
          el('div', { className: 'actions' }, [el('button', { type: 'button', className: 'primary', text: 'Back to key', onclick: back })]),
        ]);
      });
    };
    replace(content, [el('div', { className: 'card' }, [
      el('h2', { text: 'Review this transfer' }),
      el('dl', { className: 'facts' }, [
        el('dt', { text: 'From' }), el('dd', null, [el('div', { text: k.name || '(unnamed key)' }), el('div', { className: 'muted mono', style: 'font-size:11.5px', text: k.key_id })]),
        el('dt', { text: 'To' }), el('dd', { className: 'mono', style: 'word-break:break-all', text: body.destination }),
        el('dt', { text: 'Amount' }), el('dd', { style: 'font-size:17px;font-weight:650', className: 'mono', text: shown + ' ' + cfg.unit }),
        body.memo ? el('dt', { text: 'Memo' }) : null, body.memo ? el('dd', { text: body.memo }) : null,
        el('dt', { text: 'Fee' }), el('dd', { text: 'Set by the network when the transfer is prepared, and shown after.' }),
      ].filter(Boolean)),
      el('p', { className: 'muted', style: 'margin:14px 0', text: 'This is checked against your organisation\'s policy first. A transfer that policy says needs approval is refused here: these chains have no approval step yet.' }),
      el('div', { className: 'actions' }, [send, el('button', { type: 'button', className: 'ghost', text: 'Edit', onclick: edit })]),
      status,
    ])]);
  }

  function sendCard(k, content, reload) {
    if (!SEND_CHAINS[k.blockchain] || k.status !== 'active' || !O.canInitiate()) return null;
    return el('div', { className: 'card' }, [
      el('div', { className: 'card-head' }, [el('h2', { text: 'Send' }), el('button', { type: 'button', className: 'accent sm', text: 'New transfer', onclick: function () { sendForm(k, content, reload); } })]),
      el('p', { className: 'muted', text: 'Native ' + SEND_CHAINS[k.blockchain].unit + ' only.' }),
    ]);
  }

  function keys(id, content) { return id ? keyDetail(id, content) : keysList(content); }

  // -------------------------------------------------------------- transactions

  function transactions(id, content) {
    loading(content);
    api('GET', orgPath('/transactions')).then(function (r) {
      if (!r.ok) return failed(content, r);
      replace(content, [el('div', { className: 'card' }, [txTable(r.body)])]);
    });
  }

  // -------------------------------------------------------------------- agents

  function agentRow(a) {
    return el('tr', { className: 'link', onclick: function () { O.navigate('agents', a.agentId); } }, [
      el('td', null, [el('div', { text: a.name })]),
      el('td', { className: 'mono', text: a.allowedTokens.join(', ') }),
      el('td', { className: 'num mono', text: fmtZar(a.perTransferLimitZar) }),
      el('td', { className: 'num mono', text: fmtZar(a.dailyLimitZar) }),
      el('td', null, [tag(a.status)]),
      el('td', { className: 'muted hide-sm', text: relative(a.expiresAt) }),
    ]);
  }

  function agentCreateForm(content, keyOptions) {
    var name = el('input', { id: 'a-name', required: true, placeholder: 'Procurement bot' });
    var keySel = el('select', { id: 'a-key' }, keyOptions.map(function (k) { return el('option', { value: k.key_id, text: (k.name || k.key_id) + ' · ' + k.blockchain }); }));
    var tokens = el('input', { id: 'a-tokens', required: true, placeholder: 'ZARP, USDC' });
    var recipients = el('input', { id: 'a-recipients', placeholder: '0x… (comma-separated, optional)' });
    var per = el('input', { id: 'a-per', type: 'number', min: '1', step: '0.01', required: true, placeholder: '5000' });
    var daily = el('input', { id: 'a-daily', type: 'number', min: '1', step: '0.01', required: true, placeholder: '10000' });
    var days = el('input', { id: 'a-days', type: 'number', min: '1', max: '365', required: true, value: '30' });
    var status = el('div');
    var form = el('form', {
      onsubmit: function (e) {
        e.preventDefault();
        var body = {
          name: name.value, keyId: keySel.value,
          allowedTokens: tokens.value.split(',').map(function (s) { return s.trim().toUpperCase(); }).filter(Boolean),
          perTransferLimitZar: Number(per.value), dailyLimitZar: Number(daily.value), expiresInDays: Number(days.value),
        };
        var rec = recipients.value.split(',').map(function (s) { return s.trim(); }).filter(Boolean);
        if (rec.length) body.allowedRecipients = rec;
        status.replaceChildren(notice('Creating…'));
        api('POST', orgPath('/agents'), body).then(function (r) {
          if (!r.ok) return status.replaceChildren(notice(errorText(r), 'error'));
          replace(content, [
            notice('Agent created. Copy its key now -- it is shown only this once.', 'ok'),
            el('div', { className: 'card' }, [
              el('div', { className: 'eyebrow', text: 'Agent key' }),
              el('p', { className: 'mono', style: 'word-break:break-all;font-size:13px;margin-top:6px', text: r.body.apiKey }),
            ]),
            el('div', { className: 'actions' }, [el('button', { type: 'button', className: 'primary', text: 'Done', onclick: function () { agentsList(content); } })]),
          ]);
        });
      },
    }, [
      el('label', { for: 'a-name', text: 'Name' }), name,
      el('label', { for: 'a-key', text: 'Key it may spend from' }), keySel,
      el('label', { for: 'a-tokens', text: 'Allowed tokens' }), tokens,
      el('label', { for: 'a-recipients', text: 'Allowed recipients' }), recipients,
      el('div', { className: 'field-row' }, [
        el('div', null, [el('label', { for: 'a-per', text: 'Per-transfer limit (ZAR)' }), per]),
        el('div', null, [el('label', { for: 'a-daily', text: '24h budget (ZAR)' }), daily]),
      ]),
      el('label', { for: 'a-days', text: 'Expires in (days)' }), days,
      el('div', { className: 'actions' }, [
        el('button', { type: 'submit', className: 'accent', text: 'Create agent' }),
        el('button', { type: 'button', className: 'ghost', text: 'Cancel', onclick: function () { agentsList(content); } }),
      ]),
      status,
    ]);
    replace(content, [el('div', { className: 'card' }, [el('h2', { text: 'New agent' }), form])]);
  }

  function agentsList(content) {
    loading(content);
    api('GET', orgPath('/agents')).then(function (r) {
      if (!r.ok) return failed(content, r);
      var parts = [];
      if (O.isAdmin()) parts.push(el('div', { className: 'actions', style: 'margin-bottom:14px' }, [
        el('button', { type: 'button', className: 'accent', text: '+ New agent', onclick: function () {
          api('GET', orgPath('/keys')).then(function (kr) {
            if (!kr.ok || !kr.body.length) return replace(content, [notice('Provision a key first -- an agent needs one to spend from.', 'error')]);
            agentCreateForm(content, kr.body);
          });
        } }),
      ]));
      parts.push(el('div', { className: 'card' }, [sheet(['Name', 'Tokens', { label: 'Per-transfer', num: true }, { label: '24h budget', num: true }, 'Status', 'Expires'], r.body.map(agentRow), { emptyText: 'No agents yet. Agents hold a scoped credential that can only pay, within limits you set.' })]));
      replace(content, parts);
    });
  }

  function agentDetail(agentId, content) {
    loading(content);
    api('GET', orgPath('/agents/' + encodeURIComponent(agentId))).then(function (r) {
      if (r.status === 404) return replace(content, [empty('No such agent.')]);
      if (!r.ok) return failed(content, r);
      var a = r.body, rec = a.record;
      var reasonRows = Object.keys(rec.refusalsByReason || {}).map(function (k) {
        return el('tr', null, [el('td', { text: k }), el('td', { className: 'num mono', text: String(rec.refusalsByReason[k]) })]);
      });
      var parts = [
        el('button', { type: 'button', className: 'ghost sm', text: '← All agents', onclick: function () { O.navigate('agents'); } }),
        el('div', { className: 'row', style: 'margin:12px 0 16px' }, [el('h1', { style: 'font-size:19px', text: a.name }), tag(a.status)]),
        statGrid([
          { label: 'Spent · 24h', value: fmtZar(rec.spent24hZar) },
          { label: 'Remaining · 24h', value: fmtZar(rec.remaining24hZar), tone: 'accent' },
          { label: 'Transfers', value: String(rec.transfers) },
          { label: 'Refused', value: String(rec.refused), tone: rec.refused ? 'critical' : null },
        ]),
        el('div', { className: 'card' }, [el('dl', { className: 'facts' }, [
          el('dt', { text: 'Key' }), el('dd', { className: 'mono', text: a.keyId }),
          el('dt', { text: 'Allowed tokens' }), el('dd', { text: a.allowedTokens.join(', ') }),
          el('dt', { text: 'Allowed recipients' }), el('dd', { className: 'mono', text: a.allowedRecipients ? a.allowedRecipients.join(', ') : 'any' }),
          el('dt', { text: 'Per-transfer limit' }), el('dd', { text: fmtZar(a.perTransferLimitZar) }),
          el('dt', { text: '24h budget' }), el('dd', { text: fmtZar(a.dailyLimitZar) }),
          el('dt', { text: 'Expires' }), el('dd', { text: fmtDate(a.expiresAt) }),
          el('dt', { text: 'Created' }), el('dd', { text: fmtDate(a.createdAt) }),
        ])]),
      ];
      if (reasonRows.length) parts.push(el('div', { className: 'card' }, [el('div', { className: 'card-head' }, [el('h2', { text: 'Refusals by reason' })]), sheet(['Reason', { label: 'Count', num: true }], reasonRows)]));
      if (O.isAdmin() && a.status === 'active') {
        var status = el('div');
        parts.push(el('div', { className: 'card' }, [
          el('h2', { text: 'Revoke' }),
          el('p', { className: 'muted', text: 'Immediate and permanent. This agent will not be able to spend again; create a new one for different terms.' }),
          el('div', { className: 'actions' }, [el('button', { type: 'button', className: 'danger', text: 'Revoke this agent', onclick: function () {
            api('POST', orgPath('/agents/' + encodeURIComponent(agentId) + '/revoke')).then(function (r2) {
              if (!r2.ok) return status.replaceChildren(notice(errorText(r2), 'error'));
              agentDetail(agentId, content);
            });
          } })]),
          status,
        ]));
      }
      replace(content, parts);
    });
  }

  function agents(id, content) { return id ? agentDetail(id, content) : agentsList(content); }

  // ----------------------------------------------------------------- approvals

  var approvalsTab = 'pending';

  function approvalSummaryCard(a) {
    var s = a.summary || {};
    return el('div', { className: 'card link', role: 'button', tabindex: '0', onclick: function () { O.navigate('approvals', a.approvalId); } }, [
      el('div', { className: 'row' }, [el('span', { className: 'amount', text: O.fmtWei(s.valueWei) }), tag(a.status)]),
      el('div', { className: 'mono muted', style: 'font-size:12.5px;margin-top:4px', text: 'to ' + (s.to || '?') }),
      el('div', { className: 'row muted', style: 'margin-top:8px;font-size:12px' }, [
        el('span', { text: a.approvals + ' of ' + a.requiredApprovals + ' approvals · ' + chainName(s.chainId) }),
        el('span', { text: a.status === 'pending' ? 'expires ' + relative(a.expiresAt) : relative(a.decidedAt || a.createdAt) }),
      ]),
    ]);
  }

  function approvalsList(content) {
    var tabsEl = el('div', { className: 'tabs' });
    var body = el('div');
    function buildTabs() {
      replace(tabsEl, ['pending', 'approved', 'rejected', 'expired'].map(function (t) {
        return el('button', { type: 'button', className: approvalsTab === t ? 'active' : '', text: t, onclick: function () { approvalsTab = t; buildTabs(); load(); } });
      }));
    }
    function load(quiet) {
      if (!quiet) replace(body, [O.skeleton()]);
      api('GET', orgPath('/approvals?status=' + approvalsTab)).then(function (r) {
        if (!r.ok) return replace(body, [notice(errorText(r), 'error')]);
        replace(body, r.body.length ? r.body.map(approvalSummaryCard) : [empty(approvalsTab === 'pending' ? 'Nothing is waiting for approval.' : 'None.')]);
      });
    }
    replace(content, [tabsEl, body]);
    buildTabs();
    load();
  }

  function decisionPanel(a, mine, initiatedByMe, onDone) {
    if (a.status !== 'pending') return null;
    if (initiatedByMe) return notice('You requested this transfer, so you cannot approve or reject it. Someone else must.');
    if (mine) return notice('You ' + (mine.decision === 'approve' ? 'approved' : 'rejected') + ' this. Waiting for others.', 'ok');
    if (!O.canDecide()) return notice('Your role (' + O.role() + ') can see approvals but not decide on them.');
    var sso = O.state.me.authProvider === 'workos_sso';
    if (!sso && !O.state.me.mfaEnabled) return notice('Turn on two-factor authentication before approving or rejecting transfers.', 'error');

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
        if (r.ok) return onDone(decision === 'approve'
          ? (r.body.status === 'approved' ? 'Approved. The transfer has its approvals and will now be signed.' : 'Your approval is recorded. It still needs ' + (r.body.requiredApprovals - r.body.approvals) + ' more.')
          : 'Rejected. The transfer will not be signed.');
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

  function approvalDetail(approvalId, content, flash) {
    api('GET', orgPath('/approvals/' + encodeURIComponent(approvalId))).then(function (r) {
      if (!r.ok) return failed(content, r);
      var a = r.body, s = a.summary || {};
      var mine = (a.decisions || []).filter(function (d) { return d.userId === O.state.me.id; })[0];
      var initiatedByMe = a.initiatedByUserId && a.initiatedByUserId === O.state.me.id;

      var facts = el('dl', { className: 'facts' }, [
        el('dt', { text: 'Amount' }), el('dd', { className: 'mono', style: 'font-size:17px;font-weight:650', text: O.fmtWei(s.valueWei) }),
        el('dt', { text: 'To' }), el('dd', { className: 'mono', text: s.to || '?' }),
        el('dt', { text: 'Network' }), el('dd', { text: chainName(s.chainId) }),
        el('dt', { text: 'Requested by' }), el('dd', { text: a.initiatedBy }),
        el('dt', { text: 'Requested' }), el('dd', { text: fmtDate(a.createdAt) }),
        el('dt', { text: a.status === 'pending' ? 'Expires' : 'Closed' }),
        el('dd', { text: a.status === 'pending' ? relative(a.expiresAt) + ' (' + fmtDate(a.expiresAt) + ')' : fmtDate(a.decidedAt || a.expiresAt) }),
      ].concat(s.data && s.data !== '0x' ? [el('dt', { text: 'Contract call' }), el('dd', { className: 'mono trunc', text: s.data })] : []));

      var reasons = (s.reasons || []).length
        ? el('div', { style: 'margin-top:14px' }, [el('h2', { style: 'font-size:13px;margin-bottom:6px', text: 'Why this needs approval' }), el('ul', { className: 'plain' }, s.reasons.map(function (x) { return el('li', { text: x }); }))])
        : null;

      var decisions = el('div', null, [el('div', { className: 'card-head' }, [el('h2', { text: 'Decisions (' + a.approvals + ' of ' + a.requiredApprovals + ')' })])].concat(
        (a.decisions || []).length === 0 ? [el('p', { className: 'muted', text: 'No decisions yet.' })] :
        a.decisions.map(function (d) {
          return el('div', { className: 'card tight' }, [
            el('div', { className: 'row' }, [el('strong', { style: 'font-size:13px', text: d.fullName + ' (' + d.email + ')' }), tag(d.decision === 'approve' ? 'approved' : 'rejected')]),
            el('div', { className: 'muted', style: 'font-size:12px;margin-top:3px', text: fmtDate(d.createdAt) + ' · verified by ' + (d.stepUp === 'sso' ? 'single sign-on' : 'one-time code') }),
            d.reason ? el('div', { style: 'margin-top:4px;font-size:13px', text: d.reason }) : null,
          ]);
        })));

      replace(content, [
        el('button', { type: 'button', className: 'ghost sm', text: '← Approvals', onclick: function () { O.navigate('approvals'); } }),
        el('div', { className: 'row', style: 'margin:12px 0 14px' }, [el('h1', { style: 'font-size:19px', text: 'Transfer approval' }), tag(a.status)]),
        flash ? notice(flash, 'ok') : null,
        el('div', { className: 'card' }, [facts, reasons]),
        decisionPanel(a, mine, initiatedByMe, function (msg) { approvalDetail(approvalId, content, msg); }),
        el('div', { className: 'card' }, [decisions]),
      ]);
    });
  }

  function approvals(id, content) {
    loading(content);
    if (id) approvalDetail(id, content); else approvalsList(content);
  }

  // -------------------------------------------------------------------- policy

  function policy(id, content) {
    loading(content);
    api('GET', orgPath('/approval-policy')).then(function (r) {
      if (!r.ok) return failed(content, r);
      var p = r.body;
      var parts = [
        el('div', { className: 'card' }, [el('dl', { className: 'facts' }, [
          el('dt', { text: 'Approvals needed' }), el('dd', { text: p.requiredApprovals + (p.requiredApprovals >= 2 ? ' (dual control)' : ' (single approver)') }),
          el('dt', { text: 'Time to decide' }), el('dd', { text: p.windowMinutes + ' minutes' }),
          el('dt', { text: 'Set' }), el('dd', { text: p.isDefault ? 'default' : fmtDate(p.updatedAt) }),
        ])]),
        el('p', { className: 'muted', text: 'The person who requests a transfer can never approve it. Each person decides once. One rejection stops it. A change applies to new requests, not ones already waiting.' }),
      ];
      if (O.isAdmin()) {
        var req = el('input', { type: 'number', min: '1', max: '10', value: String(p.requiredApprovals), id: 'req' });
        var win = el('input', { type: 'number', min: '5', max: '10080', value: String(p.windowMinutes), id: 'win' });
        var status = el('div');
        parts.push(el('div', { className: 'card' }, [
          el('h2', { text: 'Change policy' }),
          el('form', { onsubmit: function (e) {
            e.preventDefault();
            api('PUT', orgPath('/approval-policy'), { requiredApprovals: Number(req.value), windowMinutes: Number(win.value) }).then(function (r2) {
              status.replaceChildren(r2.ok ? notice('Saved.', 'ok') : notice(errorText(r2), 'error'));
              if (r2.ok) setTimeout(function () { policy(null, content); }, 500);
            });
          } }, [
            el('div', { className: 'field-row' }, [
              el('div', null, [el('label', { for: 'req', text: 'Approvals needed (1–10)' }), req]),
              el('div', null, [el('label', { for: 'win', text: 'Minutes to decide (5–10080)' }), win]),
            ]),
            el('div', { className: 'actions' }, [el('button', { type: 'submit', className: 'accent', text: 'Save policy' })]),
          ]),
          status,
        ]));
      }
      replace(content, parts);
    });
  }

  // -------------------------------------------------------------------- people

  var ROLES = ['admin', 'approver', 'operator', 'auditor', 'viewer', 'billing_admin'];

  function peopleView(id, content) {
    loading(content);
    api('GET', orgPath('/members')).then(function (r) {
      if (!r.ok) return failed(content, r);
      var removeStatus = el('div');
      var rows = r.body.map(function (m) {
        var removeBtn = O.isAdmin() ? el('button', { type: 'button', className: 'ghost sm', text: 'Remove', onclick: function (e) {
          e.target.disabled = true;
          api('DELETE', orgPath('/members/' + encodeURIComponent(m.userId))).then(function (r2) {
            if (!r2.ok) { e.target.disabled = false; return removeStatus.replaceChildren(notice(errorText(r2), 'error')); }
            peopleView(null, content);
          });
        } }) : null;
        return el('tr', null, [
          el('td', null, [el('div', { text: m.fullName }), el('div', { className: 'muted mono', style: 'font-size:11.5px', text: m.email })]),
          el('td', null, [tag('info', m.role)]),
          el('td', { className: 'muted hide-sm', text: m.authProvider === 'workos_sso' ? 'SSO' : (m.mfaEnabled ? '2FA on' : '2FA off') }),
          el('td', null, [removeBtn]),
        ]);
      });
      var parts = [
        removeStatus,
        el('div', { className: 'card' }, [sheet(['Person', 'Role', 'Sign-in', ''], rows)]),
        el('p', { className: 'muted', text: 'Approvers approve; operators request transfers; admins do both, but never approve their own. Auditors and viewers can only read.' }),
      ];
      if (O.isAdmin()) {
        var email = el('input', { type: 'email', id: 'm-email', required: true, placeholder: 'person@example.com' });
        var sel = el('select', { id: 'm-role' }, ROLES.map(function (x) { return el('option', { value: x, text: x }); }));
        sel.value = 'approver';
        var status = el('div');
        parts.push(el('div', { className: 'card' }, [
          el('h2', { text: 'Add a person or change a role' }),
          el('form', { onsubmit: function (e) {
            e.preventDefault();
            api('PUT', orgPath('/members'), { email: email.value, role: sel.value }).then(function (r2) {
              status.replaceChildren(r2.ok ? notice(r2.body.email + ' is now ' + r2.body.role + '.', 'ok') : notice(errorText(r2), 'error'));
              if (r2.ok) setTimeout(function () { peopleView(null, content); }, 700);
            });
          } }, [
            el('div', { className: 'field-row' }, [
              el('div', null, [el('label', { for: 'm-email', text: 'Email' }), email]),
              el('div', null, [el('label', { for: 'm-role', text: 'Role' }), sel]),
            ]),
            el('div', { className: 'actions' }, [el('button', { type: 'submit', className: 'accent', text: 'Save' })]),
          ]),
          el('p', { className: 'muted', style: 'font-size:12px', text: 'The person must already have an account (registered, or signed in once with SSO).' }),
          status,
        ]));
      }
      replace(content, parts);
    });
  }

  // ----------------------------------------------------------------- travel rule

  var trTab = 'all';
  var TR_TABS = ['all', 'awaiting_transmission', 'transmitted', 'failed', 'not_applicable_unhosted'];

  function trRow(rec) {
    return el('tr', { className: 'link', onclick: function () { O.navigate('travel-rule', rec.recordId); } }, [
      el('td', { className: 'muted', text: fmtDate(rec.createdAt) }),
      el('td', { className: 'mono', text: rec.asset }),
      el('td', { className: 'num mono', text: rec.valueZar !== null ? fmtZar(rec.valueZar) : 'unpriced' }),
      el('td', { className: 'mono trunc hide-sm', text: short(rec.beneficiaryAddress, 6) }),
      el('td', null, [tag(rec.transmissionStatus)]),
    ]);
  }

  function travelRuleList(content) {
    var tabsEl = el('div', { className: 'tabs' });
    var body = el('div');
    function buildTabs() {
      replace(tabsEl, TR_TABS.map(function (t) {
        return el('button', { type: 'button', className: trTab === t ? 'active' : '', text: t.replace(/_/g, ' '), onclick: function () { trTab = t; buildTabs(); load(); } });
      }));
    }
    function load() {
      replace(body, [O.skeleton()]);
      api('GET', orgPath('/travel-rule/records' + (trTab === 'all' ? '' : '?status=' + trTab))).then(function (r) {
        if (!r.ok) return replace(body, [notice(errorText(r), 'error')]);
        replace(body, [el('div', { className: 'card' }, [sheet(['When', 'Asset', { label: 'Value', num: true }, 'Beneficiary', 'Status'], r.body.map(trRow), { emptyText: 'No records.' })])]);
      });
    }
    replace(content, [tabsEl, body]);
    buildTabs();
    load();
  }

  function travelRuleDetail(recordId, content) {
    api('GET', orgPath('/travel-rule/records/' + encodeURIComponent(recordId))).then(function (r) {
      if (r.status === 404) return replace(content, [empty('No such record.')]);
      if (!r.ok) return failed(content, r);
      var rec = r.body;
      var parts = [
        el('button', { type: 'button', className: 'ghost sm', text: '← Travel Rule records', onclick: function () { O.navigate('travel-rule'); } }),
        el('div', { className: 'row', style: 'margin:12px 0 14px' }, [el('h1', { style: 'font-size:19px', text: rec.asset + ' transfer' }), tag(rec.transmissionStatus)]),
        el('div', { className: 'card' }, [el('dl', { className: 'facts' }, [
          el('dt', { text: 'Value' }), el('dd', { text: rec.valueZar !== null ? fmtZar(rec.valueZar) + ' (' + rec.valuation + ')' : 'could not be valued (' + rec.valuation + ')' }),
          el('dt', { text: 'Amount' }), el('dd', { className: 'mono', text: rec.amount + ' base units of ' + rec.asset }),
          el('dt', { text: 'Beneficiary' }), el('dd', { className: 'mono', text: rec.beneficiaryAddress }),
          el('dt', { text: 'Beneficiary wallet' }), el('dd', { text: rec.beneficiaryUnhosted ? 'unhosted (self-custodied)' : 'hosted by a provider' }),
          el('dt', { text: 'Transaction' }), el('dd', { className: 'mono', text: rec.txHash || 'not yet signed' }),
          el('dt', { text: 'Recorded' }), el('dd', { text: fmtDate(rec.createdAt) }),
          rec.transmissionReference ? el('dt', { text: 'Reference' }) : null, rec.transmissionReference ? el('dd', { className: 'mono', text: rec.transmissionReference }) : null,
          rec.transmissionError ? el('dt', { text: 'Last error' }) : null, rec.transmissionError ? el('dd', { text: rec.transmissionError }) : null,
        ])]),
      ];
      if (rec.ivms101) {
        parts.push(el('div', { className: 'card' }, [
          el('div', { className: 'card-head' }, [el('h2', { text: 'IVMS101 payload' })]),
          el('pre', { className: 'mono', style: 'white-space:pre-wrap;word-break:break-word;font-size:12px;margin:0' }, [el('span', { text: JSON.stringify(rec.ivms101, null, 2) })]),
        ]));
      }
      if (O.isAdmin() && (rec.transmissionStatus === 'awaiting_transmission' || rec.transmissionStatus === 'failed')) {
        var ref = el('input', { id: 'tr-ref', required: true, placeholder: 'provider reference or export batch id' });
        var status = el('div');
        parts.push(el('div', { className: 'card' }, [
          el('h2', { text: 'Record as sent' }),
          el('p', { className: 'muted', text: 'Use this when the information was sent outside the platform -- a provider portal, a manual export.' }),
          el('form', { onsubmit: function (e) {
            e.preventDefault();
            api('POST', orgPath('/travel-rule/records/' + encodeURIComponent(recordId) + '/transmitted'), { reference: ref.value }).then(function (r2) {
              if (!r2.ok) return status.replaceChildren(notice(errorText(r2), 'error'));
              travelRuleDetail(recordId, content);
            });
          } }, [
            el('label', { for: 'tr-ref', text: 'Reference' }), ref,
            el('div', { className: 'actions' }, [el('button', { type: 'submit', className: 'accent', text: 'Mark transmitted' })]),
          ]),
          status,
        ]));
      }
      replace(content, parts);
    });
  }

  function travelRule(id, content) { loading(content); if (id) travelRuleDetail(id, content); else travelRuleList(content); }

  // ------------------------------------------------------------- reconciliation

  function reconRow(run) {
    return el('tr', { className: 'link', onclick: function () { O.navigate('reconciliation', run.runId); } }, [
      el('td', { className: 'muted', text: fmtDate(run.at) }),
      el('td', null, [tag('info', run.kind)]),
      el('td', { className: 'muted', text: run.kind === 'chain' ? chainName(run.chainId) : '—' }),
      el('td', null, [run.summary.needsAttention ? tag('critical', 'needs attention') : tag('ok', 'clean')]),
      el('td', { className: 'muted hide-sm', text: run.requestedBy }),
    ]);
  }

  function reconRunForms(content) {
    var kind = el('select', { id: 'rc-kind' }, [el('option', { value: 'evm', text: 'EVM chain' }), el('option', { value: 'solana', text: 'Solana' }), el('option', { value: 'cosmos', text: 'Cosmos' })]);
    var chainId = el('input', { type: 'number', id: 'rc-chain', placeholder: '1' });
    var since = el('input', { type: 'number', id: 'rc-since', min: '1', value: '168' });
    var missing = el('input', { type: 'number', id: 'rc-missing', min: '1', value: '30' });
    var chainStatus = el('div');
    var chainForm = el('form', { onsubmit: function (e) {
      e.preventDefault();
      if (kind.value === 'evm' && !chainId.value) return chainStatus.replaceChildren(notice('Enter the chain id.', 'error'));
      chainStatus.replaceChildren(notice('Running — this reads the chain for every signed transfer, it can take a moment…'));
      var target = kind.value === 'evm' ? { chainId: Number(chainId.value) } : { blockchain: kind.value };
      api('POST', orgPath('/reconciliation/chain'), Object.assign(target, { sinceHours: Number(since.value), missingAfterMinutes: Number(missing.value) })).then(function (r) {
        if (!r.ok) return chainStatus.replaceChildren(notice(errorText(r), 'error'));
        O.navigate('reconciliation', r.body.runId);
      });
    } }, [
      el('div', { className: 'field-row' }, [
        el('div', null, [el('label', { for: 'rc-kind', text: 'Network' }), kind]),
        el('div', null, [el('label', { for: 'rc-chain', text: 'EVM chain id' }), chainId]),
      ]),
      el('div', { className: 'field-row' }, [
        el('div', null, [el('label', { for: 'rc-since', text: 'Look back (hours)' }), since]),
      ]),
      el('label', { for: 'rc-missing', text: 'Treat as missing after (minutes)' }), missing,
      el('div', { className: 'actions' }, [el('button', { type: 'submit', className: 'accent', text: 'Run chain check' })]),
      chainStatus,
    ]);

    var start = el('input', { type: 'datetime-local', id: 'rs-start', required: true });
    var end = el('input', { type: 'datetime-local', id: 'rs-end', required: true });
    var rows = el('textarea', { id: 'rs-rows', rows: '5', placeholder: '0xtxhash,ZARP,1000000000000000000\n0xtxhash,USDC,5000000' });
    var stStatus = el('div');
    var stForm = el('form', { onsubmit: function (e) {
      e.preventDefault();
      var parsed = rows.value.split('\n').map(function (l) { return l.trim(); }).filter(Boolean).map(function (l) {
        var parts = l.split(',').map(function (s) { return s.trim(); });
        return { txHash: parts[0], asset: parts[1], amount: parts[2] };
      });
      stStatus.replaceChildren(notice('Comparing…'));
      api('POST', orgPath('/reconciliation/statements'), {
        periodStart: new Date(start.value).toISOString(), periodEnd: new Date(end.value).toISOString(), rows: parsed,
      }).then(function (r) {
        if (!r.ok) return stStatus.replaceChildren(notice(errorText(r), 'error'));
        O.navigate('reconciliation', r.body.runId);
      });
    } }, [
      el('div', { className: 'field-row' }, [
        el('div', null, [el('label', { for: 'rs-start', text: 'Period start' }), start]),
        el('div', null, [el('label', { for: 'rs-end', text: 'Period end' }), end]),
      ]),
      el('label', { for: 'rs-rows', text: 'Statement rows' }, ), rows,
      el('p', { className: 'muted', style: 'font-size:11.5px;margin-top:4px', text: 'One per line: tx hash, asset symbol, amount in base units.' }),
      el('div', { className: 'actions' }, [el('button', { type: 'submit', className: 'accent', text: 'Compare statement' })]),
      stStatus,
    ]);

    replace(content, [
      el('div', { className: 'field-row' }, [
        el('div', { className: 'card' }, [el('h2', { text: 'Run a chain check' }), el('p', { className: 'muted', style: 'font-size:12px', text: 'Compares every signed transfer with what the chain shows, and flags any transaction from this organisation\'s addresses that was never signed here.' }), chainForm]),
        el('div', { className: 'card' }, [el('h2', { text: 'Compare a statement' }), el('p', { className: 'muted', style: 'font-size:12px', text: 'Compares a customer\'s own records against what was signed.' }), stForm]),
      ]),
    ]);
  }

  function reconciliationList(content) {
    var parts = [];
    if (O.isAdmin()) {
      var open = el('button', { type: 'button', className: 'accent', text: 'Run a reconciliation', onclick: function () { reconRunForms(content); } });
      parts.push(el('div', { className: 'actions', style: 'margin-bottom:14px' }, [open]));
    }
    var table = el('div', { className: 'card' }, [O.skeleton()]);
    parts.push(table);
    replace(content, parts);
    api('GET', orgPath('/reconciliation/runs')).then(function (r) {
      if (!r.ok) return replace(table, [notice(errorText(r), 'error')]);
      replace(table, [sheet(['When', 'Kind', 'Chain', 'Result', 'Requested by'], r.body.map(reconRow), { emptyText: 'No reconciliation runs yet.' })]);
    });
  }

  function breakRow(b) {
    return el('tr', null, [
      el('td', null, [tag(b.severity, b.classification)]),
      el('td', { className: 'mono trunc hide-sm', text: short(b.txHash || b.address || b.requestId, 8) }),
      el('td', { text: b.detail }),
    ]);
  }

  function reconDetail(runId, content) {
    api('GET', orgPath('/reconciliation/runs/' + encodeURIComponent(runId))).then(function (r) {
      if (r.status === 404) return replace(content, [empty('No such run.')]);
      if (!r.ok) return failed(content, r);
      var run = r.body, s = run.summary;
      var statCards;
      if (run.kind === 'chain') {
        statCards = statGrid([
          { label: 'Examined', value: String(s.examined) },
          { label: 'Addresses checked', value: String(s.addressesChecked) },
          { label: 'Critical breaks', value: String(s.critical), tone: s.critical ? 'critical' : null },
          { label: 'Chain', value: chainName(s.chainId) },
        ]);
      } else {
        statCards = statGrid([
          { label: 'Statement rows', value: String(s.statementRows) },
          { label: 'Ledger rows', value: String(s.ledgerRows) },
          { label: 'Matched', value: String(s.matched), tone: 'accent' },
          { label: 'Breaks', value: String(s.breaks), tone: s.breaks ? 'critical' : null },
        ]);
      }
      replace(content, [
        el('button', { type: 'button', className: 'ghost sm', text: '← Reconciliation runs', onclick: function () { O.navigate('reconciliation'); } }),
        el('div', { className: 'row', style: 'margin:12px 0 14px' }, [
          el('h1', { style: 'font-size:19px', text: (run.kind === 'chain' ? 'Chain check' : 'Statement comparison') }),
          s.needsAttention ? tag('critical', 'needs attention') : tag('ok', 'clean'),
        ]),
        statCards,
        el('div', { className: 'card' }, [el('div', { className: 'card-head' }, [el('h2', { text: 'Breaks' })]), sheet(['Type', 'Reference', 'Detail'], (run.breaks || []).map(breakRow), { emptyText: 'No breaks. Everything the platform signed matches.' })]),
      ]);
    });
  }

  function reconciliation(id, content) { loading(content); if (id) reconDetail(id, content); else reconciliationList(content); }

  // -------------------------------------------------------------------- compliance

  function compliance(id, content) {
    var day = new Date().toISOString().slice(0, 10);
    var chain = 'ethereum';
    function load() {
      replace(content, [O.skeleton()]);
      api('GET', orgPath('/compliance?day=' + day + '&chain=' + encodeURIComponent(chain))).then(function (r) {
        if (!r.ok) return failed(content, r);
        var d = r.body;
        var dayInput = el('input', { type: 'date', value: day, onchange: function (e) { day = e.target.value; load(); } });
        var chainInput = el('input', { value: chain, placeholder: 'ethereum', onchange: function (e) { chain = e.target.value || 'ethereum'; load(); } });
        var rows = (d.currencies || []).map(function (c) {
          return el('tr', null, [
            el('td', { text: c.currency }),
            el('td', { className: 'num mono', text: String(c.total) }),
            el('td', { className: 'num mono', text: c.have_threshold ? String(c.threshold) : '—' }),
            el('td', null, [c.over_threshold ? tag('critical', 'over threshold') : tag('ok', 'within threshold')]),
          ]);
        });
        var parts = [
          el('div', { className: 'filterbar' }, [
            el('div', null, [el('label', { style: 'margin:0 0 3px', text: 'Day' }), dayInput]),
            el('div', null, [el('label', { style: 'margin:0 0 3px', text: 'Chain' }), chainInput]),
          ]),
        ];
        if (d.error) parts.push(notice(d.error, 'error'));
        parts.push(el('div', { className: 'card' }, [sheet(['Currency', { label: 'Total moved', num: true }, { label: 'Threshold', num: true }, 'Status'], rows, { emptyText: 'No reportable activity for this day.' })]));
        if (d.undecoded_count) parts.push(banner('Undecoded transactions: ' + d.undecoded_count, 'These moved value this platform could not attribute to a currency -- they are not represented in the totals above.', 'warn'));
        if ((d.unvalued_assets || []).length) parts.push(banner('Unvalued assets', d.unvalued_assets.join(', ') + ' have no configured price source and are excluded from the totals above.', 'warn'));
        replace(content, parts);
      });
    }
    load();
  }

  // -------------------------------------------------------------------- webhooks

  function webhooks(id, content) {
    loading(content);
    api('GET', orgPath('/webhooks')).then(function (r) {
      if (!r.ok) return failed(content, r);
      var d = r.body;
      var rows = (d.hooks || []).map(function (w) {
        return el('tr', null, [
          el('td', { className: 'mono trunc', text: w.url }),
          el('td', { className: 'muted', text: (w.events || []).join(', ') }),
          el('td', null, [w.is_active ? tag('ok', 'active') : tag('critical', 'inactive')]),
          el('td', { className: 'muted hide-sm', text: fmtDate(w.created_at) }),
        ]);
      });
      var parts = [];
      if (d.error) parts.push(notice(d.error, 'error'));
      parts.push(el('div', { className: 'card' }, [sheet(['URL', 'Events', 'Status', 'Created'], rows, { emptyText: 'No webhooks configured.' })]));
      replace(content, parts);
    });
  }

  // ----------------------------------------------------------------------- export

  window.OFBViews = {
    overview: overview,
    keys: keys,
    transactions: transactions,
    agents: agents,
    approvals: approvals,
    policy: policy,
    people: peopleView,
    travelRule: travelRule,
    reconciliation: reconciliation,
    compliance: compliance,
    webhooks: webhooks,
  };
})();
