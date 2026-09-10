import React, { useState, useEffect } from 'react';
import { View, Text, ScrollView, StyleSheet, TouchableOpacity, Switch, Alert, TextInput } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { MaterialCommunityIcons } from '@expo/vector-icons';
import { useAuthStore } from '../store/auth';
import * as SecureStore from 'expo-secure-store';

export function SettingsScreen() {
  const insets = useSafeAreaInsets();
  const { user, logout } = useAuthStore();
  const [biometricsEnabled, setBiometricsEnabled] = useState(false);
  const [notificationsEnabled, setNotificationsEnabled] = useState(true);
  const [autoSignEnabled, setAutoSignEnabled] = useState(false);
  const [spendingLimit, setSpendingLimit] = useState('10000');
  const [showApiKey, setShowApiKey] = useState(false);
  const [apiKey, setApiKey] = useState('');

  useEffect(() => {
    loadSettings();
  }, []);

  const loadSettings = async () => {
    try {
      const [bio, notif, autoSign, limit, key] = await Promise.all([
        SecureStore.getItemAsync('biometrics_enabled'),
        SecureStore.getItemAsync('notifications_enabled'),
        SecureStore.getItemAsync('auto_sign_enabled'),
        SecureStore.getItemAsync('spending_limit'),
        SecureStore.getItemAsync('api_key'),
      ]);

      setBiometricsEnabled(bio === 'true');
      setNotificationsEnabled(notif !== 'false');
      setAutoSignEnabled(autoSign === 'true');
      setSpendingLimit(limit || '10000');
      setApiKey(key || '');
    } catch (error) {
      console.error('Failed to load settings:', error);
    }
  };

  const handleBiometricsToggle = async (value: boolean) => {
    try {
      await SecureStore.setItemAsync('biometrics_enabled', value.toString());
      setBiometricsEnabled(value);
    } catch (error) {
      console.error('Failed to save biometrics setting:', error);
    }
  };

  const handleNotificationsToggle = async (value: boolean) => {
    try {
      await SecureStore.setItemAsync('notifications_enabled', value.toString());
      setNotificationsEnabled(value);
    } catch (error) {
      console.error('Failed to save notifications setting:', error);
    }
  };

  const handleAutoSignToggle = async (value: boolean) => {
    try {
      await SecureStore.setItemAsync('auto_sign_enabled', value.toString());
      setAutoSignEnabled(value);
    } catch (error) {
      console.error('Failed to save auto-sign setting:', error);
    }
  };

  const handleSpendingLimitChange = async (value: string) => {
    setSpendingLimit(value);
    try {
      await SecureStore.setItemAsync('spending_limit', value);
    } catch (error) {
      console.error('Failed to save spending limit:', error);
    }
  };

  const handleLogout = () => {
    Alert.alert(
      'Logout',
      'Are you sure you want to logout?',
      [
        { text: 'Cancel', onPress: () => {}, style: 'cancel' },
        {
          text: 'Logout',
          onPress: async () => {
            await logout();
          },
          style: 'destructive',
        },
      ]
    );
  };

  const copyToClipboard = (text: string) => {
    // In a real app, use react-native-clipboard
    Alert.alert('Copied', 'API Key copied to clipboard');
  };

  return (
    <ScrollView
      style={[styles.container, { paddingTop: insets.top }]}
      scrollEventThrottle={16}
    >
      {/* Header */}
      <View style={styles.header}>
        <Text style={styles.title}>Settings</Text>
      </View>

      {/* Account Section */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Account</Text>

        <View style={styles.accountCard}>
          <View style={styles.accountInfo}>
            <View style={styles.avatar}>
              <MaterialCommunityIcons name="account-circle" size={40} color="#3b82f6" />
            </View>
            <View style={styles.accountDetails}>
              <Text style={styles.accountName}>{user?.name || 'User'}</Text>
              <Text style={styles.accountEmail}>{user?.email || 'email@example.com'}</Text>
            </View>
          </View>
          <TouchableOpacity style={styles.editButton}>
            <MaterialCommunityIcons name="pencil" size={18} color="#3b82f6" />
          </TouchableOpacity>
        </View>

        {/* API Key */}
        <View style={styles.settingItem}>
          <Text style={styles.settingLabel}>API Key</Text>
          <TouchableOpacity
            style={styles.apiKeyButton}
            onPress={() => setShowApiKey(!showApiKey)}
          >
            <Text style={styles.apiKeyText}>
              {showApiKey ? apiKey : '••••••••' + apiKey.slice(-8)}
            </Text>
            <MaterialCommunityIcons
              name={showApiKey ? 'eye-off' : 'eye'}
              size={16}
              color="#3b82f6"
            />
          </TouchableOpacity>
          <TouchableOpacity
            style={styles.copyButton}
            onPress={() => copyToClipboard(apiKey)}
          >
            <MaterialCommunityIcons name="content-copy" size={16} color="#9ca3af" />
            <Text style={styles.copyButtonText}>Copy</Text>
          </TouchableOpacity>
        </View>
      </View>

      {/* Security Section */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Security</Text>

        <View style={styles.settingItem}>
          <View style={styles.settingLabelRow}>
            <MaterialCommunityIcons name="fingerprint" size={20} color="#3b82f6" />
            <View style={styles.settingLabelContent}>
              <Text style={styles.settingLabel}>Biometric Authentication</Text>
              <Text style={styles.settingDescription}>Use fingerprint or face ID</Text>
            </View>
          </View>
          <Switch
            value={biometricsEnabled}
            onValueChange={handleBiometricsToggle}
            trackColor={{ false: '#334155', true: '#3b82f680' }}
            thumbColor={biometricsEnabled ? '#3b82f6' : '#6b7280'}
          />
        </View>

        <View style={styles.divider} />

        <View style={styles.settingItem}>
          <View style={styles.settingLabelRow}>
            <MaterialCommunityIcons name="shield-check" size={20} color="#10b981" />
            <View style={styles.settingLabelContent}>
              <Text style={styles.settingLabel}>Auto-Signature</Text>
              <Text style={styles.settingDescription}>Auto-approve low-value transactions</Text>
            </View>
          </View>
          <Switch
            value={autoSignEnabled}
            onValueChange={handleAutoSignToggle}
            trackColor={{ false: '#334155', true: '#3b82f680' }}
            thumbColor={autoSignEnabled ? '#3b82f6' : '#6b7280'}
          />
        </View>

        <View style={styles.divider} />

        <View style={styles.settingItem}>
          <View style={styles.settingLabelRow}>
            <MaterialCommunityIcons name="cash" size={20} color="#f59e0b" />
            <View style={styles.settingLabelContent}>
              <Text style={styles.settingLabel}>Daily Spending Limit</Text>
              <Text style={styles.settingDescription}>Max auto-sign amount per day</Text>
            </View>
          </View>
          <View style={styles.spendingLimitInput}>
            <Text style={styles.currencySymbol}>$</Text>
            <TextInput
              style={styles.limitInput}
              value={spendingLimit}
              onChangeText={handleSpendingLimitChange}
              keyboardType="decimal-pad"
              placeholder="10000"
              placeholderTextColor="#6b7280"
            />
          </View>
        </View>
      </View>

      {/* Notifications Section */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Notifications</Text>

        <View style={styles.settingItem}>
          <View style={styles.settingLabelRow}>
            <MaterialCommunityIcons name="bell" size={20} color="#3b82f6" />
            <View style={styles.settingLabelContent}>
              <Text style={styles.settingLabel}>Push Notifications</Text>
              <Text style={styles.settingDescription}>Get alerts for signings and updates</Text>
            </View>
          </View>
          <Switch
            value={notificationsEnabled}
            onValueChange={handleNotificationsToggle}
            trackColor={{ false: '#334155', true: '#3b82f680' }}
            thumbColor={notificationsEnabled ? '#3b82f6' : '#6b7280'}
          />
        </View>

        <View style={styles.divider} />

        <TouchableOpacity style={styles.settingItem}>
          <View style={styles.settingLabelRow}>
            <MaterialCommunityIcons name="history" size={20} color="#9ca3af" />
            <View style={styles.settingLabelContent}>
              <Text style={styles.settingLabel}>Notification History</Text>
              <Text style={styles.settingDescription}>View past notifications</Text>
            </View>
          </View>
          <MaterialCommunityIcons name="chevron-right" size={20} color="#6b7280" />
        </TouchableOpacity>
      </View>

      {/* About Section */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>About</Text>

        <TouchableOpacity style={styles.infoItem}>
          <Text style={styles.infoLabel}>App Version</Text>
          <Text style={styles.infoValue}>1.0.0</Text>
        </TouchableOpacity>

        <TouchableOpacity style={styles.infoItem}>
          <Text style={styles.infoLabel}>Terms of Service</Text>
          <MaterialCommunityIcons name="chevron-right" size={20} color="#6b7280" />
        </TouchableOpacity>

        <TouchableOpacity style={styles.infoItem}>
          <Text style={styles.infoLabel}>Privacy Policy</Text>
          <MaterialCommunityIcons name="chevron-right" size={20} color="#6b7280" />
        </TouchableOpacity>

        <TouchableOpacity style={styles.infoItem}>
          <Text style={styles.infoLabel}>Support</Text>
          <MaterialCommunityIcons name="chevron-right" size={20} color="#6b7280" />
        </TouchableOpacity>
      </View>

      {/* Logout Button */}
      <TouchableOpacity
        style={styles.logoutButton}
        onPress={handleLogout}
      >
        <MaterialCommunityIcons name="logout" size={18} color="#ef4444" />
        <Text style={styles.logoutText}>Logout</Text>
      </TouchableOpacity>

      <View style={styles.footer} />
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
    marginVertical: 24,
  },
  title: {
    fontSize: 28,
    fontWeight: 'bold',
    color: 'white',
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
  accountCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#334155',
    marginBottom: 12,
  },
  accountInfo: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    flex: 1,
  },
  avatar: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: '#334155',
    justifyContent: 'center',
    alignItems: 'center',
  },
  accountDetails: {
    flex: 1,
  },
  accountName: {
    fontSize: 16,
    fontWeight: '600',
    color: 'white',
  },
  accountEmail: {
    fontSize: 12,
    color: '#9ca3af',
    marginTop: 4,
  },
  editButton: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: '#334155',
    justifyContent: 'center',
    alignItems: 'center',
  },
  settingItem: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    marginBottom: 8,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#334155',
  },
  settingLabelRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    flex: 1,
  },
  settingLabelContent: {
    flex: 1,
  },
  settingLabel: {
    fontSize: 14,
    fontWeight: '600',
    color: 'white',
  },
  settingDescription: {
    fontSize: 12,
    color: '#6b7280',
    marginTop: 4,
  },
  apiKeyButton: {
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    backgroundColor: '#0f172a',
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    marginRight: 8,
  },
  apiKeyText: {
    fontSize: 12,
    fontFamily: 'Courier New',
    color: '#3b82f6',
  },
  copyButton: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 12,
    paddingVertical: 8,
    backgroundColor: '#334155',
    borderRadius: 8,
    gap: 4,
  },
  copyButtonText: {
    fontSize: 12,
    color: '#9ca3af',
    fontWeight: '500',
  },
  spendingLimitInput: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#0f172a',
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  currencySymbol: {
    fontSize: 14,
    fontWeight: '600',
    color: '#3b82f6',
    marginRight: 4,
  },
  limitInput: {
    flex: 1,
    fontSize: 14,
    color: 'white',
    paddingVertical: 4,
  },
  divider: {
    height: 1,
    backgroundColor: '#334155',
    marginHorizontal: 16,
    marginBottom: 8,
  },
  infoItem: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    marginBottom: 8,
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#334155',
  },
  infoLabel: {
    fontSize: 14,
    fontWeight: '600',
    color: 'white',
  },
  infoValue: {
    fontSize: 12,
    color: '#6b7280',
    fontWeight: '500',
  },
  logoutButton: {
    backgroundColor: '#ef444420',
    borderRadius: 12,
    padding: 16,
    marginVertical: 24,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 8,
    borderWidth: 1,
    borderColor: '#ef4444',
  },
  logoutText: {
    fontSize: 16,
    fontWeight: '600',
    color: '#ef4444',
  },
  footer: {
    height: 24,
  },
});
