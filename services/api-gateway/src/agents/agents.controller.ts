import {
  BadRequestException,
  Body,
  Controller,
  Get,
  HttpCode,
  HttpStatus,
  Inject,
  NotFoundException,
  Optional,
  Param,
  Post,
  Query,
  Req,
  ServiceUnavailableException,
  UseGuards,
} from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { withEvmNonceLock } from '../blockchain/evm-nonce-lock';
import {
  IsArray,
  IsInt,
  IsNumber,
  IsObject,
  IsOptional,
  IsString,
  Matches,
  MaxLength,
  Min,
} from 'class-validator';
import { v4 as uuid } from 'uuid';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { KeysService } from '../keys/keys.service';
import { CustomerService } from '../customers/customer.service';
import { TokenRegistryService } from '../tokens/token-registry.service';
import { AuditService } from '../database/audit.service';
import { parseUnits } from '../keys/erc20';
import type { TravelRuleInput } from '../travel-rule/travel-rule';
import { AgentsService } from './agents.service';
import { AgentKeyGuard, AgentRequest } from './agent-key.guard';

export class CreateAgentDto {
  @IsString() @MaxLength(120) name: string;
  @IsString() keyId: string;
  @IsArray() @IsString({ each: true }) allowedTokens: string[];
  @IsOptional() @IsArray() @Matches(/^0x[0-9a-fA-F]{40}$/, { each: true }) allowedRecipients?: string[];
  @IsNumber() perTransferLimitZar: number;
  @IsNumber() dailyLimitZar: number;
  @IsInt() expiresInDays: number;
}

export class AgentTransferDto {
  @IsString() token: string;
  @Matches(/^0x[0-9a-fA-F]{40}$/) recipient: string;
  @Matches(/^[0-9]+(\.[0-9]+)?$/) amount: string;
  @IsInt() chainId: number;
  // Omitted (the normal case for an AI agent): the platform reads the nonce and fees from
  // the chain when it signs, and broadcasts the transaction itself. Given: the caller owns
  // nonce and fees, and gets back a signed transaction to broadcast (the original contract).
  @IsOptional() @IsInt() @Min(0) nonce?: number;
  @IsOptional() @Matches(/^[0-9]+$/) gasPrice?: string;
  @IsOptional() @Matches(/^[0-9]+$/) maxFeePerGas?: string;
  @IsOptional() @Matches(/^[0-9]+$/) maxPriorityFeePerGas?: string;
  // Why the agent is paying: kept on the spend record for whoever reviews
  // the agent's history.
  @IsOptional() @IsString() @MaxLength(500) purpose?: string;
  @IsOptional() @IsString() idempotencyKey?: string;
  @IsOptional() @IsObject() travelRule?: TravelRuleInput;
}

// Organisation admins create, inspect and revoke agents.
@Controller('organisations/:customerId/agents')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class AgentsAdminController {
  constructor(
    private readonly agents: AgentsService,
    private readonly audit: AuditService,
  ) {}

  @Post()
  @RequireTenantRole(...CAN_MANAGE)
  async create(@Param('customerId') customerId: string, @CurrentUser() claims: JwtClaims, @Body() dto: CreateAgentDto) {
    const { agent, apiKey } = await this.agents.create({ ...dto, customerId, createdBy: claims.sub });
    await this.audit.logEvent({
      type: 'agent.created',
      requestId: agent.agentId,
      customerId,
      message: `${claims.email} created agent "${agent.name}": ${agent.allowedTokens.join('/')}, R${agent.perTransferLimitZar}/transfer, R${agent.dailyLimitZar}/24h, until ${agent.expiresAt}`,
      status: 'created',
    });
    // The key is shown once, here, and never again.
    return { ...agent, apiKey };
  }

  @Get()
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string) {
    return this.agents.list(customerId);
  }

  @Get(':agentId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  get(@Param('customerId') customerId: string, @Param('agentId') agentId: string) {
    return this.agents.get(customerId, agentId);
  }

  @Post(':agentId/revoke')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  async revoke(@Param('customerId') customerId: string, @Param('agentId') agentId: string, @CurrentUser() claims: JwtClaims) {
    const agent = await this.agents.revoke(customerId, agentId, claims.sub);
    await this.audit.logEvent({
      type: 'agent.revoked',
      requestId: agentId,
      customerId,
      message: `${claims.email} revoked agent "${agent.name}"`,
      status: 'revoked',
    });
    return agent;
  }
}

// The agent's own surface: what it may spend, and spending it.
@Controller('agent')
@UseGuards(AgentKeyGuard)
export class AgentController {
  constructor(
    private readonly agents: AgentsService,
    private readonly keys: KeysService,
    private readonly customers: CustomerService,
    private readonly tokens: TokenRegistryService,
    private readonly rpc: EvmRpcService,
    // For the per-key nonce lock shared with every other EVM sending path.
    @Optional() @Inject(PG_POOL) private readonly pool?: Pool,
  ) {}

  @Get('me')
  async me(@Req() req: AgentRequest) {
    const { agent } = req;
    return {
      agentId: agent.agentId,
      name: agent.name,
      keyId: agent.keyId,
      allowedTokens: agent.allowedTokens,
      allowedRecipients: agent.allowedRecipients,
      perTransferLimitZar: agent.perTransferLimitZar,
      dailyLimitZar: agent.dailyLimitZar,
      expiresAt: agent.expiresAt,
      remaining24hZar: (await this.agents.record(agent.customerId, agent)).remaining24hZar,
    };
  }

  // The tokens this agent may pay in, with the chain and contract each lives on: what an
  // agent needs in order to ask for a payment it is allowed to make.
  @Get('tokens')
  async allowedTokens(@Req() req: AgentRequest) {
    const allowed = new Set(req.agent.allowedTokens.map((t) => t.toUpperCase()));
    const all = await this.tokens.list();
    return {
      tokens: all
        .filter((t) => allowed.has(t.symbol.toUpperCase()) && t.status === 'verified')
        .map((t) => ({
          symbol: t.symbol,
          name: t.name,
          chainId: t.chainId,
          contractAddress: t.contractAddress,
          decimals: t.decimals,
          pegCurrency: t.pegCurrency,
          platformBroadcasts: this.rpc.configured(t.chainId),
        })),
    };
  }

  // The agent's own history, newest first.
  @Get('transfers')
  async history(@Req() req: AgentRequest, @Query('limit') limit?: string) {
    return { transfers: await this.agents.spends(req.agent, Number(limit ?? 20)) };
  }

  // One payment: what the platform recorded, and, given the chain, what the chain says.
  @Get('transfers/:requestId')
  async status(@Req() req: AgentRequest, @Param('requestId') requestId: string, @Query('chainId') chainId?: string) {
    const spend = await this.agents.spend(req.agent, requestId);
    if (!spend) throw new NotFoundException(`no payment ${requestId} for this agent`);
    const chain = Number(chainId);
    if (!spend.txHash || !Number.isInteger(chain) || chain <= 0 || !this.rpc.configured(chain)) {
      return { ...spend, onChain: null };
    }
    const receipt = await this.rpc.provider(chain).getTransactionReceipt(spend.txHash);
    return {
      ...spend,
      onChain: receipt
        ? { chainId: chain, state: receipt.status === 1 ? 'confirmed' : 'reverted', blockNumber: receipt.blockNumber, confirmations: await receipt.confirmations() }
        : { chainId: chain, state: 'pending', blockNumber: null, confirmations: 0 },
    };
  }

  // Pay. The budget is reserved before signing and released if signing
  // fails; the transfer itself goes through the same route a person's does
  // -- registered token, policy, Travel Rule, threshold signature.
  @Post('transfers')
  @HttpCode(HttpStatus.OK)
  async transfer(@Req() req: AgentRequest, @Body() dto: AgentTransferDto) {
    const { agent } = req;
    const token = await this.tokens.requireTransactable(dto.chainId, dto.token);
    // Checked before any budget is reserved: an agent that cannot be served should not
    // leave a released reservation behind for every attempt.
    const platformPrepared = dto.nonce === undefined;
    if (platformPrepared && !this.rpc.configured(dto.chainId)) {
      throw new ServiceUnavailableException(
        `the platform has no RPC endpoint for chain ${dto.chainId} (EVM_RPC_${dto.chainId}), so it cannot read the nonce and fees or broadcast; pass nonce and fees to receive a signed transaction instead`,
      );
    }
    const requestId = dto.idempotencyKey ?? uuid();
    const reservation = await this.agents.reserve(agent, {
      requestId,
      token: token.symbol,
      tokenDecimals: token.decimals,
      tokenPeg: token.pegCurrency ?? null,
      recipient: dto.recipient,
      amountBaseUnits: parseUnits(dto.amount, token.decimals),
      purpose: dto.purpose,
    });
    const customer = await this.customers.getByCustomerId(agent.customerId);
    try {
      const { purpose, ...transfer } = dto;
      void purpose;
      const out = platformPrepared
        ? await this.signAndBroadcast(customer, agent.keyId, agent.customerId, { ...transfer, token: token.symbol, idempotencyKey: requestId })
        : { ...(await this.keys.sendToken(customer, agent.keyId, { ...transfer, token: token.symbol, idempotencyKey: requestId } as any)), broadcast: false };
      await this.agents.settle(agent, reservation.spendId, { txHash: out.transaction_hash });
      return { ...out, agent: { agentId: agent.agentId, valueZar: reservation.valueZar, remaining24hZar: reservation.remaining24hZar } };
    } catch (err) {
      await this.agents.settle(agent, reservation.spendId, { failed: (err as Error).message.slice(0, 500) });
      throw err;
    }
  }

  // Nonce and fees read from the chain under the key's nonce lock, signed through the
  // ordinary token route (registry, policy, Travel Rule, threshold signature), then
  // broadcast before the lock is released.
  private async signAndBroadcast(customer: any, keyId: string, customerId: string, transfer: Record<string, any>) {
    const key = await this.keys.getKey(keyId, customerId);
    if (!key?.address) throw new BadRequestException(`key ${keyId} has no address yet`);
    const provider = this.rpc.provider(transfer.chainId);
    return withEvmNonceLock(this.pool, transfer.chainId, key.address, async () => {
      const [nonce, fee] = await Promise.all([provider.getTransactionCount(key.address, 'pending'), provider.getFeeData()]);
      const fees = transfer.gasPrice || transfer.maxFeePerGas
        ? {}
        : fee.maxFeePerGas != null && fee.maxPriorityFeePerGas != null
          ? { maxFeePerGas: fee.maxFeePerGas.toString(), maxPriorityFeePerGas: fee.maxPriorityFeePerGas.toString() }
          : { gasPrice: (fee.gasPrice ?? 0n).toString() };
      const signed = await this.keys.sendToken(customer, keyId, { ...transfer, ...fees, nonce } as any);
      const sent = await provider.broadcastTransaction(signed.raw_transaction);
      return { ...signed, transaction_hash: sent.hash, broadcast: true };
    });
  }
}
