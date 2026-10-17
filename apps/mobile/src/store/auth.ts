import { create } from 'zustand';
import * as SecureStore from 'expo-secure-store';

interface User {
  id: string;
  name: string;
  email: string;
  organization?: string;
  created_at: string;
}

interface AuthState {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  setUser: (user: User | null) => void;
  login: (email: string, apiKey: string) => Promise<void>;
  logout: () => Promise<void>;
  checkAuth: () => Promise<void>;
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,

  setUser: (user) => {
    set({ user, isAuthenticated: !!user });
  },

  login: async (email: string, apiKey: string) => {
    try {
      // Verify API key by calling health endpoint
      const response = await fetch('https://api.openfireblocks.io/v1/health', {
        method: 'GET',
        headers: {
          'X-API-Key': apiKey,
        },
      });

      if (!response.ok) {
        throw new Error('Invalid API key');
      }

      // Save API key securely
      await SecureStore.setItemAsync('api_key', apiKey);

      // Create mock user (in production, fetch from /v1/me endpoint)
      const user: User = {
        id: 'user_' + Date.now(),
        name: email.split('@')[0],
        email,
        created_at: new Date().toISOString(),
      };

      set({ user, isAuthenticated: true });
    } catch (error) {
      console.error('Login failed:', error);
      throw error;
    }
  },

  logout: async () => {
    try {
      await SecureStore.deleteItemAsync('api_key');
      set({ user: null, isAuthenticated: false });
    } catch (error) {
      console.error('Logout failed:', error);
      throw error;
    }
  },

  checkAuth: async () => {
    try {
      const apiKey = await SecureStore.getItemAsync('api_key');
      if (!apiKey) {
        set({ user: null, isAuthenticated: false, isLoading: false });
        return;
      }

      // Verify API key is still valid
      const response = await fetch('https://api.openfireblocks.io/v1/health', {
        method: 'GET',
        headers: {
          'X-API-Key': apiKey,
        },
      });

      if (!response.ok) {
        await SecureStore.deleteItemAsync('api_key');
        set({ user: null, isAuthenticated: false, isLoading: false });
        return;
      }

      // API key is valid, create mock user
      const email = await SecureStore.getItemAsync('user_email') || 'user@example.com';
      const user: User = {
        id: 'user_' + Date.now(),
        name: email.split('@')[0],
        email,
        created_at: new Date().toISOString(),
      };

      set({ user, isAuthenticated: true, isLoading: false });
    } catch (error) {
      console.error('Auth check failed:', error);
      set({ user: null, isAuthenticated: false, isLoading: false });
    }
  },
}));
