import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { PolicyService } from './policy.service';
import { mtlsHttpOptions } from '../common/mtls';

// Wraps the HTTP client to the OPA policy-service. Presents a client
// certificate when MTLS_CERT_FILE/MTLS_KEY_FILE/MTLS_CA_FILE are set --
// policy evaluation gates every signing request, so it's one of the
// highest-value links to authenticate (the same reasoning that made
// temporal-worker -> policy-service the second mTLS link built).
@Module({
  imports: [HttpModule.register({ timeout: 5000, ...mtlsHttpOptions() })],
  providers: [PolicyService],
  exports: [PolicyService],
})
export class PolicyModule {}
