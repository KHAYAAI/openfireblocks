// The dashboard's HTML.
//
// Server-rendered, with no build step and no client framework. That is a
// product decision rather than a taste one: this platform is sold
// self-hosted, and every additional artefact a customer has to build,
// bundle, serve and patch is a reason for their platform team to say no.
// A dashboard that ships inside the API gateway's existing container adds
// nothing to deploy.
//
// It is also the honest scope. What a compliance officer needs is to see
// balances, read a transaction history that names the real recipient and
// amount, and understand why something was refused. None of that needs a
// single-page application, and the version of this built on one would
// still be three weeks from answering those questions.

import { escape } from 'querystring';

// -- escaping --
//
// Every value interpolated into a page goes through this. Not because any
// particular field is known to be attacker-controlled, but because
// deciding that per field is how one gets missed: a webhook URL, a key
// name and a token symbol are all customer-supplied, and a transaction
// hash is chain-supplied.
export function h(value: unknown): string {
  if (value === null || value === undefined) {
    return '';
  }
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function urlPart(value: string): string {
  return escape(value);
}

export interface Nav {
  active: string;
  customerName: string;
  tier: string;
}

const STYLE = `
:root {
  --bg: #0f1115; --panel: #171a21; --line: #262b35; --text: #e6e9ef;
  --muted: #9099ab; --accent: #5b9dff; --ok: #3fb950; --warn: #d29922;
  --bad: #f85149; --mono: ui-monospace, SFMono-Regular, Menlo, monospace;
}
@media (prefers-color-scheme: light) {
  :root:not([data-theme="dark"]) {
    --bg: #f6f7f9; --panel: #ffffff; --line: #e1e4e8; --text: #1b1f24;
    --muted: #59636e; --accent: #0969da; --ok: #1a7f37; --warn: #9a6700;
    --bad: #cf222e;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; background: var(--bg); color: var(--text);
  font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
header {
  border-bottom: 1px solid var(--line); background: var(--panel);
  padding: 0 16px; position: sticky; top: 0; z-index: 5;
}
.bar { max-width: 1180px; margin: 0 auto; display: flex; align-items: center;
  gap: 20px; height: 56px; flex-wrap: wrap; }
.brand { font-weight: 650; letter-spacing: -0.01em; margin-right: 4px; }
nav { display: flex; gap: 16px; flex: 1; flex-wrap: wrap; }
nav a { color: var(--muted); padding: 4px 0; border-bottom: 2px solid transparent; }
nav a.on { color: var(--text); border-bottom-color: var(--accent); }
.who { color: var(--muted); font-size: 13px; }
main { max-width: 1180px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 22px; margin: 0 0 4px; letter-spacing: -0.02em; }
h2 { font-size: 16px; margin: 32px 0 12px; letter-spacing: -0.01em; }
.sub { color: var(--muted); margin: 0 0 24px; font-size: 14px; }
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 12px; margin-bottom: 8px; }
.card { background: var(--panel); border: 1px solid var(--line); border-radius: 10px;
  padding: 14px 16px; }
.card .n { font-size: 26px; font-weight: 600; letter-spacing: -0.02em; }
.card .l { color: var(--muted); font-size: 13px; margin-top: 2px; }
.panel { background: var(--panel); border: 1px solid var(--line);
  border-radius: 10px; overflow: hidden; }
table { width: 100%; border-collapse: collapse; font-size: 14px; }
th, td { text-align: left; padding: 10px 14px; border-bottom: 1px solid var(--line);
  vertical-align: top; }
th { color: var(--muted); font-weight: 500; font-size: 12px;
  text-transform: uppercase; letter-spacing: 0.04em; }
tr:last-child td { border-bottom: none; }
.mono { font-family: var(--mono); font-size: 13px; }
.trunc { font-family: var(--mono); font-size: 13px; }
.pill { display: inline-block; padding: 1px 8px; border-radius: 99px;
  font-size: 12px; border: 1px solid var(--line); }
.pill.ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, transparent); }
.pill.warn { color: var(--warn); border-color: color-mix(in srgb, var(--warn) 40%, transparent); }
.pill.bad { color: var(--bad); border-color: color-mix(in srgb, var(--bad) 40%, transparent); }
.empty { padding: 28px 16px; color: var(--muted); text-align: center; font-size: 14px; }
.note { background: color-mix(in srgb, var(--warn) 9%, var(--panel));
  border: 1px solid color-mix(in srgb, var(--warn) 35%, transparent);
  border-radius: 8px; padding: 12px 14px; font-size: 14px; margin: 16px 0; }
.note strong { color: var(--warn); }
form.login { max-width: 380px; margin: 14vh auto; background: var(--panel);
  border: 1px solid var(--line); border-radius: 12px; padding: 28px; }
label { display: block; font-size: 13px; color: var(--muted); margin-bottom: 6px; }
input[type=password], input[type=text], input[type=date], select {
  width: 100%; padding: 9px 11px; border-radius: 8px; border: 1px solid var(--line);
  background: var(--bg); color: var(--text); font: inherit;
}
button { padding: 9px 16px; border-radius: 8px; border: 1px solid var(--accent);
  background: var(--accent); color: #fff; font: inherit; font-weight: 550;
  cursor: pointer; }
button.ghost { background: transparent; color: var(--text); border-color: var(--line); }
.err { color: var(--bad); font-size: 14px; margin-bottom: 14px; }
.row { display: flex; gap: 10px; align-items: flex-end; flex-wrap: wrap; margin-bottom: 14px; }
.row > div { flex: 0 0 auto; }
.kv { display: grid; grid-template-columns: max-content 1fr; gap: 6px 20px;
  font-size: 14px; padding: 14px 16px; }
.kv dt { color: var(--muted); }
.kv dd { margin: 0; font-family: var(--mono); font-size: 13px; word-break: break-all; }
@media (max-width: 680px) {
  .bar { height: auto; padding: 10px 0; }
  table { font-size: 13px; }
  th, td { padding: 8px 10px; }
}
`;

export function layout(title: string, nav: Nav | null, body: string): string {
  const tabs: Array<[string, string]> = [
    ['overview', 'Overview'],
    ['keys', 'Keys'],
    ['transactions', 'Transactions'],
    ['compliance', 'Compliance'],
    ['webhooks', 'Webhooks'],
  ];
  const header = nav
    ? `<header><div class="bar">
         <span class="brand">OpenFireblocks</span>
         <nav>${tabs
           .map(
             ([slug, label]) =>
               `<a href="/dashboard/${slug}"${nav.active === slug ? ' class="on"' : ''}>${label}</a>`,
           )
           .join('')}</nav>
         <span class="who">${h(nav.customerName)} · ${h(nav.tier)} ·
           <a href="/dashboard/sign-out">Sign out</a></span>
       </div></header>`
    : '';

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${h(title)} · OpenFireblocks</title>
<style>${STYLE}</style>
</head>
<body>${header}<main>${body}</main></body>
</html>`;
}

export function loginPage(error?: string): string {
  return layout(
    'Sign in',
    null,
    `<form class="login" method="post" action="/dashboard/sign-in">
       <h1>OpenFireblocks</h1>
       <p class="sub">Sign in with your API key.</p>
       ${error ? `<p class="err">${h(error)}</p>` : ''}
       <label for="k">API key</label>
       <input id="k" name="apiKey" type="password" autocomplete="off" autofocus required>
       <p style="margin:16px 0 0"><button type="submit">Sign in</button></p>
     </form>`,
  );
}

export function statusPill(status: string): string {
  const s = (status ?? '').toLowerCase();
  if (['active', 'completed', 'confirmed', 'signed', 'delivered'].includes(s)) {
    return `<span class="pill ok">${h(status)}</span>`;
  }
  if (['failed', 'denied', 'rejected', 'suspended'].includes(s)) {
    return `<span class="pill bad">${h(status)}</span>`;
  }
  return `<span class="pill warn">${h(status)}</span>`;
}

// Long hex is unreadable in full and ambiguous when truncated to one end.
// Both ends, with the middle elided, is what lets someone match a value
// against another system by eye.
export function shortHex(value: string | null | undefined, keep = 8): string {
  if (!value) {
    return '—';
  }
  const s = String(value);
  if (s.length <= keep * 2 + 3) {
    return h(s);
  }
  return `<span title="${h(s)}">${h(s.slice(0, keep))}…${h(s.slice(-keep))}</span>`;
}

export function when(value: string | Date | null | undefined): string {
  if (!value) {
    return '—';
  }
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) {
    return '—';
  }
  return `<span title="${h(d.toISOString())}">${h(d.toISOString().slice(0, 16).replace('T', ' '))}</span>`;
}

export function table(headers: string[], rows: string[], emptyMessage: string): string {
  if (rows.length === 0) {
    return `<div class="panel"><div class="empty">${h(emptyMessage)}</div></div>`;
  }
  return `<div class="panel"><table>
    <thead><tr>${headers.map((c) => `<th>${h(c)}</th>`).join('')}</tr></thead>
    <tbody>${rows.join('')}</tbody></table></div>`;
}
