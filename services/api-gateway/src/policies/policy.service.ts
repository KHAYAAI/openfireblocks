import { Injectable, Logger } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { lastValueFrom } from 'rxjs';

export interface PolicyInput {
  customerId: string;
  customerTier: string;
  to: string;
  value: string; // wei
  chainId: number;
  whitelist?: string[];
  blockedCountries?: string[];
  country?: string;
}

export interface PolicyDecision {
  approved: boolean;
  denials: string[];
  requiresApproval: boolean;
  reason: string;
}

// Client for the OPA-backed policy-service. Fails CLOSED: if the policy service
// is unreachable the transaction is denied, never silently approved.
@Injectable()
export class PolicyService {
  private readonly logger = new Logger(PolicyService.name);
  private readonly url =
    process.env.POLICY_SERVICE_URL ?? 'http://localhost:8081';

  constructor(private readonly http: HttpService) {}

  async evaluate(input: PolicyInput): Promise<PolicyDecision> {
    try {
      const res = await lastValueFrom(
        this.http.post<PolicyDecision>(`${this.url}/evaluate`, input),
      );
      return res.data;
    } catch (err) {
      this.logger.error(
        `policy service unreachable, denying by default: ${
          (err as Error).message
        }`,
      );
      return {
        approved: false,
        denials: ['policy service unavailable'],
        requiresApproval: false,
        reason: 'policy service unavailable (fail-closed)',
      };
    }
  }
}
