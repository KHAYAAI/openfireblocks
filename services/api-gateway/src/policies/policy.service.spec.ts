import { of, throwError } from 'rxjs';
import { HttpService } from '@nestjs/axios';
import { PolicyService } from './policy.service';

const input = {
  customerId: 'demo',
  customerTier: 'pro',
  to: '0xabc',
  value: '1000',
  chainId: 11155111,
};

describe('PolicyService', () => {
  it('returns the policy-service decision on success', async () => {
    const http = {
      post: jest.fn(() =>
        of({ data: { approved: true, denials: [], requiresApproval: false, reason: 'ok' } }),
    ) } as unknown as HttpService;

    const svc = new PolicyService(http);
    const d = await svc.evaluate(input);
    expect(d.approved).toBe(true);
  });

  it('fails CLOSED when the policy service is unreachable', async () => {
    const http = {
      post: jest.fn(() => throwError(() => new Error('ECONNREFUSED'))),
    } as unknown as HttpService;

    const svc = new PolicyService(http);
    const d = await svc.evaluate(input);
    expect(d.approved).toBe(false);
    expect(d.denials).toContain('policy service unavailable');
  });
});
