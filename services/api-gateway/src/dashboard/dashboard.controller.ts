import {
  Body,
  Controller,
  Get,
  Post,
  Query,
  Param,
  Req,
  Res,
  Logger,
} from '@nestjs/common';
import { Throttle } from '@nestjs/throttler';
import type { Request, Response } from 'express';
import { CustomerService, Customer } from '../customers/customer.service';
import { PostgresService } from '../database/postgres.service';
import { KeysService } from '../keys/keys.service';
import { DashboardService } from './dashboard.service';
import { clearSessionCookie, issueSession, readSession } from './session';
import { TokenRevocationService } from '../identity/token-revocation.service';
import { layout, loginPage, Nav } from './views';
import {
  compliancePage,
  keyDetailPage,
  keysPage,
  overviewPage,
  transactionsPage,
  webhooksPage,
} from './pages';

// The dashboard.
//
// Every route here is read-only. That is deliberate for a first version:
// the things a dashboard is for -- seeing balances, reading a transaction
// history, understanding a refusal -- are all reads, and a read-only
// surface cannot be the thing that moves money by mistake. Provisioning a
// key and sending a transfer stay on the API, where policy, idempotency
// and the audit trail already live and are already tested.
//
// Authentication is the tenant's own API key, exchanged once for a signed
// session cookie. Note what that means: the dashboard grants exactly what
// the API key grants, and every query below is tenant-scoped through the
// same row-level security as the API. There is no separate dashboard
// authorisation model to get wrong.
@Controller('dashboard')
export class DashboardController {
  private readonly logger = new Logger(DashboardController.name);

  constructor(
    private readonly customers: CustomerService,
    private readonly postgres: PostgresService,
    private readonly keys: KeysService,
    private readonly data: DashboardService,
    // Optional like elsewhere in this codebase: without it, sign-out
    // still clears the browser's cookie, it just cannot also revoke the
    // session server-side (the same gap AUTH-04 is about, just not
    // fixed in this deployment -- see TokenRevocationService's own
    // comment on why that is a logged, not silent, degradation).
    private readonly revocation?: TokenRevocationService,
  ) {}

  private html(res: Response, body: string, status = 200) {
    res.status(status).type('html').send(body);
  }

  // Resolves the signed-in customer, or null. Also the enforcement point
  // for AUTH-04: a session's token is checked against the revocation
  // denylist on every request, which is what makes sign-out (and an
  // administrative revoke) actually end a session instead of only
  // clearing the browser's copy of the cookie while the exact same value
  // keeps authenticating if replayed.
  private async current(req: Request): Promise<Customer | null> {
    const session = readSession(req.headers.cookie);
    if (!session) {
      return null;
    }
    if (typeof this.revocation?.isRevoked === 'function' && (await this.revocation.isRevoked(session.token))) {
      return null;
    }
    try {
      return await this.customers.getByCustomerId(session.customerId);
    } catch {
      // The session names a customer that no longer exists or is no longer
      // readable. Treated as signed out rather than as an error: the user
      // can do nothing about it except sign in again.
      return null;
    }
  }

  private navFor(customer: Customer, active: string): Nav {
    return { active, customerName: customer.name, tier: customer.tier };
  }

  @Get()
  async root(@Req() req: Request, @Res() res: Response) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    return res.redirect('/dashboard/overview');
  }

  @Get('sign-in')
  signInPage(@Req() req: Request, @Res() res: Response) {
    if (readSession(req.headers.cookie)) {
      return res.redirect('/dashboard/overview');
    }
    return this.html(res, loginPage());
  }

  // The same budget as the JSON login (10 a minute per address), not the
  // global 100 (AUTH-01, round 3): this exchanges an API key for a session
  // cookie, so it is a credential-guessing surface and was ten times looser
  // than the endpoint that guards passwords. Keys are 192-bit random, so this
  // limits stuffing of weak or leaked keys rather than blind brute force.
  @Post('sign-in')
  @Throttle({ default: { limit: 10, ttl: 60_000 } })
  async signIn(@Body() body: { apiKey?: string }, @Res() res: Response) {
    const apiKey = (body?.apiKey ?? '').trim();
    if (!apiKey) {
      return this.html(res, loginPage('Enter an API key.'), 400);
    }

    const customer = await this.customers.getByApiKey(apiKey);
    if (!customer) {
      // One message for "no such key" and "key belongs to a suspended
      // account". Distinguishing them tells an attacker which keys exist.
      return this.html(res, loginPage('That API key was not accepted.'), 401);
    }

    const { cookie } = issueSession(customer.customer_id);
    res.setHeader('Set-Cookie', cookie);
    return res.redirect(303, '/dashboard/overview');
  }

  // Sign-out changes state (it denylists the session server-side), so it is a
  // POST and never a GET (AUTHZ-02, round 2): as a GET, any link, prefetch or
  // embedded request a third party could get a signed-in browser to make would
  // sign that user out. The session cookie is SameSite=Strict, which already
  // keeps a cross-site POST from carrying it; the Origin check below is the
  // second layer for any same-site context. A GET here only sends the person
  // back to the page, and changes nothing.
  @Get('sign-out')
  signOutPage(@Res() res: Response) {
    return res.redirect(303, '/dashboard/overview');
  }

  @Post('sign-out')
  async signOut(@Req() req: Request, @Res() res: Response) {
    const origin = req.headers.origin;
    if (origin && typeof origin === 'string') {
      let originHost = '';
      try {
        originHost = new URL(origin).host;
      } catch {
        originHost = '';
      }
      if (originHost !== req.headers.host) {
        return res.status(403).send('cross-origin sign-out refused');
      }
    }
    const session = readSession(req.headers.cookie);
    if (session) {
      const remainingSeconds = Math.ceil((session.expiresAt - Date.now()) / 1000);
      if (typeof this.revocation?.revoke === 'function') {
        await this.revocation.revoke(session.token, remainingSeconds);
      }
    }
    res.setHeader('Set-Cookie', clearSessionCookie());
    return res.redirect(303, '/dashboard/sign-in');
  }

  @Get('overview')
  async overview(@Req() req: Request, @Res() res: Response) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    const data = await this.data.overview(customer);
    return this.html(res, overviewPage(this.navFor(customer, 'overview'), data));
  }

  @Get('keys')
  async keysList(@Req() req: Request, @Res() res: Response) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    const rows = await this.postgres.listKeys(customer.customer_id);
    return this.html(res, keysPage(this.navFor(customer, 'keys'), rows as never));
  }

  @Get('keys/:keyId')
  async keyDetail(
    @Req() req: Request,
    @Res() res: Response,
    @Param('keyId') keyId: string,
    @Query('chainId') chainId?: string,
  ) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    const detail = await this.data.keyDetail(customer, keyId, chainId ? Number(chainId) : undefined);
    if (!detail) {
      return this.html(
        res,
        layout(
          'Not found',
          this.navFor(customer, 'keys'),
          `<h1>No such key</h1><p class="sub">This account has no key ${keyId}.</p>`,
        ),
        404,
      );
    }
    return this.html(res, keyDetailPage(this.navFor(customer, 'keys'), detail));
  }

  @Get('transactions')
  async transactions(@Req() req: Request, @Res() res: Response) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    const rows = await this.postgres.listTransactions(customer.customer_id, 200);
    return this.html(
      res,
      transactionsPage(this.navFor(customer, 'transactions'), rows as never),
    );
  }

  @Get('compliance')
  async compliance(
    @Req() req: Request,
    @Res() res: Response,
    @Query('day') day?: string,
    @Query('chain') chain?: string,
  ) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    const data = await this.data.compliance(customer, day, chain);
    return this.html(res, compliancePage(this.navFor(customer, 'compliance'), data));
  }

  @Get('webhooks')
  async webhooks(@Req() req: Request, @Res() res: Response) {
    const customer = await this.current(req);
    if (!customer) {
      return res.redirect('/dashboard/sign-in');
    }
    const { hooks, error } = await this.data.webhooks(customer);
    return this.html(res, webhooksPage(this.navFor(customer, 'webhooks'), hooks, error));
  }
}
