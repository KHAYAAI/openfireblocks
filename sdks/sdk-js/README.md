# @openfireblocks/sdk

Dependency-free JavaScript/TypeScript client for the OpenFireblocks API.

## Install

```bash
npm install @openfireblocks/sdk
```

## Usage

```ts
import { OpenFireblocksClient } from '@openfireblocks/sdk';

const client = new OpenFireblocksClient({
  baseUrl: 'https://api.openfireblocks.example',
  apiKey: process.env.OFB_API_KEY!, // your tenant API key
});

const result = await client.sign({
  chainId: 11155111, // Sepolia
  to: '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045',
  value: '0',
  gasLimit: 21000,
  gasPrice: '20000000000',
  nonce: 0,
});

console.log(result.txHash, result.status); // 'signed' | 'broadcasted'

const txs = await client.listTransactions();
const audit = await client.getAuditTrail(result.requestId);
```

## Multi-Chain Signing

Sign transactions on multiple blockchains (Ethereum, Bitcoin, Solana, Cosmos):

```ts
// Get supported chains
const chains = await client.getSupportedChains();
console.log(chains.chains); // ['ethereum', 'bitcoin', 'solana', 'cosmos-hub']

// Sign on Ethereum
const ethResult = await client.signMultiChain({
  chainId: 'ethereum',
  message: '0xdeadbeef',
  metadata: {
    network: 'mainnet',
  },
});

// Sign on Bitcoin
const btcResult = await client.signMultiChain({
  chainId: 'bitcoin',
  message: '0x...',
  metadata: {
    network: 'testnet',
    utxos: [{ txid: '...', vout: 0, amount: 50000 }],
  },
});

// Sign on Solana
const solanaResult = await client.signMultiChain({
  chainId: 'solana',
  message: '0x...',
  metadata: {
    recentBlockhash: '...',
  },
});

// Sign on Cosmos
const cosmosResult = await client.signMultiChain({
  chainId: 'cosmos-hub',
  message: '0x...',
  metadata: {
    account_number: 123,
    sequence: 0,
  },
});

// Broadcast a signed transaction
const broadcastResult = await client.broadcastTransaction({
  chainId: 'ethereum',
  signedTx: ethResult.signedTx!,
});

console.log(broadcastResult.txHash, broadcastResult.status);
```

Non-2xx responses throw `OpenFireblocksError` with `.status` and the parsed
`.body` (e.g. policy denials return HTTP 403 with `{ denials: [...] }`).

Runtimes without a global `fetch` (Node < 18) can pass one:
`new OpenFireblocksClient({ ..., fetch: myFetch })`.

## Develop

```bash
npm ci && npm run build && npm test
```
