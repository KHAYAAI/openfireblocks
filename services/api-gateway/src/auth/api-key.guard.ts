import {
  CanActivate,
  ExecutionContext,
  Injectable,
  UnauthorizedException,
} from '@nestjs/common';
import { Request } from 'express';
import { CustomerService, Customer } from '../customers/customer.service';

// Express request augmented with the resolved tenant.
export interface AuthenticatedRequest extends Request {
  customer: Customer;
}

// Authenticates requests by API key, supplied either as `Authorization: Bearer <key>`
// or `X-API-Key: <key>`. On success the resolved customer is attached to the
// request so downstream handlers can scope work to that tenant.
@Injectable()
export class ApiKeyGuard implements CanActivate {
  constructor(private readonly customers: CustomerService) {}

  async canActivate(context: ExecutionContext): Promise<boolean> {
    const req = context.switchToHttp().getRequest<AuthenticatedRequest>();
    const apiKey = this.extractKey(req);
    if (!apiKey) {
      throw new UnauthorizedException('missing API key');
    }

    const customer = await this.customers.getByApiKey(apiKey);
    if (!customer) {
      throw new UnauthorizedException('invalid or suspended API key');
    }

    req.customer = customer;
    return true;
  }

  private extractKey(req: Request): string | null {
    const header = req.headers['authorization'];
    if (header && header.startsWith('Bearer ')) {
      return header.slice('Bearer '.length).trim();
    }
    const apiKeyHeader = req.headers['x-api-key'];
    if (typeof apiKeyHeader === 'string' && apiKeyHeader.length > 0) {
      return apiKeyHeader;
    }
    return null;
  }
}
