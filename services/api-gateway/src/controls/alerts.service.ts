import { Injectable, Logger } from '@nestjs/common';

export type AlertSeverity = 'info' | 'warning' | 'critical';

// Tells people when something needs them, without a person having to be
// looking at the console: a transfer waiting on approval, a send that failed,
// the organisation frozen, the whitelist or approval policy changed.
//
// Configured by the operator, never by a tenant, so there is no tenant-chosen
// URL to point at an internal address:
//
//   ALERT_WEBHOOK_URL         a Slack-compatible incoming webhook ({"text": ...}),
//                             which Slack, Mattermost and Teams connectors accept
//   ALERT_TELEGRAM_BOT_TOKEN  with ALERT_TELEGRAM_CHAT_ID
//
// An alert is a courtesy and a transfer never waits on one: failures are
// logged and swallowed, and every call has a short timeout. Messages carry
// the organisation and what happened, never keys, addresses in full, or
// secrets.
@Injectable()
export class AlertsService {
  private readonly logger = new Logger(AlertsService.name);

  isConfigured(): boolean {
    return Boolean(process.env.ALERT_WEBHOOK_URL || (process.env.ALERT_TELEGRAM_BOT_TOKEN && process.env.ALERT_TELEGRAM_CHAT_ID));
  }

  async notify(alert: { severity: AlertSeverity; title: string; detail?: string; organisation?: string }): Promise<void> {
    if (!this.isConfigured()) return;
    const icon = alert.severity === 'critical' ? '🔴' : alert.severity === 'warning' ? '🟠' : '🔵';
    const text = `${icon} OpenFireblocks${alert.organisation ? ` · ${alert.organisation}` : ''}\n${alert.title}${alert.detail ? `\n${alert.detail}` : ''}`;
    const sends: Promise<unknown>[] = [];
    const url = process.env.ALERT_WEBHOOK_URL;
    if (url) sends.push(this.post(url, { text }));
    const tok = process.env.ALERT_TELEGRAM_BOT_TOKEN, chat = process.env.ALERT_TELEGRAM_CHAT_ID;
    if (tok && chat) sends.push(this.post(`https://api.telegram.org/bot${tok}/sendMessage`, { chat_id: chat, text }));
    await Promise.allSettled(sends);
  }

  private async post(url: string, body: unknown): Promise<void> {
    try {
      const res = await fetch(url, {
        method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
        signal: AbortSignal.timeout(5000),
      });
      if (!res.ok) this.logger.warn(`alert delivery returned ${res.status}`);
    } catch (e) {
      // Never log the URL: webhook URLs and bot tokens are secrets.
      this.logger.warn(`alert delivery failed: ${(e as Error).name}`);
    }
  }
}
