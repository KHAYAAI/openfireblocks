import {
  BadRequestException,
  ForbiddenException,
  Injectable,
  InternalServerErrorException,
  Logger,
  Optional,
  ServiceUnavailableException,
} from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { lastValueFrom } from 'rxjs';
import { v4 as uuid } from 'uuid';
import { PostgresService } from '../database/postgres.service';
import { AuditService } from '../database/audit.service';
import { EthereumService } from '../blockchain/ethereum.service';
import { PolicyService } from '../policies/policy.service';
import { RiskService } from '../risk/risk.service';
import { BillingService } from '../billing/billing.service';
import { MetricsService } from '../monitoring/metrics.service';
import { Customer } from '../customers/customer.service';
import { SignRequestDto } from './dto/sign-request.dto';
import { resolveTransferIntent } from '../keys/transfer-intent';
import { TokenRegistryService } from '../tokens/token-registry.service';
import { GovernedContracts } from '../keys/governed-contracts';
import { ControlsService } from '../controls/controls.service';
import { ApprovalRequiredException } from '../keys/approval-required.exception';

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
  // What this transaction actually moves, as decoded from its calldata --
  // the token symbol (or 'NATIVE'), and the recipient/amount a reader
  // should compare against the whitelist and amount limit, which for a
  // token transfer are not req.to/req.value.
  asset: string;
  recipient: string | null;
  amount: string | null;
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
    private readonly billing: BillingService,
    private readonly metrics: MetricsService,
    // Required, not optional: the freeze and whitelist are the organisation's
    // own stop, and POST /sign once signed straight past them (AUTHZ-01). A
    // build of this service without it must fail to start, not silently skip.
    private readonly controls: ControlsService,
    // Optional like elsewhere in this codebase: a deployment or test that
    // builds this service directly without a token registry or governed-
    // contracts recogniser gets no token transfers and no governed calls,
    // which is the same behaviour resolveTransferIntent already gives
    // KeysService when either is absent.
    @Optional() private readonly tokens?: TokenRegistryService,
    @Optional() private readonly governed?: GovernedContracts,
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
      // What this transaction actually moves, decoded from the same
      // calldata that is about to be forwarded to mpc-signer verbatim --
      // not the envelope's own to/value, which for an ERC-20 transfer are
      // the token contract and zero. Evaluating policy on the envelope
      // instead of this is how a destination whitelist and an amount limit
      // could both exist, both work for a native transfer, and neither
      // apply to a token one.
      const intent = await resolveTransferIntent(
        {
          customerId,
          arbitraryContractCallsEnabled: customer.arbitrary_contract_calls_enabled,
          tokens: this.tokens,
          governed: this.governed,
        },
        req.chainId,
        req.to,
        req.value ?? '0',
        req.data,
      );

      // 0. The organisation's own stops, asked before anything else and
      // against what the transaction really moves: a freeze stops everything,
      // and an enforced whitelist limits where it may go. KeysService asks
      // these first on every route that signs; this route did not, so a
      // tenant API key alone could sign a 20 ETH transfer while the
      // organisation was frozen and to an address off its whitelist.
      try {
        await this.controls.assertCanSign(customerId);
        await this.controls.assertDestinationAllowed(customerId, 'ethereum', intent.policyTo);
      } catch (err) {
        if (err instanceof ForbiddenException) {
          await this.audit.logEvent({
            type: 'CONTROLS_DENIED',
            requestId,
            customerId,
            message: (err as Error).message,
            status: 'denied',
          });
          this.metrics.signRequests.inc({ status: 'denied', chain: 'ethereum' });
        }
        throw err;
      }

      // 1. Policy evaluation (fail-closed), against what the transaction
      // decodes to, not its envelope.
      const overrides = (customer.policies ?? {}) as Record<string, unknown>;
      const decision = await this.policy.evaluate({
        customerId,
        customerTier: customer.tier,
        to: intent.policyTo,
        value: req.value ?? '0',
        chainId: req.chainId,
        asset: intent.asset,
        assetAmount: intent.assetAmount,
        assetDecimals: intent.assetDecimals,
        pegCurrency: intent.pegCurrency,
        isAllowance: intent.isAllowance,
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

      // Approved on condition that people sign off. This route has no way to
      // open an approval request, so it refuses rather than signing at once:
      // a transfer above the approval threshold goes through the console or a
      // settlement, where approvers are asked. (It used to look only at
      // `approved`, so a high-value transfer was signed with no approval.)
      if (decision.requiresApproval) {
        await this.audit.logEvent({
          type: 'APPROVAL_REQUIRED',
          requestId,
          customerId,
          message: (decision.approvalReasons ?? []).join('; ') || 'policy requires approval',
          status: 'denied',
        });
        this.metrics.signRequests.inc({ status: 'denied', chain: 'ethereum' });
        throw new ApprovalRequiredException(decision.approvalReasons ?? [], requestId);
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

      // 3. Persist transaction metadata (status: signed), tenant-scoped,
      // including what it actually moves -- recordTransfer, not
      // saveTransaction, so the decoded asset/recipient/amount lands in
      // the same columns KeysService's own signing route writes, and the
      // daily aggregate that decides whether a filing is due is not
      // silently missing every transfer made through this route.
      await this.postgres.recordTransfer({
        rowId: requestId,
        requestId,
        customerId,
        chain: 'ethereum',
        to: req.to,
        data: req.data ?? null,
        value: req.value ?? '0',
        gasLimit: req.gasLimit,
        // Record the effective fee: legacy gasPrice, else the 1559 fee cap.
        gasPrice: req.gasPrice ?? req.maxFeePerGas ?? '',
        nonce: req.nonce,
        signedTx,
        txHash,
        status: 'signed',
        assetSymbol: intent.asset,
        assetContract: intent.contractAddress ?? null,
        assetDecimals: intent.assetDecimals,
        assetPeg: intent.pegCurrency ?? null,
        effectiveTo: intent.effectiveTo,
        effectiveAmount: intent.effectiveAmount,
      });

      await this.audit.logEvent({
        type: 'SIGN_SUCCESS',
        requestId,
        customerId,
        signature: signedTx.slice(0, 66),
        hash: txHash,
        status: 'signed',
      });

      // Meter usage (best-effort; never blocks signing).
      await this.billing.recordSigned(customerId);

      // 4. Broadcast to the network if an RPC endpoint is configured.
      let broadcasted = false;
      let finalHash = txHash;
      if (this.ethereum.canBroadcast) {
        try {
          finalHash = await this.ethereum.broadcastTransaction(signedTx);
          broadcasted = true;
          await this.postgres.updateStatus(requestId, customerId, 'broadcasted', finalHash);
          await this.billing.recordBroadcast(customerId);
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
        asset: intent.asset,
        recipient: intent.effectiveTo,
        amount: intent.effectiveAmount,
      };
    } catch (error) {
      // Re-throw policy/risk denials untouched: each is already audited
      // (POLICY_DENIED / RISK_DENIED) and counted above, and wrapping it
      // here would both double those and hide its real HTTP status behind
      // a generic 500.
      if (error instanceof ForbiddenException) {
        throw error;
      }
      // A rejected or undecodable request (bad calldata, no token
      // registry) is the caller's to fix, not an outage -- carry its own
      // status and message through rather than reporting "Signing failed".
      if (error instanceof BadRequestException || error instanceof ServiceUnavailableException) {
        this.metrics.signRequests.inc({ status: 'failed', chain: 'ethereum' });
        await this.audit.logEvent({
          type: 'SIGN_FAILED',
          requestId,
          customerId,
          status: 'failed',
          errorMessage: (error as Error).message,
        });
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
