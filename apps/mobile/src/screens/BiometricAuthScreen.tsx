import React, { useEffect, useState } from 'react';
import { View, Text, StyleSheet, TouchableOpacity, Alert, ActivityIndicator } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { MaterialCommunityIcons } from '@expo/vector-icons';
import * as SecureStore from 'expo-secure-store';
import * as LocalAuthentication from 'expo-local-authentication';
import { useAuthStore } from '../store/auth';

interface BiometricStatus {
  available: boolean;
  supportedTypes: string[];
  isEnabled: boolean;
}

export function BiometricAuthScreen() {
  const insets = useSafeAreaInsets();
  const { user, logout } = useAuthStore();
  const [biometricStatus, setBiometricStatus] = useState<BiometricStatus>({
    available: false,
    supportedTypes: [],
    isEnabled: false,
  });
  const [loading, setLoading] = useState(true);
  const [authenticating, setAuthenticating] = useState(false);

  useEffect(() => {
    checkBiometricAvailability();
  }, []);

  const checkBiometricAvailability = async () => {
    try {
      const compatible = await LocalAuthentication.hasHardwareAsync();
      if (!compatible) {
        setBiometricStatus({
          available: false,
          supportedTypes: [],
          isEnabled: false,
        });
        setLoading(false);
        return;
      }

      const supportedAuthentication = await LocalAuthentication.supportedAuthenticationTypesAsync();
      const isEnabled = await SecureStore.getItemAsync('biometrics_enabled') === 'true';

      const typeLabels = supportedAuthentication.map((type) => {
        switch (type) {
          case LocalAuthentication.AuthenticationType.FACIAL_RECOGNITION:
            return 'Face ID';
          case LocalAuthentication.AuthenticationType.FINGERPRINT:
            return 'Fingerprint';
          case LocalAuthentication.AuthenticationType.IRIS:
            return 'Iris';
          default:
            return 'Unknown';
        }
      });

      setBiometricStatus({
        available: compatible,
        supportedTypes: typeLabels,
        isEnabled,
      });
    } catch (error) {
      console.error('Failed to check biometric availability:', error);
    } finally {
      setLoading(false);
    }
  };

  const handleEnableBiometrics = async () => {
    try {
      setAuthenticating(true);

      // First, authenticate to confirm
      const result = await LocalAuthentication.authenticateAsync({
        reason: 'Enable biometric authentication',
        fallbackLabel: 'Use passcode',
        disableDeviceFallback: false,
      });

      if (result.success) {
        // Save biometric preference
        await SecureStore.setItemAsync('biometrics_enabled', 'true');
        setBiometricStatus((prev) => ({ ...prev, isEnabled: true }));
        Alert.alert(
          'Success',
          `${biometricStatus.supportedTypes[0]} authentication has been enabled.`
        );
      } else if (result.error === 'user_cancel') {
        // User cancelled
      } else {
        Alert.alert('Authentication Failed', 'Please try again.');
      }
    } catch (error) {
      console.error('Failed to enable biometrics:', error);
      Alert.alert('Error', 'Failed to enable biometric authentication.');
    } finally {
      setAuthenticating(false);
    }
  };

  const handleDisableBiometrics = async () => {
    try {
      await SecureStore.setItemAsync('biometrics_enabled', 'false');
      setBiometricStatus((prev) => ({ ...prev, isEnabled: false }));
      Alert.alert('Success', 'Biometric authentication has been disabled.');
    } catch (error) {
      console.error('Failed to disable biometrics:', error);
      Alert.alert('Error', 'Failed to disable biometric authentication.');
    }
  };

  const handleTestBiometrics = async () => {
    try {
      setAuthenticating(true);

      const result = await LocalAuthentication.authenticateAsync({
        reason: 'Test your biometric authentication',
        fallbackLabel: 'Use passcode',
        disableDeviceFallback: false,
      });

      if (result.success) {
        Alert.alert('Success', 'Biometric authentication works!');
      } else {
        Alert.alert('Failed', 'Biometric authentication failed.');
      }
    } catch (error) {
      console.error('Failed to test biometrics:', error);
      Alert.alert('Error', 'Failed to test biometric authentication.');
    } finally {
      setAuthenticating(false);
    }
  };

  const getBiometricIcon = () => {
    const type = biometricStatus.supportedTypes[0];
    if (type === 'Face ID') {
      return 'face-recognition';
    } else if (type === 'Fingerprint') {
      return 'fingerprint';
    }
    return 'lock';
  };

  if (loading) {
    return (
      <View style={[styles.container, { paddingTop: insets.top }]}>
        <ActivityIndicator size="large" color="#3b82f6" />
      </View>
    );
  }

  return (
    <View style={[styles.container, { paddingTop: insets.top }]}>
      <View style={styles.header}>
        <Text style={styles.title}>Biometric Authentication</Text>
        <Text style={styles.subtitle}>Secure your account with your fingerprint or face</Text>
      </View>

      {biometricStatus.available ? (
        <>
          {/* Status Card */}
          <View style={styles.statusCard}>
            <View
              style={[
                styles.iconContainer,
                {
                  backgroundColor: biometricStatus.isEnabled
                    ? '#10b98120'
                    : '#f59e0b20',
                },
              ]}
            >
              <MaterialCommunityIcons
                name={getBiometricIcon()}
                size={40}
                color={biometricStatus.isEnabled ? '#10b981' : '#f59e0b'}
              />
            </View>

            <View style={styles.statusContent}>
              <Text style={styles.statusTitle}>
                {biometricStatus.supportedTypes[0] || 'Biometric'} Available
              </Text>
              <Text style={styles.statusSubtitle}>
                {biometricStatus.isEnabled ? 'Enabled' : 'Disabled'}
              </Text>
            </View>

            <View
              style={[
                styles.statusBadge,
                {
                  backgroundColor: biometricStatus.isEnabled
                    ? '#10b98120'
                    : '#f59e0b20',
                },
              ]}
            >
              <Text
                style={{
                  color: biometricStatus.isEnabled ? '#10b981' : '#f59e0b',
                  fontWeight: '600',
                }}
              >
                {biometricStatus.isEnabled ? 'ON' : 'OFF'}
              </Text>
            </View>
          </View>

          {/* Supported Types */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Supported Methods</Text>
            {biometricStatus.supportedTypes.map((type) => (
              <View key={type} style={styles.methodItem}>
                <MaterialCommunityIcons
                  name={type === 'Face ID' ? 'face-recognition' : 'fingerprint'}
                  size={20}
                  color="#3b82f6"
                />
                <Text style={styles.methodText}>{type}</Text>
                <MaterialCommunityIcons name="check-circle" size={20} color="#10b981" />
              </View>
            ))}
          </View>

          {/* Benefits */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Benefits</Text>
            <View style={styles.benefitItem}>
              <MaterialCommunityIcons name="shield-check" size={18} color="#10b981" />
              <Text style={styles.benefitText}>Fast and secure authentication</Text>
            </View>
            <View style={styles.benefitItem}>
              <MaterialCommunityIcons name="clock-fast" size={18} color="#10b981" />
              <Text style={styles.benefitText}>Skip password entry every time</Text>
            </View>
            <View style={styles.benefitItem}>
              <MaterialCommunityIcons name="lock-check" size={18} color="#10b981" />
              <Text style={styles.benefitText}>Only you can unlock this device</Text>
            </View>
          </View>

          {/* Actions */}
          <View style={styles.actions}>
            {!biometricStatus.isEnabled ? (
              <TouchableOpacity
                style={styles.primaryButton}
                onPress={handleEnableBiometrics}
                disabled={authenticating}
              >
                {authenticating ? (
                  <ActivityIndicator color="white" />
                ) : (
                  <>
                    <MaterialCommunityIcons name="check-circle" size={20} color="white" />
                    <Text style={styles.primaryButtonText}>Enable {biometricStatus.supportedTypes[0]}</Text>
                  </>
                )}
              </TouchableOpacity>
            ) : (
              <>
                <TouchableOpacity
                  style={styles.secondaryButton}
                  onPress={handleTestBiometrics}
                  disabled={authenticating}
                >
                  {authenticating ? (
                    <ActivityIndicator color="#3b82f6" />
                  ) : (
                    <>
                      <MaterialCommunityIcons name="test-tube" size={20} color="#3b82f6" />
                      <Text style={styles.secondaryButtonText}>Test Authentication</Text>
                    </>
                  )}
                </TouchableOpacity>

                <TouchableOpacity
                  style={styles.dangerButton}
                  onPress={handleDisableBiometrics}
                >
                  <MaterialCommunityIcons name="lock-open" size={20} color="#ef4444" />
                  <Text style={styles.dangerButtonText}>Disable</Text>
                </TouchableOpacity>
              </>
            )}
          </View>
        </>
      ) : (
        <View style={styles.unavailableContainer}>
          <MaterialCommunityIcons name="lock-alert" size={48} color="#f59e0b" />
          <Text style={styles.unavailableTitle}>Not Available</Text>
          <Text style={styles.unavailableText}>
            Your device doesn't support biometric authentication.
          </Text>
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#0f172a',
    paddingHorizontal: 16,
    paddingVertical: 16,
  },
  header: {
    marginBottom: 24,
  },
  title: {
    fontSize: 28,
    fontWeight: 'bold',
    color: 'white',
  },
  subtitle: {
    fontSize: 14,
    color: '#9ca3af',
    marginTop: 8,
  },
  statusCard: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
    borderColor: '#334155',
    marginBottom: 24,
    flexDirection: 'row',
    alignItems: 'center',
  },
  iconContainer: {
    width: 56,
    height: 56,
    borderRadius: 12,
    justifyContent: 'center',
    alignItems: 'center',
    marginRight: 16,
  },
  statusContent: {
    flex: 1,
  },
  statusTitle: {
    fontSize: 16,
    fontWeight: '600',
    color: 'white',
  },
  statusSubtitle: {
    fontSize: 12,
    color: '#9ca3af',
    marginTop: 4,
  },
  statusBadge: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 6,
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
  methodItem: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
    borderColor: '#334155',
    marginBottom: 8,
    flexDirection: 'row',
    alignItems: 'center',
  },
  methodText: {
    fontSize: 14,
    fontWeight: '500',
    color: 'white',
    flex: 1,
    marginLeft: 12,
  },
  benefitItem: {
    backgroundColor: '#1e293b',
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
    borderColor: '#334155',
    marginBottom: 8,
    flexDirection: 'row',
    alignItems: 'center',
  },
  benefitText: {
    fontSize: 14,
    color: '#e2e8f0',
    flex: 1,
    marginLeft: 12,
  },
  actions: {
    gap: 12,
    marginTop: 24,
  },
  primaryButton: {
    backgroundColor: '#3b82f6',
    borderRadius: 12,
    paddingVertical: 14,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 8,
  },
  primaryButtonText: {
    color: 'white',
    fontWeight: '600',
    fontSize: 14,
  },
  secondaryButton: {
    backgroundColor: 'transparent',
    borderWidth: 2,
    borderColor: '#3b82f6',
    borderRadius: 12,
    paddingVertical: 12,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 8,
  },
  secondaryButtonText: {
    color: '#3b82f6',
    fontWeight: '600',
    fontSize: 14,
  },
  dangerButton: {
    backgroundColor: '#ef444420',
    borderWidth: 1,
    borderColor: '#ef4444',
    borderRadius: 12,
    paddingVertical: 12,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 8,
  },
  dangerButtonText: {
    color: '#ef4444',
    fontWeight: '600',
    fontSize: 14,
  },
  unavailableContainer: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
  },
  unavailableTitle: {
    fontSize: 18,
    fontWeight: 'bold',
    color: 'white',
    marginTop: 16,
  },
  unavailableText: {
    fontSize: 14,
    color: '#9ca3af',
    marginTop: 8,
    textAlign: 'center',
  },
});
