# syntax=docker/dockerfile:1

# Build stage.
#
# Runs as a sidecar next to every Vault replica -- see the package doc in
# main.go for why a Vault with real storage needs one at all.
FROM golang:1.24-bookworm AS build

# Optional trust anchor for building behind a TLS-intercepting egress proxy
# (corporate MITM, sandboxed CI). Passed as a BuildKit secret rather than a
# build arg so it has no size limit and never lands in an image layer; when
# the secret is not supplied the mount is empty and this is a no-op.
RUN --mount=type=secret,id=egress_ca \
    if [ -s /run/secrets/egress_ca ]; then \
      cat /run/secrets/egress_ca >> /etc/ssl/certs/ca-certificates.crt; \
    fi

WORKDIR /src

# No `COPY go.sum` and no `go mod download`: this module has no external
# dependencies (it talks to Vault over net/http from the standard library),
# so there is no go.sum to copy and nothing to fetch.
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/vault-unseal .

# Runtime stage.
#
# The :nonroot variant of distroless/static ships a CA bundle, which this
# service needs (unlike the others) because it dials Vault over HTTPS.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/vault-unseal /usr/local/bin/vault-unseal

# Writes the issued cert/key to CERT_OUT_DIR, which the chart mounts as an
# emptyDir shared with the main container -- never to the image filesystem,
# so readOnlyRootFilesystem holds.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/vault-unseal"]
