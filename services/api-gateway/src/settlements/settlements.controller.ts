import {
  Body,
  Controller,
  Get,
  HttpCode,
  HttpStatus,
  Param,
  Post,
  UseGuards,
} from '@nestjs/common';
import { IsBoolean } from 'class-validator';
import { TemporalService } from './temporal.service';
import { SignRequestDto } from '../sign/dto/sign-request.dto';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';

class ApprovalDto {
  @IsBoolean()
  approved: boolean;
}

// Durable settlement API: starts a Temporal workflow that runs
// policy → sign → broadcast → monitor with retries and an approval gate.
// Use this (instead of synchronous /sign) when settlement finality must survive
// crashes and when high-value transactions need a human approval step.
@Controller('settlements')
@UseGuards(ApiKeyGuard)
export class SettlementsController {
  constructor(private readonly temporal: TemporalService) {}

  @Post()
  @HttpCode(HttpStatus.ACCEPTED)
  start(@CurrentCustomer() customer: Customer, @Body() req: SignRequestDto) {
    return this.temporal.start(customer.customer_id, customer.tier, req);
  }

  @Get(':workflowId')
  status(
    @CurrentCustomer() customer: Customer,
    @Param('workflowId') workflowId: string,
  ) {
    return this.temporal.status(customer.customer_id, workflowId);
  }

  @Post(':workflowId/approve')
  @HttpCode(HttpStatus.OK)
  async approve(
    @CurrentCustomer() customer: Customer,
    @Param('workflowId') workflowId: string,
    @Body() dto: ApprovalDto,
  ) {
    await this.temporal.approve(customer.customer_id, workflowId, dto.approved);
    return { workflowId, approved: dto.approved };
  }
}
