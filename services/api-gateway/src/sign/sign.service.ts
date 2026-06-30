import {
  ForbiddenException,
  Injectable,
  InternalServerErrorException,
  Logger,
} from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { lastValueFrom } from 'rxjs';
import { v4 as uuid } from 'uuid';
import { PostgresService } from '../database/postgres.service';
import { AuditService } from '../database/audit.service';
import { EthereumService } from '../blockchain/ethereum.service';
import { PolicyService } from '../policies/policy.service';
import { RiskService } from '../risk/risk.service';
import { MetricsService } from '../monitoring/metrics.service';
import { Customer } from '../customers/customer.service';
import { SignRequestDto } from './dto/sign-request.dto';

// Shape of the MPC signer's /sign response.
interface MpcSignResponse {
  requestId: string;
  signedTx: string;
  txHash: string;
  from: string;
  status: string;
  auditLogId: number;
}

export interface SignResult {
  requestId: string;
  signedTx: string;
  txHash: string;
  from: string;
  status: 'signed' | 'broadcasted';
  broadcasted: boolean;
}

// Orchestrates a Phase 1 sign request, scoped to an authenticated tenant:
//   audit(received) -> policy check -> MPC sign -> persist -> optional broadcast -> audit
// Every branch (including policy denials and failures) is recorded in the
// per-tenant PostgreSQL audit trail and counted in Prometheus metrics.
@Injectable()
export class SignService {
  private readonly logger = new Logger(SignService.name);

  constructor(
    private readonly http: HttpService,
    private readonly postgres: PostgresService,
    private readonly audit: AuditService,
    private readonly ethereum: EthereumService,
    private readonly policy: PolicyService,
    private readonly risk: RiskService,
    private readonly metrics: MetricsService,
  ) {}

  async sign(customer: Customer, req: SignRequestDto): Promise<SignResult> {
    const requestId = uuid();
    const customerId = customer.customer_id;
    const stopTimer = this.metrics.signLatency.startTimer({ chain: 'ethereum' });

    await this.audit.logEvent({
      type: 'SIGN_REQUEST_RECEIVED',
      requestId,
      customerId,
      message: `to=${req.to} value=${req.value ?? '0'} nonce=${req.nonce}`,
      status: 'pending',
    });

    try {
      // 1. Policy evaluation (fail-closed).
      const overrides = (customer.policies ?? {}) as Record<string, unknown>;
      const decision = await this.policy.evaluate({
        customerId,
        customerTier: customer.tier,
        to: req.to,
        value: req.value ?? '0',
        chainId: req.chainId,
        whitelist: overrides.whitelist as string[] | undefined,
        blockedCountries: overrides.blockedCountries as string[] | undefined,
        country: req.country,
      });

      if (!decision.approved) {
        for (const d of decision.denials) {
          this.metrics.policyDenials.inc({ policy_type: d });
        }
        await this.audit.logEvent({
          type: 'POLICY_DENIED',
          requestId,
          customerId,
          message: decision.denials.join('; '),
          status: 'denied',
        });
        this.metrics.signRequests.inc({ status: 'denied', chain: 'ethereum' });
        throw new ForbiddenException({
          error: 'policy denied',
          denials: decision.denials,
          requiresApproval: decision.requiresApproval,
          requestId,
        });
      }

      // 1b. Velocity / risk control (per-tenant hourly transaction limit).
      const velocity = await this.risk.checkAndRecord(customerId, customer.tier);
      if (!velocity.allowed) {
        this.metrics.riskDenials.inc({ reason: 'velocity' });
        await this.audit.logEvent({
          type: 'RISK_DENIED',
          requestId,
          customerId,
          message: velocity.reason,
          status: 'denied',
        });
        this.metrics.signRequests.inc({ status: 'denied', chain: 'ethereum' });
        throw new ForbiddenException({
          error: 'risk denied',
          reason: velocity.reason,
          requestId,
        });
      }

      // 2. Call the MPC signer service.
      const mpcSignerUrl =
        process.env.MPC_SIGNER_URL ?? 'http://localhost:8080';
      const response = await lastValueFrom(
        this.http.post<MpcSignResponse>(`${mpcSignerUrl}/sign`, req),
      );
      const { signedTx, txHash, from } = response.data;

      // 3. Persist transaction metadata (status: signed), tenant-scoped.
      await this.postgres.saveTransaction({
        requestId,
        customerId,
        chain: 'ethereum',
        to: req.to,
        data: req.data ?? '',
        value: req.value ?? '0',
        gasLimit: req.gasLimit,
        // Record the effective fee: legacy gasPrice, else the 1559 fee cap.
        gasPrice: req.gasPrice ?? req.maxFeePerGas ?? '',
        nonce: req.nonce,
        signedTx,
        txHash,
        status: 'signed',
      });

      await this.audit.logEvent({
        type: 'SIGN_SUCCESS',
        requestId,
        customerId,
        signature: signedTx.slice(0, 66),
        hash: txHash,
        status: 'signed',
      });

      // 4. Broadcast to the network if an RPC endpoint is configured.
      let broadcasted = false;
      let finalHash = txHash;
      if (this.ethereum.canBroadcast) {
        try {
          finalHash = await this.ethereum.broadcastTransaction(signedTx);
          broadcasted = true;
          await this.postgres.updateStatus(requestId, 'broadcasted', finalHash);
          await this.audit.logEvent({
            type: 'BROADCAST_SUCCESS',
            requestId,
            customerId,
            hash: finalHash,
            status: 'broadcasted',
          });
        } catch (broadcastErr) {
          const message = (broadcastErr as Error).message;
          this.logger.error(`broadcast failed for ${requestId}: ${message}`);
          this.metrics.broadcastErrors.inc({ chain: 'ethereum', reason: 'rpc' });
          await this.audit.logEvent({
            type: 'BROADCAST_FAILED',
            requestId,
            customerId,
            hash: txHash,
            status: 'failed',
            errorMessage: message,
          });
        }
      }

      this.metrics.signRequests.inc({
        status: broadcasted ? 'broadcasted' : 'signed',
        chain: 'ethereum',
      });

      return {
        requestId,
        signedTx,
        txHash: finalHash,
        from,
        status: broadcasted ? 'broadcasted' : 'signed',
        broadcasted,
      };
    } catch (error) {
      // Re-throw policy denials untouched (already audited + counted).
      if (error instanceof ForbiddenException) {
        throw error;
      }
      const message = (error as Error).message;
      this.metrics.signRequests.inc({ status: 'failed', chain: 'ethereum' });
      await this.audit.logEvent({
        type: 'SIGN_FAILED',
        requestId,
        customerId,
        status: 'failed',
        errorMessage: message,
      });
      throw new InternalServerErrorException({
        error: 'Signing failed',
        detail: message,
        requestId,
      });
    } finally {
      stopTimer();
    }
  }
}
