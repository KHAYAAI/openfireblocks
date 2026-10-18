import React, { useEffect, useState } from 'react';
import {
  View,
  Text,
  ScrollView,
  StyleSheet,
  TouchableOpacity,
  ActivityIndicator,
  Share,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { MaterialCommunityIcons } from '@expo/vector-icons';
import { api } from '../lib/api';

interface TransactionDetail {
  transaction_id: string;
  signing_id: string;
  key_name: string;
  blockchain: string;
  status: 'pending' | 'signing' | 'confirmed' | 'failed';
  amount?: string;
  destination_address: string;
  source_address?: string;
  transaction_hash?: string;
  gas_price?: string;
  gas_used?: string;
  nonce?: number;
  confirmation_count?: number;
  created_at: string;
  signed_at?: string;
  confirmed_at?: string;
  error_message?: string;
  metadata?: Record<string, any>;
}

interface TransactionDetailsScreenProps {
  transactionId?: string;
  signingId?: string;
}

export function TransactionDetailsScreen(props: TransactionDetailsScreenProps) {
  const insets = useSafeAreaInsets();
  const [transaction, setTransaction] = useState<TransactionDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [autoRefresh, setAutoRefresh] = useState(true);

  useEffect(() => {
    fetchTransaction();

    // Auto-refresh if transaction is pending/signing
    let interval: NodeJS.Timeout;
    if (autoRefresh && transaction?.status !== 'confirmed' && transaction?.status !== 'failed') {
      interval = setInterval(fetchTransaction, 5000);
    }

    return () => {
      if (interval) clearInterval(interval);
    };
  }, [props.transactionId, props.signingId, autoRefresh]);

  const fetchTransaction = async () => {
    try {
      const id = props.transactionId || props.signingId;
      if (!id) return;

      const res = await api.get(`/v1/signings/${id}`);
      setTransaction(res.data);
    } catch (error) {
      console.error('Failed to fetch transaction:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleShare = async () => {
    try {
      await Share.share({
        message: `Transaction: ${transaction?.transaction_hash || transaction?.signing_id}
Status: ${transaction?.status}
Blockchain: ${transaction?.blockchain}
Created: ${new Date(transaction?.created_at || '').toLocaleString()}`,
      });
    } catch (error) {
      console.error('Failed to share:', error);
    }
  };

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'pending':
        return { color: '#f59e0b', background: '#f59e0b20', icon: 'clock-outline' };
      case 'signing':
        return { color: '#3b82f6', background: '#3b82f620', icon: 'sync' };
      case 'confirmed':
        return { color: '#10b981', background: '#10b98120', icon: 'check-circle' };
      case 'failed':
        return { color: '#ef4444', background: '#ef444420', icon: 'alert-circle' };
      default:
        return { color: '#6b7280', background: '#6b728020', icon: 'help-circle' };
    }
  };

  const getBlockchainIcon = (blockchain: string) => {
    switch (blockchain.toLowerCase()) {
      case 'bitcoin':
        return 'bitcoin';
      case 'ethereum':
        return 'ethereum';
      case 'solana':
        return 'diamond';
      case 'polygon':
        return 'pentagon';
      default:
        return 'coin';
    }
  };

  const formatAddress = (address: string, length: number = 12) => {
    if (!address) return '';
    if (address.length <= length) return address;
    return `${address.substring(0, length)}...${address.substring(address.length - 6)}`;
  };

  if (loading) {
    return (
      <View style={[styles.container, { paddingTop: insets.top }]}>
        <ActivityIndicator size="large" color="#3b82f6" />
      </View>
    );
  }

  if (!transaction) {
    return (
      <View style={[styles.container, { paddingTop: insets.top }]}>
        <Text style={styles.errorText}>Transaction not found</Text>
      </View>
    );
  }

  const statusInfo = getStatusColor(transaction.status);

  return (
    <ScrollView style={[styles.container, { paddingTop: insets.top }]} scrollEventThrottle={16}>
      {/* Header with Status */}
      <View style={styles.header}>
        <View style={[styles.statusIcon, { backgroundColor: statusInfo.background }]}>
          <MaterialCommunityIcons name={statusInfo.icon} size={32} color={statusInfo.color} />
        </View>
        <View style={styles.headerContent}>
          <Text style={styles.status}>{transaction.status.toUpperCase()}</Text>
          <Text style={styles.title}>{transaction.key_name}</Text>
        </View>
        <TouchableOpacity style={styles.shareButton} onPress={handleShare}>
          <MaterialCommunityIcons name="share-variant" size={20} color="#3b82f6" />
        </TouchableOpacity>
      </View>

      {/* Amount and Blockchain */}
      {transaction.amount && (
        <View style={styles.amountCard}>
          <View style={styles.amountContent}>
            <Text style={styles.amountLabel}>Amount</Text>
            <Text style={styles.amountValue}>{transaction.amount}</Text>
          </View>
          <View
            style={[
              styles.blockchainBadge,
              { backgroundColor: '#334155' },
            ]}
          >
            <MaterialCommunityIcons
              name={getBlockchainIcon(transaction.blockchain)}
              size={20}
              color="#3b82f6"
            />
            <Text style={styles.blockchainName}>{transaction.blockchain.toUpperCase()}</Text>
          </View>
        </View>
      )}

      {/* Transaction Hashes */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Details</Text>

        {transaction.transaction_hash && (
          <View style={styles.detailItem}>
            <View style={styles.detailLabel}>
              <MaterialCommunityIcons name="identifier" size={16} color="#9ca3af" />
              <Text style={styles.detailLabelText}>Transaction Hash</Text>
            </View>
            <Text style={styles.detailValue} selectable>
              {formatAddress(transaction.transaction_hash, 16)}
            </Text>
          </View>
        )}

        {transaction.signing_id && (
          <View style={styles.detailItem}>
            <View style={styles.detailLabel}>
              <MaterialCommunityIcons name="fingerprint" size={16} color="#9ca3af" />
              <Text style={styles.detailLabelText}>Signing ID</Text>
            </View>
            <Text style={styles.detailValue} selectable>
              {formatAddress(transaction.signing_id, 16)}
            </Text>
          </View>
        )}

        {transaction.destination_address && (
          <View style={styles.detailItem}>
            <View style={styles.detailLabel}>
              <MaterialCommunityIcons name="send" size={16} color="#9ca3af" />
              <Text style={styles.detailLabelText}>Destination</Text>
            </View>
            <Text style={styles.detailValue} selectable>
              {formatAddress(transaction.destination_address, 16)}
            </Text>
          </View>
        )}

        {transaction.source_address && (
          <View style={styles.detailItem}>
            <View style={styles.detailLabel}>
              <MaterialCommunityIcons name="inbox" size={16} color="#9ca3af" />
              <Text style={styles.detailLabelText}>Source</Text>
            </View>
            <Text style={styles.detailValue} selectable>
              {formatAddress(transaction.source_address, 16)}
            </Text>
          </View>
        )}
      </View>

      {/* Gas Information (EVM only) */}
      {(transaction.gas_price || transaction.gas_used) && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Gas Information</Text>

          {transaction.gas_price && (
            <View style={styles.detailItem}>
              <Text style={styles.detailLabelText}>Gas Price</Text>
              <Text style={styles.detailValue}>{transaction.gas_price}</Text>
            </View>
          )}

          {transaction.gas_used && (
            <View style={styles.detailItem}>
              <Text style={styles.detailLabelText}>Gas Used</Text>
              <Text style={styles.detailValue}>{transaction.gas_used}</Text>
            </View>
          )}

          {transaction.nonce && (
            <View style={styles.detailItem}>
              <Text style={styles.detailLabelText}>Nonce</Text>
              <Text style={styles.detailValue}>{transaction.nonce}</Text>
            </View>
          )}
        </View>
      )}

      {/* Confirmation Information */}
      {transaction.confirmation_count !== undefined && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Confirmations</Text>
          <View style={styles.progressContainer}>
            <View style={styles.progressBar}>
              <View
                style={[
                  styles.progressFill,
                  {
                    width: `${Math.min((transaction.confirmation_count / 6) * 100, 100)}%`,
                  },
                ]}
              />
            </View>
            <Text style={styles.progressText}>
              {transaction.confirmation_count} / 6 confirmations
            </Text>
          </View>
        </View>
      )}

      {/* Timestamps */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Timeline</Text>

        <View style={styles.timelineItem}>
          <View style={styles.timelineDot} />
          <View style={styles.timelineContent}>
            <Text style={styles.timelineLabel}>Created</Text>
            <Text style={styles.timelineTime}>
              {new Date(transaction.created_at).toLocaleString()}
            </Text>
          </View>
        </View>

        {transaction.signed_at && (
          <View style={styles.timelineItem}>
            <View style={[styles.timelineDot, { backgroundColor: '#3b82f6' }]} />
            <View style={styles.timelineContent}>
              <Text style={styles.timelineLabel}>Signed</Text>
              <Text style={styles.timelineTime}>
                {new Date(transaction.signed_at).toLocaleString()}
              </Text>
            </View>
          </View>
        )}

        {transaction.confirmed_at && (
          <View style={styles.timelineItem}>
            <View style={[styles.timelineDot, { backgroundColor: '#10b981' }]} />
            <View style={styles.timelineContent}>
              <Text style={styles.timelineLabel}>Confirmed</Text>
              <Text style={styles.timelineTime}>
                {new Date(transaction.confirmed_at).toLocaleString()}
              </Text>
            </View>
          </View>
        )}
      </View>

      {/* Error Message */}
      {transaction.error_message && (
        <View style={[styles.section, styles.errorSection]}>
          <View style={styles.errorHeader}>
            <MaterialCommunityIcons name="alert-circle" size={20} color="#ef4444" />
            <Text style={styles.errorTitle}>Error</Text>
          </View>
          <Text style={styles.errorMessage}>{transaction.error_message}</Text>
        </View>
      )}

      {/* Auto-Refresh Toggle */}
      {(transaction.status === 'pending' || transaction.status === 'signing') && (
        <TouchableOpacity
          style={[styles.refreshButton, autoRefresh && styles.refreshButtonActive]}
          onPress={() => setAutoRefresh(!autoRefresh)}
        >
          <MaterialCommunityIcons
            name={autoRefresh ? 'sync' : 'sync-off'}
            size={16}
            color={autoRefresh ? '#3b82f6' : '#9ca3af'}
          />
          <Text style={[styles.refreshText, autoRefresh && styles.refreshTextActive]}>
            {autoRefresh ? 'Auto-refreshing...' : 'Tap to enable auto-refresh'}
          </Text>
        </TouchableOpacity>
      )}

      <View style={{ height: 40 }} />
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
    alignItems: 'center',
    gap: 12,
    marginVertical: 24,
  },
  statusIcon: {
    width: 56,
    height: 56,
    borderRadius: 12,
    justifyContent: 'center',
    alignItems: 'center',
  },
  headerContent: {
    flex: 1,
  },
  status: {
    fontSize: 12,
    fontWeight: '600',
    color: '#9ca3af',
    textTransform: 'uppercase',
  },
  title: {
    fontSize: 18,
    fontWeight: 'bold',
    color: 'white',
    marginTop: 4,
  },
  shareButton: {
    width: 40,
    height: 40,
    borderRadius: 8,
    backgroundColor: '#334155',
    justifyContent: 'center',
    alignItems: 'center',
  },
  amountCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
    borderColor: '#334155',
    marginBottom: 24,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  amountContent: {
    flex: 1,
  },
  amountLabel: {
    fontSize: 12,
    color: '#9ca3af',
    marginBottom: 4,
  },
  amountValue: {
    fontSize: 20,
    fontWeight: 'bold',
    color: 'white',
  },
  blockchainBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 8,
    gap: 6,
  },
  blockchainName: {
    fontSize: 12,
    fontWeight: '600',
    color: '#3b82f6',
  },
  section: {
    marginBottom: 24,
  },
  sectionTitle: {
    fontSize: 14,
    fontWeight: '600',
    color: '#9ca3af',
    marginBottom: 12,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
  },
  detailItem: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 12,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: '#334155',
  },
  detailLabel: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    marginBottom: 6,
  },
  detailLabelText: {
    fontSize: 12,
    color: '#9ca3af',
    fontWeight: '500',
  },
  detailValue: {
    fontSize: 12,
    fontFamily: 'Courier New',
    color: '#3b82f6',
  },
  progressContainer: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 12,
    borderWidth: 1,
    borderColor: '#334155',
  },
  progressBar: {
    height: 8,
    backgroundColor: '#334155',
    borderRadius: 4,
    overflow: 'hidden',
    marginBottom: 8,
  },
  progressFill: {
    height: '100%',
    backgroundColor: '#3b82f6',
  },
  progressText: {
    fontSize: 12,
    color: '#9ca3af',
    textAlign: 'center',
  },
  timelineItem: {
    flexDirection: 'row',
    marginBottom: 16,
    paddingLeft: 12,
  },
  timelineDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: '#6b7280',
    marginRight: 12,
    marginTop: 4,
  },
  timelineContent: {
    flex: 1,
  },
  timelineLabel: {
    fontSize: 12,
    color: '#9ca3af',
    marginBottom: 2,
  },
  timelineTime: {
    fontSize: 13,
    color: 'white',
    fontWeight: '500',
  },
  errorSection: {
    backgroundColor: '#ef444420',
    borderWidth: 1,
    borderColor: '#ef4444',
    borderRadius: 12,
    padding: 12,
  },
  errorHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    marginBottom: 8,
  },
  errorTitle: {
    fontSize: 12,
    fontWeight: '600',
    color: '#ef4444',
  },
  errorMessage: {
    fontSize: 12,
    color: '#fca5a5',
  },
  refreshButton: {
    backgroundColor: 'transparent',
    borderWidth: 1,
    borderColor: '#9ca3af',
    borderRadius: 12,
    paddingVertical: 12,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 8,
    marginBottom: 24,
  },
  refreshButtonActive: {
    backgroundColor: '#3b82f620',
    borderColor: '#3b82f6',
  },
  refreshText: {
    fontSize: 12,
    fontWeight: '500',
    color: '#9ca3af',
  },
  refreshTextActive: {
    color: '#3b82f6',
  },
  errorText: {
    color: '#ef4444',
    textAlign: 'center',
    marginTop: 20,
  },
});
