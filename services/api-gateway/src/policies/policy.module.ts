import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { PolicyService } from './policy.service';

// Wraps the HTTP client to the OPA policy-service.
@Module({
  imports: [HttpModule.register({ timeout: 5000 })],
  providers: [PolicyService],
  exports: [PolicyService],
})
export class PolicyModule {}
