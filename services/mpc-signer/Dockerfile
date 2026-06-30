# Multi-stage build for the Go MPC signer. Produces a small static image.
FROM golang:1.24-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Build a statically linked binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mpc-signer .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/mpc-signer /mpc-signer
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/mpc-signer"]
