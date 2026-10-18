-- Generic OpenID Connect sign-in (Keycloak, Entra ID, Okta, any standards-
-- compliant provider) for people, alongside the WorkOS path from migration 015.
--
-- A sovereign customer cannot use a SaaS identity broker: a bank or a
-- government has its own identity provider inside its own boundary. An OIDC
-- user is identified by the pair (issuer, subject) -- the subject alone is
-- only unique within one issuer.
ALTER TABLE users
  ADD COLUMN oidc_issuer  VARCHAR(512),
  ADD COLUMN oidc_subject VARCHAR(255);

ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
  CHECK (auth_provider IN ('password', 'workos_sso', 'oidc'));

ALTER TABLE users DROP CONSTRAINT users_credential_matches_provider_check;
ALTER TABLE users ADD CONSTRAINT users_credential_matches_provider_check
  CHECK (
    (auth_provider = 'password' AND password_hash IS NOT NULL)
    OR (auth_provider = 'workos_sso' AND workos_user_id IS NOT NULL)
    OR (auth_provider = 'oidc' AND oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL)
  );

-- Both halves or neither: an issuer with no subject (or the reverse) is a
-- half-made identity.
ALTER TABLE users ADD CONSTRAINT users_oidc_identity_pair_check
  CHECK ((oidc_issuer IS NULL) = (oidc_subject IS NULL));

CREATE UNIQUE INDEX idx_users_oidc_identity
  ON users (oidc_issuer, oidc_subject) WHERE oidc_issuer IS NOT NULL;
