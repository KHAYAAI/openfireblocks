import { Injectable, Optional, UnauthorizedException } from '@nestjs/common';
import { PassportStrategy } from '@nestjs/passport';
import { ExtractJwt, Strategy } from 'passport-jwt';
import { JwtClaims } from './auth.service';
import { TokenRevocationService } from './token-revocation.service';

// Dev-only fallback so the service boots without extra setup locally; every
// real deployment must set JWT_SECRET (32+ random bytes) or tokens signed
// with this default would be forgeable by anyone who reads this file.
const DEV_ONLY_SECRET = 'dev-only-insecure-jwt-secret-do-not-use-in-production';

export function jwtSecret(): string {
  const secret = process.env.JWT_SECRET;
  if (!secret) {
    if (process.env.NODE_ENV === 'production') {
      throw new Error('JWT_SECRET must be set in production');
    }
    return DEV_ONLY_SECRET;
  }
  return secret;
}

@Injectable()
export class JwtAuthStrategy extends PassportStrategy(Strategy, 'jwt') {
  constructor(@Optional() private readonly revocation?: TokenRevocationService) {
    super({
      jwtFromRequest: ExtractJwt.fromAuthHeaderAsBearerToken(),
      ignoreExpiration: false,
      secretOrKey: jwtSecret(),
    });
  }

  // Runs after signature + expiry are already verified by passport-jwt.
  // Returning the claims attaches them to req.user. Also rejects a token
  // whose jti is on the revocation denylist (AUTH-03) -- a signature and
  // expiry that are still both valid is exactly the case a signed-out or
  // administratively-revoked token is in, which is the whole reason a
  // denylist exists rather than relying on expiry alone.
  //
  // Several live specs build this strategy through a minimal
  // Test.createTestingModule with a useMocker() fallback that auto-mocks
  // any unresolved provider as a generic jest-mock object -- not this
  // service's actual shape. this.revocation is then neither the real
  // service nor undefined, it is an object with no isRevoked method, and
  // calling it would throw on every authenticated request in those
  // suites. Checked structurally rather than trusted by type for exactly
  // that reason.
  async validate(payload: JwtClaims): Promise<JwtClaims> {
    if (!payload.sub || !payload.email) {
      throw new UnauthorizedException('malformed token');
    }
    if (typeof this.revocation?.isRevoked === 'function' && (await this.revocation.isRevoked(payload.jti))) {
      throw new UnauthorizedException('token has been revoked');
    }
    return payload;
  }
}
