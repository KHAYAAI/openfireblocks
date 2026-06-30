import {
  CallHandler,
  ExecutionContext,
  Injectable,
  NestInterceptor,
} from '@nestjs/common';
import { Observable } from 'rxjs';
import { tap } from 'rxjs/operators';
import { Request, Response } from 'express';
import { MetricsService } from './metrics.service';

// Counts every HTTP request by method, route template and status code so we can
// chart RPS and error rates per endpoint in Grafana.
@Injectable()
export class MetricsInterceptor implements NestInterceptor {
  constructor(private readonly metrics: MetricsService) {}

  intercept(context: ExecutionContext, next: CallHandler): Observable<unknown> {
    const http = context.switchToHttp();
    const req = http.getRequest<Request>();
    // Prefer the route pattern (e.g. /sign) over the raw URL to bound cardinality.
    const route =
      (req.route?.path as string | undefined) ?? req.path ?? 'unknown';

    const record = () => {
      const res = http.getResponse<Response>();
      this.metrics.httpRequests.inc({
        method: req.method,
        route,
        status: String(res.statusCode),
      });
    };

    return next.handle().pipe(
      tap({ next: record, error: record }),
    );
  }
}
