'use client';

import { useEffect, useState } from 'react';
import { Plus, Copy, Trash2, Eye, EyeOff, RotateCw } from 'lucide-react';
import { AdminLayout } from '@/components/layout/AdminLayout';
import { api } from '@/lib/api';

interface APIKey {
  key_id: string;
  name: string;
  key: string;
  prefix: string;
  last_used?: string;
  created_at: string;
  expires_at?: string;
  permissions: string[];
  is_active: boolean;
}

export default function APIKeysPage() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showKey, setShowKey] = useState<string | null>(null);
  const [formData, setFormData] = useState({
    name: '',
    permissions: [] as string[],
  });

  useEffect(() => {
    fetchAPIKeys();
  }, []);

  const fetchAPIKeys = async () => {
    try {
      const res = await api.get('/v1/api-keys');
      setKeys(res.data || []);
    } catch (error) {
      console.error('Failed to fetch API keys:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleCreateKey = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await api.post('/v1/api-keys', {
        name: formData.name,
        permissions: formData.permissions.length > 0 ? formData.permissions : ['sign', 'read'],
      });

      setFormData({ name: '', permissions: [] });
      setShowCreateModal(false);
      fetchAPIKeys();
    } catch (error) {
      console.error('Failed to create API key:', error);
      alert('Failed to create API key');
    }
  };

  const handleRevokeKey = async (keyId: string) => {
    if (!confirm('Are you sure? This action cannot be undone.')) return;

    try {
      await api.delete(`/v1/api-keys/${keyId}`);
      fetchAPIKeys();
    } catch (error) {
      console.error('Failed to revoke API key:', error);
      alert('Failed to revoke API key');
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    alert('Copied to clipboard!');
  };

  const permissionOptions = ['sign', 'read', 'write', 'admin'];

  const handlePermissionToggle = (permission: string) => {
    setFormData((prev) => ({
      ...prev,
      permissions: prev.permissions.includes(permission)
        ? prev.permissions.filter((p) => p !== permission)
        : [...prev.permissions, permission],
    }));
  };

  return (
    <AdminLayout>
      <div className="space-y-6">
        {/* Header */}
        <div className="flex justify-between items-center">
          <div>
            <h1 className="text-3xl font-bold text-white">API Keys</h1>
            <p className="text-slate-400 mt-2">Manage your API keys for programmatic access</p>
          </div>
          <button
            onClick={() => setShowCreateModal(true)}
            className="flex items-center gap-2 bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
          >
            <Plus className="w-5 h-5" />
            Create Key
          </button>
        </div>

        {/* Warning */}
        <div className="bg-yellow-900/20 border border-yellow-700 rounded-lg p-4">
          <p className="text-yellow-300 text-sm">
            Keep your API keys secure. Never share them publicly. Rotate them regularly for security.
          </p>
        </div>

        {/* API Keys Table */}
        <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden">
          {loading ? (
            <div className="p-8 text-center text-slate-400">Loading API keys...</div>
          ) : keys.length === 0 ? (
            <div className="p-8 text-center text-slate-400">
              No API keys yet. Create one to get started.
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-800/50">
                    <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Name</th>
                    <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Key</th>
                    <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Permissions</th>
                    <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Last Used</th>
                    <th className="px-6 py-3 text-left text-sm font-semibold text-slate-300">Created</th>
                    <th className="px-6 py-3 text-right text-sm font-semibold text-slate-300">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {keys.map((key) => (
                    <tr key={key.key_id} className="border-b border-slate-800 hover:bg-slate-800/50">
                      <td className="px-6 py-4">
                        <p className="text-white font-medium">{key.name}</p>
                      </td>
                      <td className="px-6 py-4">
                        <div className="flex items-center gap-2">
                          <code className="text-sm text-blue-400 bg-slate-800 px-2 py-1 rounded font-mono">
                            {showKey === key.key_id ? key.key : key.prefix + '•••••'}
                          </code>
                          <button
                            onClick={() => setShowKey(showKey === key.key_id ? null : key.key_id)}
                            className="text-slate-400 hover:text-slate-300"
                          >
                            {showKey === key.key_id ? (
                              <EyeOff className="w-4 h-4" />
                            ) : (
                              <Eye className="w-4 h-4" />
                            )}
                          </button>
                          <button
                            onClick={() => copyToClipboard(key.key)}
                            className="text-slate-400 hover:text-slate-300"
                          >
                            <Copy className="w-4 h-4" />
                          </button>
                        </div>
                      </td>
                      <td className="px-6 py-4">
                        <div className="flex gap-2">
                          {key.permissions.map((perm) => (
                            <span
                              key={perm}
                              className="px-2 py-1 bg-blue-900/30 text-blue-300 rounded text-xs font-medium"
                            >
                              {perm}
                            </span>
                          ))}
                        </div>
                      </td>
                      <td className="px-6 py-4 text-sm text-slate-400">
                        {key.last_used ? new Date(key.last_used).toLocaleDateString() : 'Never'}
                      </td>
                      <td className="px-6 py-4 text-sm text-slate-400">
                        {new Date(key.created_at).toLocaleDateString()}
                      </td>
                      <td className="px-6 py-4 text-right">
                        <button
                          onClick={() => handleRevokeKey(key.key_id)}
                          className="text-red-400 hover:text-red-300 p-2"
                        >
                          <Trash2 className="w-5 h-5" />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Create Key Modal */}
        {showCreateModal && (
          <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6 max-w-md w-full">
              <h2 className="text-2xl font-bold text-white mb-4">Create API Key</h2>

              <form onSubmit={handleCreateKey} className="space-y-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Key Name</label>
                  <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    placeholder="e.g., Production Server"
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                    required
                  />
                  <p className="text-xs text-slate-500 mt-1">Use a descriptive name for tracking</p>
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Permissions</label>
                  <div className="space-y-2">
                    {permissionOptions.map((perm) => (
                      <label key={perm} className="flex items-center">
                        <input
                          type="checkbox"
                          checked={formData.permissions.includes(perm)}
                          onChange={() => handlePermissionToggle(perm)}
                          className="rounded border-slate-700 bg-slate-800"
                        />
                        <span className="ml-2 text-sm text-slate-300 capitalize">{perm}</span>
                      </label>
                    ))}
                  </div>
                  <p className="text-xs text-slate-500 mt-2">
                    • sign: Initiate signing requests<br />
                    • read: Read key and transaction data<br />
                    • write: Create and update keys<br />
                    • admin: Full access
                  </p>
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
                    Create
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
