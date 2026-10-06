# syntax=docker/dockerfile:1

# services/backup had no Dockerfile, so the backup code (pg_dump, Vault export,
# failover) had no way to run anywhere but a shell.
FROM golang:1.25.10-bookworm AS build

# Optional trust anchor for building behind a TLS-intercepting egress proxy,
# passed as a BuildKit secret so it never lands in a layer.
RUN --mount=type=secret,id=egress_ca \
    if [ -s /run/secrets/egress_ca ]; then \
      cat /run/secrets/egress_ca >> /etc/ssl/certs/ca-certificates.crt; \
    fi

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/backup-server ./cmd/backup-server

# Runtime: the official Postgres 16 image, for pg_dump and pg_restore.
#
# Not distroless, because this service shells out to the Postgres client
# tools and a dump from a client older than the server can be refused or
# subtly wrong. 16 matches the database the chart and Terraform provision;
# change it with the server, not separately.
FROM postgres:16-bookworm

COPY --from=build /out/backup-server /usr/local/bin/backup-server

# Not root, and the same uid the chart runs every workload as (1000). The dump
# directory is a mounted volume the chart makes group-writable via fsGroup, and
# the root filesystem is read-only, so nothing in the image needs to be writable.
RUN rm -rf /docker-entrypoint-initdb.d /var/lib/postgresql/data \
 && mkdir -p /var/lib/openfireblocks-backup \
 && chown 1000:1000 /var/lib/openfireblocks-backup
USER 1000:1000

ENV PORT=8088 BACKUP_DUMP_DIR=/var/lib/openfireblocks-backup
EXPOSE 8088
ENTRYPOINT ["/usr/local/bin/backup-server"]
