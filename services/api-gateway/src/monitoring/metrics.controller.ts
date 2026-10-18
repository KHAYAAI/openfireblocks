import { Controller, Get, Headers, Res, UnauthorizedException } from '@nestjs/common';
import { SkipThrottle } from '@nestjs/throttler';
import { Response } from 'express';
import { timingSafeEqual } from 'crypto';
import { MetricsService } from './metrics.service';

// Prometheus scrape endpoint.
//
// With METRICS_TOKEN unset it stays unauthenticated, as it always was, so an existing
// in-cluster Prometheus keeps working: restrict it by network policy or mesh. With it
// set, a scrape must present it as a bearer token. Set it wherever the gateway can be
// reached from outside the cluster: the metrics describe routes, volumes and error rates,
// which is reconnaissance for anyone who can read them.
@Controller('metrics')
@SkipThrottle()
export class MetricsController {
  constructor(private readonly metrics: MetricsService) {}

  @Get()
  async scrape(@Res() res: Response, @Headers('authorization') authorization?: string) {
    const required = process.env.METRICS_TOKEN;
    if (required) {
      const given = (authorization ?? '').replace(/^Bearer\s+/i, '');
      const a = Buffer.from(given); const b = Buffer.from(required);
      if (a.length !== b.length || !timingSafeEqual(a, b)) throw new UnauthorizedException('metrics need a bearer token');
    }
    res.setHeader('Content-Type', this.metrics.contentType());
    res.send(await this.metrics.metrics());
  }
}
