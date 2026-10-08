// Split out for the same reason as risk/redis-client.token.ts: the module
// that provides this needs the token, and the service that needs the
// token would otherwise import the module that imports the service,
// which Nest only catches at DI-scan time, not at tsc time.
export const AUTH_REDIS_CLIENT = 'AUTH_REDIS_CLIENT';
