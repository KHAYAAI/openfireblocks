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
} from 'recharts';
import { Lock, TrendingUp, Clock, CheckCircle, AlertCircle, ArrowRight } from 'lucide-react';
import { api } from '@/lib/api';

interface DashboardStats {
  total_keys: number;
  active_keys: number;
  total_signings: number;
  successful_signings: number;
  failed_signings: number;
  pending_signings: number;
  avg_signing_time: number;
  monthly_volume: number;
}

interface SigningRequest {
  id: string;
  key_name: string;
  blockchain: string;
  status: 'pending' | 'signing' | 'confirmed' | 'failed';
  created_at: string;
  confirmed_at?: string;
}

interface ChartData {
  date: string;
  signings: number;
  value: number;
}

export default function CustomerDashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [signings, setSignings] = useState<SigningRequest[]>([]);
  const [chartData, setChartData] = useState<ChartData[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
  }, []);

  const fetchData = async () => {
    try {
      const [statsRes, signingsRes, chartRes] = await Promise.all([
        api.get('/v1/customer/dashboard/stats'),
        api.get('/v1/customer/signings?limit=5'),
        api.get('/v1/customer/signings/chart'),
      ]);

      setStats(statsRes.data);
      setSignings(signingsRes.data || []);
      setChartData(chartRes.data || []);
    } catch (error) {
      console.error('Failed to fetch dashboard data:', error);
    } finally {
      setLoading(false);
    }
  };

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'pending':
        return 'text-yellow-400 bg-yellow-900/20';
      case 'signing':
        return 'text-blue-400 bg-blue-900/20';
      case 'confirmed':
        return 'text-green-400 bg-green-900/20';
      case 'failed':
        return 'text-red-400 bg-red-900/20';
      default:
        return 'text-gray-400 bg-gray-900/20';
    }
  };

  const getStatusIcon = (status: string) => {
    switch (status) {
      case 'pending':
        return <Clock className="w-4 h-4" />;
      case 'signing':
        return <Clock className="w-4 h-4 animate-spin" />;
      case 'confirmed':
        return <CheckCircle className="w-4 h-4" />;
      case 'failed':
        return <AlertCircle className="w-4 h-4" />;
      default:
        return null;
    }
  };

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-950">
      {/* Header */}
      <div className="border-b border-slate-800 bg-slate-900/50 backdrop-blur-lg sticky top-0 z-10">
        <div className="max-w-7xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Lock className="w-6 h-6 text-blue-400" />
            <div>
              <h1 className="text-lg font-bold text-white">OpenFireblocks</h1>
              <p className="text-xs text-slate-400">Institutional Custody</p>
            </div>
          </div>
          <button className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-white rounded-lg text-sm font-medium transition-colors">
            Logout
          </button>
        </div>
      </div>

      <div className="max-w-7xl mx-auto px-6 py-8 space-y-8">
        {/* Welcome Section */}
        <div className="bg-gradient-to-r from-blue-900/20 to-purple-900/20 border border-blue-800/30 rounded-lg p-8">
          <h2 className="text-2xl font-bold text-white mb-2">Welcome back</h2>
          <p className="text-slate-400">
            Your institutional custody and signing platform is running smoothly. Monitor your keys, signings, and compliance status below.
          </p>
        </div>

        {/* Key Metrics */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          <div className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-slate-700 transition-colors">
            <div className="flex items-center justify-between mb-4">
              <p className="text-sm font-medium text-slate-400">Active Keys</p>
              <Lock className="w-5 h-5 text-blue-400" />
            </div>
            <p className="text-3xl font-bold text-white">{stats?.active_keys || 0}</p>
            <p className="text-xs text-slate-500 mt-2">of {stats?.total_keys || 0} total</p>
          </div>

          <div className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-slate-700 transition-colors">
            <div className="flex items-center justify-between mb-4">
              <p className="text-sm font-medium text-slate-400">Successful Signings</p>
              <CheckCircle className="w-5 h-5 text-green-400" />
            </div>
            <p className="text-3xl font-bold text-green-400">{stats?.successful_signings || 0}</p>
            <p className="text-xs text-slate-500 mt-2">
              {stats && Math.round((stats.successful_signings / stats.total_signings) * 100) || 0}% success rate
            </p>
          </div>

          <div className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-slate-700 transition-colors">
            <div className="flex items-center justify-between mb-4">
              <p className="text-sm font-medium text-slate-400">Pending Signings</p>
              <Clock className="w-5 h-5 text-yellow-400" />
            </div>
            <p className="text-3xl font-bold text-yellow-400">{stats?.pending_signings || 0}</p>
            <p className="text-xs text-slate-500 mt-2">Avg time: {stats?.avg_signing_time || 0}s</p>
          </div>

          <div className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-slate-700 transition-colors">
            <div className="flex items-center justify-between mb-4">
              <p className="text-sm font-medium text-slate-400">Monthly Volume</p>
              <TrendingUp className="w-5 h-5 text-purple-400" />
            </div>
            <p className="text-3xl font-bold text-white">{stats?.monthly_volume || 0}</p>
            <p className="text-xs text-slate-500 mt-2">transactions</p>
          </div>
        </div>

        {/* Charts Grid */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Signing Volume */}
          <div className="bg-slate-900 border border-slate-800 rounded-lg p-6">
            <h3 className="text-lg font-bold text-white mb-4">Signing Volume (7 Days)</h3>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={chartData}>
                <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                <XAxis dataKey="date" stroke="#9ca3af" style={{ fontSize: '12px' }} />
                <YAxis stroke="#9ca3af" style={{ fontSize: '12px' }} />
                <Tooltip
                  contentStyle={{
                    backgroundColor: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    color: '#ffffff',
                  }}
                />
                <Line
                  type="monotone"
                  dataKey="signings"
                  stroke="#3b82f6"
                  dot={{ fill: '#3b82f6', r: 4 }}
                  strokeWidth={2}
                />
              </LineChart>
            </ResponsiveContainer>
          </div>

          {/* Signing Status Distribution */}
          <div className="bg-slate-900 border border-slate-800 rounded-lg p-6">
            <h3 className="text-lg font-bold text-white mb-4">Status Distribution</h3>
            <ResponsiveContainer width="100%" height={300}>
              <BarChart
                data={[
                  {
                    status: 'Confirmed',
                    count: stats?.successful_signings || 0,
                    fill: '#10b981',
                  },
                  {
                    status: 'Pending',
                    count: stats?.pending_signings || 0,
                    fill: '#f59e0b',
                  },
                  {
                    status: 'Failed',
                    count: stats?.failed_signings || 0,
                    fill: '#ef4444',
                  },
                ]}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                <XAxis dataKey="status" stroke="#9ca3af" style={{ fontSize: '12px' }} />
                <YAxis stroke="#9ca3af" style={{ fontSize: '12px' }} />
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

        {/* Recent Signings */}
        <div className="bg-slate-900 border border-slate-800 rounded-lg p-6">
          <div className="flex items-center justify-between mb-6">
            <h3 className="text-lg font-bold text-white">Recent Signings</h3>
            <a href="/signings" className="flex items-center gap-2 text-blue-400 hover:text-blue-300 text-sm font-medium">
              View all <ArrowRight className="w-4 h-4" />
            </a>
          </div>

          {signings.length === 0 ? (
            <p className="text-slate-400 py-8 text-center">No recent signings</p>
          ) : (
            <div className="space-y-3">
              {signings.map((signing) => (
                <div
                  key={signing.id}
                  className="flex items-center justify-between p-4 bg-slate-800 rounded-lg hover:bg-slate-800/70 transition-colors"
                >
                  <div className="flex items-center gap-4 flex-1">
                    <div className={`flex items-center justify-center w-10 h-10 rounded-lg ${getStatusColor(signing.status)}`}>
                      {getStatusIcon(signing.status)}
                    </div>
                    <div>
                      <p className="text-white font-medium text-sm">{signing.key_name}</p>
                      <p className="text-xs text-slate-400">
                        {signing.blockchain.toUpperCase()} •{' '}
                        {new Date(signing.created_at).toLocaleTimeString()}
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    <span
                      className={`px-3 py-1 rounded text-xs font-medium ${getStatusColor(
                        signing.status
                      )}`}
                    >
                      {signing.status.charAt(0).toUpperCase() + signing.status.slice(1)}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Quick Actions */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <a
            href="/keys"
            className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-blue-600 hover:bg-blue-900/10 transition-all group"
          >
            <Lock className="w-6 h-6 text-blue-400 mb-3 group-hover:text-blue-300" />
            <h4 className="font-medium text-white mb-1">Manage Keys</h4>
            <p className="text-sm text-slate-400">Create and manage your signing keys</p>
          </a>

          <a
            href="/sign"
            className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-green-600 hover:bg-green-900/10 transition-all group"
          >
            <CheckCircle className="w-6 h-6 text-green-400 mb-3 group-hover:text-green-300" />
            <h4 className="font-medium text-white mb-1">Sign Transaction</h4>
            <p className="text-sm text-slate-400">Sign and broadcast transactions</p>
          </a>

          <a
            href="/settings"
            className="bg-slate-900 border border-slate-800 rounded-lg p-6 hover:border-purple-600 hover:bg-purple-900/10 transition-all group"
          >
            <TrendingUp className="w-6 h-6 text-purple-400 mb-3 group-hover:text-purple-300" />
            <h4 className="font-medium text-white mb-1">API Configuration</h4>
            <p className="text-sm text-slate-400">Manage API keys and settings</p>
          </a>
        </div>
      </div>
    </div>
  );
}
