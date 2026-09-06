import axios, { AxiosInstance } from 'axios';
import * as SecureStore from 'expo-secure-store';

class OpenFireblocksAPI {
  private client: AxiosInstance;
  private baseURL: string;

  constructor() {
    this.baseURL = process.env.EXPO_PUBLIC_API_URL || 'https://api.openfireblocks.io';

    this.client = axios.create({
      baseURL: this.baseURL,
      timeout: 30000,
      headers: {
        'Content-Type': 'application/json',
      },
    });

    // Request interceptor: add API key from secure storage
    this.client.interceptors.request.use(async (config) => {
      try {
        const apiKey = await SecureStore.getItemAsync('api_key');
        if (apiKey) {
          config.headers['X-API-Key'] = apiKey;
        }
      } catch (error) {
        console.error('Failed to retrieve API key from secure storage:', error);
      }
      return config;
    });

    // Response interceptor: handle errors
    this.client.interceptors.response.use(
      (response) => response,
      (error) => {
        if (error.response?.status === 401) {
          // Unauthorized - clear storage and trigger login
          SecureStore.deleteItemAsync('api_key');
          SecureStore.deleteItemAsync('user');
        }
        return Promise.reject(error);
      }
    );
  }

  async get<T = any>(url: string, config?: any) {
    return this.client.get<T>(url, config);
  }

  async post<T = any>(url: string, data?: any, config?: any) {
    return this.client.post<T>(url, data, config);
  }

  async put<T = any>(url: string, data?: any, config?: any) {
    return this.client.put<T>(url, data, config);
  }

  async delete<T = any>(url: string, config?: any) {
    return this.client.delete<T>(url, config);
  }

  // Auth
  async setApiKey(apiKey: string) {
    try {
      await SecureStore.setItemAsync('api_key', apiKey);
    } catch (error) {
      console.error('Failed to save API key:', error);
    }
  }

  async getApiKey(): Promise<string | null> {
    try {
      return await SecureStore.getItemAsync('api_key');
    } catch (error) {
      console.error('Failed to retrieve API key:', error);
      return null;
    }
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

  // Health
  async health() {
    return this.get('/v1/health');
  }
}

export const api = new OpenFireblocksAPI();
