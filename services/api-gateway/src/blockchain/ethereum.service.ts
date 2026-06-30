import { Injectable, Logger } from '@nestjs/common';
import { ethers } from 'ethers';

// Thin wrapper around an Ethereum JSON-RPC provider (Sepolia in Phase 0).
// Handles broadcasting signed transactions and the gas/nonce helpers the
// gateway exposes so callers can build a valid SignRequest.
@Injectable()
export class EthereumService {
  private readonly logger = new Logger(EthereumService.name);
  private readonly provider: ethers.JsonRpcProvider | null;

  constructor() {
    const rpcUrl = process.env.ETHEREUM_RPC_SEPOLIA;
    if (!rpcUrl || rpcUrl.includes('YOUR_KEY')) {
      // Broadcasting is optional in Phase 0: without an RPC URL the gateway
      // still signs and audits, it just cannot push the tx on-chain.
      this.logger.warn(
        'ETHEREUM_RPC_SEPOLIA not configured; broadcasting is disabled',
      );
      this.provider = null;
    } else {
      this.provider = new ethers.JsonRpcProvider(rpcUrl);
    }
  }

  // True when an RPC endpoint is configured and broadcasting is possible.
  get canBroadcast(): boolean {
    return this.provider !== null;
  }

  // Broadcasts a raw signed transaction and returns its hash.
  async broadcastTransaction(signedTx: string): Promise<string> {
    if (!this.provider) {
      throw new Error('Ethereum RPC not configured');
    }
    const txResponse = await this.provider.broadcastTransaction(signedTx);
    return txResponse.hash;
  }

  async getTransactionReceipt(txHash: string) {
    if (!this.provider) throw new Error('Ethereum RPC not configured');
    return this.provider.getTransactionReceipt(txHash);
  }

  async getFeeData() {
    if (!this.provider) throw new Error('Ethereum RPC not configured');
    return this.provider.getFeeData();
  }

  async getNonce(address: string): Promise<number> {
    if (!this.provider) throw new Error('Ethereum RPC not configured');
    return this.provider.getTransactionCount(address, 'pending');
  }
}
