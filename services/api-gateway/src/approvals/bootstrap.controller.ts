import { Body, ConflictException, Controller, ForbiddenException, Post, UseGuards } from '@nestjs/common';
import { v4 as uuid } from 'uuid';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { AuditService } from '../database/audit.service';
import { ApprovalsService } from './approvals.service';
import { MemberDto } from './approvals.controller';

// The one thing an API key may do with membership: name the
// organisation's first admin, once.
//
// After that, only admins -- people, signed in -- manage who can approve.
// If the API key could keep granting roles, whoever held it could make
// accounts they control into approvers and approve their own machine's
// transfers, which is precisely what segregation of duties rules out.
@Controller('organisation')
@UseGuards(ApiKeyGuard)
export class OrganisationBootstrapController {
  constructor(
    private readonly approvals: ApprovalsService,
    private readonly audit: AuditService,
  ) {}

  @Post('first-admin')
  async firstAdmin(@CurrentCustomer() customer: Customer, @Body() dto: MemberDto) {
    if (dto.role !== 'admin') {
      throw new ForbiddenException('an API key can only appoint the first admin; admins appoint everyone else');
    }
    if ((await this.approvals.countAdmins(customer.customer_id)) > 0) {
      throw new ConflictException('this organisation already has an admin; roles are now managed by admins');
    }
    const member = await this.approvals.setMember(customer.customer_id, dto.email, 'admin');
    await this.audit.logEvent({
      type: 'membership.first_admin',
      requestId: uuid(),
      customerId: customer.customer_id,
      message: `API key appointed ${member.email} as first admin`,
      status: 'changed',
    });
    return member;
  }
}
