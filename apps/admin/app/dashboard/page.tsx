'use client';

import { useEffect, useState } from 'react';
import { BarChart, Bar, LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer, PieChart, Pie, Cell } from 'recharts';
import { AlertCircle, CheckCircle, Clock, TrendingUp } from 'lucide-react';
import { AdminLayout } from '@/components/layout/AdminLayout';
import { api } from '@/lib/api';

interface DashboardStats {
  totalCustomers: number;
  activeKeys: number;
  signingsToday: number;
  pendingApprovals: number;
  systemUptime: number;
  avgSigningLatency: number;
  riskAlerts: number;
  complianceIssues: number;
}

interface ChartData {
  name: string;
  value: number;
  date?: string;
}

export default function AdminDashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [chartData, setChartData] = useState<ChartData[]>([]);
  const [riskAlerts, setRiskAlerts] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchData = async () => {
      try {
        const [statsRes, chartRes, alertsRes] = await Promise.all([
          api.get('/admin/v1/stats'),
          api.get('/admin/v1/metrics/chart'),
          api.get('/admin/v1/alerts?severity=high'),
        ]);

        setStats(statsRes.data);
        setChartData(chartRes.data);
        setRiskAlerts(alertsRes.data);
      } catch (error) {
        console.error('Failed to fetch admin data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, []);

  if (loading) {
    return (
      <AdminLayout>
        <div className="text-slate-400">Loading admin dashboard...</div>
      </AdminLayout>
    );
  }

  const riskDistribution = [
    { name: 'Low', value: 120 },
    { name: 'Medium', value: 45 },
    { name: 'High', value: stats?.riskAlerts || 0 },
  ];

  const COLORS = ['#10b981', '#f59e0b', '#ef4444'];

  return (
    <AdminLayout>
      <div className="space-y-8">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-white">Operations Dashboard</h1>
          <p className="text-slate-400 mt-2">Real-time platform monitoring and compliance</p>
        </div>

        {/* Key Metrics */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6">
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Total Customers</p>
                <p className="text-3xl font-bold text-white mt-2">{stats?.totalCustomers}</p>
              </div>
              <TrendingUp className="w-10 h-10 text-blue-400" />
            </div>
          </div>

          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Signings Today</p>
                <p className="text-3xl font-bold text-white mt-2">{stats?.signingsToday}</p>
              </div>
              <CheckCircle className="w-10 h-10 text-green-400" />
            </div>
          </div>

          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">System Uptime</p>
                <p className="text-3xl font-bold text-white mt-2">{stats?.systemUptime}%</p>
              </div>
              <CheckCircle className="w-10 h-10 text-green-400" />
            </div>
          </div>

          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-400">Risk Alerts</p>
                <p className="text-3xl font-bold text-white mt-2">{stats?.riskAlerts}</p>
              </div>
              <AlertCircle className="w-10 h-10 text-red-400" />
            </div>
          </div>
        </div>

        {/* Charts Grid */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Signing Volume */}
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <h2 className="text-xl font-bold text-white mb-4">Signing Volume (7 Days)</h2>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={chartData}>
                <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
                <XAxis dataKey="name" stroke="#9ca3af" />
                <YAxis stroke="#9ca3af" />
                <Tooltip
                  contentStyle={{
                    backgroundColor: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                    color: '#ffffff',
                  }}
                />
                <Line type="monotone" dataKey="value" stroke="#3b82f6" dot={{ fill: '#3b82f6' }} />
              </LineChart>
            </ResponsiveContainer>
          </div>

          {/* Risk Distribution */}
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <h2 className="text-xl font-bold text-white mb-4">Risk Distribution</h2>
            <ResponsiveContainer width="100%" height={300}>
              <PieChart>
                <Pie
                  data={riskDistribution}
                  cx="50%"
                  cy="50%"
                  labelLine={false}
                  label={({ name, value }) => `${name}: ${value}`}
                  outerRadius={80}
                  fill="#8884d8"
                  dataKey="value"
                >
                  {riskDistribution.map((entry, index) => (
                    <Cell key={`cell-${index}`} fill={COLORS[index % COLORS.length]} />
                  ))}
                </Pie>
                <Tooltip
                  contentStyle={{
                    backgroundColor: '#1e293b',
                    border: '1px solid #334155',
                    borderRadius: '8px',
                  }}
                />
              </PieChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* Alerts & Incidents */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
          <div className="flex justify-between items-center mb-6">
            <h2 className="text-xl font-bold text-white">High-Priority Alerts</h2>
            <button className="text-sm text-blue-400 hover:text-blue-300">View All</button>
          </div>

          <div className="space-y-3">
            {riskAlerts.length === 0 ? (
              <p className="text-slate-400 py-4">No high-priority alerts</p>
            ) : (
              riskAlerts.slice(0, 5).map((alert) => (
                <div
                  key={alert.id}
                  className="flex items-center justify-between p-4 bg-slate-800 rounded border-l-4 border-red-500"
                >
                  <div>
                    <p className="text-white font-medium">{alert.title}</p>
                    <p className="text-sm text-slate-400 mt-1">{alert.description}</p>
                  </div>
                  <button className="px-3 py-1 text-sm bg-red-600 hover:bg-red-700 text-white rounded">
                    Investigate
                  </button>
                </div>
              ))
            )}
          </div>
        </div>

        {/* Compliance Status */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
          <h2 className="text-xl font-bold text-white mb-6">Compliance Status</h2>
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-white font-medium">SOC 2 Type II</p>
                <p className="text-sm text-slate-400">Observation Period: 6 months (Month 6/6)</p>
              </div>
              <div className="px-3 py-1 bg-green-900 text-green-200 rounded text-sm font-medium">
                On Track
              </div>
            </div>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-white font-medium">ISO 27001:2022</p>
                <p className="text-sm text-slate-400">Implementation: 80% complete</p>
              </div>
              <div className="px-3 py-1 bg-yellow-900 text-yellow-200 rounded text-sm font-medium">
                In Progress
              </div>
            </div>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-white font-medium">GDPR Compliance</p>
                <p className="text-sm text-slate-400">All requirements implemented</p>
              </div>
              <div className="px-3 py-1 bg-green-900 text-green-200 rounded text-sm font-medium">
                Complete
              </div>
            </div>
          </div>
        </div>
      </div>
    </AdminLayout>
  );
}
