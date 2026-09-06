-- WorkOS SSO (AuthKit) support for dashboard users.
--
-- Until now every row in `users` came from the local password flow
-- (services/api-gateway/src/identity/users.service.ts), so password_hash
-- was NOT NULL. An SSO-provisioned user has no password at all -- the
-- identity provider holds the credential -- so that constraint has to
-- relax, and the row needs to record which provider owns it.
--
-- auth_provider is deliberately NOT NULL with a 'password' default: every
-- pre-existing row is a password user, and a row can never be silently
-- ambiguous about how it authenticates.
ALTER TABLE users
  ALTER COLUMN password_hash DROP NOT NULL,
  ADD COLUMN workos_user_id VARCHAR(255),
  ADD COLUMN workos_organization_id VARCHAR(255),
  ADD COLUMN auth_provider VARCHAR(50) NOT NULL DEFAULT 'password';

ALTER TABLE users
  ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('password', 'workos_sso'));

-- A password user must actually have a password; an SSO user must be
-- linked to a WorkOS identity. Neither half can be half-provisioned.
ALTER TABLE users
  ADD CONSTRAINT users_credential_matches_provider_check
    CHECK (
      (auth_provider = 'password' AND password_hash IS NOT NULL)
      OR
      (auth_provider = 'workos_sso' AND workos_user_id IS NOT NULL)
    );

CREATE UNIQUE INDEX idx_users_workos_user_id
  ON users(workos_user_id) WHERE workos_user_id IS NOT NULL;
