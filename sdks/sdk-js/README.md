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
  to: '0x742d35Cc6634C0532925a3b844Bc9e7595f42bE',
  value: '0',
  gasLimit: 21000,
  gasPrice: '20000000000',
  nonce: 0,
});

console.log(result.txHash, result.status); // 'signed' | 'broadcasted'

const txs = await client.listTransactions();
const audit = await client.getAuditTrail(result.requestId);
```

Non-2xx responses throw `OpenFireblocksError` with `.status` and the parsed
`.body` (e.g. policy denials return HTTP 403 with `{ denials: [...] }`).

Runtimes without a global `fetch` (Node < 18) can pass one:
`new OpenFireblocksClient({ ..., fetch: myFetch })`.

## Develop

```bash
npm ci && npm run build && npm test
```
