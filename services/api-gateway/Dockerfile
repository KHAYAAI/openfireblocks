# syntax=docker/dockerfile:1

# Build stage.
#
# services/api-gateway is a NestJS/TypeScript service. The Dockerfile this
# replaces was a Go build -- `go mod download` then
# `go build -o api-gateway .` -- against a go.mod that does not exist in
# this directory, so it could never have produced an image. This is the
# only internet-facing service in the platform.
#
# node:22 because @workos-inc/node@10 declares `engines: node >=22.11.0`.
# The full bookworm image, not -slim, because `bcrypt` is a native addon:
# it falls back to a node-gyp build that needs python3, make and g++. Only
# the build stage pays for that toolchain.
FROM node:22-bookworm AS build

# Optional trust anchor for building behind a TLS-intercepting egress proxy
# (corporate MITM, sandboxed CI). Passed as a BuildKit secret rather than a
# build arg so it has no size limit and never lands in an image layer; when
# the secret is not supplied the mount is empty and this is a no-op.
# Scoped to npm's own cafile rather than NODE_EXTRA_CA_CERTS, so an absent
# secret doesn't leave node warning about an empty trust file on every
# subsequent build step.
RUN --mount=type=secret,id=egress_ca \
    if [ -s /run/secrets/egress_ca ]; then \
      cp /run/secrets/egress_ca /usr/local/etc/egress-ca.pem && \
      npm config set --global cafile /usr/local/etc/egress-ca.pem; \
    fi

WORKDIR /app

# Dependency layer first so source edits don't re-resolve the lockfile.
# `npm ci` (not `install`) so the build is reproducible from
# package-lock.json and fails loudly if the two have drifted.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .

RUN npm run build && npm prune --omit=dev

# Runtime stage.
#
# -slim here, and only dist/ plus the pruned production node_modules -- no
# source, no compiler toolchain, no devDependencies, and no trace of the
# build-time CA. bcrypt's compiled .node addon is carried across inside
# node_modules and links against the same bookworm glibc.
FROM node:22-bookworm-slim AS runtime

WORKDIR /app

COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY --from=build /app/package.json ./package.json

ENV NODE_ENV=production

# The node image's built-in `node` user is uid 1000, which is exactly what
# the chart's podSecurityContext (runAsNonRoot: true, runAsUser: 1000)
# requires. Nothing is written to the filesystem at runtime, so
# readOnlyRootFilesystem holds.
USER node

EXPOSE 8080

# No HEALTHCHECK: Kubernetes probes /health and /health/ready over HTTP via
# the chart's livenessProbe/readinessProbe instead.
CMD ["node", "dist/main.js"]
