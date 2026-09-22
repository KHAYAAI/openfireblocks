import { Injectable, Logger } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { lastValueFrom } from 'rxjs';

export interface PolicyInput {
  customerId: string;
  customerTier: string;

  // Who receives the value. For a token transfer this is the ERC-20
  // recipient decoded out of the calldata, NOT the transaction's own `to`
  // -- that is the token contract, which is the same address for every
  // transfer of that token and made the counterparty whitelist incapable
  // of distinguishing one recipient from another.
  to: string;

  // The native value attached to the transaction, in wei. Legitimately "0"
  // for a token transfer.
  value: string;
  chainId: number;

  // What actually moves. 'NATIVE' for a plain transfer, otherwise a
  // registry symbol. The wei-denominated rules cannot govern a token --
  // the units are not comparable -- so the policy service applies limits
  // denominated in the peg currency to these instead.
  asset?: string;
  assetAmount?: string; // base units
  assetDecimals?: number;
  pegCurrency?: string; // 'USD', 'ZAR', absent when unpegged

  // An approve() call: nothing moves now, and the spender may move
  // assetAmount at any later time.
  isAllowance?: boolean;

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
      // Still fail closed either way, but do not report a malformed
      // request as an outage. A 4xx means *we* sent something the policy
      // service rejected -- a caller-side bug that an operator would
      // otherwise spend the incident hunting in the wrong service, since
      // "policy service unavailable" points at a healthy dependency.
      const status = (err as { response?: { status?: number } }).response?.status;
      const isClientError = typeof status === 'number' && status >= 400 && status < 500;
      const detail = isClientError
        ? `policy service rejected the request (HTTP ${status}) -- this is a malformed evaluation request, not an outage`
        : `policy service unreachable`;

      this.logger.error(`${detail}, denying by default: ${(err as Error).message}`);

      return {
        approved: false,
        denials: [isClientError ? 'policy evaluation request rejected' : 'policy service unavailable'],
        requiresApproval: false,
        reason: `${detail} (fail-closed)`,
      };
    }
  }
}
