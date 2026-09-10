'use client';

import { useEffect, useState } from 'react';
import { CheckCircle, Clock, AlertCircle } from 'lucide-react';
import { api } from '@/lib/api';

interface Signing {
  id: string;
  key_id: string;
  status: 'pending' | 'in_progress' | 'completed' | 'failed';
  latency_ms?: number;
  created_at: string;
}

export function RecentSignings() {
  const [signings, setSignings] = useState<Signing[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchSignings = async () => {
      try {
        const response = await api.get('/v1/sign?limit=5');
        setSignings(response.data || []);
      } catch (error) {
        console.error('Failed to fetch signings:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchSignings();
  }, []);

  const getStatusIcon = (status: Signing['status']) => {
    switch (status) {
      case 'completed':
        return <CheckCircle className="w-4 h-4 text-green-400" />;
      case 'in_progress':
      case 'pending':
        return <Clock className="w-4 h-4 text-yellow-400" />;
      case 'failed':
        return <AlertCircle className="w-4 h-4 text-red-400" />;
    }
  };

  if (loading) {
    return <div className="text-slate-400">Loading signings...</div>;
  }

  if (signings.length === 0) {
    return (
      <div className="text-center py-8">
        <p className="text-slate-400">No signings yet.</p>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {signings.map((signing) => (
        <div
          key={signing.id}
          className="flex items-center justify-between p-3 bg-slate-800 rounded"
        >
          <div className="flex items-center gap-3">
            {getStatusIcon(signing.status)}
            <div>
              <p className="text-sm font-medium text-white">{signing.id.slice(0, 8)}</p>
              <p className="text-xs text-slate-400">
                {new Date(signing.created_at).toLocaleDateString()}
              </p>
            </div>
          </div>
          <div className="text-right">
            <p className="text-xs text-slate-300 capitalize">{signing.status}</p>
            {signing.latency_ms && (
              <p className="text-xs text-slate-400">{signing.latency_ms}ms</p>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
