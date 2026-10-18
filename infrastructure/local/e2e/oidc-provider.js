// A real, independent OpenID Connect provider (node-oidc-provider, an
// OpenID-certified implementation) for testing the gateway's OIDC sign-in
// against something that was not written by the same person. Development
// only: its login form accepts any email and password.
const mod = require(process.env.OIDC_PROVIDER_PATH || 'oidc-provider');
const Provider = mod.Provider || mod.default || mod;
const port = Number(process.env.PORT || 18090);
const issuer = `http://127.0.0.1:${port}`;

const provider = new Provider(issuer, {
  clients: [{
    client_id: 'ofb-console', client_secret: 's3cr3t-for-tests',
    redirect_uris: [process.env.REDIRECT_URI], grant_types: ['authorization_code'], response_types: ['code'],
    token_endpoint_auth_method: 'client_secret_post',
  }],
  pkce: { required: () => true },
  features: { devInteractions: { enabled: true } },
  claims: { openid: ['sub'], email: ['email', 'email_verified'], profile: ['name'] },
  async findAccount(_ctx, id) {
    return { accountId: id, async claims() { return { sub: id, email: id, email_verified: process.env.EMAIL_VERIFIED !== 'false', name: id.split('@')[0] }; } };
  },
  cookies: { keys: ['e2e-only-key-one', 'e2e-only-key-two'] },
  // The consent step is skipped for a known first-party client.
  async loadExistingGrant(ctx) {
    const grantId = ctx.oidc.result?.consent?.grantId || ctx.oidc.session.grantIdFor(ctx.oidc.client.clientId);
    if (grantId) return ctx.oidc.provider.Grant.find(grantId);
    const grant = new ctx.oidc.provider.Grant({ clientId: ctx.oidc.client.clientId, accountId: ctx.oidc.session.accountId });
    grant.addOIDCScope('openid email profile');
    await grant.save();
    return grant;
  },
});
provider.listen(port, '127.0.0.1');
