import { Injectable } from '@nestjs/common';
import { AuthGuard } from '@nestjs/passport';

// Protects human-user (dashboard) routes with a JWT, as distinct from
// ApiKeyGuard (machine tenant auth) and AdminGuard (static admin token).
@Injectable()
export class JwtAuthGuard extends AuthGuard('jwt') {}
