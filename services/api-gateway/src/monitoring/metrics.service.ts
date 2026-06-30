import { Injectable } from '@nestjs/common';
import {
  Registry,
  Counter,
  Histogram,
  Gauge,
  collectDefaultMetrics,
} from 'prom-client';

// Owns the Prometheus registry and the platform's business metrics. Exposed via
// GET /metrics (MetricsController) and scraped by Prometheus.
@Injectable()
export class MetricsService {
  readonly registry = new Registry();

  readonly signRequests = new Counter({
    name: 'sign_requests_total',
    help: 'Total sign requests by outcome and chain',
    labelNames: ['status', 'chain'] as const,
    registers: [this.registry],
  });

  readonly signLatency = new Histogram({
    name: 'sign_latency_seconds',
    help: 'End-to-end sign request latency',
    buckets: [0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5],
    labelNames: ['chain'] as const,
    registers: [this.registry],
  });

  readonly policyDenials = new Counter({
    name: 'policy_denials_total',
    help: 'Total policy denials by reason',
    labelNames: ['policy_type'] as const,
    registers: [this.registry],
  });

  readonly riskDenials = new Counter({
    name: 'risk_denials_total',
    help: 'Total risk/velocity denials',
    labelNames: ['reason'] as const,
    registers: [this.registry],
  });

  readonly broadcastErrors = new Counter({
    name: 'broadcast_errors_total',
    help: 'Total broadcast errors',
    labelNames: ['chain', 'reason'] as const,
    registers: [this.registry],
  });

  readonly httpRequests = new Counter({
    name: 'http_requests_total',
    help: 'Total HTTP requests by method, route and status code',
    labelNames: ['method', 'route', 'status'] as const,
    registers: [this.registry],
  });

  constructor() {
    // Node process/GC/heap metrics, prefixed to avoid name clashes.
    collectDefaultMetrics({ register: this.registry, prefix: 'gateway_' });
  }

  metrics(): Promise<string> {
    return this.registry.metrics();
  }

  contentType(): string {
    return this.registry.contentType;
  }
}
