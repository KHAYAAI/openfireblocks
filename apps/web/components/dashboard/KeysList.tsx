'use client';

import { useEffect, useState } from 'react';
import { CheckCircle, Clock, AlertCircle } from 'lucide-react';
import { api } from '@/lib/api';

interface Key {
  id: string;
  name: string;
  blockchain: string;
  threshold: number;
  total_parties: number;
  status: 'active' | 'pending_dkg' | 'inactive';
  address?: string;
  created_at: string;
}

export function KeysList() {
  const [keys, setKeys] = useState<Key[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchKeys = async () => {
      try {
        const response = await api.get('/v1/keys');
        setKeys(response.data || []);
      } catch (error) {
        console.error('Failed to fetch keys:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchKeys();
  }, []);

  const getStatusIcon = (status: Key['status']) => {
    switch (status) {
      case 'active':
        return <CheckCircle className="w-5 h-5 text-green-400" />;
      case 'pending_dkg':
        return <Clock className="w-5 h-5 text-yellow-400" />;
      case 'inactive':
        return <AlertCircle className="w-5 h-5 text-slate-400" />;
    }
  };

  const getStatusLabel = (status: Key['status']) => {
    switch (status) {
      case 'active':
        return 'Active';
      case 'pending_dkg':
        return 'Pending DKG';
      case 'inactive':
        return 'Inactive';
    }
  };

  if (loading) {
    return <div className="text-slate-400">Loading keys...</div>;
  }

  if (keys.length === 0) {
    return (
      <div className="text-center py-8">
        <p className="text-slate-400">No keys yet. Create your first key to get started.</p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {keys.map((key) => (
        <div
          key={key.id}
          className="flex items-center justify-between p-4 bg-slate-800 rounded-lg hover:bg-slate-700 transition"
        >
          <div className="flex-1">
            <div className="flex items-center gap-3">
              <h4 className="font-medium text-white">{key.name}</h4>
              <span className="px-2 py-1 bg-slate-700 text-xs rounded text-slate-300">
                {key.blockchain.toUpperCase()}
              </span>
            </div>
            <p className="text-xs text-slate-400 mt-1">
              {key.threshold}-of-{key.total_parties} threshold
              {key.address && ` • ${key.address.slice(0, 10)}...`}
            </p>
          </div>
          <div className="flex items-center gap-3">
            <div className="text-right">
              <div className="flex items-center gap-2 justify-end">
                {getStatusIcon(key.status)}
                <span className="text-sm text-slate-300">
                  {getStatusLabel(key.status)}
                </span>
              </div>
            </div>
            <button className="px-3 py-1 text-xs text-blue-400 hover:text-blue-300 font-medium">
              View
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
