// Split out from database.module.ts: that file imports PostgresService and
// AuditService (to list them as providers), and both of those import the
// PG_POOL token — keeping the token in database.module.ts created a
// module.ts <-> service.ts circular import that only shows up at Nest's
// DI-scanning time (tsc doesn't catch it) as "Nest can't resolve dependencies
// of PostgresService (?)". Every provider that needs PG_POOL should import it
// from here, not from database.module.ts.
export const PG_POOL = 'PG_POOL';
