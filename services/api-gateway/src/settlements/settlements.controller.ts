import {
  Controller,
  Optional,
  Get,
  GoneException,
  HttpCode,
  HttpStatus,
  Param,
  Post,
  Body,
  UseGuards,
} from '@nestjs/common';
import { TemporalService } from './temporal.service';
import { SignRequestDto } from '../sign/dto/sign-request.dto';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { ControlsService } from '../controls/controls.service';

// Durable settlement API: starts a Temporal workflow that runs
// policy → sign → broadcast → monitor with retries and an approval gate.
// Use this (instead of synchronous /sign) when settlement finality must survive
// crashes and when high-value transactions need a human approval step.
@Controller('settlements')
@UseGuards(ApiKeyGuard)
export class SettlementsController {
  // Optional so tests that build the controller directly need not stub it.
  constructor(private readonly temporal: TemporalService, @Optional() private readonly controls?: ControlsService) {}

  @Post()
  @HttpCode(HttpStatus.ACCEPTED)
  async start(@CurrentCustomer() customer: Customer, @Body() req: SignRequestDto) {
    // A frozen organisation starts nothing, and an enforced whitelist applies
    // to settlements the same as to every other transfer.
    await this.controls?.assertCanSign(customer.customer_id);
    await this.controls?.assertDestinationAllowed(customer.customer_id, 'ethereum', req.to);
    return this.temporal.start(customer.customer_id, customer.tier, req);
  }

  @Get(':workflowId')
  status(
    @CurrentCustomer() customer: Customer,
    @Param('workflowId') workflowId: string,
  ) {
    return this.temporal.status(customer.customer_id, workflowId);
  }

  // Removed. This approved a transfer with the tenant's API key: the same
  // credential that started it, no record of which person approved, and
  // one approval always enough. Approvals are now made by named people
  // with the approver role, under the organisation's quorum -- see
  // ApprovalsController.
  @Post(':workflowId/approve')
  approve(): never {
    throw new GoneException(
      'approving with an API key has been removed; approvals are made by people with the approver role: ' +
        'POST /organisations/:customerId/approvals/:approvalId/decisions',
    );
  }
}
