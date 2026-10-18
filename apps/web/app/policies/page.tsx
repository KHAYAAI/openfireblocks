'use client';

import { useEffect, useState } from 'react';
import { Plus, Trash2, Edit2, Shield, DollarSign, Clock, Lock } from 'lucide-react';
import { AdminLayout } from '@/components/layout/AdminLayout';
import { api } from '@/lib/api';

interface Policy {
  policy_id: string;
  key_id: string;
  name: string;
  description: string;
  status: string;
  rules: PolicyRule[];
  approvals?: ApprovalConfig;
  created_at: string;
}

interface PolicyRule {
  rule_id: string;
  type: string;
  description: string;
  enabled: boolean;
  config: any;
}

interface ApprovalConfig {
  required: number;
  approvers: string[];
  timeout_minutes: number;
}

export default function PoliciesPage() {
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [keys, setKeys] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [formData, setFormData] = useState({
    key_id: '',
    name: '',
    description: '',
    rules: [{ type: 'amount_limit', config: { max_amount: '100000' } }],
    approvals: { required: 2, approvers: [], timeout_minutes: 60 },
  });

  useEffect(() => {
    fetchData();
  }, []);

  const fetchData = async () => {
    try {
      const [policiesRes, keysRes] = await Promise.all([
        api.get('/v1/policies'),
        api.get('/v1/keys'),
      ]);
      setPolicies(policiesRes.data || []);
      setKeys(keysRes.data || []);
    } catch (error) {
      console.error('Failed to fetch data:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleCreatePolicy = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await api.post('/v1/policies', formData);
      setFormData({
        key_id: '',
        name: '',
        description: '',
        rules: [{ type: 'amount_limit', config: { max_amount: '100000' } }],
        approvals: { required: 2, approvers: [], timeout_minutes: 60 },
      });
      setShowCreateModal(false);
      fetchData();
    } catch (error) {
      console.error('Failed to create policy:', error);
      alert('Failed to create policy');
    }
  };

  const handleDeletePolicy = async (policyId: string) => {
    if (!confirm('Are you sure? This will remove all signing restrictions for this key.')) return;

    try {
      await api.delete(`/v1/policies/${policyId}`);
      fetchData();
    } catch (error) {
      console.error('Failed to delete policy:', error);
      alert('Failed to delete policy');
    }
  };

  const getRuleIcon = (type: string) => {
    switch (type) {
      case 'amount_limit':
        return <DollarSign className="w-5 h-5 text-yellow-400" />;
      case 'time_based':
        return <Clock className="w-5 h-5 text-blue-400" />;
      case 'whitelist':
        return <Shield className="w-5 h-5 text-green-400" />;
      case 'blockchain':
        return <Lock className="w-5 h-5 text-purple-400" />;
      default:
        return <Shield className="w-5 h-5 text-slate-400" />;
    }
  };

  const getRuleLabel = (type: string) => {
    return type.replace(/_/g, ' ').replace(/\b\w/g, (l) => l.toUpperCase());
  };

  return (
    <AdminLayout>
      <div className="space-y-6">
        {/* Header */}
        <div className="flex justify-between items-center">
          <div>
            <h1 className="text-3xl font-bold text-white">Signing Policies</h1>
            <p className="text-slate-400 mt-2">Define rules and approval workflows for signing</p>
          </div>
          <button
            onClick={() => setShowCreateModal(true)}
            className="flex items-center gap-2 bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
          >
            <Plus className="w-5 h-5" />
            Create Policy
          </button>
        </div>

        {/* Policies List */}
        {loading ? (
          <div className="text-slate-400">Loading policies...</div>
        ) : policies.length === 0 ? (
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-12 text-center">
            <Shield className="w-12 h-12 text-slate-500 mx-auto mb-4" />
            <p className="text-slate-400">No policies created yet</p>
            <p className="text-sm text-slate-500 mt-2">Create a policy to add signing restrictions</p>
          </div>
        ) : (
          <div className="space-y-4">
            {policies.map((policy) => (
              <div key={policy.policy_id} className="bg-slate-900 rounded-lg border border-slate-800 p-6 hover:border-slate-700 transition-colors">
                <div className="flex items-start justify-between mb-4">
                  <div>
                    <h3 className="text-lg font-bold text-white">{policy.name}</h3>
                    <p className="text-sm text-slate-400 mt-1">{policy.description}</p>
                  </div>
                  <div className="flex gap-2">
                    <button className="p-2 text-blue-400 hover:text-blue-300 hover:bg-blue-900/20 rounded transition-colors">
                      <Edit2 className="w-5 h-5" />
                    </button>
                    <button
                      onClick={() => handleDeletePolicy(policy.policy_id)}
                      className="p-2 text-red-400 hover:text-red-300 hover:bg-red-900/20 rounded transition-colors"
                    >
                      <Trash2 className="w-5 h-5" />
                    </button>
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-4 mb-4">
                  <div className="bg-slate-800 rounded p-3">
                    <p className="text-xs text-slate-400 mb-1">Key</p>
                    <p className="text-sm text-white font-mono">
                      {keys.find((k) => k.id === policy.key_id)?.name || policy.key_id.substring(0, 12)}...
                    </p>
                  </div>
                  <div className="bg-slate-800 rounded p-3">
                    <p className="text-xs text-slate-400 mb-1">Status</p>
                    <span className="inline-block px-2 py-1 bg-green-900/30 text-green-300 rounded text-xs font-medium">
                      {policy.status === 'active' ? 'Active' : 'Inactive'}
                    </span>
                  </div>
                </div>

                {/* Rules */}
                <div className="mb-4">
                  <p className="text-xs text-slate-400 mb-2">Rules</p>
                  <div className="space-y-2">
                    {policy.rules.map((rule) => (
                      <div
                        key={rule.rule_id}
                        className="flex items-center gap-3 p-3 bg-slate-800 rounded"
                      >
                        {getRuleIcon(rule.type)}
                        <div className="flex-1">
                          <p className="text-sm text-white font-medium">{getRuleLabel(rule.type)}</p>
                          <p className="text-xs text-slate-400">{rule.description}</p>
                        </div>
                        <span className={`px-2 py-1 rounded text-xs font-medium ${
                          rule.enabled
                            ? 'bg-green-900/30 text-green-300'
                            : 'bg-slate-700 text-slate-400'
                        }`}>
                          {rule.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Approvals */}
                {policy.approvals && policy.approvals.required > 0 && (
                  <div className="pt-4 border-t border-slate-700">
                    <p className="text-xs text-slate-400 mb-2">Approvals Required</p>
                    <p className="text-sm text-slate-300">
                      {policy.approvals.required} out of {policy.approvals.approvers.length} approvers needed
                    </p>
                  </div>
                )}

                <p className="text-xs text-slate-500 mt-4">
                  Created {new Date(policy.created_at).toLocaleDateString()}
                </p>
              </div>
            ))}
          </div>
        )}

        {/* Create Policy Modal */}
        {showCreateModal && (
          <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6 max-w-lg w-full max-h-[90vh] overflow-y-auto">
              <h2 className="text-2xl font-bold text-white mb-4">Create Signing Policy</h2>

              <form onSubmit={handleCreatePolicy} className="space-y-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Select Key</label>
                  <select
                    value={formData.key_id}
                    onChange={(e) => setFormData({ ...formData, key_id: e.target.value })}
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                    required
                  >
                    <option value="">Choose a key...</option>
                    {keys.map((key) => (
                      <option key={key.id} value={key.id}>
                        {key.name} ({key.blockchain})
                      </option>
                    ))}
                  </select>
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Policy Name</label>
                  <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    placeholder="e.g., Daily Limit $100K"
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                    required
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Description</label>
                  <textarea
                    value={formData.description}
                    onChange={(e) => setFormData({ ...formData, description: e.target.value })}
                    placeholder="Describe the policy purpose"
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500 resize-none"
                    rows={3}
                  />
                </div>

                <div className="bg-slate-800 rounded p-4">
                  <p className="text-sm font-medium text-slate-300 mb-3">Signing Rules</p>
                  <div className="space-y-3">
                    <div>
                      <label className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          defaultChecked
                          className="rounded border-slate-700 bg-slate-800"
                        />
                        <span className="text-sm text-slate-300">Amount Limit</span>
                      </label>
                      <input
                        type="text"
                        placeholder="Max amount (e.g., 100000)"
                        defaultValue="100000"
                        className="mt-2 w-full bg-slate-700 border border-slate-600 rounded px-3 py-2 text-white text-sm focus:outline-none"
                      />
                    </div>
                    <label className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        className="rounded border-slate-700 bg-slate-800"
                      />
                      <span className="text-sm text-slate-300">Whitelist Addresses</span>
                    </label>
                    <label className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        className="rounded border-slate-700 bg-slate-800"
                      />
                      <span className="text-sm text-slate-300">Time-Based Restrictions</span>
                    </label>
                  </div>
                </div>

                <div className="flex gap-3 pt-4">
                  <button
                    type="button"
                    onClick={() => setShowCreateModal(false)}
                    className="flex-1 px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded font-medium transition-colors"
                  >
                    Cancel
                  </button>
                  <button
                    type="submit"
                    className="flex-1 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded font-medium transition-colors"
                  >
                    Create Policy
                  </button>
                </div>
              </form>
            </div>
          </div>
        )}
      </div>
    </AdminLayout>
  );
}
