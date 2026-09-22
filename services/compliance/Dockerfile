# syntax=docker/dockerfile:1

# Build stage.
#
# golang:1.24 because services/compliance/go.mod declares `go 1.24`. The
# golang:1.21-alpine base used across this repository could never have
# compiled these modules, and its runtime stage ran `apk add`, which needs
# network access to the Alpine mirrors at build time.
FROM golang:1.24-bookworm AS build

# Optional trust anchor for building behind a TLS-intercepting egress proxy
# (corporate MITM, sandboxed CI). A BuildKit secret rather than a build arg:
# no size limit, and it never lands in an image layer. Absent, the mount is
# empty and this is a no-op.
RUN --mount=type=secret,id=egress_ca \
    if [ -s /run/secrets/egress_ca ]; then \
      cat /run/secrets/egress_ca >> /etc/ssl/certs/ca-certificates.crt; \
    fi

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0: nothing here needs cgo, and a static binary is what the
# distroless/static runtime below can execute.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/compliance .

# Runtime stage.
#
# distroless/static rather than alpine:3.18: no package manager at build
# time, no shell at runtime, and it satisfies the chart's podSecurityContext
# (runAsNonRoot / readOnlyRootFilesystem). The alpine stage this replaces
# installed the binary under /root/, which a pod running as uid 1000 cannot
# read.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/compliance /usr/local/bin/compliance

USER 65532:65532

# No HEALTHCHECK: the old one shelled out to wget, which does not exist in a
# distroless image. Kubernetes probes over HTTP via the chart instead.
ENTRYPOINT ["/usr/local/bin/compliance"]
