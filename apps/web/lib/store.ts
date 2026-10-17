import { create } from 'zustand';

interface Customer {
  customer_id: string;
  name: string;
  email: string;
  kyc_status: 'pending' | 'approved' | 'rejected';
}

interface AuthState {
  customer: Customer | null;
  apiKey: string | null;
  isAuthenticated: boolean;
  setCustomer: (customer: Customer) => void;
  setApiKey: (key: string) => void;
  logout: () => void;
}

export const useCustomerStore = create<AuthState>((set) => {
  // Initialize from localStorage
  if (typeof window !== 'undefined') {
    const storedApiKey = localStorage.getItem('api_key');
    const storedCustomer = localStorage.getItem('customer');
  }

  return {
    customer: null,
    apiKey: null,
    isAuthenticated: false,
    setCustomer: (customer: Customer) => {
      set({ customer, isAuthenticated: true });
      if (typeof window !== 'undefined') {
        localStorage.setItem('customer', JSON.stringify(customer));
      }
    },
    setApiKey: (apiKey: string) => {
      set({ apiKey });
      if (typeof window !== 'undefined') {
        localStorage.setItem('api_key', apiKey);
      }
    },
    logout: () => {
      set({ customer: null, apiKey: null, isAuthenticated: false });
      if (typeof window !== 'undefined') {
        localStorage.removeItem('api_key');
        localStorage.removeItem('customer');
      }
    },
  };
});

interface UIState {
  sidebarOpen: boolean;
  setSidebarOpen: (open: boolean) => void;
  notifications: Array<{
    id: string;
    type: 'success' | 'error' | 'info' | 'warning';
    message: string;
  }>;
  addNotification: (type: UIState['notifications'][0]['type'], message: string) => void;
  removeNotification: (id: string) => void;
}

export const useUIStore = create<UIState>((set) => ({
  sidebarOpen: false,
  setSidebarOpen: (open: boolean) => set({ sidebarOpen: open }),
  notifications: [],
  addNotification: (type, message) =>
    set((state) => ({
      notifications: [
        ...state.notifications,
        {
          id: Math.random().toString(36),
          type,
          message,
        },
      ],
    })),
  removeNotification: (id) =>
    set((state) => ({
      notifications: state.notifications.filter((n) => n.id !== id),
    })),
}));
