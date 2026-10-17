# Image signing on a real registry (2026-10-17)

First run of the supply-chain control in `.github/workflows/deploy.yaml` against a real registry.

## Setup
- 14 private Amazon ECR repositories in `eu-west-1` (one per service from `scripts/build-images.sh --list`),
  tag-immutable, scan on push.
- GitHub authenticates through OIDC: an IAM role (`ofb-github-build`) whose trust policy admits only
  `repo:KHAYAAI/openfireblocks:ref:refs/heads/claude/openfireblocks-main`, with permission to push to ECR
  and nothing else. No AWS key is stored in GitHub.
- Run by hand (`workflow_dispatch`) with `skip_gates` ticked, at commit `8364ebe`.

## Result
- All 14 build jobs succeeded: each image was built, pushed, signed by digest (keyless) and given an SBOM
  attestation. The deploy jobs were skipped, as designed for this mode.
- For `api-gateway` the registry holds the image (tag = the commit), a `.sig` object and an `.att` object
  for digest `sha256:692e5c46be1320b54df3bfaefdf11e27021d351a2506764d75a3fc19bce8d8b2`.
- `cosign verify` with the identity `^https://github.com/KHAYAAI/openfireblocks/\.github/workflows/deploy\.yaml@`
  and issuer `https://token.actions.githubusercontent.com`: **passed**. Claims validated; the signature's
  presence in the transparency log was verified offline; the certificate chained to the trusted authority.
  The certificate names workflow `deploy.yaml@refs/heads/claude/openfireblocks-main`, trigger
  `workflow_dispatch`, commit `8364ebe3240788ac6aedb585a056ce796e8badf1`.
- `cosign verify-attestation --type spdxjson` on the same image and identity: **passed** (same checks), so the
  SBOM attestation is signed by the same workflow.
- The same command with identity `^https://github.com/OTHERORG/`: **refused** ("none of the expected
  identities matched"), naming the real signer. The check does discriminate.

## Not yet done
- Only `api-gateway` was checked; the other 13 images were built and signed by the same job but not
  individually verified.
- The two gate jobs failed and were skipped by `skip_gates`: Snyk needs `SNYK_TOKEN`, and the workflow's
  `test` job runs an invalid command (`go test -coverage` at the repository root). A push to the default
  branch will therefore not build until those are fixed.
- The deploy job's own signature check (before rolling out) has not run.
