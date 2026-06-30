'use client';

import { useEffect, useState } from 'react';
import { KeyIcon, GitSignIcon, HardDriveIcon, BarChart3Icon } from 'lucide-react';
import { DashboardLayout } from '@/components/layout/DashboardLayout';
import { StatsCard } from '@/components/dashboard/StatsCard';
import { KeysList } from '@/components/dashboard/KeysList';
import { RecentSignings } from '@/components/dashboard/RecentSignings';
import { UsageChart } from '@/components/dashboard/UsageChart';
import { useCustomerStore } from '@/lib/store';
import { api } from '@/lib/api';

export default function Dashboard() {
  const { customer } = useCustomerStore();
  const [stats, setStats] = useState({
    totalKeys: 0,
    activeSignings: 0,
    monthlyTransactions: 0,
    uptime: '99.9%',
  });
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchStats = async () => {
      try {
        const response = await api.get('/v1/dashboard/stats');
        setStats(response.data);
      } catch (error) {
        console.error('Failed to fetch stats:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchStats();
  }, []);

  return (
    <DashboardLayout>
      <div className="space-y-8">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-white">Dashboard</h1>
          <p className="text-slate-400 mt-2">
            Welcome back, {customer?.name || 'Customer'}
          </p>
        </div>

        {/* Stats Grid */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6">
          <StatsCard
            title="Active Keys"
            value={stats.totalKeys}
            icon={KeyIcon}
            trend="+2 this month"
            trendPositive={true}
          />
          <StatsCard
            title="Signing Requests"
            value={stats.monthlyTransactions}
            icon={GitSignIcon}
            trend="+12% vs last month"
            trendPositive={true}
          />
          <StatsCard
            title="API Health"
            value={stats.uptime}
            icon={HardDriveIcon}
            trend="All systems operational"
            trendPositive={true}
          />
          <StatsCard
            title="Network Status"
            value="Multi-Region"
            icon={BarChart3Icon}
            trend="Primary + Secondary"
            trendPositive={true}
          />
        </div>

        {/* Main Content Grid */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          {/* Keys Section */}
          <div className="lg:col-span-2">
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
              <div className="flex justify-between items-center mb-6">
                <h2 className="text-xl font-bold text-white">Your Keys</h2>
                <button className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium">
                  + New Key
                </button>
              </div>
              <KeysList />
            </div>
          </div>

          {/* Usage Chart */}
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
            <h2 className="text-xl font-bold text-white mb-6">Usage This Month</h2>
            <UsageChart />
          </div>
        </div>

        {/* Recent Signings */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
          <div className="flex justify-between items-center mb-6">
            <h2 className="text-xl font-bold text-white">Recent Signings</h2>
            <a href="/signings" className="text-blue-400 hover:text-blue-300 text-sm">
              View All →
            </a>
          </div>
          <RecentSignings />
        </div>
      </div>
    </DashboardLayout>
  );
}
