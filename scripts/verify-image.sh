#!/usr/bin/env bash
#
# Check that an OpenFireblocks image was built and signed by the project's
# release workflow, and show what is inside it. For a customer's security team
# before they deploy; needs `cosign` and `jq`.
#
#   REPO=your-org/openfireblocks scripts/verify-image.sh <registry/image@sha256:...>
#
# REPO is the GitHub repository whose deploy workflow is trusted to sign.
set -euo pipefail
IMAGE="${1:?usage: verify-image.sh <image-reference>}"
REPO="${REPO:?set REPO to the GitHub repository that builds the images, e.g. your-org/openfireblocks}"
IDENTITY="^https://github.com/${REPO}/\.github/workflows/deploy\.yaml@"
ISSUER="https://token.actions.githubusercontent.com"

echo "== signature"
cosign verify "$IMAGE" --certificate-identity-regexp "$IDENTITY" --certificate-oidc-issuer "$ISSUER" > /dev/null
echo "signed by this repository's deploy workflow"

echo "== bill of materials"
cosign verify-attestation "$IMAGE" --type spdxjson \
  --certificate-identity-regexp "$IDENTITY" --certificate-oidc-issuer "$ISSUER" \
  | jq -r '.payload | @base64d | fromjson | .predicate.packages | length | "\(.) packages listed in the SBOM"'
