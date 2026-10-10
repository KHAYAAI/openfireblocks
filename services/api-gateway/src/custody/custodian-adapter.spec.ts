import { assertSafeBaseUrl, CustodianError, RestCustodianAdapter } from './custodian-adapter';

const reply = (body: unknown, init: { status?: number; text?: string } = {}) =>
  (async () => new Response(init.text ?? JSON.stringify(body), { status: init.status ?? 200 })) as unknown as typeof fetch;
const adapter = (f: typeof fetch) => new RestCustodianAdapter('https://bank.example/api', 'tok', f);

describe('connector URL', () => {
  it('requires https, except for localhost, and carries no credentials', () => {
    expect(() => assertSafeBaseUrl('http://bank.example')).toThrow(/https/);
    expect(() => assertSafeBaseUrl('ftp://bank.example')).toThrow(/https/);
    expect(() => assertSafeBaseUrl('https://u:p@bank.example')).toThrow(/credentials/);
    expect(() => assertSafeBaseUrl('nonsense')).toThrow(/valid/);
    expect(assertSafeBaseUrl('http://127.0.0.1:8080').hostname).toBe('127.0.0.1');
    expect(assertSafeBaseUrl('http://internal-bank', true).hostname).toBe('internal-bank');
  });
});

describe('what a connector says is validated, not believed', () => {
  it('sends the bearer token, an idempotency key on transfers, and refuses redirects', async () => {
    const seen: any[] = [];
    const f = (async (url: string, init: any) => { seen.push({ url, init }); return new Response(JSON.stringify({ id: 'x1', status: 'pending' })); }) as unknown as typeof fetch;
    await adapter(f).initiateTransfer({ accountId: 'a1', destination: '0xabc', asset: 'ETH', amount: '1', idempotencyKey: 'approval:1' });
    expect(seen[0].url).toBe('https://bank.example/api/transfers');
    expect(seen[0].init.headers.authorization).toBe('Bearer tok');
    expect(seen[0].init.headers['idempotency-key']).toBe('approval:1');
    expect(seen[0].init.redirect).toBe('error');
  });
  it('rejects malformed accounts', async () => {
    await expect(adapter(reply({ accounts: [{ id: 'a b', name: 'x', blockchain: 'ethereum' }] })).listAccounts()).rejects.toThrow(/malformed account/);
    await expect(adapter(reply({ accounts: [{ id: 'a', name: 'x', blockchain: 'Ethereum!' }] })).listAccounts()).rejects.toThrow(/malformed account/);
    await expect(adapter(reply({ nothing: true })).listAccounts()).rejects.toThrow(/malformed accounts/);
  });
  it('rejects malformed balances: non-integer, negative, huge decimals, wrong asset', async () => {
    for (const bad of [{ asset: 'ETH', amount: '1.5', decimals: 18 }, { asset: 'ETH', amount: '-1', decimals: 18 }, { asset: 'ETH', amount: '1', decimals: 99 }, { asset: 'ETH', amount: 5, decimals: 1.5 }, { asset: 'E T H', amount: '1', decimals: 1 }]) {
      await expect(adapter(reply({ balances: [bad] })).getBalances('a1')).rejects.toThrow(/malformed balance/);
    }
    expect(await adapter(reply({ balances: [{ asset: 'ETH', amount: '12345678901234567890123', decimals: 18 }] })).getBalances('a1')).toEqual([{ asset: 'ETH', amount: '12345678901234567890123', decimals: 18 }]);
  });
  it('rejects a transfer with an unknown status or a bad id', async () => {
    await expect(adapter(reply({ id: 'x', status: 'settled' })).getTransfer('x')).rejects.toThrow(/malformed transfer/);
    await expect(adapter(reply({ id: '../x', status: 'pending' })).getTransfer('x')).rejects.toThrow(/malformed transfer/);
  });
  it('refuses path tricks in identifiers before making a request', async () => {
    let called = false;
    const f = (async () => { called = true; return new Response('{}'); }) as unknown as typeof fetch;
    await expect(adapter(f).getBalances('../../admin')).rejects.toThrow(/invalid account id/);
    await expect(adapter(f).getTransfer('a/b')).rejects.toThrow(/invalid transfer id/);
    expect(called).toBe(false);
  });
  it('turns HTTP errors, non-JSON and oversized answers into errors that say what happened', async () => {
    await expect(adapter(reply({}, { status: 503, text: 'down' })).listAccounts()).rejects.toThrow(/answered 503/);
    await expect(adapter(reply({}, { text: '<html>' })).listAccounts()).rejects.toThrow(/did not answer with JSON/);
    await expect(adapter(reply({}, { text: ' '.repeat(1_000_001) })).listAccounts()).rejects.toThrow(/unreasonably large/);
    const down = (async () => { throw new Error('ECONNREFUSED'); }) as unknown as typeof fetch;
    await expect(adapter(down).listAccounts()).rejects.toThrow(CustodianError);
  });
});
