'use client';

import { useEffect, useState } from 'react';
import { DashboardLayout } from '@/components/layout/DashboardLayout';
import { api } from '@/lib/api';
import { CheckCircle } from 'lucide-react';

interface Plan {
  id: string;
  name: string;
  price: number;
  signingLimit: number;
  keyLimit: number;
  features: string[];
}

interface Invoice {
  id: string;
  amount: number;
  status: 'paid' | 'unpaid' | 'overdue';
  dueDate: string;
}

export default function BillingPage() {
  const [subscription, setSubscription] = useState<any>(null);
  const [usage, setUsage] = useState<any>(null);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchBillingData = async () => {
      try {
        const [subRes, usageRes, invoicesRes] = await Promise.all([
          api.getSubscription(),
          api.getUsage(),
          api.getInvoices(),
        ]);

        setSubscription(subRes.data);
        setUsage(usageRes.data);
        setInvoices(invoicesRes.data);
      } catch (error) {
        console.error('Failed to fetch billing data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchBillingData();
  }, []);

  const plans: Plan[] = [
    {
      id: 'starter',
      name: 'Starter',
      price: 99,
      signingLimit: 1000,
      keyLimit: 10,
      features: [
        'Up to 10 threshold keys',
        '1000 signing requests/month',
        'Email support',
        'Basic analytics',
      ],
    },
    {
      id: 'professional',
      name: 'Professional',
      price: 499,
      signingLimit: 50000,
      keyLimit: 100,
      features: [
        'Up to 100 threshold keys',
        '50,000 signing requests/month',
        'Priority support',
        'Advanced analytics',
        'Custom integrations',
      ],
    },
    {
      id: 'enterprise',
      name: 'Enterprise',
      price: 0,
      signingLimit: 0,
      keyLimit: 0,
      features: [
        'Unlimited everything',
        'Dedicated support',
        'Custom SLA',
        'On-premise deployment',
      ],
    },
  ];

  if (loading) {
    return (
      <DashboardLayout>
        <div className="text-slate-400">Loading billing information...</div>
      </DashboardLayout>
    );
  }

  return (
    <DashboardLayout>
      <div className="space-y-8">
        <div>
          <h1 className="text-3xl font-bold text-white">Billing & Usage</h1>
          <p className="text-slate-400 mt-2">Manage your subscription and view usage</p>
        </div>

        {/* Current Plan */}
        {subscription && (
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <h2 className="text-xl font-bold text-white mb-4">Current Plan</h2>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-lg font-medium text-white capitalize">
                  {subscription.plan_name || 'Professional'}
                </p>
                <p className="text-sm text-slate-400 mt-1">
                  Renews on {new Date(subscription.current_period_end).toLocaleDateString()}
                </p>
              </div>
              <button className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-white rounded-lg text-sm font-medium">
                Change Plan
              </button>
            </div>
          </div>
        )}

        {/* Usage */}
        {usage && (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
              <h3 className="text-sm font-medium text-slate-400 mb-4">API Requests</h3>
              <p className="text-3xl font-bold text-white">{usage.api_requests}</p>
              <p className="text-xs text-slate-400 mt-2">this month</p>
            </div>
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
              <h3 className="text-sm font-medium text-slate-400 mb-4">Signing Requests</h3>
              <div className="space-y-2">
                <p className="text-3xl font-bold text-white">
                  {usage.signing_requests} / {usage.available_signings}
                </p>
                <div className="w-full bg-slate-800 rounded-full h-2">
                  <div
                    className="bg-green-600 h-2 rounded-full"
                    style={{
                      width: `${(usage.signing_requests / usage.available_signings) * 100}%`,
                    }}
                  />
                </div>
              </div>
            </div>
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
              <h3 className="text-sm font-medium text-slate-400 mb-4">Keys</h3>
              <p className="text-3xl font-bold text-white">
                {usage.key_operations} / {usage.available_keys}
              </p>
              <p className="text-xs text-slate-400 mt-2">active threshold keys</p>
            </div>
          </div>
        )}

        {/* Invoices */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
          <h2 className="text-xl font-bold text-white mb-4">Invoices</h2>
          <div className="space-y-2">
            {invoices.length === 0 ? (
              <p className="text-slate-400 py-4">No invoices yet</p>
            ) : (
              invoices.map((invoice) => (
                <div
                  key={invoice.id}
                  className="flex items-center justify-between p-4 bg-slate-800 rounded"
                >
                  <div>
                    <p className="text-white font-medium">Invoice {invoice.id.slice(0, 8)}</p>
                    <p className="text-xs text-slate-400">
                      Due {new Date(invoice.dueDate).toLocaleDateString()}
                    </p>
                  </div>
                  <div className="text-right">
                    <p className="text-white font-medium">${(invoice.amount / 100).toFixed(2)}</p>
                    <span
                      className={`text-xs ${
                        invoice.status === 'paid' ? 'text-green-400' : 'text-yellow-400'
                      }`}
                    >
                      {invoice.status.charAt(0).toUpperCase() + invoice.status.slice(1)}
                    </span>
                  </div>
                </div>
              ))
            )}
          </div>
        </div>

        {/* Plans Comparison */}
        <div>
          <h2 className="text-xl font-bold text-white mb-4">Available Plans</h2>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            {plans.map((plan) => (
              <div
                key={plan.id}
                className={`rounded-lg border p-6 ${
                  subscription?.plan_id === plan.id
                    ? 'bg-blue-900 border-blue-600'
                    : 'bg-slate-900 border-slate-800'
                }`}
              >
                <h3 className="text-lg font-bold text-white mb-2">{plan.name}</h3>
                <p className="text-3xl font-bold text-white mb-6">
                  {plan.price === 0 ? 'Custom' : `$${plan.price}`}
                  <span className="text-sm text-slate-400">/month</span>
                </p>
                <ul className="space-y-3 mb-6">
                  {plan.features.map((feature, idx) => (
                    <li key={idx} className="flex items-center gap-2 text-slate-300">
                      <CheckCircle className="w-4 h-4 text-green-400" />
                      {feature}
                    </li>
                  ))}
                </ul>
                <button
                  disabled={subscription?.plan_id === plan.id}
                  className="w-full px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 text-white rounded-lg font-medium"
                >
                  {subscription?.plan_id === plan.id ? 'Current Plan' : 'Upgrade'}
                </button>
              </div>
            ))}
          </div>
        </div>
      </div>
    </DashboardLayout>
  );
}
