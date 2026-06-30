# Contributing

## Layout
A monorepo of independently-buildable services and SDKs — see the
[README](README.md). Each service owns its Dockerfile and dependency manifest.

## Prerequisites
Go 1.24+, Node 22+, Python 3.8+, Docker (for the local stack).

## Develop
```bash
make build      # build everything
make verify     # run all tests (what CI runs)
make up         # start the local stack
make smoke      # e2e smoke test against the running stack
make test-tss   # full threshold-MPC proof (~75s, off by default)
```

## Conventions
- Every change must keep `make verify` green. CI builds and tests all Go
  modules, the gateway and both JS/Python SDKs on each push/PR.
- Go: `go vet` must pass (the threshold-MPC `tss` package is build-tagged and
  excluded from default vet — see `services/mpc-signer/tss/README.md`).
- Security controls fail **closed** (policy, sanctions). Don't weaken that.
- Secrets never go in git. Audit-log every transaction lifecycle event.
- New transaction policies: add a `.rego` rule + a test in `policy-service`.

## Security
See [SECURITY.md](SECURITY.md) and the
[audit checklist](docs/security/audit-checklist.md). Report vulnerabilities
privately — do not open public issues.
