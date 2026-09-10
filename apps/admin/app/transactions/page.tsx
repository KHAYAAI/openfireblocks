'use client';

import { useEffect, useState } from 'react';
import {
  LineChart,
  Line,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
  ScatterChart,
  Scatter,
} from 'recharts';
import { AlertCircle, CheckCircle, Clock, XCircle } from 'lucide-react';
import { AdminLayout } from '@/components/layout/AdminLayout';
import { api } from '@/lib/api';

interface Transaction {
  id: string;
  customer_id: string;
  blockchain: string;
  amount: string;
  status: 'pending' | 'broadcasted' | 'confirmed' | 'failed';
  gas_price?: string;
  created_at: string;
  confirmed_at?: string;
}

interface TransactionStats {
  total_transactions: number;
  confirmed_count: number;
  pending_count: number;
  failed_count: number;
  total_volume: string;
  avg_confirmation_time: number;
}

export default function TransactionsPage() {
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const [stats, setStats] = useState<TransactionStats | null>(null);
  const [chartData, setChartData] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedBlockchain, setSelectedBlockchain] = useState<'all' | 'bitcoin' | 'ethereum' | 'solana'>('all');

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 10000); // Refresh every 10 seconds
    return () => clearInterval(interval);
  }, [selectedBlockchain]);

  const fetchData = async () => {
    try {
      const [txRes, statsRes, chartRes] = await Promise.all([
        api.get('/admin/v1/transactions'),
        api.get('/admin/v1/transactions/stats'),
        api.get('/admin/v1/transactions/chart'),
      ]);

      setTransactions(txRes.data || []);
      setStats(statsRes.data);
      setChartData(chartRes.data || []);
    } catch (error) {
      console.error('Failed to fetch transaction data:', error);
    } finally {
      setLoading(false);
    }
  };

  const getStatusIcon = (status: string) => {
    switch (status) {
      case 'pending':
        return <Clock className="w-5 h-5 text-yellow-400" />;
      case 'broadcasted':
        return <Clock className="w-5 h-5 text-blue-400" />;
      case 'confirmed':
        return <CheckCircle className="w-5 h-5 text-green-400" />;
      case 'failed':
        return <XCircle className="w-5 h-5 text-red-400" />;
      default:
        return null;
    }
  };

  const getStatusBadgeColor = (status: string) => {
    switch (status) {
      case 'confirmed':
        return 'bg-green-900/30 text-green-300';
      case 'pending':
        return 'bg-yellow-900/30 text-yellow-300';
      case 'broadcasted':
        return 'bg-blue-900/30 text-blue-300';
      case 'failed':
        return 'bg-red-900/30 text-red-300';
      default:
        return 'bg-slate-900/30 text-slate-300';
    }
  };

  const filteredTransactions = transactions.filter((tx) =>
    selectedBlockchain === 'all' ? true : tx.blockchain === selectedBlockchain
  );

  return (
    <AdminLayout>
      <div className="space-y-8">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-white">Transaction Monitoring</h1>
          <p className="text-slate-400 mt-2">Real-time transaction tracking and analytics</p>
        </div>

        {/* Key Metrics */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6">
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Total Transactions</p>
                <p className="text-3xl font-bold text-white mt-2">{stats?.total_transactions || 0}</p>
              </div>
              <AlertCircle className="w-10 h-10 text-blue-400" />
            </div>
          </div>

          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Confirmed</p>
                <p className="text-3xl font-bold text-green-400 mt-2">{stats?.confirmed_count || 0}</p>
              </div>
              <CheckCircle className="w-10 h-10 text-green-400" />
            </div>
          </div>

          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Pending</p>
                <p className="text-3xl font-bold text-yellow-400 mt-2">{stats?.pending_count || 0}</p>
              </div>
              <Clock className="w-10 h-10 text-yellow-400" />
            </div>
          </div>

          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Avg Confirmation</p>
                <p className="text-3xl font-bold text-white mt-2">{stats?.avg_confirmation_time || 0}s</p>
              </div>
              <CheckCircle className="w-10 h-10 text-blue-400" />
            </div>
          </div>
        </div>

        {/* Charts */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Transaction Volume */}
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <h2 className="text-xl font-bold text-white mb-4">Transaction Volume (24h)</h2>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={chartData}>
                <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                <XAxis dataKey="time" stroke="#9ca3af" />
                <YAxis stroke="#9ca3af" />
                <Tooltip
                  contentStyle={{
                    backgroundColor: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    color: '#ffffff',
                  }}
                />
                <Line type="monotone" dataKey="transactions" stroke="#3b82f6" dot={false} />
              </LineChart>
            </ResponsiveContainer>
          </div>

          {/* Status Distribution */}
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <h2 className="text-xl font-bold text-white mb-4">Status Distribution</h2>
            <ResponsiveContainer width="100%" height={300}>
              <BarChart
                data={[
                  { status: 'Confirmed', count: stats?.confirmed_count || 0, fill: '#10b981' },
                  { status: 'Pending', count: stats?.pending_count || 0, fill: '#f59e0b' },
                  { status: 'Failed', count: stats?.failed_count || 0, fill: '#ef4444' },
                ]}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                <XAxis dataKey="status" stroke="#9ca3af" />
                <YAxis stroke="#9ca3af" />
                <Tooltip
                  contentStyle={{
                    backgroundColor: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    color: '#ffffff',
                  }}
                />
                <Bar dataKey="count" fill="#3b82f6" />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* Blockchain Filter */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
          <div className="flex gap-2">
            {['all', 'bitcoin', 'ethereum', 'solana'].map((chain) => (
              <button
                key={chain}
                onClick={() => setSelectedBlockchain(chain as any)}
                className={`px-4 py-2 rounded font-medium transition-colors ${
                  selectedBlockchain === chain
                    ? 'bg-blue-600 text-white'
                    : 'bg-slate-800 text-slate-300 hover:bg-slate-700'
                }`}
              >
                {chain === 'all' ? 'All Blockchains' : chain.charAt(0).toUpperCase() + chain.slice(1)}
              </button>
            ))}
          </div>
        </div>

        {/* Transactions Table */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-slate-800 bg-slate-800/50">
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Transaction ID</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Blockchain</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Amount</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Status</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Created</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Confirmation Time</th>
                </tr>
              </thead>
              <tbody>
                {loading ? (
                  <tr>
                    <td colSpan={6} className="px-6 py-8 text-center text-slate-400">
                      Loading transactions...
                    </td>
                  </tr>
                ) : filteredTransactions.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="px-6 py-8 text-center text-slate-400">
                      No transactions found
                    </td>
                  </tr>
                ) : (
                  filteredTransactions.slice(0, 10).map((tx) => (
                    <tr key={tx.id} className="border-b border-slate-800 hover:bg-slate-800/50">
                      <td className="px-6 py-4 font-mono text-sm text-blue-400">
                        {tx.id.substring(0, 12)}...
                      </td>
                      <td className="px-6 py-4">
                        <span className="text-white font-medium uppercase">{tx.blockchain}</span>
                      </td>
                      <td className="px-6 py-4 text-white font-medium">{tx.amount}</td>
                      <td className="px-6 py-4">
                        <div className="flex items-center gap-2">
                          {getStatusIcon(tx.status)}
                          <span className={`px-2 py-1 rounded text-xs font-medium ${getStatusBadgeColor(tx.status)}`}>
                            {tx.status.charAt(0).toUpperCase() + tx.status.slice(1)}
                          </span>
                        </div>
                      </td>
                      <td className="px-6 py-4 text-sm text-slate-400">
                        {new Date(tx.created_at).toLocaleString()}
                      </td>
                      <td className="px-6 py-4 text-sm text-slate-400">
                        {tx.confirmed_at
                          ? `${Math.round(
                              (new Date(tx.confirmed_at).getTime() - new Date(tx.created_at).getTime()) / 1000
                            )}s`
                          : '-'}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </AdminLayout>
  );
}
