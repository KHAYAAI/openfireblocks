import {
  Injectable,
  Logger,
  ServiceUnavailableException,
} from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { lastValueFrom } from 'rxjs';
import { EthereumService } from './ethereum.service';

export interface PreparedTx {
  from: string;
  chainId: number;
  nonce: number;
  gasLimit: number;
  maxFeePerGas?: string;
  maxPriorityFeePerGas?: string;
  gasPrice?: string;
}

// Builds the fee/nonce/gas fields a client needs to construct a sign request,
// using live network data. Requires an RPC endpoint (broadcasting enabled).
@Injectable()
export class PrepareService {
  private readonly logger = new Logger(PrepareService.name);
  private cachedAddress: string | null = null;

  constructor(
    private readonly http: HttpService,
    private readonly ethereum: EthereumService,
  ) {}

  async prepare(input: {
    to: string;
    data?: string;
    value?: string;
  }): Promise<PreparedTx> {
    if (!this.ethereum.canBroadcast) {
      throw new ServiceUnavailableException(
        'gas estimation requires an Ethereum RPC endpoint (ETHEREUM_RPC_SEPOLIA)',
      );
    }

    const from = await this.signerAddress();
    const [nonce, feeData, chainId] = await Promise.all([
      this.ethereum.getNonce(from),
      this.ethereum.getFeeData(),
      this.ethereum.getChainId(),
    ]);

    // Estimate gas; fall back to the 21000 base cost if estimation reverts.
    let gasLimit = 21000;
    try {
      const estimated = await this.ethereum.estimateGas({
        from,
        to: input.to,
        data: input.data,
        value: input.value,
      });
      // Add a 20% buffer for safety.
      gasLimit = Number((estimated * 120n) / 100n);
    } catch (err) {
      this.logger.warn(
        `gas estimation failed, using base 21000: ${(err as Error).message}`,
      );
    }

    return {
      from,
      chainId,
      nonce,
      gasLimit,
      maxFeePerGas: feeData.maxFeePerGas?.toString(),
      maxPriorityFeePerGas: feeData.maxPriorityFeePerGas?.toString(),
      gasPrice: feeData.gasPrice?.toString(),
    };
  }

  // Fetches and caches the MPC signer's address.
  private async signerAddress(): Promise<string> {
    if (this.cachedAddress) return this.cachedAddress;
    const url = process.env.MPC_SIGNER_URL ?? 'http://localhost:8080';
    const res = await lastValueFrom(
      this.http.get<{ address: string }>(`${url}/address`),
    );
    this.cachedAddress = res.data.address;
    return this.cachedAddress;
  }
}
