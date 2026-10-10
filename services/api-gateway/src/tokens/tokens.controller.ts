import {
  Body,
  Controller,
  Get,
  HttpCode,
  HttpStatus,
  Param,
  Post,
  Query,
  UseGuards,
} from '@nestjs/common';
import { TokenRegistryService } from './token-registry.service';
import { AdminGuard } from '../auth/admin.guard';
import { ApiKeyGuard } from '../auth/api-key.guard';
import {
  RegisterTokenDto,
  SetTokenAddressDto,
  SuspendTokenDto,
} from './dto/register-token.dto';

// Operator-facing. Registering a contract as a token the platform will
// move money through is a decision somebody makes on the record, not
// something a tenant does for themselves -- the whole value of the
// registry is that a symbol means the same contract for everybody.
@Controller('admin/tokens')
@UseGuards(AdminGuard)
export class AdminTokensController {
  constructor(private readonly registry: TokenRegistryService) {}

  @Get()
  async list(@Query('chainId') chainId?: string) {
    return { tokens: await this.registry.list(chainId ? Number(chainId) : undefined) };
  }

  @Post()
  @HttpCode(HttpStatus.CREATED)
  async register(@Body() dto: RegisterTokenDto) {
    const token = await this.registry.register(dto);
    return {
      token,
      // Said plainly, because a token that looks registered and silently
      // refuses to transact is a confusing half hour for whoever comes
      // next.
      next_step:
        token.status === 'awaiting_address'
          ? 'Record the contract address obtained from the issuer, then verify.'
          : 'Run POST /admin/tokens/:tokenId/verify before this token can be used.',
    };
  }

  @Post(':tokenId/address')
  async setAddress(@Param('tokenId') tokenId: string, @Body() dto: SetTokenAddressDto) {
    return {
      token: await this.registry.setAddress(tokenId, dto.contractAddress),
      next_step: 'Run POST /admin/tokens/:tokenId/verify before this token can be used.',
    };
  }

  // Asks the contract what it is. This is the only way a token becomes
  // transactable.
  @Post(':tokenId/verify')
  @HttpCode(HttpStatus.OK)
  async verify(@Param('tokenId') tokenId: string) {
    return { token: await this.registry.verify(tokenId) };
  }

  @Post(':tokenId/suspend')
  @HttpCode(HttpStatus.OK)
  async suspend(@Param('tokenId') tokenId: string, @Body() dto: SuspendTokenDto) {
    return { token: await this.registry.suspend(tokenId, dto.reason) };
  }
}

// Tenant-facing, read-only. A customer needs to know which tokens they can
// send and what the platform believes their decimals are, because that is
// what their own amount arithmetic has to agree with.
@Controller('tokens')
@UseGuards(ApiKeyGuard)
export class TokensController {
  constructor(private readonly registry: TokenRegistryService) {}

  @Get()
  async list(@Query('chainId') chainId?: string) {
    const tokens = await this.registry.list(chainId ? Number(chainId) : undefined);
    return {
      tokens: tokens.map((t) => ({
        chain_id: t.chainId,
        symbol: t.symbol,
        name: t.name,
        decimals: t.decimals,
        peg_currency: t.pegCurrency,
        contract_address: t.contractAddress,
        issuer: t.issuer,
        // Exposed rather than filtered down to the usable ones: a customer
        // asking why they cannot send ZARP is better served by seeing it
        // listed as awaiting an address than by it being absent.
        transactable: t.status === 'verified',
        status: t.status,
      })),
    };
  }
}
