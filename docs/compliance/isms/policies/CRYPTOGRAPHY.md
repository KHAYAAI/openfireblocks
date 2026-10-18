# Cryptography and key management policy

ISO/IEC 27001:2022 A.8.24. SOC 2 CC6.1, CC6.7.
Version 0.1, draft, 11 October 2026. Owner: Engineering lead. **Approver: CEO. Not yet approved.** Review: yearly.

Detailed procedures: `docs/security/key-rotation.md`, `docs/deployment/KEY-RECOVERY.md`,
`docs/deployment/SHARE-STORE.md`, `docs/engineering/PKCS11-HSM-SIGNING.md`,
`docs/deployment/SEPARATE-HOSTS.md`.

## 1. Signing keys (the product)

1. Signing keys are threshold keys (2-of-3 ECDSA or Ed25519). No whole private key is ever
   assembled, stored or transmitted.
2. Each key share is sealed at rest: in Vault/OpenBao, an encrypted share store, or behind
   PKCS#11 in an HSM.
3. For production, the three parties run on **separate hosts, administered by different people**,
   so no single administrator can reach two shares. Until that is in place, the platform is used
   only for test networks or capped pilots.
4. Keys are generated only through the platform's distributed key generation; never imported.
5. Recovery from 2 of 3 parties is drilled at least yearly (`exercises/DR-TEST.md`).
6. Rotation: a new key is generated and funds are swept to it; retired shares are deleted after
   the retention window (30 days by default).

## 2. Algorithms and protocols

| Use | Standard |
|---|---|
| Transport | TLS 1.2 or higher (prefer 1.3); mTLS between internal services |
| Passwords | bcrypt, cost 12 (`services/api-gateway/src/identity/users.service.ts`) |
| Tokens | HMAC-SHA256 JWTs with short expiry and revocation (`jti` denylist) |
| Hashes for evidence | SHA-256 (for example the access-review snapshot hash) |
| Backups | Client-side encryption before they leave the cluster (`docs/deployment/OFFSITE-BACKUPS.md`) |
| Release integrity | Keyless cosign signatures and SBOMs for container images |

Deprecated or home-made cryptography is not allowed. New cryptographic dependencies need the
engineering lead's approval and an entry in the SBOM.

## 3. Secrets

Secrets live in a secrets manager or Kubernetes secrets sourced from one, are referenced by name,
and rotate as listed in `docs/security/key-rotation.md`. Any secret that is exposed (in a log,
chat, commit or screen share) is rotated the same day.

## 4. Company keys

Release-signing identity, SSH keys and cloud credentials belong to named people, are protected by
the device and MFA, and are revoked when the person leaves.
