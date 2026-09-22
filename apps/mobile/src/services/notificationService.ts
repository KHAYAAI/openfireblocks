import * as Notifications from 'expo-notifications';
import * as Device from 'expo-device';
import { Platform } from 'react-native';
import * as SecureStore from 'expo-secure-store';

Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowAlert: true,
    shouldPlaySound: true,
    shouldSetBadge: true,
  }),
});

export interface PushNotification {
  id: string;
  title: string;
  body: string;
  data: Record<string, any>;
  timestamp: number;
  read: boolean;
}

export interface NotificationPreferences {
  signing_completed: boolean;
  settlement_completed: boolean;
  kyc_updates: boolean;
  policy_violations: boolean;
  security_alerts: boolean;
}

class NotificationService {
  private static instance: NotificationService;

  private constructor() {}

  static getInstance(): NotificationService {
    if (!NotificationService.instance) {
      NotificationService.instance = new NotificationService();
    }
    return NotificationService.instance;
  }

  async requestPermissions(): Promise<boolean> {
    if (!Device.isDevice) {
      console.log('Must use physical device for push notifications');
      return false;
    }

    if (Platform.OS === 'android') {
      await Notifications.setNotificationChannelAsync('default', {
        name: 'default',
        importance: Notifications.AndroidImportance.MAX,
        vibrationPattern: [0, 250, 250, 250],
        lightColor: '#FF231F7C',
      });
    }

    const { status: existingStatus } = await Notifications.getPermissionsAsync();
    let finalStatus = existingStatus;

    if (existingStatus !== 'granted') {
      const { status } = await Notifications.requestPermissionsAsync();
      finalStatus = status;
    }

    return finalStatus === 'granted';
  }

  async getDeviceToken(): Promise<string | null> {
    try {
      const token = await Notifications.getExpoPushTokenAsync({
        projectId: process.env.EXPO_PROJECT_ID || 'YOUR_PROJECT_ID',
      });
      return token.data;
    } catch (error) {
      console.error('Failed to get device token:', error);
      return null;
    }
  }

  async registerDeviceToken(apiKey: string): Promise<void> {
    try {
      const token = await this.getDeviceToken();
      if (!token) return;

      const response = await fetch('https://api.openfireblocks.io/v1/notifications/register', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': apiKey,
        },
        body: JSON.stringify({
          device_token: token,
          platform: Platform.OS,
          device_name: Device.modelName || 'Unknown',
        }),
      });

      if (!response.ok) {
        throw new Error(`Failed to register device token: ${response.statusText}`);
      }

      // Save token locally
      await SecureStore.setItemAsync('device_token', token);
    } catch (error) {
      console.error('Failed to register device token:', error);
    }
  }

  subscribeToNotifications(
    onNotification: (notification: PushNotification) => void
  ): () => void {
    // Listen for notifications received while app is foregrounded
    const foregroundSubscription = Notifications.addNotificationReceivedListener(
      (notification) => {
        const pushNotification: PushNotification = {
          id: notification.request.identifier,
          title: notification.request.content.title || '',
          body: notification.request.content.body || '',
          data: notification.request.content.data,
          timestamp: Date.now(),
          read: false,
        };
        onNotification(pushNotification);
      }
    );

    // Listen for tap actions on notifications
    const responseSubscription = Notifications.addNotificationResponseReceivedListener(
      (response) => {
        const { notification } = response;
        const pushNotification: PushNotification = {
          id: notification.request.identifier,
          title: notification.request.content.title || '',
          body: notification.request.content.body || '',
          data: notification.request.content.data,
          timestamp: Date.now(),
          read: true,
        };
        onNotification(pushNotification);

        // Handle notification tap
        this.handleNotificationTap(pushNotification);
      }
    );

    // Return unsubscribe function
    return () => {
      foregroundSubscription.remove();
      responseSubscription.remove();
    };
  }

  private handleNotificationTap(notification: PushNotification): void {
    const { type, data } = notification.data;

    switch (type) {
      case 'signing_completed':
        // Navigate to signing details
        console.log('Signing completed:', data);
        break;

      case 'settlement_completed':
        // Navigate to settlement details
        console.log('Settlement completed:', data);
        break;

      case 'kyc_update':
        // Navigate to compliance status
        console.log('KYC update:', data);
        break;

      case 'policy_violation':
        // Navigate to policies
        console.log('Policy violation:', data);
        break;

      case 'security_alert':
        // Show security alert
        console.log('Security alert:', data);
        break;

      default:
        console.log('Unknown notification type:', type);
    }
  }

  async getPreferences(apiKey: string): Promise<NotificationPreferences> {
    try {
      const response = await fetch(
        'https://api.openfireblocks.io/v1/notifications/preferences',
        {
          method: 'GET',
          headers: {
            'X-API-Key': apiKey,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to fetch preferences');
      }

      return await response.json();
    } catch (error) {
      console.error('Failed to get notification preferences:', error);
      // Return defaults
      return {
        signing_completed: true,
        settlement_completed: true,
        kyc_updates: true,
        policy_violations: true,
        security_alerts: true,
      };
    }
  }

  async updatePreferences(
    apiKey: string,
    preferences: NotificationPreferences
  ): Promise<void> {
    try {
      const response = await fetch(
        'https://api.openfireblocks.io/v1/notifications/preferences',
        {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-API-Key': apiKey,
          },
          body: JSON.stringify(preferences),
        }
      );

      if (!response.ok) {
        throw new Error('Failed to update preferences');
      }
    } catch (error) {
      console.error('Failed to update notification preferences:', error);
      throw error;
    }
  }

  async sendLocalNotification(
    title: string,
    body: string,
    data: Record<string, any> = {}
  ): Promise<void> {
    try {
      await Notifications.scheduleNotificationAsync({
        content: {
          title,
          body,
          data,
          sound: true,
          badge: 1,
        },
        trigger: null, // Send immediately
      });
    } catch (error) {
      console.error('Failed to send local notification:', error);
    }
  }

  async getNotificationHistory(apiKey: string, limit: number = 50): Promise<PushNotification[]> {
    try {
      const response = await fetch(
        `https://api.openfireblocks.io/v1/notifications/history?limit=${limit}`,
        {
          method: 'GET',
          headers: {
            'X-API-Key': apiKey,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to fetch notification history');
      }

      return await response.json();
    } catch (error) {
      console.error('Failed to get notification history:', error);
      return [];
    }
  }

  async markAsRead(apiKey: string, notificationId: string): Promise<void> {
    try {
      const response = await fetch(
        `https://api.openfireblocks.io/v1/notifications/${notificationId}/read`,
        {
          method: 'POST',
          headers: {
            'X-API-Key': apiKey,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to mark notification as read');
      }
    } catch (error) {
      console.error('Failed to mark notification as read:', error);
    }
  }

  async deleteNotification(apiKey: string, notificationId: string): Promise<void> {
    try {
      const response = await fetch(
        `https://api.openfireblocks.io/v1/notifications/${notificationId}`,
        {
          method: 'DELETE',
          headers: {
            'X-API-Key': apiKey,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to delete notification');
      }
    } catch (error) {
      console.error('Failed to delete notification:', error);
    }
  }
}

export const notificationService = NotificationService.getInstance();
