'use client';

import { useEffect, useState } from 'react';
import { Plus, Trash2, AlertCircle, CheckCircle, Zap } from 'lucide-react';
import { AdminLayout } from '@/components/layout/AdminLayout';
import { api } from '@/lib/api';

interface Integration {
  integration_id: string;
  name: string;
  description: string;
  type: string;
  status: string;
  webhook_url?: string;
  events: string[];
  created_at: string;
  last_activated?: string;
}

export default function IntegrationsPage() {
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [formData, setFormData] = useState({
    name: '',
    description: '',
    type: 'webhook',
    webhook_url: '',
    events: [] as string[],
  });

  useEffect(() => {
    fetchIntegrations();
  }, []);

  const fetchIntegrations = async () => {
    try {
      const res = await api.get('/v1/integrations');
      setIntegrations(res.data || []);
    } catch (error) {
      console.error('Failed to fetch integrations:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleCreateIntegration = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await api.post('/v1/integrations', {
        name: formData.name,
        description: formData.description,
        type: formData.type,
        webhook_url: formData.webhook_url,
        events: formData.events.length > 0 ? formData.events : ['signing', 'key_created'],
      });

      setFormData({
        name: '',
        description: '',
        type: 'webhook',
        webhook_url: '',
        events: [],
      });
      setShowCreateModal(false);
      fetchIntegrations();
    } catch (error) {
      console.error('Failed to create integration:', error);
      alert('Failed to create integration');
    }
  };

  const handleDeleteIntegration = async (integrationId: string) => {
    if (!confirm('Are you sure you want to delete this integration?')) return;

    try {
      await api.delete(`/v1/integrations/${integrationId}`);
      fetchIntegrations();
    } catch (error) {
      console.error('Failed to delete integration:', error);
      alert('Failed to delete integration');
    }
  };

  const eventOptions = [
    'signing',
    'signing_completed',
    'signing_failed',
    'key_created',
    'key_deleted',
    'kyc_verified',
    'kyc_rejected',
  ];

  const handleEventToggle = (event: string) => {
    setFormData((prev) => ({
      ...prev,
      events: prev.events.includes(event)
        ? prev.events.filter((e) => e !== event)
        : [...prev.events, event],
    }));
  };

  const getTypeColor = (type: string) => {
    switch (type) {
      case 'webhook':
        return 'text-blue-400 bg-blue-900/20';
      case 'api_key':
        return 'text-purple-400 bg-purple-900/20';
      case 'oauth':
        return 'text-green-400 bg-green-900/20';
      default:
        return 'text-gray-400 bg-gray-900/20';
    }
  };

  const getStatusIcon = (status: string) => {
    return status === 'active' ? (
      <CheckCircle className="w-5 h-5 text-green-400" />
    ) : (
      <AlertCircle className="w-5 h-5 text-yellow-400" />
    );
  };

  return (
    <AdminLayout>
      <div className="space-y-6">
        {/* Header */}
        <div className="flex justify-between items-center">
          <div>
            <h1 className="text-3xl font-bold text-white">Integrations</h1>
            <p className="text-slate-400 mt-2">Manage webhooks and third-party integrations</p>
          </div>
          <button
            onClick={() => setShowCreateModal(true)}
            className="flex items-center gap-2 bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
          >
            <Plus className="w-5 h-5" />
            Add Integration
          </button>
        </div>

        {/* Integration Cards */}
        {loading ? (
          <div className="text-slate-400">Loading integrations...</div>
        ) : integrations.length === 0 ? (
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-12 text-center">
            <Zap className="w-12 h-12 text-slate-500 mx-auto mb-4" />
            <p className="text-slate-400">No integrations yet</p>
            <p className="text-sm text-slate-500 mt-2">Create your first integration to get started</p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            {integrations.map((integration) => (
              <div key={integration.integration_id} className="bg-slate-900 rounded-lg border border-slate-800 p-6 hover:border-slate-700 transition-colors">
                <div className="flex items-start justify-between mb-4">
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-2">
                      <h3 className="text-lg font-bold text-white">{integration.name}</h3>
                      {getStatusIcon(integration.status)}
                    </div>
                    <p className="text-sm text-slate-400 mb-3">{integration.description}</p>
                    <span className={`inline-block px-2 py-1 rounded text-xs font-medium ${getTypeColor(integration.type)}`}>
                      {integration.type.replace('_', ' ').toUpperCase()}
                    </span>
                  </div>
                  <button
                    onClick={() => handleDeleteIntegration(integration.integration_id)}
                    className="p-2 text-red-400 hover:text-red-300 hover:bg-red-900/20 rounded transition-colors"
                  >
                    <Trash2 className="w-5 h-5" />
                  </button>
                </div>

                {integration.webhook_url && (
                  <div className="mb-4 p-3 bg-slate-800 rounded border border-slate-700">
                    <p className="text-xs text-slate-400 mb-1">Webhook URL</p>
                    <p className="text-sm text-slate-300 break-all font-mono">{integration.webhook_url}</p>
                  </div>
                )}

                <div className="mb-4">
                  <p className="text-xs text-slate-400 mb-2">Events</p>
                  <div className="flex flex-wrap gap-2">
                    {integration.events.map((event) => (
                      <span
                        key={event}
                        className="px-2 py-1 bg-slate-800 text-slate-300 rounded text-xs"
                      >
                        {event}
                      </span>
                    ))}
                  </div>
                </div>

                <div className="flex items-center justify-between text-xs text-slate-500 pt-4 border-t border-slate-700">
                  <span>Created {new Date(integration.created_at).toLocaleDateString()}</span>
                  <button className="text-blue-400 hover:text-blue-300 font-medium">
                    Test
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Create Integration Modal */}
        {showCreateModal && (
          <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
            <div className="bg-slate-900 rounded-lg border border-slate-800 p-6 max-w-md w-full">
              <h2 className="text-2xl font-bold text-white mb-4">Create Integration</h2>

              <form onSubmit={handleCreateIntegration} className="space-y-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Name</label>
                  <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    placeholder="My Integration"
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                    required
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Description</label>
                  <input
                    type="text"
                    value={formData.description}
                    onChange={(e) => setFormData({ ...formData, description: e.target.value })}
                    placeholder="Integration description"
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Type</label>
                  <select
                    value={formData.type}
                    onChange={(e) => setFormData({ ...formData, type: e.target.value })}
                    className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                  >
                    <option value="webhook">Webhook</option>
                    <option value="api_key">API Key</option>
                    <option value="oauth">OAuth</option>
                  </select>
                </div>

                {formData.type === 'webhook' && (
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Webhook URL</label>
                    <input
                      type="url"
                      value={formData.webhook_url}
                      onChange={(e) => setFormData({ ...formData, webhook_url: e.target.value })}
                      placeholder="https://example.com/webhook"
                      className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-2 text-white focus:outline-none focus:border-blue-500"
                      required={formData.type === 'webhook'}
                    />
                  </div>
                )}

                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Events</label>
                  <div className="space-y-2 max-h-40 overflow-y-auto">
                    {eventOptions.map((event) => (
                      <label key={event} className="flex items-center">
                        <input
                          type="checkbox"
                          checked={formData.events.includes(event)}
                          onChange={() => handleEventToggle(event)}
                          className="rounded border-slate-700 bg-slate-800"
                        />
                        <span className="ml-2 text-sm text-slate-300">{event}</span>
                      </label>
                    ))}
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
