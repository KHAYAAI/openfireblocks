import { Body, Controller, Get, HttpCode, HttpStatus, Param, Post, UseGuards } from '@nestjs/common';
import { IsOptional, IsString, Matches, MaxLength } from 'class-validator';
import { JwtAuthGuard } from '../../identity/jwt-auth.guard';
import { CurrentUser } from '../../identity/current-user.decorator';
import { JwtClaims } from '../../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../../approvals/tenant-role.guard';
import { CAN_MANAGE, CAN_READ_APPROVALS } from '../../approvals/roles';
import { TrisaService } from './trisa.service';

class CounterpartyDto {
  @IsString() @MaxLength(200) name: string;
  @IsOptional() @IsString() @MaxLength(20) lei?: string;
  @IsString() @MaxLength(255) endpoint: string;
  @IsOptional() @IsString() @MaxLength(255) commonName?: string;
  @IsOptional() @IsString() @MaxLength(8000) sealingPublicKeyPem?: string;
}
class TrustDto { @IsString() @Matches(/^SHA256:[A-Za-z0-9+/]{43}$/) keySignature: string; }

// Who this organisation exchanges Travel Rule information with directly (TRISA), and
// what other providers have sent it. Adding a counterparty and trusting it are two
// different people's acts: personal data goes only to a counterparty a second person
// has checked, against the key signature the counterparty gave them out-of-band.
@Controller('organisations/:customerId/travel-rule/trisa')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class TrisaController {
  constructor(private readonly trisa: TrisaService) {}

  @Get('status')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  status() { return { configured: this.trisa.configured(), listening: this.trisa.boundPort ?? null }; }

  @Get('counterparties')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string) { return this.trisa.listCounterparties(customerId); }

  @Post('counterparties')
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  add(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: CounterpartyDto) {
    return this.trisa.addCounterparty(customerId, u.sub, dto);
  }

  @Post('counterparties/:id/fetch-key')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  fetchKey(@Param('customerId') customerId: string, @Param('id') id: string) { return this.trisa.fetchKey(customerId, id); }

  @Post('counterparties/:id/trust')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  trust(@Param('customerId') customerId: string, @Param('id') id: string, @CurrentUser() u: JwtClaims, @Body() dto: TrustDto) {
    return this.trisa.trust(customerId, id, u.sub, dto.keySignature);
  }

  @Post('counterparties/:id/revoke')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  revoke(@Param('customerId') customerId: string, @Param('id') id: string) { return this.trisa.revoke(customerId, id); }

  @Get('inbound')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  inbound(@Param('customerId') customerId: string) { return this.trisa.listInbound(customerId); }
}
