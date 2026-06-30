import { Module } from '@nestjs/common';
import { RiskService } from './risk.service';

// Velocity / risk controls. Exported for the sign flow.
@Module({
  providers: [RiskService],
  exports: [RiskService],
})
export class RiskModule {}
