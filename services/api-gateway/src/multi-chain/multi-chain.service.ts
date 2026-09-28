import { Injectable, Logger } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { firstValueFrom } from 'rxjs';
import { SignMultiChainRequest, SignMultiChainResponse } from './multi-chain.controller';

/**
 * MultiChainService handles multi-chain signing orchestration.
 * Routes to mpc-signer service via HTTP.
 */
@Injectable()
export class MultiChainService {
  private readonly logger = new Logger(MultiChainService.name);
  private readonly mpcSignerUrl = process.env.MPC_SIGNER_URL || 'http://localhost:8080';
  private readonly supportedChains = ['ethereum', 'bitcoin', 'solana', 'cosmos-hub'];

  constructor(private httpService: HttpService) {}

  /**
   * Validate API key and return customer.
   */
  async validateApiKey(apiKey: string) {
    // TODO: Query PostgreSQL for API key + customer
    // For now, return mock customer
    if (apiKey === 'test-api-key') {
      return { id: 'test-customer', name: 'Test Customer' };
    }
    return null;
  }

  /**
   * Check if chain is supported.
   */
  isValidChain(chainId: string): boolean {
    return this.supportedChains.includes(chainId);
  }

  /**
   * Get list of supported chains.
   */
  getSupportedChains(): string[] {
    return this.supportedChains;
  }

  /**
   * Sign a transaction on any blockchain.
   * Routes to mpc-signer service which uses chain-specific signers.
   */
  async signMultiChain(
    customerId: string,
    request: SignMultiChainRequest,
  ): Promise<SignMultiChainResponse> {
    const requestId = this.generateRequestId();

    this.logger.debug(`Signing on ${request.chainId}`, {
      customerId,
      requestId,
      chainId: request.chainId,
    });

    try {
      // Route to mpc-signer service
      const response = await firstValueFrom(
        this.httpService.post(`${this.mpcSignerUrl}/sign-multi-chain`, {
          chainId: request.chainId,
          message: request.message,
          metadata: request.metadata,
        }),
      );

      return {
        requestId,
        chainId: request.chainId,
        signature: response.data.signature,
        signedTx: response.data.signedTx,
        from: response.data.from,
        status: 'signed',
        broadcasted: false,
      };
    } catch (error) {
      this.logger.error(`Signing failed on ${request.chainId}`, {
        customerId,
        requestId,
        error: error.message,
      });

      return {
        requestId,
        chainId: request.chainId,
        signature: '',
        from: '',
        status: 'failed',
        broadcasted: false,
        error: error.message,
      };
    }
  }

  /**
   * Broadcast a signed transaction.
   * Routes to appropriate blockchain RPC.
   */
  async broadcastTransaction(chainId: string, signedTx: string): Promise<string> {
    this.logger.debug(`Broadcasting on ${chainId}`);

    try {
      const response = await firstValueFrom(
        this.httpService.post(`${this.mpcSignerUrl}/broadcast`, {
          chainId,
          signedTx,
        }),
      );

      return response.data.txHash;
    } catch (error) {
      this.logger.error(`Broadcast failed on ${chainId}`, {
        error: error.message,
      });
      throw error;
    }
  }

  /**
   * Audit log event.
   */
  async auditLog(event: {
    customerId: string;
    action: string;
    chainId?: string;
    requestId?: string;
    txHash?: string;
    error?: string;
  }) {
    // TODO: Log to PostgreSQL + immudb
    this.logger.debug(`Audit: ${event.action}`, event);
  }

  /**
   * Generate unique request ID.
   */
  private generateRequestId(): string {
    return `req_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
  }
}
