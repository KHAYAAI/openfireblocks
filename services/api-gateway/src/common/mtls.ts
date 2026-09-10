import { Agent } from 'https';
import { readFileSync } from 'fs';

// Node counterpart to services/mpc-party/mtls.go's clientTLSConfigFromEnv and
// services/temporal-worker/activities/mtls.go -- same three-env-var opt-in
// convention (MTLS_CERT_FILE / MTLS_KEY_FILE / MTLS_CA_FILE), same certs, since
// both sides are configured from the same Vault-PKI-issued bundle in practice
// (infrastructure/terraform/modules/vault-pki, issued per-pod by
// services/vault-pki-init).
//
// Precedence, matching the Go implementations exactly:
//   - none of the three set: mTLS is off, plain HTTP. A valid, if less secure,
//     local-dev configuration -- not an error.
//   - all three set: an https.Agent presenting this service's client
//     certificate and trusting only the given CA.
//   - partially set, or files unreadable: throws. A half-configured mTLS setup
//     silently falling back to plaintext is the failure mode worth being loud
//     about -- the same reason NewActivities calls log.Fatalf on this case.
export const MTLS_CERT_FILE = 'MTLS_CERT_FILE';
export const MTLS_KEY_FILE = 'MTLS_KEY_FILE';
export const MTLS_CA_FILE = 'MTLS_CA_FILE';

export interface MtlsAgentResult {
  agent: Agent | undefined;
  enabled: boolean;
}

export function mtlsAgentFromEnv(env: NodeJS.ProcessEnv = process.env): MtlsAgentResult {
  const certFile = env[MTLS_CERT_FILE];
  const keyFile = env[MTLS_KEY_FILE];
  const caFile = env[MTLS_CA_FILE];

  const set = [certFile, keyFile, caFile].filter(Boolean).length;
  if (set === 0) {
    return { agent: undefined, enabled: false };
  }
  if (set !== 3) {
    throw new Error(
      `mTLS is partially configured: set all of ${MTLS_CERT_FILE}, ${MTLS_KEY_FILE}, ${MTLS_CA_FILE}, or none of them`,
    );
  }

  let cert: Buffer;
  let key: Buffer;
  let ca: Buffer;
  try {
    cert = readFileSync(certFile as string);
    key = readFileSync(keyFile as string);
    ca = readFileSync(caFile as string);
  } catch (err) {
    throw new Error(`failed to read mTLS material: ${(err as Error).message}`);
  }

  return {
    agent: new Agent({
      cert,
      key,
      ca,
      // Explicit rather than relying on the default: the CA above is the only
      // trust anchor for these internal links, and the server's certificate
      // must genuinely chain to it.
      rejectUnauthorized: true,
    }),
    enabled: true,
  };
}

// Axios/HttpModule config fragment. Returns {} when mTLS isn't configured so
// callers can spread it unconditionally.
export function mtlsHttpOptions(env: NodeJS.ProcessEnv = process.env): { httpsAgent?: Agent } {
  const { agent } = mtlsAgentFromEnv(env);
  return agent ? { httpsAgent: agent } : {};
}
