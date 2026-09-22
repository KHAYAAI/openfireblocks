import { Controller, Post, Body, Headers, HttpException, HttpStatus } from '@nestjs/common';
import { MultiChainService } from './multi-chain.service';
import { ApiTags, ApiOperation, ApiResponse, ApiBearerAuth } from '@nestjs/swagger';

/**
 * MultiChainController handles multi-chain signing requests.
 * Supports: Ethereum, Bitcoin, Solana, Cosmos
 */
@ApiTags('multi-chain')
@ApiBearerAuth()
@Controller('sign-multi-chain')
export class MultiChainController {
  constructor(private multiChainService: MultiChainService) {}

  /**
   * Sign a transaction on any supported blockchain.
   *
   * POST /sign-multi-chain
   * {
   *   "chainId": "bitcoin",
   *   "message": "0xdeadbeef",  // RLP (Ethereum), script (Bitcoin), instruction (Solana), SignDoc (Cosmos)
   *   "metadata": {
   *     "network": "testnet",
   *     "utxos": [...],
   *     "recentBlockhash": "...",
   *     "account_number": 123
   *   }
   * }
   */
  @Post()
  @ApiOperation({ summary: 'Sign transaction on any supported blockchain' })
  @ApiResponse({ status: 200, description: 'Transaction signed successfully' })
  @ApiResponse({ status: 400, description: 'Invalid request' })
  @ApiResponse({ status: 401, description: 'Unauthorized' })
  async signMultiChain(
    @Body() request: SignMultiChainRequest,
    @Headers('x-api-key') apiKey: string,
  ) {
    // Validate API key and get customer
    const customer = await this.multiChainService.validateApiKey(apiKey);
    if (!customer) {
      throw new HttpException('Unauthorized', HttpStatus.UNAUTHORIZED);
    }

    // Validate chain
    if (!this.multiChainService.isValidChain(request.chainId)) {
      throw new HttpException(
        { error: `Unsupported chain: ${request.chainId}` },
        HttpStatus.BAD_REQUEST,
      );
    }

    // Route to appropriate signer
    try {
      const response = await this.multiChainService.signMultiChain(
        customer.id,
        request,
      );

      // Audit log
      await this.multiChainService.auditLog({
        customerId: customer.id,
        action: 'MULTI_CHAIN_SIGN_SUCCESS',
        chainId: request.chainId,
        requestId: response.requestId,
      });

      return response;
    } catch (error) {
      // Audit log failure
      await this.multiChainService.auditLog({
        customerId: customer.id,
        action: 'MULTI_CHAIN_SIGN_FAILED',
        chainId: request.chainId,
        error: error.message,
      });

      throw new HttpException(
        { error: `Signing failed: ${error.message}` },
        HttpStatus.INTERNAL_SERVER_ERROR,
      );
    }
  }

  /**
   * Get supported chains.
   * GET /sign-multi-chain/chains
   */
  @Post('chains')
  @ApiOperation({ summary: 'Get list of supported chains' })
  @ApiResponse({ status: 200, description: 'List of supported chains' })
  async getSupportedChains() {
    return {
      chains: this.multiChainService.getSupportedChains(),
      count: this.multiChainService.getSupportedChains().length,
    };
  }

  /**
   * Broadcast a signed transaction.
   * POST /sign-multi-chain/broadcast
   */
  @Post('broadcast')
  @ApiOperation({ summary: 'Broadcast a signed transaction' })
  async broadcast(
    @Body() request: BroadcastRequest,
    @Headers('x-api-key') apiKey: string,
  ) {
    const customer = await this.multiChainService.validateApiKey(apiKey);
    if (!customer) {
      throw new HttpException('Unauthorized', HttpStatus.UNAUTHORIZED);
    }

    try {
      const txHash = await this.multiChainService.broadcastTransaction(
        request.chainId,
        request.signedTx,
      );

      await this.multiChainService.auditLog({
        customerId: customer.id,
        action: 'MULTI_CHAIN_BROADCAST_SUCCESS',
        chainId: request.chainId,
        txHash,
      });

      return { txHash, status: 'broadcasted' };
    } catch (error) {
      await this.multiChainService.auditLog({
        customerId: customer.id,
        action: 'MULTI_CHAIN_BROADCAST_FAILED',
        chainId: request.chainId,
        error: error.message,
      });

      throw new HttpException(
        { error: `Broadcast failed: ${error.message}` },
        HttpStatus.INTERNAL_SERVER_ERROR,
      );
    }
  }
}

/**
 * Request to sign a transaction on any blockchain.
 */
export interface SignMultiChainRequest {
  chainId: string; // "ethereum", "bitcoin", "solana", "cosmos-hub"
  message: string; // Hex-encoded message (RLP for Ethereum, script for Bitcoin, etc.)
  metadata?: {
    network?: string; // "mainnet", "testnet"
    [key: string]: any; // Chain-specific metadata
  };
}

/**
 * Response after signing.
 */
export interface SignMultiChainResponse {
  requestId: string;
  chainId: string;
  signature: string; // 65-byte or variable-length signature (hex)
  signedTx?: string; // Full signed transaction (optional)
  from: string; // Signer address
  status: 'signed' | 'failed';
  broadcasted: boolean;
  error?: string;
}

/**
 * Request to broadcast a signed transaction.
 */
export interface BroadcastRequest {
  chainId: string;
  signedTx: string; // Hex-encoded signed transaction
}
