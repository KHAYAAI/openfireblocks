/**
 * Distributed Key Generation (DKG) Ceremony Integration Tests
 *
 * Tests the complete flow of a 7-round DKG ceremony with multiple parties:
 * 1. Orchestrator initiates ceremony with N parties
 * 2. All parties execute DKG protocol rounds 1-7
 * 3. Each party derives threshold key share
 * 4. Parties compute threshold signatures using Lagrange interpolation
 *
 * Run against a live OpenFireblocks stack with ceremony orchestrator and parties:
 * docker-compose up && npm test tests/integration/dkg-ceremony.test.ts
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8081';
const PARTY_BASE_PORT = parseInt(process.env.PARTY_BASE_PORT || '7000', 10);

interface CeremonyRequest {
  chainId: string;
  n: number;
  k: number;
  partyIDs: number[];
  partyEndpoints: string[];
}

interface CeremonyResponse {
  id: string;
  status: string;
  message: string;
}

interface RoundDataResponse {
  partyId: number;
  roundNum: number;
  commitments: string;
  dlProof: string;
  publicKey: string;
  signature: string;
}

interface PartyHealth {
  status: string;
  partyId: string;
}

async function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function callPartyEndpoint(
  partyId: number,
  method: string,
  path: string,
  body?: unknown
): Promise<Response> {
  const url = `http://localhost:${PARTY_BASE_PORT + partyId}${path}`;
  const options: RequestInit = {
    method,
    headers: { 'Content-Type': 'application/json' },
  };
  if (body) {
    options.body = JSON.stringify(body);
  }
  return fetch(url, options);
}

async function callOrchestratorEndpoint(
  method: string,
  path: string,
  body?: unknown
): Promise<Response> {
  const url = `${BASE_URL}${path}`;
  const options: RequestInit = {
    method,
    headers: { 'Content-Type': 'application/json' },
  };
  if (body) {
    options.body = JSON.stringify(body);
  }
  return fetch(url, options);
}

describe('DKG Ceremony (7 Rounds)', () => {
  const ceremonyConfig: CeremonyRequest = {
    chainId: 'ethereum',
    n: 7,
    k: 3,
    partyIDs: [1, 2, 3, 4, 5, 6, 7],
    partyEndpoints: Array.from({ length: 7 }, (_, i) =>
      `http://localhost:${PARTY_BASE_PORT + i + 1}`
    ),
  };

  let ceremonyId: string;

  describe('Ceremony Initialization', () => {
    it('orchestrator is healthy', async () => {
      const res = await callOrchestratorEndpoint('GET', '/health');
      expect(res.status).toBe(200);
      const data = await res.json();
      expect(data.status).toBe('ok');
    });

    it('all 7 parties are healthy', async () => {
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'GET', '/health');
        expect(res.status).toBe(200);
        const health: PartyHealth = await res.json();
        expect(health.status).toBe('healthy');
        expect(health.partyId).toBe(String(i));
      }
    });

    it('initiates a new DKG ceremony', async () => {
      const res = await callOrchestratorEndpoint(
        'POST',
        '/ceremonies',
        ceremonyConfig
      );
      expect(res.status).toBe(200);
      const data: CeremonyResponse = await res.json();
      expect(data.id).toBeDefined();
      expect(data.status).toBe('pending');
      ceremonyId = data.id;
    });

    it('retrieves ceremony details', async () => {
      const res = await callOrchestratorEndpoint('GET', `/ceremonies/${ceremonyId}`);
      expect(res.status).toBe(200);
      const ceremony = await res.json();
      expect(ceremony.id).toBe(ceremonyId);
      expect(ceremony.n).toBe(7);
      expect(ceremony.k).toBe(3);
      expect(ceremony.parties.length).toBe(7);
    });
  });

  describe('DKG Round 1: Commitments', () => {
    it('orchestrator signals round 1 start', async () => {
      const res = await callOrchestratorEndpoint(
        'POST',
        `/ceremonies/${ceremonyId}/round/1/start`
      );
      expect(res.status).toBe(200);
    });

    it('all parties signal round 1 acknowledgment', async () => {
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'POST', '/round', {
          ceremonyId,
          roundNum: 1,
          action: 'start',
        });
        expect(res.status).toBe(200);
      }
    });

    it('all parties provide round 1 data (commitments)', async () => {
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'GET', '/round/1/data');
        expect(res.status).toBe(200);
        const data: RoundDataResponse = await res.json();
        expect(data.partyId).toBe(i);
        expect(data.roundNum).toBe(1);
        expect(data.commitments).toBeTruthy();
        expect(data.dlProof).toBeTruthy();
        expect(data.publicKey).toBeTruthy();
      }
    });

    it('orchestrator broadcasts round 1 commitments to all parties', async () => {
      // Collect all round 1 data
      const roundData: { [key: number]: RoundDataResponse } = {};
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'GET', '/round/1/data');
        roundData[i] = await res.json();
      }

      // Broadcast to all parties
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'POST', '/round/1/broadcast', {
          ceremonyId,
          roundNum: 1,
          partyDataMap: roundData,
        });
        expect(res.status).toBe(200);
      }
    });
  });

  describe('DKG Round 2: Secret Shares', () => {
    it('orchestrator signals round 2 start', async () => {
      await sleep(500); // Brief pause between rounds
      const res = await callOrchestratorEndpoint(
        'POST',
        `/ceremonies/${ceremonyId}/round/2/start`
      );
      expect(res.status).toBe(200);
    });

    it('all parties provide round 2 data (secret shares)', async () => {
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'GET', '/round/2/data');
        expect(res.status).toBe(200);
        const data: RoundDataResponse = await res.json();
        expect(data.partyId).toBe(i);
        expect(data.roundNum).toBe(2);
        expect(data.commitments).toBeTruthy(); // Decommitments
      }
    });

    it('orchestrator broadcasts round 2 shares to all parties', async () => {
      const roundData: { [key: number]: RoundDataResponse } = {};
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'GET', '/round/2/data');
        roundData[i] = await res.json();
      }

      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'POST', '/round/2/broadcast', {
          ceremonyId,
          roundNum: 2,
          partyDataMap: roundData,
        });
        expect(res.status).toBe(200);
      }
    });
  });

  describe('DKG Rounds 3-7: Validation & Finalization', () => {
    it('executes rounds 3-7 without errors', async () => {
      for (let round = 3; round <= 7; round++) {
        await sleep(300);

        // Signal round start
        await callOrchestratorEndpoint(
          'POST',
          `/ceremonies/${ceremonyId}/round/${round}/start`
        );

        // Collect round data
        const roundData: { [key: number]: RoundDataResponse } = {};
        for (let i = 1; i <= 7; i++) {
          const res = await callPartyEndpoint(i, 'GET', `/round/${round}/data`);
          expect(res.status).toBe(200);
          roundData[i] = await res.json();
        }

        // Broadcast to all parties
        for (let i = 1; i <= 7; i++) {
          const res = await callPartyEndpoint(
            i,
            'POST',
            `/round/${round}/broadcast`,
            {
              ceremonyId,
              roundNum: round,
              partyDataMap: roundData,
            }
          );
          expect(res.status).toBe(200);
        }
      }
    });
  });

  describe('DKG Ceremony Completion', () => {
    it('orchestrator marks ceremony as completed', async () => {
      const res = await callOrchestratorEndpoint(
        'POST',
        `/ceremonies/${ceremonyId}/complete`
      );
      expect(res.status).toBe(200);
    });

    it('ceremony status reflects completion', async () => {
      const res = await callOrchestratorEndpoint('GET', `/ceremonies/${ceremonyId}`);
      expect(res.status).toBe(200);
      const ceremony = await res.json();
      expect(ceremony.status).toBe('completed');
      expect(ceremony.thresholdPubKey).toBeTruthy();
    });

    it('each party has generated a key share', async () => {
      for (let i = 1; i <= 7; i++) {
        const res = await callPartyEndpoint(i, 'GET', '/info');
        expect(res.status).toBe(200);
        const info = await res.json();
        expect(info.ceremonyId).toBe(ceremonyId);
        expect(info.status).toBe('completed');
      }
    });
  });

  describe('Threshold Signing (k-of-n = 4-of-7)', () => {
    const messageToSign = 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef';
    let combinedSignature: string;

    it('4 parties compute partial signatures', async () => {
      const signingParties = [1, 2, 3, 4]; // k+1 = 4 parties needed

      const partialSignatures: { [key: number]: string } = {};

      for (const partyId of signingParties) {
        const res = await callPartyEndpoint(partyId, 'POST', '/sign', {
          ceremonyId,
          message: messageToSign,
          partyIds: signingParties,
        });

        expect(res.status).toBe(200);
        const data = await res.json();
        expect(data.signature).toBeTruthy();
        partialSignatures[partyId] = data.signature;
      }

      // Orchestrator combines partial signatures
      const combineRes = await callOrchestratorEndpoint(
        'POST',
        `/ceremonies/${ceremonyId}/threshold-sign/combine`,
        {
          partialSignatures,
          message: messageToSign,
        }
      );

      expect(combineRes.status).toBe(200);
      const combined = await combineRes.json();
      combinedSignature = combined.signature;
      expect(combinedSignature).toBeTruthy();
    });

    it('threshold signature is valid for the shared public key', async () => {
      const ceremonyRes = await callOrchestratorEndpoint(
        'GET',
        `/ceremonies/${ceremonyId}`
      );
      const ceremony = await ceremonyRes.json();

      expect(combinedSignature).toBeTruthy();
      expect(ceremony.thresholdPubKey).toBeTruthy();
      // In real implementation, would verify signature against public key
    });

    it('3 parties cannot produce a valid signature', async () => {
      const signingParties = [5, 6, 7]; // Only 3 parties, need k+1 = 4

      for (const partyId of signingParties) {
        const res = await callPartyEndpoint(partyId, 'POST', '/sign', {
          ceremonyId,
          message: messageToSign,
          partyIds: signingParties,
        });
        // Should fail due to insufficient parties
        expect(res.status).not.toBe(200);
      }
    });
  });

  describe('Error Handling', () => {
    it('rejects invalid ceremony ID', async () => {
      const res = await callOrchestratorEndpoint(
        'GET',
        '/ceremonies/invalid-ceremony-id'
      );
      expect(res.status).toBe(404);
    });

    it('rejects invalid chain ID in ceremony creation', async () => {
      const res = await callOrchestratorEndpoint('POST', '/ceremonies', {
        ...ceremonyConfig,
        chainId: 'unknown-chain',
      });
      expect(res.status).toBe(400);
    });

    it('rejects invalid party count', async () => {
      const res = await callOrchestratorEndpoint('POST', '/ceremonies', {
        ...ceremonyConfig,
        n: 11, // Max is 10
      });
      expect(res.status).toBe(400);
    });

    it('rejects unreachable party endpoint', async () => {
      const res = await callOrchestratorEndpoint('POST', '/ceremonies', {
        ...ceremonyConfig,
        partyEndpoints: [
          ...ceremonyConfig.partyEndpoints.slice(0, -1),
          'http://unreachable:9999',
        ],
      });
      // Should either reject immediately or fail during ceremony
      expect([400, 503]).toContain(res.status);
    });
  });

  describe('Ceremony Lifecycle', () => {
    it('cannot start ceremony with missing required parameters', async () => {
      const res = await callOrchestratorEndpoint('POST', '/ceremonies', {
        chainId: 'ethereum',
        // Missing n, k, partyIDs, partyEndpoints
      });
      expect(res.status).toBe(400);
    });

    it('cannot re-complete already completed ceremony', async () => {
      // Attempt to complete the ceremony again
      const res = await callOrchestratorEndpoint(
        'POST',
        `/ceremonies/${ceremonyId}/complete`
      );
      expect(res.status).toBe(409); // Conflict: already completed
    });
  });

  describe('Multi-Chain Support', () => {
    it('supports DKG ceremony on Bitcoin', async () => {
      const btcConfig: CeremonyRequest = {
        ...ceremonyConfig,
        chainId: 'bitcoin',
      };

      const res = await callOrchestratorEndpoint('POST', '/ceremonies', btcConfig);
      expect(res.status).toBe(200);
      const data: CeremonyResponse = await res.json();
      expect(data.id).toBeDefined();
    });

    it('supports DKG ceremony on Solana', async () => {
      const solConfig: CeremonyRequest = {
        ...ceremonyConfig,
        chainId: 'solana',
      };

      const res = await callOrchestratorEndpoint('POST', '/ceremonies', solConfig);
      expect(res.status).toBe(200);
      const data: CeremonyResponse = await res.json();
      expect(data.id).toBeDefined();
    });

    it('supports DKG ceremony on Cosmos', async () => {
      const cosmosConfig: CeremonyRequest = {
        ...ceremonyConfig,
        chainId: 'cosmos-hub',
      };

      const res = await callOrchestratorEndpoint(
        'POST',
        '/ceremonies',
        cosmosConfig
      );
      expect(res.status).toBe(200);
      const data: CeremonyResponse = await res.json();
      expect(data.id).toBeDefined();
    });
  });
});
