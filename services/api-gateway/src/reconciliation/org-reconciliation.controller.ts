import { Body, Controller, Get, Param, Post, UseGuards } from '@nestjs/common';
import { IsArray, IsIn, IsInt, IsOptional, IsString, Min, ValidateIf } from 'class-validator';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { ReconciliationService } from './reconciliation.service';
import { StatementRow } from './reconcile';

class ChainRunDto {
  // An EVM chain id, or -- for a Solana or Cosmos key -- the blockchain name.
  // Exactly one of them.
  @ValidateIf((o) => o.blockchain === undefined) @IsInt() chainId?: number;
  @ValidateIf((o) => o.chainId === undefined) @IsIn(['solana', 'cosmos']) blockchain?: 'solana' | 'cosmos';
  @IsOptional() @IsInt() @Min(1) sinceHours?: number;
  @IsOptional() @IsInt() @Min(1) missingAfterMinutes?: number;
}

class StatementDto {
  @IsString() periodStart: string;
  @IsString() periodEnd: string;
  @IsArray() rows: StatementRow[];
}

// Reconciliation runs, for a named person -- the same engine
// /reconciliation exposes to an API key. Starting a run is restricted to
// admins: it is a compliance action with its own record, attributed to
// whoever asked for it, not to "api-key".
@Controller('organisations/:customerId/reconciliation')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgReconciliationController {
  constructor(private readonly recon: ReconciliationService) {}

  @Post('chain')
  @RequireTenantRole(...CAN_MANAGE)
  chain(@Param('customerId') customerId: string, @CurrentUser() claims: JwtClaims, @Body() dto: ChainRunDto) {
    const { blockchain, chainId, ...rest } = dto;
    return blockchain
      ? this.recon.runNative({ customerId, requestedBy: claims.email, blockchain, ...rest })
      : this.recon.runChain({ customerId, requestedBy: claims.email, chainId: chainId!, ...rest });
  }

  @Post('statements')
  @RequireTenantRole(...CAN_MANAGE)
  statement(@Param('customerId') customerId: string, @CurrentUser() claims: JwtClaims, @Body() dto: StatementDto) {
    return this.recon.runStatement({ customerId, requestedBy: claims.email, ...dto });
  }

  @Get('runs')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string) {
    return this.recon.list(customerId);
  }

  @Get('runs/:runId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  get(@Param('customerId') customerId: string, @Param('runId') runId: string) {
    return this.recon.get(customerId, runId);
  }
}
