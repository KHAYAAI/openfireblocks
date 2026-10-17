# Asset inventory

ISO/IEC 27001:2022 A.5.9, A.5.12, A.7.14, A.8.1. SOC 2 CC6.1. Checked quarterly.
Pre-filled from the repository on 11 October 2026; **people, devices and account IDs: to complete.**

## Information and software

| Asset | Location | Owner | Class | Notes |
|---|---|---|---|---|
| Source code (all services, charts, Terraform) | GitHub `khayaai/openfireblocks` | Engineering lead | Public while the repository is public (R-21); otherwise Confidential | Default branch is `claude/openfireblocks-mvp-plan-hvr6p1`: consider renaming to `main` and protecting it |
| Container images and SBOMs | Private Amazon ECR, eu-west-1, 14 repositories, tag-immutable, scan on push | Engineering lead | Internal | Signed with cosign |
| ISMS and policy documents | This directory; Sprinto | ISMS owner | Internal | |
| Filled-in ISMS records | Sprinto / Google Drive | ISMS owner | Confidential | Not in git |
| Customer contracts and NDAs | Google Drive [to complete] | CEO | Confidential | |
| Prospect and customer contacts | Gmail [and CRM, to complete] | CEO | Confidential (personal information) | Opt-out list kept |
| Pentest reports | `docs/security/`, vendor portals | Engineering lead | Confidential until fixed | Fixed reports are in the repository |

## Secrets and key material (staging)

| Asset | Location | Owner | Class |
|---|---|---|---|
| Signing-key shares (3 parties) | Vault/OpenBao or encrypted share store per party | Key custodians | Restricted |
| `JWT_SECRET`, `ADMIN_API_KEY`, database passwords, backup encryption key | Kubernetes secrets from [secrets manager, to complete] | Engineering lead | Restricted |
| AWS root credentials and MFA device | [to complete: offline, two-person] | CEO | Restricted |
| Release-signing identity | Keyless (GitHub OIDC to Sigstore) | Engineering lead | Restricted (the CI identity) |

## Cloud resources

| Asset | Account / region | Owner | Notes |
|---|---|---|---|
| AWS account(s) for staging | [account ID, to complete] / af-south-1 and/or eu-west-1 (`infrastructure/terraform`) | Engineering lead | Run `scripts/compliance/aws-baseline-check.sh` |
| DNS and TLS for the staging hostname | [to complete] / Let's Encrypt via Caddy | Engineering lead | |

## Devices

| Device | Holder | Serial | Encryption | Sprinto agent | Issued | Returned / disposed (date, method) |
|---|---|---|---|---|---|---|
| | | | | | | |
