import {
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

// Orchestrates a Phase 0 sign request:
//   audit(received) -> MPC sign -> persist tx -> optional broadcast -> audit(success)
// Every branch (including failures) is recorded in the PostgreSQL audit trail.
@Injectable()
export class SignService {
  private readonly logger = new Logger(SignService.name);

  constructor(
    private readonly http: HttpService,
    private readonly postgres: PostgresService,
    private readonly audit: AuditService,
    private readonly ethereum: EthereumService,
  ) {}

  async sign(req: SignRequestDto): Promise<SignResult> {
    const requestId = uuid();

    await this.audit.logEvent({
      type: 'SIGN_REQUEST_RECEIVED',
      requestId,
      message: `to=${req.to} value=${req.value ?? '0'} nonce=${req.nonce}`,
      status: 'pending',
    });

    try {
      // 1. Call the MPC signer service.
      const mpcSignerUrl =
        process.env.MPC_SIGNER_URL ?? 'http://localhost:8080';
      const response = await lastValueFrom(
        this.http.post<MpcSignResponse>(`${mpcSignerUrl}/sign`, req),
      );
      const { signedTx, txHash, from } = response.data;

      // 2. Persist transaction metadata (status: signed).
      await this.postgres.saveTransaction({
        requestId,
        chain: 'ethereum',
        to: req.to,
        data: req.data ?? '',
        value: req.value ?? '0',
        gasLimit: req.gasLimit,
        gasPrice: req.gasPrice,
        nonce: req.nonce,
        signedTx,
        txHash,
        status: 'signed',
      });

      await this.audit.logEvent({
        type: 'SIGN_SUCCESS',
        requestId,
        signature: signedTx.slice(0, 66),
        hash: txHash,
        status: 'signed',
      });

      // 3. Broadcast to the network if an RPC endpoint is configured.
      let broadcasted = false;
      let finalHash = txHash;
      if (this.ethereum.canBroadcast) {
        try {
          finalHash = await this.ethereum.broadcastTransaction(signedTx);
          broadcasted = true;
          await this.postgres.updateStatus(
            requestId,
            'broadcasted',
            finalHash,
          );
          await this.audit.logEvent({
            type: 'BROADCAST_SUCCESS',
            requestId,
            hash: finalHash,
            status: 'broadcasted',
          });
        } catch (broadcastErr) {
          // Signing succeeded; surface the broadcast failure but keep the
          // signed tx so the caller can retry broadcasting themselves.
          const message = (broadcastErr as Error).message;
          this.logger.error(`broadcast failed for ${requestId}: ${message}`);
          await this.audit.logEvent({
            type: 'BROADCAST_FAILED',
            requestId,
            hash: txHash,
            status: 'failed',
            errorMessage: message,
          });
        }
      }

      return {
        requestId,
        signedTx,
        txHash: finalHash,
        from,
        status: broadcasted ? 'broadcasted' : 'signed',
        broadcasted,
      };
    } catch (error) {
      const message = (error as Error).message;
      await this.audit.logEvent({
        type: 'SIGN_FAILED',
        requestId,
        status: 'failed',
        errorMessage: message,
      });
      throw new InternalServerErrorException({
        error: 'Signing failed',
        detail: message,
        requestId,
      });
    }
  }
}
