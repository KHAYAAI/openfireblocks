# Multi-stage build for the Go OPA policy-service. Policies are embedded into
# the binary (go:embed), so the runtime image needs no extra files.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/policy-service .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/policy-service /policy-service
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/policy-service"]
