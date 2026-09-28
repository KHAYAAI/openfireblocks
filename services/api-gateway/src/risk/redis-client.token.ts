// Split out from risk.module.ts: that file imports RiskService, and
// risk.service.ts needs this token, so keeping the token in risk.module.ts
// created a module.ts <-> service.ts circular import that only shows up at
// Nest's DI-scanning time (tsc doesn't catch it) as "circular dependency
// detected inside RiskModule".
export const REDIS_CLIENT = 'REDIS_CLIENT';
