# Contributing

## Prerequisites

- Go (see `go.mod` for version)
- [Kind](https://kind.sigs.k8s.io/)
- Docker
- kubectl

## Running Tests

### Unit tests

```bash
make test
```

### Linting

```bash
make lint-fix
```

### E2E tests

Three modes depending on your workflow:

**Full run** — creates a Kind cluster, deploys everything, runs tests, tears down:

```bash
make test-e2e
```

**Interactive** — same as above but keeps the cluster alive for debugging. Reuses an existing cluster if one is already running:

```bash
make test-e2e-interactive
```

After tests finish you can inspect the cluster with `kubectl` and re-run tests quickly with:

```bash
make test-e2e-existing
```

To clean up the cluster when done:

```bash
make kind-delete
```
