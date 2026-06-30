# Threat Model

Scope: the OpenFireblocks MVP (api-gateway, mpc-signer, policy-service,
temporal-worker) and its data stores. Methodology: STRIDE per trust boundary.

## Trust boundaries

1. Internet → api-gateway (untrusted callers, authenticated by API key)
2. api-gateway → internal services (mpc-signer, policy-service, Temporal)
3. mpc-signer → Vault (key material)
4. services → datastores (PostgreSQL, immudb)

## Assets

| Asset | Sensitivity |
|-------|-------------|
| Signing key material | Critical — controls funds |
| Tenant API keys | High — impersonation |
| Audit trail | High — compliance / non-repudiation |
| Policy definitions | Medium — controls limits |

## STRIDE findings & mitigations

| Threat | Vector | Mitigation (status) |
|--------|--------|---------------------|
| **Spoofing** | Forged tenant identity | API-key auth guard; Keycloak/JWT planned. Admin endpoints behind a separate admin token (fail-closed). |
| **Tampering** | Altered audit records | immudb cryptographic ledger + PostgreSQL; tamper-evident proofs (immudb). |
| **Tampering** | Mutated transaction in flight | EIP-155 signing binds chainId; signed tx is immutable; broadcast verifies hash. |
| **Repudiation** | "I didn't authorize this" | Per-tenant audit trail of every lifecycle event with request ids. |
| **Information disclosure** | Key leakage | Keys live only in Vault / signer memory; never logged, never returned by the API. Containers run read-only, non-root. |
| **Information disclosure** | Cross-tenant data read | All queries scoped by `customer_id`; RLS scaffolding available; tenant-scoped read endpoints. |
| **Denial of service** | Request flood | Rate limiting at the gateway/ingress (Kong/Envoy) — **to be enabled**; HPA for capacity. |
| **Elevation of privilege** | Bypass policy | Policy check is mandatory and fail-closed in the gateway and the workflow before any signing. |

## Known gaps (tracked for Phase 2/3)

- Real MPC threshold signing (currently a single shared key) — **highest priority**.
- API-key hashing at rest (currently stored as-is for the MVP).
- Rate limiting / WAF not yet enabled by default.
- mTLS between internal services (service mesh) not yet enabled.
- OFAC/sanctions screening and velocity limits (risk engine) not yet built.

These gaps are why this build is **not** production-ready for customer funds; see
the audit checklist.
