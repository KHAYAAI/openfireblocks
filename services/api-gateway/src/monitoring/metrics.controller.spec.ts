import { UnauthorizedException } from '@nestjs/common';
import { MetricsController } from './metrics.controller';

const res = () => { const r: any = { headers: {} as Record<string, string>, body: undefined as unknown }; r.setHeader = (k: string, v: string) => { r.headers[k] = v; }; r.send = (b: unknown) => { r.body = b; }; return r; };
const svc: any = { contentType: () => 'text/plain', metrics: async () => 'up 1' };

describe('/metrics', () => {
  afterEach(() => { delete process.env.METRICS_TOKEN; });

  it('is open when no token is configured, as before', async () => {
    const r = res(); await new MetricsController(svc).scrape(r);
    expect(r.body).toBe('up 1');
  });
  it('needs the bearer token once one is configured', async () => {
    process.env.METRICS_TOKEN = 's3cret-token';
    const c = new MetricsController(svc);
    await expect(c.scrape(res())).rejects.toThrow(UnauthorizedException);
    await expect(c.scrape(res(), 'Bearer wrong-token!')).rejects.toThrow(UnauthorizedException);
    await expect(c.scrape(res(), 'Bearer s3cret-toke')).rejects.toThrow(UnauthorizedException);
    await expect(c.scrape(res(), 's3cret-token' + 'x')).rejects.toThrow(UnauthorizedException);
    const r = res(); await c.scrape(r, 'Bearer s3cret-token');
    expect(r.body).toBe('up 1');
  });
});
