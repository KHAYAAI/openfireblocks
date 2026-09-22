import { createParamDecorator, ExecutionContext } from '@nestjs/common';
import { Request } from 'express';
import { JwtClaims } from './auth.service';

export interface AuthenticatedUserRequest extends Request {
  user: JwtClaims;
}

// Pulls the JWT claims JwtAuthGuard attached to the request, for use in a
// controller method parameter: @CurrentUser() user: JwtClaims.
export const CurrentUser = createParamDecorator(
  (_: unknown, ctx: ExecutionContext): JwtClaims => {
    const req = ctx.switchToHttp().getRequest<AuthenticatedUserRequest>();
    return req.user;
  },
);
