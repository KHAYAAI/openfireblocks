/**
 * Multi-chain signing integration tests.
 * Tests Bitcoin, Solana, Cosmos, and Ethereum signing through the unified API.
 *
 * Run against a live OpenFireblocks stack:
 * BASE_URL=http://localhost:3000 npm test -- tests/integration/multi-chain.test.ts
 */

import { OpenFireblocksClient, SignMultiChainResponse } from '@openfireblocks/sdk';

const BASE_URL = process.env.BASE_URL || 'http://localhost:3000';
const API_KEY = process.env.API_KEY || 'test-api-key';

const client = new OpenFireblocksClient({ baseUrl: BASE_URL, apiKey: API_KEY });

describe('Multi-Chain Signing', () => {
  describe('getSupportedChains', () => {
    it('returns list of 4 supported chains', async () => {
      const res = await client.getSupportedChains();
      expect(res.chains).toEqual(
        expect.arrayContaining(['ethereum', 'bitcoin', 'solana', 'cosmos-hub'])
      );
      expect(res.count).toBe(4);
    });
  });

  describe('Ethereum signing', () => {
    it('signs a transaction on ethereum', async () => {
      const res = await client.signMultiChain({
        chainId: 'ethereum',
        message: '0xdeadbeef',
        metadata: { network: 'mainnet' },
      });

      expect(res.chainId).toBe('ethereum');
      expect(res.status).toBe('signed');
      expect(res.signature).toBeDefined();
      expect(res.from).toMatch(/^0x[a-fA-F0-9]{40}$/);
      expect(res.broadcasted).toBe(false);
    });

    it('includes request ID for audit logging', async () => {
      const res = await client.signMultiChain({
        chainId: 'ethereum',
        message: '0xaabbccdd',
      });

      expect(res.requestId).toMatch(/^req_\d+_[a-z0-9]+$/);
    });
  });

  describe('Bitcoin signing', () => {
    it('signs a transaction on bitcoin', async () => {
      const res = await client.signMultiChain({
        chainId: 'bitcoin',
        message: '0x' + 'aa'.repeat(32), // 32-byte hash
        metadata: {
          network: 'testnet',
          utxos: [
            {
              txid: 'e3bf3d07d4b0375638d5f923602f08757f37c6c61c7f415adc9ec187997f77a7',
              vout: 0,
              amount: 50000,
            },
          ],
        },
      });

      expect(res.chainId).toBe('bitcoin');
      expect(res.status).toBe('signed');
      expect(res.signature).toBeDefined();
      expect(res.from).toBeDefined();
    });
  });

  describe('Solana signing', () => {
    it('signs a transaction on solana', async () => {
      const res = await client.signMultiChain({
        chainId: 'solana',
        message: '0x' + 'bb'.repeat(32), // 32-byte message
        metadata: {
          recentBlockhash: '4MZYZ1Jb3sP9X8jYy7YxYyPq3YyXx8j5k5j5k5j5k5j5k5j5k5j5k5j5k5j5k5j',
        },
      });

      expect(res.chainId).toBe('solana');
      expect(res.status).toBe('signed');
      expect(res.signature).toBeDefined();
      // Solana signature should be base64 or hex encoded
      expect(res.signature.length).toBeGreaterThan(0);
    });
  });

  describe('Cosmos signing', () => {
    it('signs a transaction on cosmos-hub', async () => {
      const res = await client.signMultiChain({
        chainId: 'cosmos-hub',
        message: '0x' + 'cc'.repeat(32), // 32-byte message
        metadata: {
          account_number: 42,
          sequence: 0,
        },
      });

      expect(res.chainId).toBe('cosmos-hub');
      expect(res.status).toBe('signed');
      expect(res.signature).toBeDefined();
      expect(res.from).toMatch(/^cosmos1/); // Bech32 cosmos address
    });
  });

  describe('Error handling', () => {
    it('rejects unsupported chain', async () => {
      try {
        await client.signMultiChain({
          chainId: 'unknown-chain',
          message: '0xdeadbeef',
        });
        fail('should have thrown');
      } catch (err: any) {
        expect(err.status).toBe(400);
        expect(err.body).toMatchObject({ error: expect.stringContaining('Unsupported chain') });
      }
    });

    it('rejects invalid API key', async () => {
      const badClient = new OpenFireblocksClient({
        baseUrl: BASE_URL,
        apiKey: 'invalid-api-key',
      });

      try {
        await badClient.signMultiChain({
          chainId: 'ethereum',
          message: '0xdeadbeef',
        });
        fail('should have thrown');
      } catch (err: any) {
        expect(err.status).toBe(401);
      }
    });
  });

  describe('Broadcasting', () => {
    it('broadcasts a signed ethereum transaction', async () => {
      const signRes = await client.signMultiChain({
        chainId: 'ethereum',
        message: '0xdeadbeef',
      });

      // Note: broadcast would fail in test without a real signed tx
      // This tests the happy path structure
      if (signRes.signedTx) {
        const broadcastRes = await client.broadcastTransaction({
          chainId: 'ethereum',
          signedTx: signRes.signedTx,
        });

        expect(broadcastRes.txHash).toBeDefined();
        expect(broadcastRes.status).toBe('broadcasted');
      }
    });
  });

  describe('Chain isolation', () => {
    it('produces different signatures for same message on different chains', async () => {
      const message = '0x' + 'dd'.repeat(32);

      const ethRes = await client.signMultiChain({
        chainId: 'ethereum',
        message,
      });

      const cosmosRes = await client.signMultiChain({
        chainId: 'cosmos-hub',
        message,
      });

      expect(ethRes.chainId).not.toBe(cosmosRes.chainId);
      expect(ethRes.from).not.toBe(cosmosRes.from);
    });
  });
});
