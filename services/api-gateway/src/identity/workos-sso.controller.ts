import {
  BadRequestException,
  Controller,
  Get,
  Query,
  Res,
} from '@nestjs/common';
import { Response } from 'express';
import { WorkosSsoService } from './workos-sso.service';
import { OidcSsoService } from './oidc-sso.service';

// Enterprise SSO endpoints (WorkOS AuthKit), sitting alongside the local
// password + TOTP routes in AuthController. Both end in the same place: a
// session JWT this platform issued and JwtAuthStrategy already trusts.
@Controller('auth/sso')
export class WorkosSsoController {
  // Generic OpenID Connect takes precedence when configured: it is the path
  // for a customer that runs its own identity provider. WorkOS stays for
  // those that use it. Both end in the same session token.
  constructor(private readonly workos: WorkosSsoService, private readonly oidc: OidcSsoService) {}

  // Whether SSO is available, so a login UI can decide to show the button
  // at all rather than sending users into a 503.
  @Get('status')
  status() {
    const provider = this.oidc.isConfigured() ? 'oidc' : this.workos.isConfigured() ? 'workos' : null;
    return { enabled: provider !== null, provider };
  }

  // Step 1: redirect the browser to WorkOS. organizationId scopes login to
  // one customer's IdP connection; omit it to let AuthKit route by domain.
  @Get('authorize')
  async authorize(
    @Res() res: Response,
    @Query('organizationId') organizationId?: string,
    @Query('loginHint') loginHint?: string,
  ) {
    if (this.oidc.isConfigured()) {
      const { url } = await this.oidc.authorizationUrl({ loginHint });
      return res.redirect(url);
    }
    const { url } = this.workos.authorizationUrl({ organizationId, loginHint });
    return res.redirect(url);
  }

  // Step 2: WorkOS redirects back here with a code. Exchange it, resolve
  // (or provision) the local user, and hand back a session token.
  //
  // Returns JSON rather than redirecting with the token in a query string:
  // tokens in URLs end up in browser history, referrer headers, and access
  // logs. A browser-based UI should call this from its callback page and
  // keep the token wherever it keeps the password-flow one.
  @Get('callback')
  async callback(
    @Query('code') code?: string,
    @Query('state') state?: string,
    @Query('error') error?: string,
    @Query('error_description') errorDescription?: string,
  ) {
    if (error) {
      throw new BadRequestException(
        `SSO failed: ${error}${errorDescription ? ` (${errorDescription})` : ''}`,
      );
    }
    if (!code) {
      throw new BadRequestException('missing authorization code');
    }
    return this.oidc.isConfigured() ? this.oidc.completeLogin(code, state) : this.workos.completeLogin(code, state);
  }
}
