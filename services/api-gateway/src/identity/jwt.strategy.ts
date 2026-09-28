import { Injectable, UnauthorizedException } from '@nestjs/common';
import { PassportStrategy } from '@nestjs/passport';
import { ExtractJwt, Strategy } from 'passport-jwt';
import { JwtClaims } from './auth.service';

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
  constructor() {
    super({
      jwtFromRequest: ExtractJwt.fromAuthHeaderAsBearerToken(),
      ignoreExpiration: false,
      secretOrKey: jwtSecret(),
    });
  }

  // Runs after signature + expiry are already verified by passport-jwt.
  // Returning the claims attaches them to req.user.
  validate(payload: JwtClaims): JwtClaims {
    if (!payload.sub || !payload.email) {
      throw new UnauthorizedException('malformed token');
    }
    return payload;
  }
}
