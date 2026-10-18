import {
  CanActivate,
  ExecutionContext,
  ForbiddenException,
  Injectable,
  NotFoundException,
  SetMetadata,
  UnauthorizedException,
} from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { JwtClaims } from '../identity/auth.service';
import { ApprovalsService } from './approvals.service';
import { TenantRole } from './roles';
import { UUID_RE } from './tenant-db';

const ROLES_KEY = 'ofb:tenantRoles';

// @RequireTenantRole(...roles): the signed-in person must hold one of
// these roles in the organisation named by the route's :customerId.
export const RequireTenantRole = (...roles: readonly TenantRole[]) => SetMetadata(ROLES_KEY, roles);

export interface TenantScopedRequest {
  user: JwtClaims;
  params: Record<string, string>;
  tenantRole?: TenantRole;
}

// Role-based access for people, per organisation.
//
// Runs after JwtAuthGuard, which establishes who the person is. This
// establishes what they may do *here*: roles live in user_customer_roles,
// one per person per organisation, not in the JWT. A token carrying a
// role would keep carrying it for an hour after an admin revoked it; a
// lookup per request does not.
//
// A person with no role in the organisation gets 404, not 403 -- whether
// an organisation id exists is not something to confirm to outsiders.
@Injectable()
export class TenantRoleGuard implements CanActivate {
  constructor(
    private readonly reflector: Reflector,
    private readonly approvals: ApprovalsService,
  ) {}

  async canActivate(ctx: ExecutionContext): Promise<boolean> {
    const allowed = this.reflector.getAllAndOverride<readonly TenantRole[]>(ROLES_KEY, [
      ctx.getHandler(),
      ctx.getClass(),
    ]);
    if (!allowed || allowed.length === 0) {
      // A route under this guard with no roles declared is a mistake;
      // refuse rather than default to open.
      throw new ForbiddenException('route has no role requirement declared');
    }
    const req = ctx.switchToHttp().getRequest<TenantScopedRequest>();
    if (!req.user?.sub) throw new UnauthorizedException();
    const customerId = req.params.customerId;
    if (!customerId || !UUID_RE.test(customerId)) throw new NotFoundException('organisation not found');

    const role = await this.approvals.roleOf(req.user.sub, customerId);
    if (!role) throw new NotFoundException('organisation not found');
    if (!allowed.includes(role)) {
      throw new ForbiddenException(`your role here (${role}) cannot do this; it needs one of: ${allowed.join(', ')}`);
    }
    req.tenantRole = role;
    return true;
  }
}
