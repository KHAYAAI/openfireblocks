/**
 * OpenFireblocks JavaScript SDK
 * Enterprise cryptocurrency key management and signing
 */

const axios = require('axios');
const crypto = require('crypto');

class OpenFireblocksClient {
  constructor(apiKey, options = {}) {
    this.apiKey = apiKey;
    this.baseURL = options.baseURL || 'https://api.openfireblocks.io/v1';
    this.timeout = options.timeout || 30000;
    this.retryAttempts = options.retryAttempts || 3;

    this.httpClient = axios.create({
      baseURL: this.baseURL,
      timeout: this.timeout,
      headers: {
        'X-API-Key': apiKey,
        'Content-Type': 'application/json',
      },
    });
  }

  async _request(method, path, data = null) {
    try {
      const config = { method, url: path };
      if (data) config.data = data;
      const response = await this.httpClient(config);
      return response.data;
    } catch (error) {
      throw this._handleError(error);
    }
  }

  _handleError(error) {
    if (error.response) {
      const err = new Error(error.response.data?.message || error.message);
      err.code = error.response.data?.error;
      err.status = error.response.status;
      return err;
    }
    return error;
  }

  // Key Management
  async listKeys(options = {}) {
    const params = new URLSearchParams();
    if (options.limit) params.append('limit', options.limit);
    if (options.blockchain) params.append('blockchain', options.blockchain);
    return this._request('GET', `/keys?${params}`);
  }

  async getKey(keyId) {
    return this._request('GET', `/keys/${keyId}`);
  }

  async createKey(blockchain, threshold, totalParties, name) {
    return this._request('POST', '/keys', {
      blockchain,
      threshold,
      total_parties: totalParties,
      name,
    });
  }

  // Signing
  async sign(keyPairId, transaction, idempotencyKey) {
    return this._request('POST', '/sign', {
      key_pair_id: keyPairId,
      transaction,
      idempotency_key: idempotencyKey || crypto.randomUUID(),
    });
  }

  async getSigningStatus(signingId) {
    return this._request('GET', `/sign/${signingId}`);
  }

  async waitForSignature(signingId, maxWaitTime = 60000) {
    const pollInterval = 1000;
    const startTime = Date.now();

    while (Date.now() - startTime < maxWaitTime) {
      const status = await this.getSigningStatus(signingId);
      if (status.status === 'completed') return status;
      if (status.status === 'failed') throw new Error(status.error);
      await new Promise(resolve => setTimeout(resolve, pollInterval));
    }

    throw new Error('Signing timeout');
  }

  // Compliance
  async registerCustomer(name, email, country) {
    return this._request('POST', '/customers', { name, email, country });
  }

  // System
  async health() {
    return this._request('GET', '/health');
  }
}

module.exports = OpenFireblocksClient;
