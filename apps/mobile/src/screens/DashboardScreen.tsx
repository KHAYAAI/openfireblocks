import React, { useEffect, useState } from 'react';
import { View, Text, ScrollView, StyleSheet, TouchableOpacity } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { MaterialCommunityIcons } from '@expo/vector-icons';
import { useAuthStore } from '../store/auth';
import { api } from '../lib/api';

interface KeySummary {
  id: string;
  name: string;
  blockchain: string;
  status: 'active' | 'pending' | 'inactive';
}

interface Stats {
  activeKeys: number;
  signingsThisMonth: number;
  walletBalance: string;
  lastSigningTime: string;
}

export function DashboardScreen() {
  const insets = useSafeAreaInsets();
  const { user } = useAuthStore();
  const [keys, setKeys] = useState<KeySummary[]>([]);
  const [stats, setStats] = useState<Stats | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchData = async () => {
      try {
        const [keysRes, statsRes] = await Promise.all([
          api.get('/v1/keys'),
          api.get('/v1/dashboard/stats'),
        ]);
        setKeys(keysRes.data);
        setStats(statsRes.data);
      } catch (error) {
        console.error('Failed to fetch dashboard data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, []);

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'active':
        return '#10b981';
      case 'pending':
        return '#f59e0b';
      default:
        return '#6b7280';
    }
  };

  return (
    <ScrollView
      style={[styles.container, { paddingTop: insets.top }]}
      scrollEventThrottle={16}
    >
      {/* Header */}
      <View style={styles.header}>
        <View>
          <Text style={styles.greeting}>Welcome back</Text>
          <Text style={styles.userName}>{user?.name || 'User'}</Text>
        </View>
        <TouchableOpacity style={styles.profileButton}>
          <MaterialCommunityIcons name="account-circle" size={40} color="#3b82f6" />
        </TouchableOpacity>
      </View>

      {/* Quick Stats */}
      {stats && (
        <View style={styles.statsGrid}>
          <View style={styles.statCard}>
            <MaterialCommunityIcons name="key" size={24} color="#3b82f6" />
            <Text style={styles.statValue}>{stats.activeKeys}</Text>
            <Text style={styles.statLabel}>Active Keys</Text>
          </View>
          <View style={styles.statCard}>
            <MaterialCommunityIcons name="signature-check" size={24} color="#10b981" />
            <Text style={styles.statValue}>{stats.signingsThisMonth}</Text>
            <Text style={styles.statLabel}>This Month</Text>
          </View>
          <View style={styles.statCard}>
            <MaterialCommunityIcons name="wallet" size={24} color="#f59e0b" />
            <Text style={styles.statValue}>{stats.walletBalance}</Text>
            <Text style={styles.statLabel}>Balance</Text>
          </View>
        </View>
      )}

      {/* Actions */}
      <View style={styles.actionsContainer}>
        <TouchableOpacity style={styles.actionButton}>
          <MaterialCommunityIcons name="plus-circle" size={20} color="white" />
          <Text style={styles.actionButtonText}>New Key</Text>
        </TouchableOpacity>
        <TouchableOpacity style={[styles.actionButton, styles.secondaryButton]}>
          <MaterialCommunityIcons name="signature-check" size={20} color="#3b82f6" />
          <Text style={[styles.actionButtonText, styles.secondaryButtonText]}>Sign</Text>
        </TouchableOpacity>
      </View>

      {/* Keys List */}
      <View style={styles.section}>
        <View style={styles.sectionHeader}>
          <Text style={styles.sectionTitle}>Your Keys</Text>
          <TouchableOpacity>
            <Text style={styles.sectionLink}>View All</Text>
          </TouchableOpacity>
        </View>

        {keys.length === 0 ? (
          <View style={styles.emptyState}>
            <MaterialCommunityIcons name="key-plus" size={40} color="#9ca3af" />
            <Text style={styles.emptyStateText}>No keys yet. Create one to get started.</Text>
          </View>
        ) : (
          keys.slice(0, 3).map((key) => (
            <TouchableOpacity key={key.id} style={styles.keyCard}>
              <View style={styles.keyCardContent}>
                <View>
                  <Text style={styles.keyName}>{key.name}</Text>
                  <Text style={styles.keyBlockchain}>{key.blockchain.toUpperCase()}</Text>
                </View>
                <View
                  style={[
                    styles.statusBadge,
                    { backgroundColor: getStatusColor(key.status) + '20' },
                  ]}
                >
                  <View
                    style={[
                      styles.statusDot,
                      { backgroundColor: getStatusColor(key.status) },
                    ]}
                  />
                  <Text style={[styles.statusText, { color: getStatusColor(key.status) }]}>
                    {key.status === 'active' ? 'Active' : 'Pending'}
                  </Text>
                </View>
              </View>
            </TouchableOpacity>
          ))
        )}
      </View>

      {/* Recent Activity */}
      <View style={[styles.section, { marginBottom: 40 }]}>
        <View style={styles.sectionHeader}>
          <Text style={styles.sectionTitle}>Recent Activity</Text>
        </View>
        <View style={styles.activityCard}>
          <MaterialCommunityIcons name="information" size={20} color="#6b7280" />
          <Text style={styles.activityText}>Pull down to refresh</Text>
        </View>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#0f172a',
    paddingHorizontal: 16,
  },
  header: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginVertical: 24,
  },
  greeting: {
    fontSize: 14,
    color: '#9ca3af',
    marginBottom: 4,
  },
  userName: {
    fontSize: 24,
    fontWeight: 'bold',
    color: '#ffffff',
  },
  profileButton: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: 'rgba(59, 130, 246, 0.1)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  statsGrid: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginBottom: 24,
    gap: 8,
  },
  statCard: {
    flex: 1,
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#334155',
  },
  statValue: {
    fontSize: 20,
    fontWeight: 'bold',
    color: '#ffffff',
    marginTop: 8,
  },
  statLabel: {
    fontSize: 12,
    color: '#9ca3af',
    marginTop: 4,
  },
  actionsContainer: {
    flexDirection: 'row',
    gap: 12,
    marginBottom: 32,
  },
  actionButton: {
    flex: 1,
    backgroundColor: '#3b82f6',
    borderRadius: 12,
    paddingVertical: 14,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 8,
  },
  secondaryButton: {
    backgroundColor: 'transparent',
    borderWidth: 1,
    borderColor: '#3b82f6',
  },
  actionButtonText: {
    color: '#ffffff',
    fontWeight: 'bold',
    fontSize: 16,
  },
  secondaryButtonText: {
    color: '#3b82f6',
  },
  section: {
    marginBottom: 24,
  },
  sectionHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 12,
  },
  sectionTitle: {
    fontSize: 18,
    fontWeight: 'bold',
    color: '#ffffff',
  },
  sectionLink: {
    fontSize: 14,
    color: '#3b82f6',
  },
  keyCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: '#334155',
  },
  keyCardContent: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  keyName: {
    fontSize: 16,
    fontWeight: '600',
    color: '#ffffff',
  },
  keyBlockchain: {
    fontSize: 12,
    color: '#9ca3af',
    marginTop: 4,
  },
  statusBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 6,
    gap: 4,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  statusText: {
    fontSize: 12,
    fontWeight: '500',
  },
  emptyState: {
    alignItems: 'center',
    paddingVertical: 32,
  },
  emptyStateText: {
    color: '#9ca3af',
    marginTop: 12,
    textAlign: 'center',
  },
  activityCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  activityText: {
    color: '#9ca3af',
    flex: 1,
  },
});
