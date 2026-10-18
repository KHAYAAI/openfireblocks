import axios, { AxiosInstance, AxiosError } from 'axios';

interface ApiClient extends AxiosInstance {}

class OpenFireblocksAPI {
  private client: ApiClient;
  private baseURL: string;

  constructor() {
    this.baseURL = process.env.NEXT_PUBLIC_API_URL || 'https://api.openfireblocks.io';

    this.client = axios.create({
      baseURL: this.baseURL,
      timeout: 30000,
      headers: {
        'Content-Type': 'application/json',
      },
    });

    // Intercept requests to add API key
    this.client.interceptors.request.use((config) => {
      const apiKey = localStorage.getItem('api_key');
      if (apiKey) {
        config.headers['X-API-Key'] = apiKey;
      }
      return config;
    });

    // Intercept responses for error handling
    this.client.interceptors.response.use(
      (response) => response,
      (error: AxiosError) => {
        if (error.response?.status === 401) {
          // Clear auth and redirect to login
          localStorage.removeItem('api_key');
          window.location.href = '/login';
        }
        return Promise.reject(error);
      }
    );
  }

  get<T = any>(url: string, config?: any) {
    return this.client.get<T>(url, config);
  }

  post<T = any>(url: string, data?: any, config?: any) {
    return this.client.post<T>(url, data, config);
  }

  put<T = any>(url: string, data?: any, config?: any) {
    return this.client.put<T>(url, data, config);
  }

  delete<T = any>(url: string, config?: any) {
    return this.client.delete<T>(url, config);
  }

  // Key Management
  async getKeys() {
    return this.get('/v1/keys');
  }

  async createKey(name: string, blockchain: string, threshold: number, totalParties: number) {
    return this.post('/v1/keys', {
      name,
      blockchain,
      threshold,
      total_parties: totalParties,
    });
  }

  async getKey(keyId: string) {
    return this.get(`/v1/keys/${keyId}`);
  }

  // Signing
  async sign(keyId: string, transaction: string) {
    return this.post('/v1/sign', {
      key_pair_id: keyId,
      transaction,
    });
  }

  async getSigningStatus(requestId: string) {
    return this.get(`/v1/sign/${requestId}`);
  }

  async listSignings(limit: number = 10) {
    return this.get('/v1/sign', { params: { limit } });
  }

  // Billing
  async getSubscription() {
    return this.get('/v1/billing/subscription');
  }

  async getUsage() {
    return this.get('/v1/billing/usage');
  }

  async getInvoices() {
    return this.get('/v1/billing/invoices');
  }

  // Health
  async health() {
    return this.get('/v1/health');
  }
}

export const api = new OpenFireblocksAPI();
