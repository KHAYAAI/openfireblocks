# Supply chain: signed images and a bill of materials

**Status: written, not yet run.** The workflow changes are in
`.github/workflows/deploy.yaml` and have been checked only as YAML. No image
has been built here (no Docker daemon) and nothing has been pushed to a
registry, so the first run of the build job is the first real test.

## What the pipeline does

For every image in `scripts/build-images.sh --list`:

1. Builds and pushes it, and records the image **digest**.
2. Signs the digest with Sigstore `cosign`, **keyless**: the signature is
   bound to this repository's `deploy.yaml` workflow identity through GitHub's
   OIDC token. There is no signing key to steal or rotate.
3. Generates an SPDX bill of materials with Syft (`anchore/sbom-action`), keeps
   it as a build artifact, and attaches it to the image as a signed attestation.
4. Before the staging deploy, `cosign verify` refuses any image that was not
   signed by this repository's `deploy.yaml`. A tag pushed by hand, or an image
   from another pipeline, stops the deploy.

## For a customer's security team

    REPO=your-org/openfireblocks scripts/verify-image.sh registry/api-gateway@sha256:...

checks the signature and prints how many packages the SBOM lists. A customer
who builds from source runs the same build with their own repository, and
trusts their own workflow identity instead.

## What this does not give you

- It proves *where* an image came from, not that the code is free of
  vulnerabilities. `security-scan` (Snyk, Semgrep, Gitleaks) is separate, and
  those actions are referenced by floating tags (`@master`, `@v1`); pin them
  to commit hashes before relying on them.
- No SLSA provenance attestation yet, and base images are not pinned by digest.
  Both are sensible next steps and neither is done.
- It is a build-and-deploy control. It does not replace the independent
  security audit or penetration test.
