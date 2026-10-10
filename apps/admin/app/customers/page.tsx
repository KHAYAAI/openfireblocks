'use client';

import { useEffect, useState } from 'react';
import { SearchIcon, ChevronRightIcon } from 'lucide-react';
import { AdminLayout } from '@/components/layout/AdminLayout';
import { api } from '@/lib/api';

interface Customer {
  id: string;
  name: string;
  email: string;
  company?: string;
  kyc_status: 'pending' | 'verified' | 'rejected';
  kyc_level: 'basic' | 'standard' | 'enhanced';
  created_at: string;
  last_signing: string;
  signing_count: number;
  is_active: boolean;
}

export default function CustomersPage() {
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchTerm, setSearchTerm] = useState('');
  const [filterStatus, setFilterStatus] = useState<'all' | 'verified' | 'pending' | 'rejected'>('all');

  useEffect(() => {
    fetchCustomers();
  }, []);

  const fetchCustomers = async () => {
    try {
      const res = await api.get('/admin/v1/customers');
      setCustomers(res.data || []);
    } catch (error) {
      console.error('Failed to fetch customers:', error);
    } finally {
      setLoading(false);
    }
  };

  const filteredCustomers = customers.filter((customer) => {
    const matchesSearch =
      customer.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
      customer.email.toLowerCase().includes(searchTerm.toLowerCase());
    const matchesFilter = filterStatus === 'all' || customer.kyc_status === filterStatus;
    return matchesSearch && matchesFilter;
  });

  const getKycStatusColor = (status: string) => {
    switch (status) {
      case 'verified':
        return 'text-green-400 bg-green-900/20';
      case 'pending':
        return 'text-yellow-400 bg-yellow-900/20';
      case 'rejected':
        return 'text-red-400 bg-red-900/20';
      default:
        return 'text-gray-400 bg-gray-900/20';
    }
  };

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'verified':
        return <span className="px-2 py-1 rounded text-xs font-medium bg-green-900/30 text-green-300">Verified</span>;
      case 'pending':
        return <span className="px-2 py-1 rounded text-xs font-medium bg-yellow-900/30 text-yellow-300">Pending</span>;
      case 'rejected':
        return <span className="px-2 py-1 rounded text-xs font-medium bg-red-900/30 text-red-300">Rejected</span>;
      default:
        return null;
    }
  };

  return (
    <AdminLayout>
      <div className="space-y-6">
        {/* Header */}
        <div>
          <h1 className="text-3xl font-bold text-white">Customer Management</h1>
          <p className="text-slate-400 mt-2">Manage customer accounts and KYC status</p>
        </div>

        {/* Stats Grid */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-4">
            <p className="text-sm text-slate-400">Total Customers</p>
            <p className="text-2xl font-bold text-white mt-2">{customers.length}</p>
          </div>
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-4">
            <p className="text-sm text-slate-400">KYC Verified</p>
            <p className="text-2xl font-bold text-green-400 mt-2">
              {customers.filter((c) => c.kyc_status === 'verified').length}
            </p>
          </div>
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-4">
            <p className="text-sm text-slate-400">Pending Verification</p>
            <p className="text-2xl font-bold text-yellow-400 mt-2">
              {customers.filter((c) => c.kyc_status === 'pending').length}
            </p>
          </div>
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-4">
            <p className="text-sm text-slate-400">Active This Month</p>
            <p className="text-2xl font-bold text-blue-400 mt-2">
              {customers.filter((c) => c.is_active).length}
            </p>
          </div>
        </div>

        {/* Search and Filter */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 p-6">
          <div className="flex flex-col md:flex-row gap-4">
            <div className="flex-1 relative">
              <SearchIcon className="absolute left-3 top-3 w-5 h-5 text-slate-500" />
              <input
                type="text"
                placeholder="Search customers..."
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
                className="w-full bg-slate-800 border border-slate-700 rounded px-10 py-2 text-white placeholder-slate-400 focus:outline-none focus:border-blue-500"
              />
            </div>
            <select
              value={filterStatus}
              onChange={(e) => setFilterStatus(e.target.value as any)}
              className="bg-slate-800 border border-slate-700 rounded px-4 py-2 text-white focus:outline-none focus:border-blue-500"
            >
              <option value="all">All Status</option>
              <option value="verified">Verified</option>
              <option value="pending">Pending</option>
              <option value="rejected">Rejected</option>
            </select>
          </div>
        </div>

        {/* Customers Table */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-slate-800 bg-slate-800/50">
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Customer</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">KYC Status</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Level</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Signings</th>
                  <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Last Active</th>
                  <th className="px-6 py-3 text-right text-sm font-semibold text-slate-300">Actions</th>
                </tr>
              </thead>
              <tbody>
                {loading ? (
                  <tr>
                    <td colSpan={6} className="px-6 py-8 text-center text-slate-400">
                      Loading customers...
                    </td>
                  </tr>
                ) : filteredCustomers.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="px-6 py-8 text-center text-slate-400">
                      No customers found
                    </td>
                  </tr>
                ) : (
                  filteredCustomers.map((customer) => (
                    <tr key={customer.id} className="border-b border-slate-800 hover:bg-slate-800/50">
                      <td className="px-6 py-4">
                        <div>
                          <p className="text-white font-medium">{customer.name}</p>
                          <p className="text-sm text-slate-400">{customer.email}</p>
                        </div>
                      </td>
                      <td className="px-6 py-4">
                        {getStatusBadge(customer.kyc_status)}
                      </td>
                      <td className="px-6 py-4">
                        <span className="text-sm text-slate-300 capitalize">{customer.kyc_level}</span>
                      </td>
                      <td className="px-6 py-4">
                        <span className="text-white font-medium">{customer.signing_count}</span>
                      </td>
                      <td className="px-6 py-4 text-sm text-slate-400">
                        {new Date(customer.last_signing).toLocaleDateString()}
                      </td>
                      <td className="px-6 py-4 text-right">
                        <button className="text-blue-400 hover:text-blue-300 p-2">
                          <ChevronRightIcon className="w-5 h-5" />
                        </button>
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
