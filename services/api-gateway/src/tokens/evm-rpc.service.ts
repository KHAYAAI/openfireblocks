import { Injectable, Logger } from '@nestjs/common';
import { ethers } from 'ethers';

// One JSON-RPC provider per chain.
//
// EthereumService holds a single provider for one hard-coded environment
// variable (ETHEREUM_RPC_SEPOLIA), which was adequate while the platform
// transacted on one testnet. Tokens are not: the same symbol exists on
// Ethereum, Polygon, Base and Arbitrum as four different contracts, and a
// balance read against the wrong chain returns zero rather than an error
// -- which reads, to a customer, exactly like their money not being there.
//
// Configuration is EVM_RPC_<chainId>, so adding a chain is an environment
// variable rather than a deploy. A chain with no configured endpoint is
// unreadable, and this service says so plainly instead of returning a
// default that would be silently wrong.
@Injectable()
export class EvmRpcService {
  private readonly logger = new Logger(EvmRpcService.name);
  private readonly providers = new Map<number, ethers.JsonRpcProvider>();

  // Reading a token's own metadata should not hang a request for a minute
  // when a node is unresponsive. Verification and balance display are both
  // better off failing quickly and saying which chain was unreachable.
  private readonly timeoutMs = Number(process.env.EVM_RPC_TIMEOUT_MS ?? 10_000);

  private urlFor(chainId: number): string | null {
    const explicit = process.env[`EVM_RPC_${chainId}`];
    if (explicit && !explicit.includes('YOUR_KEY')) {
      return explicit;
    }
    // Backwards compatibility with the single-provider configuration that
    // predates this service, so an existing Sepolia deployment keeps
    // working without a new variable.
    if (chainId === 11155111) {
      const legacy = process.env.ETHEREUM_RPC_SEPOLIA;
      if (legacy && !legacy.includes('YOUR_KEY')) {
        return legacy;
      }
    }
    return null;
  }

  configured(chainId: number): boolean {
    return this.urlFor(chainId) !== null;
  }

  provider(chainId: number): ethers.JsonRpcProvider {
    const cached = this.providers.get(chainId);
    if (cached) {
      return cached;
    }
    const url = this.urlFor(chainId);
    if (!url) {
      throw new Error(
        `no JSON-RPC endpoint configured for chain ${chainId}; set EVM_RPC_${chainId}`,
      );
    }
    // staticNetwork: the chain id is known from configuration, so there is
    // no reason to spend a round trip discovering it -- and a provider that
    // auto-detects will happily attach to whatever chain the endpoint
    // actually serves, which is how a mainnet key ends up reading testnet
    // balances.
    const provider = new ethers.JsonRpcProvider(url, chainId, {
      staticNetwork: ethers.Network.from(chainId),
    });
    this.providers.set(chainId, provider);
    return provider;
  }

  // A read-only contract call. Returns the raw 32-byte-aligned hex result.
  async call(chainId: number, to: string, data: string): Promise<string> {
    const provider = this.provider(chainId);
    const timeout = new Promise<never>((_, reject) =>
      setTimeout(
        () => reject(new Error(`chain ${chainId} did not answer within ${this.timeoutMs}ms`)),
        this.timeoutMs,
      ),
    );
    return Promise.race([provider.call({ to, data }), timeout]);
  }
}
