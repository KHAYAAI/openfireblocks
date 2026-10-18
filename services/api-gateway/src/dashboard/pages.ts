import { h, layout, Nav, shortHex, statusPill, table, when } from './views';

// The pages themselves.
//
// What each one has to answer is a question somebody actually asks, which
// is why there is no "analytics" page and no chart. A compliance officer
// asks: what do we hold, what moved, to whom, how much, and why was that
// one refused. An operator asks: is the key healthy, did the ceremony
// complete, which parties signed.

export interface OverviewData {
  keyCount: number;
  activeKeys: number;
  pendingKeys: number;
  failedKeys: number;
  signaturesThisMonth: number;
  recentTransactions: TransactionRow[];
  isolation: string | null;
}

export interface TransactionRow {
  request_id: string;
  chain: string;
  status: string;
  asset_symbol: string | null;
  effective_to: string | null;
  effective_amount: string | null;
  asset_decimals: number | null;
  asset_peg: string | null;
  to_address: string;
  amount: string;
  tx_hash: string | null;
  created_at: string;
}

// Base units to a readable amount, using the decimals recorded against
// the transaction rather than a lookup. The registry can be edited later;
// what an old transaction moved cannot.
function amountOf(tx: TransactionRow): string {
  const raw = tx.effective_amount ?? tx.amount;
  if (!raw) {
    return '—';
  }
  const decimals = tx.asset_symbol === 'NATIVE' || !tx.asset_symbol ? 18 : (tx.asset_decimals ?? 0);
  if (!decimals) {
    return h(raw);
  }
  const negative = raw.startsWith('-');
  const digits = (negative ? raw.slice(1) : raw).padStart(decimals + 1, '0');
  const whole = digits.slice(0, digits.length - decimals);
  const frac = digits.slice(digits.length - decimals).replace(/0+$/, '');
  return h(`${negative ? '-' : ''}${whole}${frac ? '.' + frac : ''}`);
}

function assetOf(tx: TransactionRow): string {
  if (!tx.asset_symbol) {
    // Null means nobody decoded this transaction -- which is a truthful
    // answer and a more useful one than showing the native value of a
    // contract call as if it were a payment.
    return '<span class="pill warn">undecoded</span>';
  }
  if (tx.asset_symbol === 'NATIVE') {
    return `<span class="mono">native</span>`;
  }
  return `<span class="mono">${h(tx.asset_symbol)}</span>`;
}

function transactionRows(txs: TransactionRow[]): string[] {
  return txs.map(
    (tx) => `<tr>
      <td>${when(tx.created_at)}</td>
      <td>${assetOf(tx)}</td>
      <td class="mono">${amountOf(tx)}</td>
      <td class="trunc">${shortHex(tx.effective_to ?? tx.to_address, 6)}</td>
      <td>${h(tx.chain)}</td>
      <td>${statusPill(tx.status)}</td>
      <td class="trunc">${shortHex(tx.tx_hash, 6)}</td>
    </tr>`,
  );
}

const TX_HEADERS = ['When', 'Asset', 'Amount', 'Recipient', 'Chain', 'Status', 'Tx hash'];

export function overviewPage(nav: Nav, d: OverviewData): string {
  const isolationNote =
    d.isolation && d.isolation !== 'multi-account'
      ? `<div class="note"><strong>Party isolation: ${h(d.isolation)}.</strong>
           The signing parties are not on separate infrastructure, so the
           2-of-3 threshold does not yet protect against a host compromise.
           This is a deployment state, not a software one — see
           docs/deployment/PARTY-ISOLATION.md.</div>`
      : '';

  return layout(
    'Overview',
    nav,
    `<h1>Overview</h1>
     <p class="sub">What this account holds and what it has moved.</p>
     ${isolationNote}
     <div class="cards">
       <div class="card"><div class="n">${d.keyCount}</div><div class="l">Keys</div></div>
       <div class="card"><div class="n">${d.activeKeys}</div><div class="l">Active</div></div>
       <div class="card"><div class="n">${d.pendingKeys}</div><div class="l">Pending DKG</div></div>
       <div class="card"><div class="n">${d.failedKeys}</div><div class="l">Failed</div></div>
       <div class="card"><div class="n">${d.signaturesThisMonth}</div><div class="l">Signatures, 30 days</div></div>
     </div>
     <h2>Recent activity</h2>
     ${table(
       TX_HEADERS,
       transactionRows(d.recentTransactions),
       'Nothing has been signed on this account yet.',
     )}`,
  );
}

export interface KeyRow {
  key_id: string;
  name: string;
  blockchain: string;
  threshold: number;
  total_parties: number;
  address: string | null;
  status: string;
  created_at: string;
}

export function keysPage(nav: Nav, keys: KeyRow[]): string {
  const rows = keys.map(
    (k) => `<tr>
      <td><a href="/dashboard/keys/${encodeURIComponent(k.key_id)}">${h(k.name || k.key_id)}</a></td>
      <td>${h(k.blockchain)}</td>
      <td class="mono">${k.threshold} of ${k.total_parties}</td>
      <td class="trunc">${shortHex(k.address, 8)}</td>
      <td>${statusPill(k.status)}</td>
      <td>${when(k.created_at)}</td>
    </tr>`,
  );
  return layout(
    'Keys',
    nav,
    `<h1>Keys</h1>
     <p class="sub">Each key is a threshold key. No party holds enough of it to sign alone.</p>
     ${table(
       ['Name', 'Chain', 'Threshold', 'Address', 'Status', 'Created'],
       rows,
       'No keys have been provisioned on this account.',
     )}`,
  );
}

export interface BalanceRow {
  symbol: string;
  balance: string | null;
  peg_currency: string | null;
  error?: string;
}

export interface KeyDetailData {
  key: KeyRow & { public_key?: string | null };
  addresses: Record<string, string> | null;
  balances: { native: string | null; tokens: BalanceRow[]; chainId: number } | null;
  balanceError: string | null;
  signings: Array<{
    request_id: string;
    status: string;
    blockchain: string;
    transaction_hash: string | null;
    signing_parties: number[] | null;
    latency_ms: number | null;
    created_at: string;
  }>;
  ceremonies: Array<{
    ceremony_id: string;
    status: string;
    current_round: number | null;
    started_at: string;
    completed_at: string | null;
    error_message: string | null;
  }>;
}

export function keyDetailPage(nav: Nav, d: KeyDetailData): string {
  const addresses = d.addresses
    ? Object.entries(d.addresses)
        .filter(([, v]) => v)
        .map(([label, value]) => `<dt>${h(label)}</dt><dd>${h(value)}</dd>`)
        .join('')
    : '';

  const balanceSection = d.balanceError
    ? `<div class="note">Balances could not be read: ${h(d.balanceError)}</div>`
    : d.balances
      ? table(
          ['Asset', 'Balance', 'Peg'],
          [
            `<tr><td class="mono">native</td><td class="mono">${h(d.balances.native ?? '—')}</td><td>—</td></tr>`,
            ...d.balances.tokens.map(
              (t) => `<tr>
                <td class="mono">${h(t.symbol)}</td>
                <td class="mono">${t.balance === null ? `<span class="pill bad">unreadable</span>` : h(t.balance)}</td>
                <td>${h(t.peg_currency ?? '—')}</td>
              </tr>`,
            ),
          ],
          'No registered tokens on this chain.',
        )
      : `<div class="panel"><div class="empty">Balance reads are not configured for this key's chain.</div></div>`;

  const signingRows = d.signings.map(
    (s) => `<tr>
      <td>${when(s.created_at)}</td>
      <td>${statusPill(s.status)}</td>
      <td class="mono">${s.signing_parties ? h(s.signing_parties.join(', ')) : '—'}</td>
      <td class="mono">${s.latency_ms === null ? '—' : h(`${s.latency_ms} ms`)}</td>
      <td class="trunc">${shortHex(s.transaction_hash, 6)}</td>
    </tr>`,
  );

  const ceremonyRows = d.ceremonies.map(
    (c) => `<tr>
      <td class="trunc">${shortHex(c.ceremony_id, 6)}</td>
      <td>${statusPill(c.status)}</td>
      <td class="mono">${c.current_round ?? '—'}</td>
      <td>${when(c.started_at)}</td>
      <td>${when(c.completed_at)}</td>
      <td>${h(c.error_message ?? '')}</td>
    </tr>`,
  );

  return layout(
    d.key.name || 'Key',
    nav,
    `<h1>${h(d.key.name || d.key.key_id)}</h1>
     <p class="sub">${h(d.key.blockchain)} · ${d.key.threshold} of ${d.key.total_parties} ·
       ${h(d.key.status)}</p>

     <div class="panel"><dl class="kv">
       <dt>Key id</dt><dd>${h(d.key.key_id)}</dd>
       ${addresses}
       ${d.key.public_key ? `<dt>Public key</dt><dd>${h(d.key.public_key)}</dd>` : ''}
     </dl></div>

     <h2>Balances</h2>
     ${balanceSection}

     <h2>Signing history</h2>
     ${table(
       ['When', 'Status', 'Parties', 'Latency', 'Tx hash'],
       signingRows,
       'This key has not signed anything.',
     )}

     <h2>Key generation</h2>
     ${table(
       ['Ceremony', 'Status', 'Round', 'Started', 'Completed', 'Error'],
       ceremonyRows,
       'No DKG ceremonies recorded.',
     )}`,
  );
}

export function transactionsPage(nav: Nav, txs: TransactionRow[]): string {
  return layout(
    'Transactions',
    nav,
    `<h1>Transactions</h1>
     <p class="sub">
       The recipient and amount shown are what each transaction actually moves —
       decoded from the calldata for a token transfer, not the contract address
       and zero that appear on the transaction itself.
     </p>
     ${table(TX_HEADERS, transactionRows(txs), 'Nothing has been signed on this account yet.')}`,
  );
}

export interface ComplianceData {
  day: string;
  chain: string;
  currencies: Array<{
    currency: string;
    total: number;
    threshold: number;
    have_threshold: boolean;
    over_threshold: boolean;
    by_asset: Record<string, string>;
    transaction_ids: string[];
  }>;
  undecoded_count: number;
  unvalued_assets: string[];
  error: string | null;
}

export function compliancePage(nav: Nav, d: ComplianceData): string {
  const rows = d.currencies.map((c) => {
    const verdict = !c.have_threshold
      ? `<span class="pill warn">no threshold set</span>`
      : c.over_threshold
        ? `<span class="pill bad">reportable</span>`
        : `<span class="pill ok">under</span>`;
    const assets = Object.entries(c.by_asset)
      .map(([sym, total]) => `${h(total)} ${h(sym)}`)
      .join(', ');
    return `<tr>
      <td class="mono">${h(c.currency)}</td>
      <td class="mono">${h(c.total.toFixed(2))}</td>
      <td class="mono">${c.have_threshold ? h(c.threshold.toFixed(2)) : '—'}</td>
      <td>${verdict}</td>
      <td class="mono">${assets || '—'}</td>
      <td class="mono">${c.transaction_ids.length}</td>
    </tr>`;
  });

  const gaps: string[] = [];
  if (d.undecoded_count > 0) {
    gaps.push(
      `${d.undecoded_count} transfer(s) could not be decoded and are <strong>not</strong> in any total above.`,
    );
  }
  if (d.unvalued_assets.length > 0) {
    gaps.push(
      `Moved but not valued (no peg, or no price): ${d.unvalued_assets.map(h).join(', ')}.`,
    );
  }

  return layout(
    'Compliance',
    nav,
    `<h1>Threshold reporting</h1>
     <p class="sub">
       One day's activity, totalled per currency and checked against that
       currency's own threshold. Totals are not converted between currencies —
       there is no exchange rate source here, and a total built on a guessed
       rate is not one a filing can rest on.
     </p>
     <form method="get" class="row">
       <div><label for="day">Day (UTC)</label>
         <input id="day" type="date" name="day" value="${h(d.day)}"></div>
       <div><label for="chain">Chain</label>
         <input id="chain" type="text" name="chain" value="${h(d.chain)}"></div>
       <div><button type="submit">Show</button></div>
     </form>
     ${d.error ? `<div class="note">${h(d.error)}</div>` : ''}
     ${
       gaps.length
         ? `<div class="note"><strong>What these totals leave out.</strong><br>${gaps.join('<br>')}</div>`
         : ''
     }
     ${table(
       ['Currency', 'Total', 'Threshold', 'Verdict', 'Assets', 'Transfers'],
       rows,
       'No activity on this chain for this day.',
     )}`,
  );
}

export interface WebhookRow {
  webhook_id: string;
  url: string;
  events: string[];
  is_active: boolean;
  created_at: string;
}

export function webhooksPage(nav: Nav, hooks: WebhookRow[], error: string | null): string {
  const rows = hooks.map(
    (w) => `<tr>
      <td class="trunc">${h(w.url)}</td>
      <td class="mono">${h((w.events ?? []).join(', '))}</td>
      <td>${w.is_active ? `<span class="pill ok">active</span>` : `<span class="pill bad">disabled</span>`}</td>
      <td>${when(w.created_at)}</td>
    </tr>`,
  );
  return layout(
    'Webhooks',
    nav,
    `<h1>Webhooks</h1>
     <p class="sub">Where this account is told that something happened.</p>
     ${error ? `<div class="note">${h(error)}</div>` : ''}
     ${table(
       ['Endpoint', 'Events', 'Status', 'Created'],
       rows,
       'No endpoints are registered, so no events are being delivered.',
     )}`,
  );
}
