import { Controller, Get } from '@nestjs/common';

// Liveness/readiness endpoint used by Docker Compose and Kubernetes probes.
@Controller()
export class AppController {
  @Get('health')
  health() {
    return { status: 'ok', service: 'api-gateway' };
  }
}
