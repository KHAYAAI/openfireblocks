# syntax=docker/dockerfile:1

# Build stage.
#
# golang:1.24 because services/policy-service/go.mod declares `go 1.24`. The
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
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/policy-service .

# ofac-sync ships in the same image rather than its own.
#
# It has to run somewhere that can write the volume this service reads,
# which in practice means the same pod, and a second image to build, scan,
# sign and keep in step for one 200-line tool is cost without a benefit.
# It is a separate ENTRYPOINT, not a second process in this one: the sync
# runs as an initContainer and a CronJob, and a screening service that
# also reaches out to Treasury on its own would be a larger thing to
# reason about than it needs to be.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/ofac-sync ./cmd/ofac-sync

# Runtime stage.
#
# distroless/static rather than alpine:3.18: no package manager to invoke at
# build time, no shell to exploit at runtime, and it satisfies the chart's
# podSecurityContext (runAsNonRoot / readOnlyRootFilesystem). The alpine
# stage this replaces installed the binary under /root/, which a pod running
# as uid 1000 cannot read.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/policy-service /usr/local/bin/policy-service
COPY --from=build /out/ofac-sync /usr/local/bin/ofac-sync

# Any non-root uid works -- the binary is world-readable and writes nothing
# to the filesystem. The chart overrides this with runAsUser: 1000.
USER 65532:65532

# No HEALTHCHECK: the old one shelled out to wget, which does not exist in a
# distroless image. Kubernetes probes this over HTTP via the chart's
# livenessProbe/readinessProbe instead.
ENTRYPOINT ["/usr/local/bin/policy-service"]
