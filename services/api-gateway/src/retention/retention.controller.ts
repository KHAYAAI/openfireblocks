import { Body, Controller, Get, HttpCode, Post, Query, UseGuards } from '@nestjs/common';
import { IsBoolean, IsInt, IsOptional, IsString, IsUUID, Length, Min } from 'class-validator';
import { AdminGuard } from '../auth/admin.guard';
import { RETENTION_FLOOR_DAYS, RetentionService } from './retention.service';

export class PurgeTravelRuleDto {
  @IsOptional() @IsInt() @Min(RETENTION_FLOOR_DAYS) retainDays?: number;
  @IsOptional() @IsUUID() customerId?: string;
  // Defaults to true: a purge deletes only when asked to, explicitly.
  @IsOptional() @IsBoolean() dryRun?: boolean;
  @IsString() @Length(3, 500) reason: string;
}

// Operator-only. Each run, dry or not, is written to the retention log.
@Controller('admin/retention')
@UseGuards(AdminGuard)
export class RetentionController {
  constructor(private readonly retention: RetentionService) {}

  @Post('travel-rule')
  @HttpCode(200)
  purge(@Body() dto: PurgeTravelRuleDto) {
    return this.retention.purgeTravelRule(dto);
  }

  @Get('purges')
  history(@Query('limit') limit?: string) {
    return this.retention.history(Number(limit ?? 50));
  }
}
