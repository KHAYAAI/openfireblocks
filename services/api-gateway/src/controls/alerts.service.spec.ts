import { createServer } from 'http';
import { AddressInfo } from 'net';
import { AlertsService } from './alerts.service';

describe('AlertsService', () => {
  afterEach(() => { delete process.env.ALERT_WEBHOOK_URL; delete process.env.ALERT_TELEGRAM_BOT_TOKEN; delete process.env.ALERT_TELEGRAM_CHAT_ID; });

  it('does nothing when nothing is configured', async () => {
    const s = new AlertsService();
    expect(s.isConfigured()).toBe(false);
    await expect(s.notify({ severity: 'info', title: 't' })).resolves.toBeUndefined();
  });

  it('posts a Slack-compatible message naming the organisation and severity', async () => {
    const seen: any[] = [];
    const srv = createServer((req, res) => { let b = ''; req.on('data', (d) => (b += d)); req.on('end', () => { seen.push(JSON.parse(b)); res.end('ok'); }); });
    await new Promise<void>((r) => srv.listen(0, '127.0.0.1', r));
    process.env.ALERT_WEBHOOK_URL = `http://127.0.0.1:${(srv.address() as AddressInfo).port}/x`;
    await new AlertsService().notify({ severity: 'critical', organisation: 'Acme', title: 'Frozen', detail: 'because' });
    await new Promise((r) => srv.close(r));
    expect(seen[0].text).toContain('Acme'); expect(seen[0].text).toContain('Frozen'); expect(seen[0].text).toContain('because'); expect(seen[0].text).toContain('🔴');
  });

  it('never throws and never waits on an unreachable destination', async () => {
    process.env.ALERT_WEBHOOK_URL = 'http://127.0.0.1:1/unreachable';
    const t0 = Date.now();
    await expect(new AlertsService().notify({ severity: 'warning', title: 't' })).resolves.toBeUndefined();
    expect(Date.now() - t0).toBeLessThan(6000);
  });
});
