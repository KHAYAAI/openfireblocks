import React, { useEffect, useState } from 'react';
import { View, Text, ScrollView, StyleSheet, TouchableOpacity, Modal, TextInput, FlatList } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { MaterialCommunityIcons } from '@expo/vector-icons';
import { api } from '../lib/api';

interface SigningRequest {
  id: string;
  key_id: string;
  blockchain: string;
  status: 'pending' | 'signing' | 'confirmed' | 'failed';
  transaction_hash?: string;
  created_at: string;
  completed_at?: string;
}

interface KeyPair {
  id: string;
  name: string;
  blockchain: string;
  status: 'active' | 'pending' | 'inactive';
}

export function SigningScreen() {
  const insets = useSafeAreaInsets();
  const [signings, setSignings] = useState<SigningRequest[]>([]);
  const [keys, setKeys] = useState<KeyPair[]>([]);
  const [loading, setLoading] = useState(true);
  const [showSignModal, setShowSignModal] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string>('');
  const [transaction, setTransaction] = useState('');
  const [filterStatus, setFilterStatus] = useState<'all' | 'pending' | 'confirmed' | 'failed'>('all');

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 5000); // Refresh every 5 seconds
    return () => clearInterval(interval);
  }, []);

  const fetchData = async () => {
    try {
      const [signingsRes, keysRes] = await Promise.all([
        api.get('/v1/signings'),
        api.getKeys(),
      ]);
      setSignings(signingsRes.data || []);
      setKeys(keysRes.data || []);
    } catch (error) {
      console.error('Failed to fetch data:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleSign = async () => {
    if (!selectedKey || !transaction.trim()) {
      alert('Please select a key and enter a transaction');
      return;
    }

    try {
      await api.sign(selectedKey, transaction);
      setSelectedKey('');
      setTransaction('');
      setShowSignModal(false);
      fetchData();
    } catch (error) {
      console.error('Failed to sign:', error);
      alert('Failed to sign transaction');
    }
  };

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'pending':
        return '#f59e0b';
      case 'signing':
        return '#3b82f6';
      case 'confirmed':
        return '#10b981';
      case 'failed':
        return '#ef4444';
      default:
        return '#6b7280';
    }
  };

  const getStatusIcon = (status: string) => {
    switch (status) {
      case 'pending':
        return 'clock-outline';
      case 'signing':
        return 'sync';
      case 'confirmed':
        return 'check-circle';
      case 'failed':
        return 'alert-circle';
      default:
        return 'help-circle';
    }
  };

  const filteredSignings = signings.filter((s) =>
    filterStatus === 'all' ? true : s.status === filterStatus
  );

  const activeKeys = keys.filter((k) => k.status === 'active');

  return (
    <ScrollView
      style={[styles.container, { paddingTop: insets.top }]}
      scrollEventThrottle={16}
    >
      <View style={styles.header}>
        <Text style={styles.title}>Sign Transactions</Text>
        <TouchableOpacity
          style={styles.signButton}
          onPress={() => setShowSignModal(true)}
          disabled={activeKeys.length === 0}
        >
          <MaterialCommunityIcons name="pencil-box-outline" size={24} color="white" />
        </TouchableOpacity>
      </View>

      {/* Filter Tabs */}
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        style={styles.filterTabs}
        scrollEventThrottle={16}
      >
        {(['all', 'pending', 'confirmed', 'failed'] as const).map((status) => (
          <TouchableOpacity
            key={status}
            style={[styles.filterTab, filterStatus === status && styles.filterTabActive]}
            onPress={() => setFilterStatus(status)}
          >
            <Text
              style={[
                styles.filterTabText,
                filterStatus === status && styles.filterTabTextActive,
              ]}
            >
              {status === 'all' ? 'All' : status.charAt(0).toUpperCase() + status.slice(1)}
            </Text>
          </TouchableOpacity>
        ))}
      </ScrollView>

      {/* Signing History */}
      {loading ? (
        <Text style={styles.loadingText}>Loading signings...</Text>
      ) : filteredSignings.length === 0 ? (
        <View style={styles.emptyState}>
          <MaterialCommunityIcons name="inbox-outline" size={48} color="#9ca3af" />
          <Text style={styles.emptyStateText}>No signings yet</Text>
          <Text style={styles.emptyStateSubtext}>Create a new signing request</Text>
        </View>
      ) : (
        <View style={styles.signingsList}>
          {filteredSignings.map((signing) => (
            <TouchableOpacity key={signing.id} style={styles.signingCard}>
              <View style={styles.signingHeader}>
                <View style={styles.signingInfo}>
                  <View
                    style={[
                      styles.statusIcon,
                      { backgroundColor: getStatusColor(signing.status) + '20' },
                    ]}
                  >
                    <MaterialCommunityIcons
                      name={getStatusIcon(signing.status)}
                      size={20}
                      color={getStatusColor(signing.status)}
                    />
                  </View>
                  <View style={styles.signingDetails}>
                    <Text style={styles.signingId}>
                      {signing.id.substring(0, 8)}...
                    </Text>
                    <Text
                      style={[
                        styles.signingStatus,
                        { color: getStatusColor(signing.status) },
                      ]}
                    >
                      {signing.status.charAt(0).toUpperCase() + signing.status.slice(1)}
                    </Text>
                  </View>
                </View>
                <Text style={styles.blockchain}>{signing.blockchain.toUpperCase()}</Text>
              </View>

              {signing.transaction_hash && (
                <View style={styles.txHashContainer}>
                  <Text style={styles.txHashLabel}>TX Hash</Text>
                  <Text style={styles.txHash}>{signing.transaction_hash.substring(0, 16)}...</Text>
                </View>
              )}

              <View style={styles.signingFooter}>
                <Text style={styles.timestamp}>
                  {new Date(signing.created_at).toLocaleString()}
                </Text>
                {signing.completed_at && (
                  <Text style={styles.completedTime}>
                    Completed: {new Date(signing.completed_at).toLocaleTimeString()}
                  </Text>
                )}
              </View>
            </TouchableOpacity>
          ))}
        </View>
      )}

      {/* Sign Modal */}
      <Modal
        visible={showSignModal}
        animationType="slide"
        transparent={true}
        onRequestClose={() => setShowSignModal(false)}
      >
        <View style={styles.modalOverlay}>
          <View style={[styles.modalContent, { paddingTop: insets.top + 20 }]}>
            <View style={styles.modalHeader}>
              <Text style={styles.modalTitle}>Sign Transaction</Text>
              <TouchableOpacity onPress={() => setShowSignModal(false)}>
                <MaterialCommunityIcons name="close" size={24} color="white" />
              </TouchableOpacity>
            </View>

            <ScrollView style={styles.modalBody}>
              <Text style={styles.label}>Select Key</Text>
              {activeKeys.length === 0 ? (
                <Text style={styles.noKeysText}>No active keys available</Text>
              ) : (
                <View style={styles.keysList}>
                  {activeKeys.map((key) => (
                    <TouchableOpacity
                      key={key.id}
                      style={[
                        styles.keyOption,
                        selectedKey === key.id && styles.keyOptionActive,
                      ]}
                      onPress={() => setSelectedKey(key.id)}
                    >
                      <View
                        style={[
                          styles.keyOptionRadio,
                          selectedKey === key.id && styles.keyOptionRadioActive,
                        ]}
                      >
                        {selectedKey === key.id && (
                          <MaterialCommunityIcons name="check" size={16} color="white" />
                        )}
                      </View>
                      <View style={styles.keyOptionContent}>
                        <Text style={styles.keyOptionName}>{key.name}</Text>
                        <Text style={styles.keyOptionBlockchain}>{key.blockchain}</Text>
                      </View>
                    </TouchableOpacity>
                  ))}
                </View>
              )}

              <Text style={[styles.label, { marginTop: 24 }]}>Transaction Data</Text>
              <TextInput
                style={[styles.input, styles.transactionInput]}
                placeholder="Paste transaction hex or JSON"
                placeholderTextColor="#6b7280"
                value={transaction}
                onChangeText={setTransaction}
                multiline
                numberOfLines={6}
                textAlignVertical="top"
              />

              <Text style={styles.helperText}>
                Enter the transaction data to sign. This can be a hex-encoded transaction or JSON format.
              </Text>
            </ScrollView>

            <View style={styles.modalFooter}>
              <TouchableOpacity
                style={styles.cancelBtn}
                onPress={() => setShowSignModal(false)}
              >
                <Text style={styles.cancelBtnText}>Cancel</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={[styles.signBtn, activeKeys.length === 0 && styles.signBtnDisabled]}
                onPress={handleSign}
                disabled={activeKeys.length === 0}
              >
                <MaterialCommunityIcons name="pencil-box-outline" size={16} color="white" />
                <Text style={styles.signBtnText}>Sign</Text>
              </TouchableOpacity>
            </View>
          </View>
        </View>
      </Modal>
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
  title: {
    fontSize: 28,
    fontWeight: 'bold',
    color: 'white',
  },
  signButton: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: '#3b82f6',
    justifyContent: 'center',
    alignItems: 'center',
  },
  filterTabs: {
    marginBottom: 16,
    marginHorizontal: -16,
    paddingHorizontal: 16,
  },
  filterTab: {
    marginRight: 8,
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 6,
    backgroundColor: '#1e293b',
    borderWidth: 1,
    borderColor: '#334155',
  },
  filterTabActive: {
    backgroundColor: '#3b82f6',
    borderColor: '#3b82f6',
  },
  filterTabText: {
    fontSize: 12,
    fontWeight: '500',
    color: '#9ca3af',
  },
  filterTabTextActive: {
    color: 'white',
  },
  loadingText: {
    color: '#9ca3af',
    textAlign: 'center',
    marginVertical: 32,
  },
  emptyState: {
    alignItems: 'center',
    paddingVertical: 48,
  },
  emptyStateText: {
    fontSize: 18,
    fontWeight: 'bold',
    color: 'white',
    marginTop: 16,
  },
  emptyStateSubtext: {
    color: '#9ca3af',
    marginTop: 8,
  },
  signingsList: {
    marginBottom: 24,
  },
  signingCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    marginBottom: 12,
    borderWidth: 1,
    borderColor: '#334155',
  },
  signingHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 12,
  },
  signingInfo: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    flex: 1,
  },
  statusIcon: {
    width: 40,
    height: 40,
    borderRadius: 8,
    justifyContent: 'center',
    alignItems: 'center',
  },
  signingDetails: {
    flex: 1,
  },
  signingId: {
    fontSize: 14,
    fontWeight: '600',
    color: 'white',
  },
  signingStatus: {
    fontSize: 12,
    fontWeight: '500',
    marginTop: 2,
  },
  blockchain: {
    fontSize: 12,
    fontWeight: '600',
    color: '#9ca3af',
    backgroundColor: '#334155',
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 4,
  },
  txHashContainer: {
    backgroundColor: '#0f172a',
    borderRadius: 8,
    padding: 10,
    marginBottom: 12,
  },
  txHashLabel: {
    fontSize: 11,
    color: '#6b7280',
    marginBottom: 4,
  },
  txHash: {
    fontSize: 12,
    fontFamily: 'Courier New',
    color: '#3b82f6',
  },
  signingFooter: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingTop: 12,
    borderTopWidth: 1,
    borderTopColor: '#334155',
  },
  timestamp: {
    fontSize: 11,
    color: '#6b7280',
  },
  completedTime: {
    fontSize: 11,
    color: '#10b981',
  },
  modalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0, 0, 0, 0.7)',
    justifyContent: 'flex-end',
  },
  modalContent: {
    backgroundColor: '#0f172a',
    borderTopLeftRadius: 20,
    borderTopRightRadius: 20,
    maxHeight: '90%',
  },
  modalHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingBottom: 16,
    borderBottomWidth: 1,
    borderBottomColor: '#334155',
  },
  modalTitle: {
    fontSize: 20,
    fontWeight: 'bold',
    color: 'white',
  },
  modalBody: {
    paddingHorizontal: 16,
    paddingVertical: 24,
  },
  label: {
    fontSize: 14,
    fontWeight: '600',
    color: 'white',
    marginBottom: 12,
  },
  noKeysText: {
    fontSize: 14,
    color: '#ef4444',
    paddingVertical: 12,
  },
  keysList: {
    marginBottom: 16,
  },
  keyOption: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 12,
    paddingVertical: 12,
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 8,
    marginBottom: 8,
  },
  keyOptionActive: {
    backgroundColor: '#3b82f620',
    borderColor: '#3b82f6',
  },
  keyOptionRadio: {
    width: 20,
    height: 20,
    borderRadius: 10,
    borderWidth: 2,
    borderColor: '#334155',
    marginRight: 12,
    justifyContent: 'center',
    alignItems: 'center',
  },
  keyOptionRadioActive: {
    backgroundColor: '#3b82f6',
    borderColor: '#3b82f6',
  },
  keyOptionContent: {
    flex: 1,
  },
  keyOptionName: {
    fontSize: 14,
    fontWeight: '600',
    color: 'white',
  },
  keyOptionBlockchain: {
    fontSize: 12,
    color: '#9ca3af',
    marginTop: 2,
  },
  input: {
    backgroundColor: '#1e293b',
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 12,
    color: 'white',
    fontSize: 13,
  },
  transactionInput: {
    minHeight: 120,
    fontFamily: 'Courier New',
  },
  helperText: {
    fontSize: 12,
    color: '#6b7280',
    marginTop: 8,
  },
  modalFooter: {
    flexDirection: 'row',
    gap: 12,
    paddingHorizontal: 16,
    paddingBottom: 24,
  },
  cancelBtn: {
    flex: 1,
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 8,
    paddingVertical: 12,
    alignItems: 'center',
  },
  cancelBtnText: {
    color: '#9ca3af',
    fontWeight: '600',
    fontSize: 14,
  },
  signBtn: {
    flex: 1,
    backgroundColor: '#3b82f6',
    borderRadius: 8,
    paddingVertical: 12,
    alignItems: 'center',
    justifyContent: 'center',
    flexDirection: 'row',
    gap: 6,
  },
  signBtnDisabled: {
    backgroundColor: '#6b7280',
    opacity: 0.5,
  },
  signBtnText: {
    color: 'white',
    fontWeight: '600',
    fontSize: 14,
  },
});
