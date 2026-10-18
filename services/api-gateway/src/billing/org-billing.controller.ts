import { Controller, Get, HttpCode, HttpStatus, Param, Post, ServiceUnavailableException, UseGuards } from '@nestjs/common';
import { CustomerService } from '../customers/customer.service';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_BILL } from '../approvals/roles';
import { BillingClient } from './billing-client.service';

// How an organisation pays, for the console: which card is on file, and a
// link to Stripe's hosted page to save one. The card is entered on Stripe's
// page, never ours, so this stays outside PCI card-entry scope and the
// console's script policy is unchanged.
@Controller('organisations/:customerId/billing')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgBillingController {
  constructor(private readonly billing: BillingClient, private readonly customers: CustomerService) {}

  @Get()
  @RequireTenantRole(...CAN_BILL)
  async status(@Param('customerId') customerId: string) {
    return this.billing.cardOnFile(customerId);
  }

  @Post('card-session')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_BILL)
  async cardSession(@Param('customerId') customerId: string) {
    // Where Stripe sends the person back to. Set from deployment config, not
    // from the request: a caller-chosen address would turn a genuine Stripe
    // page into a redirect to anywhere.
    const origin = (process.env.CONSOLE_PUBLIC_URL ?? '').trim().replace(/\/+$/, '');
    if (!origin.startsWith('https://')) {
      throw new ServiceUnavailableException('saving a card needs CONSOLE_PUBLIC_URL set to this console\'s https address');
    }
    const customer = await this.customers.getByCustomerId(customerId);
    const s = await this.billing.cardSession({
      customerId,
      email: customer.email ?? '',
      name: customer.name,
      successUrl: `${origin}/console#/billing?card=saved`,
      cancelUrl: `${origin}/console#/billing?card=cancelled`,
    });
    return { url: s.url };
  }
}
