import { spawn } from 'child_process';
import { createServer } from 'net';
import { JsonRpcProvider, Wallet, parseUnits } from 'ethers';

// Test support: an EVM chain of the spec's own.
//
// Each spec launches its own dev chain (ganache, through npx, on a free port) rather than
// sharing one: two specs driving one dev chain at the same time hung it intermittently, and
// a chain per spec also means no prerequisite to start one first. It is launched, not
// installed as a dependency of this package, because ganache bundles a dependency tree that
// `npm audit` reports on even for a dev dependency, which would break the production
// advisory gate. Everything transacts from fresh wallets funded by the chain's one
// pre-funded account.
const FUNDED_KEY = '0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a';

export interface TestChain { url: string; chainId: number; provider: JsonRpcProvider; stop(): Promise<void> }

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createServer();
    s.listen(0, '127.0.0.1', () => { const p = (s.address() as { port: number }).port; s.close(() => resolve(p)); });
    s.on('error', reject);
  });
}

export async function startChain(chainId = 1337): Promise<TestChain> {
  const port = await freePort();
  // Its own process group, so stopping takes down what npx spawned too.
  const child = spawn('npx', ['--yes', 'ganache@7.9.2', '--chain.chainId', String(chainId), '--server.port', String(port), '--logging.quiet',
    '--wallet.accounts', `${FUNDED_KEY},${10n ** 24n}`], { detached: true, stdio: 'ignore' });
  // Failing to launch at all (no npx) or exiting early (no network to fetch ganache) is a
  // reason to stop waiting now, not after the timeout.
  let launchFailure = '';
  child.on('error', (e) => { launchFailure = e.message; });
  child.on('exit', (code) => { if (code !== null) launchFailure = `ganache exited with code ${code}`; });
  const url = `http://127.0.0.1:${port}`;
  const provider = new JsonRpcProvider(url, undefined, { staticNetwork: true, batchMaxCount: 1, pollingInterval: 50, cacheTimeout: -1 });
  const stop = async () => {
    provider.destroy();
    try { process.kill(-child.pid!, 'SIGKILL'); } catch { /* already gone */ }
  };
  const deadline = Date.now() + 90_000;
  for (;;) {
    try {
      const r = await fetch(url, { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}' });
      if (r.ok) break;
    } catch { /* not up yet */ }
    if (launchFailure || Date.now() > deadline) { await stop(); throw new Error(`the dev chain did not start (is ganache available through npx?): ${launchFailure || 'timed out'}`); }
    await new Promise((res) => setTimeout(res, 250));
  }
  return { url, chainId, provider, stop };
}

// Waits for a receipt by asking for it. ethers' own tx.wait() subscribes to new blocks, and a
// dev chain that mines only when sent a transaction can mine this one between ethers' receipt
// check and its subscription -- after which no further block ever arrives and the wait never
// ends. Asking directly has no such window.
export async function waitMined(provider: JsonRpcProvider, hash: string, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const r = await provider.getTransactionReceipt(hash);
    if (r) {
      if (r.status === 0) throw new Error(`transaction ${hash} reverted`);
      return r;
    }
    if (Date.now() > deadline) throw new Error(`transaction ${hash} was not mined within ${timeoutMs} ms`);
    await new Promise((res) => setTimeout(res, 20));
  }
}

export async function fundedWallet(provider: JsonRpcProvider, eth = '20'): Promise<Wallet> {
  const w = new Wallet(Wallet.createRandom().privateKey, provider);
  const funder = new Wallet(FUNDED_KEY, provider);
  const tx = await funder.sendTransaction({ to: w.address, value: parseUnits(eth, 18) });
  await waitMined(provider, tx.hash);
  return w;
}
