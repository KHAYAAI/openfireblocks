import React, { useEffect, useState } from 'react';
import { View, Text, ScrollView, StyleSheet, TouchableOpacity, Modal, TextInput } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { MaterialCommunityIcons } from '@expo/vector-icons';
import { api } from '../lib/api';

interface KeyPair {
  id: string;
  name: string;
  blockchain: string;
  threshold: number;
  total_parties: number;
  status: 'active' | 'pending' | 'inactive';
  created_at: string;
}

export function KeysScreen() {
  const insets = useSafeAreaInsets();
  const [keys, setKeys] = useState<KeyPair[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [selectedBlockchain, setSelectedBlockchain] = useState('ethereum');
  const [threshold, setThreshold] = useState('2');
  const [totalParties, setTotalParties] = useState('3');

  useEffect(() => {
    fetchKeys();
  }, []);

  const fetchKeys = async () => {
    try {
      const res = await api.getKeys();
      setKeys(res.data || []);
    } catch (error) {
      console.error('Failed to fetch keys:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleCreateKey = async () => {
    if (!newKeyName.trim()) {
      alert('Key name is required');
      return;
    }

    try {
      await api.createKey(
        newKeyName,
        selectedBlockchain,
        parseInt(threshold),
        parseInt(totalParties)
      );
      setNewKeyName('');
      setThreshold('2');
      setTotalParties('3');
      setShowCreateModal(false);
      fetchKeys();
    } catch (error) {
      console.error('Failed to create key:', error);
      alert('Failed to create key');
    }
  };

  const blockchains = [
    { label: 'Bitcoin', value: 'bitcoin' },
    { label: 'Ethereum', value: 'ethereum' },
    { label: 'Solana', value: 'solana' },
    { label: 'Polygon', value: 'polygon' },
    { label: 'Cosmos', value: 'cosmos' },
  ];

  return (
    <ScrollView
      style={[styles.container, { paddingTop: insets.top }]}
      scrollEventThrottle={16}
    >
      <View style={styles.header}>
        <Text style={styles.title}>Your Keys</Text>
        <TouchableOpacity
          style={styles.addButton}
          onPress={() => setShowCreateModal(true)}
        >
          <MaterialCommunityIcons name="plus-circle" size={24} color="white" />
        </TouchableOpacity>
      </View>

      {loading ? (
        <Text style={styles.loadingText}>Loading keys...</Text>
      ) : keys.length === 0 ? (
        <View style={styles.emptyState}>
          <MaterialCommunityIcons name="key-plus" size={48} color="#9ca3af" />
          <Text style={styles.emptyStateText}>No keys yet</Text>
          <Text style={styles.emptyStateSubtext}>Create your first key to get started</Text>
        </View>
      ) : (
        <View style={styles.keysList}>
          {keys.map((key) => (
            <TouchableOpacity key={key.id} style={styles.keyCard}>
              <View style={styles.keyHeader}>
                <View>
                  <Text style={styles.keyName}>{key.name}</Text>
                  <Text style={styles.keyBlockchain}>{key.blockchain.toUpperCase()}</Text>
                </View>
                <View
                  style={[
                    styles.statusBadge,
                    {
                      backgroundColor:
                        key.status === 'active'
                          ? '#10b98120'
                          : key.status === 'pending'
                          ? '#f59e0b20'
                          : '#6b728020',
                    },
                  ]}
                >
                  <Text
                    style={[
                      styles.statusText,
                      {
                        color:
                          key.status === 'active'
                            ? '#10b981'
                            : key.status === 'pending'
                            ? '#f59e0b'
                            : '#6b7280',
                      },
                    ]}
                  >
                    {key.status.charAt(0).toUpperCase() + key.status.slice(1)}
                  </Text>
                </View>
              </View>

              <View style={styles.keyDetails}>
                <View style={styles.detailItem}>
                  <Text style={styles.detailLabel}>Threshold</Text>
                  <Text style={styles.detailValue}>
                    {key.threshold}-of-{key.total_parties}
                  </Text>
                </View>
                <View style={styles.detailItem}>
                  <Text style={styles.detailLabel}>Created</Text>
                  <Text style={styles.detailValue}>
                    {new Date(key.created_at).toLocaleDateString()}
                  </Text>
                </View>
              </View>

              <View style={styles.keyActions}>
                <TouchableOpacity style={styles.actionBtn}>
                  <MaterialCommunityIcons name="signature-check" size={16} color="#3b82f6" />
                  <Text style={styles.actionBtnText}>Sign</Text>
                </TouchableOpacity>
                <TouchableOpacity style={[styles.actionBtn, styles.secondaryBtn]}>
                  <MaterialCommunityIcons name="information" size={16} color="#9ca3af" />
                  <Text style={[styles.actionBtnText, { color: '#9ca3af' }]}>Details</Text>
                </TouchableOpacity>
              </View>
            </TouchableOpacity>
          ))}
        </View>
      )}

      <Modal
        visible={showCreateModal}
        animationType="slide"
        transparent={true}
        onRequestClose={() => setShowCreateModal(false)}
      >
        <View style={styles.modalOverlay}>
          <View style={[styles.modalContent, { paddingTop: insets.top + 20 }]}>
            <View style={styles.modalHeader}>
              <Text style={styles.modalTitle}>Create New Key</Text>
              <TouchableOpacity onPress={() => setShowCreateModal(false)}>
                <MaterialCommunityIcons name="close" size={24} color="white" />
              </TouchableOpacity>
            </View>

            <ScrollView style={styles.modalBody}>
              <Text style={styles.label}>Key Name</Text>
              <TextInput
                style={styles.input}
                placeholder="e.g., Main Wallet"
                placeholderTextColor="#6b7280"
                value={newKeyName}
                onChangeText={setNewKeyName}
              />

              <Text style={styles.label}>Blockchain</Text>
              <View style={styles.blockchainGrid}>
                {blockchains.map((chain) => (
                  <TouchableOpacity
                    key={chain.value}
                    style={[
                      styles.blockchainOption,
                      selectedBlockchain === chain.value && styles.blockchainOptionActive,
                    ]}
                    onPress={() => setSelectedBlockchain(chain.value)}
                  >
                    <Text
                      style={[
                        styles.blockchainOptionText,
                        selectedBlockchain === chain.value && styles.blockchainOptionTextActive,
                      ]}
                    >
                      {chain.label}
                    </Text>
                  </TouchableOpacity>
                ))}
              </View>

              <Text style={styles.label}>Threshold: {threshold}-of-{totalParties}</Text>
              <View style={styles.sliderContainer}>
                <Text style={styles.sliderLabel}>Threshold ({threshold})</Text>
                <View style={styles.sliderInputs}>
                  {[1, 2, 3, 4, 5, 6, 7].map((val) => (
                    <TouchableOpacity
                      key={`threshold-${val}`}
                      style={[
                        styles.sliderButton,
                        parseInt(threshold) === val && styles.sliderButtonActive,
                      ]}
                      onPress={() => setThreshold(val.toString())}
                    >
                      <Text
                        style={[
                          styles.sliderButtonText,
                          parseInt(threshold) === val && styles.sliderButtonTextActive,
                        ]}
                      >
                        {val}
                      </Text>
                    </TouchableOpacity>
                  ))}
                </View>
              </View>

              <View style={styles.sliderContainer}>
                <Text style={styles.sliderLabel}>Total Parties ({totalParties})</Text>
                <View style={styles.sliderInputs}>
                  {[2, 3, 4, 5, 6, 7].map((val) => (
                    <TouchableOpacity
                      key={`parties-${val}`}
                      style={[
                        styles.sliderButton,
                        parseInt(totalParties) === val && styles.sliderButtonActive,
                      ]}
                      onPress={() => setTotalParties(val.toString())}
                    >
                      <Text
                        style={[
                          styles.sliderButtonText,
                          parseInt(totalParties) === val && styles.sliderButtonTextActive,
                        ]}
                      >
                        {val}
                      </Text>
                    </TouchableOpacity>
                  ))}
                </View>
              </View>
            </ScrollView>

            <View style={styles.modalFooter}>
              <TouchableOpacity
                style={styles.cancelBtn}
                onPress={() => setShowCreateModal(false)}
              >
                <Text style={styles.cancelBtnText}>Cancel</Text>
              </TouchableOpacity>
              <TouchableOpacity style={styles.createBtn} onPress={handleCreateKey}>
                <Text style={styles.createBtnText}>Create Key</Text>
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
  addButton: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: '#3b82f6',
    justifyContent: 'center',
    alignItems: 'center',
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
  keysList: {
    marginBottom: 24,
  },
  keyCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    marginBottom: 12,
    borderWidth: 1,
    borderColor: '#334155',
  },
  keyHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'flex-start',
    marginBottom: 12,
  },
  keyName: {
    fontSize: 16,
    fontWeight: '600',
    color: 'white',
  },
  keyBlockchain: {
    fontSize: 12,
    color: '#9ca3af',
    marginTop: 4,
  },
  statusBadge: {
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 6,
  },
  statusText: {
    fontSize: 12,
    fontWeight: '500',
  },
  keyDetails: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginBottom: 12,
    paddingBottom: 12,
    borderBottomWidth: 1,
    borderBottomColor: '#334155',
  },
  detailItem: {
    flex: 1,
  },
  detailLabel: {
    fontSize: 11,
    color: '#6b7280',
    marginBottom: 4,
  },
  detailValue: {
    fontSize: 14,
    fontWeight: '600',
    color: 'white',
  },
  keyActions: {
    flexDirection: 'row',
    gap: 8,
  },
  actionBtn: {
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#334155',
    borderRadius: 8,
    paddingVertical: 8,
    gap: 4,
  },
  secondaryBtn: {
    backgroundColor: 'transparent',
    borderWidth: 1,
    borderColor: '#334155',
  },
  actionBtnText: {
    color: '#3b82f6',
    fontWeight: '500',
    fontSize: 12,
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
    marginBottom: 8,
  },
  input: {
    backgroundColor: '#1e293b',
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 12,
    color: 'white',
    marginBottom: 24,
    fontSize: 14,
  },
  blockchainGrid: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 8,
    marginBottom: 24,
  },
  blockchainOption: {
    flex: 1,
    minWidth: '45%',
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 8,
    paddingVertical: 12,
    alignItems: 'center',
  },
  blockchainOptionActive: {
    backgroundColor: '#3b82f6',
    borderColor: '#3b82f6',
  },
  blockchainOptionText: {
    fontSize: 13,
    fontWeight: '500',
    color: '#9ca3af',
  },
  blockchainOptionTextActive: {
    color: 'white',
  },
  sliderContainer: {
    marginBottom: 24,
  },
  sliderLabel: {
    fontSize: 12,
    color: '#6b7280',
    marginBottom: 8,
  },
  sliderInputs: {
    flexDirection: 'row',
    gap: 6,
  },
  sliderButton: {
    flex: 1,
    borderWidth: 1,
    borderColor: '#334155',
    borderRadius: 6,
    paddingVertical: 8,
    alignItems: 'center',
  },
  sliderButtonActive: {
    backgroundColor: '#3b82f6',
    borderColor: '#3b82f6',
  },
  sliderButtonText: {
    fontSize: 12,
    fontWeight: '500',
    color: '#9ca3af',
  },
  sliderButtonTextActive: {
    color: 'white',
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
  createBtn: {
    flex: 1,
    backgroundColor: '#3b82f6',
    borderRadius: 8,
    paddingVertical: 12,
    alignItems: 'center',
  },
  createBtnText: {
    color: 'white',
    fontWeight: '600',
    fontSize: 14,
  },
});
