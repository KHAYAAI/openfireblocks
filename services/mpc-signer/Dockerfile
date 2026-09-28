# syntax=docker/dockerfile:1

# Build stage.
#
# golang:1.24 because services/mpc-signer/go.mod declares `go 1.24`. The
# golang:1.21-alpine base this replaces could never have compiled the
# module, and its runtime stage ran `apk add ca-certificates`, which needs
# network access to the Alpine package mirrors at build time.
FROM golang:1.24-bookworm AS build

# Optional trust anchor for building behind a TLS-intercepting egress proxy
# (corporate MITM, sandboxed CI). Passed as a BuildKit secret rather than a
# build arg so it has no size limit and never lands in an image layer; when
# the secret is not supplied the mount is empty and this is a no-op.
#   docker build --secret id=egress_ca,src=/path/to/bundle.pem
RUN --mount=type=secret,id=egress_ca \
    if [ -s /run/secrets/egress_ca ]; then \
      cat /run/secrets/egress_ca >> /etc/ssl/certs/ca-certificates.crt; \
    fi

WORKDIR /src

# Dependency layer first so source edits don't re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 (the old Dockerfile used 1): nothing in this tree needs
# cgo -- tss-lib, lib/pq and the Temporal SDK are all pure Go -- and a
# static binary is what the distroless/static runtime below can execute.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/mpc-signer .

# ---------------------------------------------------------------------------
# Hardware-signing variant: docker build --target pkcs11 .
#
# PKCS#11 means dlopen()ing the HSM vendor's module, which needs cgo and a
# libc -- so this is a separate image rather than a mode of the default
# one, and the default stays static and cgo-free. It also carries hsm-key,
# the tool that creates the key on the token (the service never does).
#
# The vendor's module is not in this image. It is licensed, versioned and
# configured by the HSM vendor (CloudHSM, Luna, YubiHSM) and mounted into
# the pod; see mpcSigner.hsm in the chart.
FROM build AS build-pkcs11
RUN CGO_ENABLED=1 GOOS=linux go build -tags pkcs11 -trimpath -ldflags='-s -w' -o /out/mpc-signer . && \
    CGO_ENABLED=1 GOOS=linux go build -tags pkcs11 -trimpath -ldflags='-s -w' -o /out/hsm-key ./cmd/hsm-key

# distroless/cc: glibc and libstdc++, which vendor PKCS#11 modules link
# against, and still no shell or package manager.
FROM gcr.io/distroless/cc-debian12:nonroot AS pkcs11
COPY --from=build-pkcs11 /out/mpc-signer /usr/local/bin/mpc-signer
COPY --from=build-pkcs11 /out/hsm-key /usr/local/bin/hsm-key
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/mpc-signer"]

# ---------------------------------------------------------------------------
# Runtime stage (the default target -- it must stay last).
#
# distroless/static rather than alpine:3.18: no package manager to invoke at
# build time, no shell to exploit at runtime, and it satisfies the chart's
# podSecurityContext (runAsNonRoot / readOnlyRootFilesystem). The alpine
# stage this replaces installed the binary under /root/, which a pod running
# as uid 1000 cannot read.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/mpc-signer /usr/local/bin/mpc-signer

# Any non-root uid works -- the binary is world-readable and writes nothing
# to the filesystem. The chart overrides this with runAsUser: 1000.
USER 65532:65532

# No HEALTHCHECK: the old one shelled out to wget, which does not exist in a
# distroless image. Kubernetes probes this over HTTP via the chart's
# livenessProbe/readinessProbe instead.
ENTRYPOINT ["/usr/local/bin/mpc-signer"]
