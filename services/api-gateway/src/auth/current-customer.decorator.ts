import { createParamDecorator, ExecutionContext } from '@nestjs/common';
import { AuthenticatedRequest } from './api-key.guard';
import { Customer } from '../customers/customer.service';

// Injects the authenticated tenant resolved by ApiKeyGuard into a handler param.
export const CurrentCustomer = createParamDecorator(
  (_data: unknown, ctx: ExecutionContext): Customer => {
    const req = ctx.switchToHttp().getRequest<AuthenticatedRequest>();
    return req.customer;
  },
);
