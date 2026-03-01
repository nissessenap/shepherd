---
title: Troubleshooting
weight: 6
---

Common issues and their solutions when working with Shepherd.

## Environment Variable Confusion

**Symptom**: GitHub adapter or API server fails to authenticate with GitHub, or authenticates as the wrong app.

**Cause**: The adapter and API server both use the same environment variable names (`SHEPHERD_GITHUB_APP_ID`, `SHEPHERD_GITHUB_INSTALLATION_ID`, `SHEPHERD_GITHUB_PRIVATE_KEY_PATH`), but they refer to **different GitHub Apps**:

| Component | GitHub App |
|-----------|-----------|
| GitHub Adapter (`shepherd github`) | **Trigger App** — receives webhooks, posts comments |
| API Server (`shepherd api`) | **Runner App** — generates installation tokens for runners |

**Fix**: Verify that each deployment mounts the correct app's credentials. See [GitHub Apps Explained](architecture/github-apps/) for details on the two-app model.

## Token Endpoint Returns 503

**Symptom**: `GET /api/v1/tasks/{taskID}/token` returns:

```json
{"error": "GitHub App not configured"}
```

**Cause**: The API server was started without GitHub App credentials. The three flags (`--github-app-id`, `--github-installation-id`, `--github-private-key-path`) are all-or-nothing — all three must be set or the token endpoint is disabled.

**Fix**: Set all three environment variables or flags for the Runner App. See [Configuration Reference](setup/configuration/#api-server-shepherd-api).

## Token Endpoint Returns 409

**Symptom**: `GET /api/v1/tasks/{taskID}/token` returns:

```json
{"error": "token already issued for this task"}
```

**Cause**: The token endpoint is **one-time use** per task. This prevents token replay attacks. The `tokenIssued` field on the AgentTask status tracks this.

**Fix**: Store the token when you first retrieve it. If you've lost it, the task must be recreated. There is no way to re-issue a token for the same task.

## Task Stuck in Pending

**Symptom**: An AgentTask stays in `Pending` phase and never transitions to `Running`.

**Cause**: The SandboxClaim created by the operator is not becoming Ready. Common reasons:

1. **SandboxTemplate doesn't exist** — check that the template named in `spec.runner.sandboxTemplateName` exists in the task's namespace
2. **agent-sandbox operator not running** — verify the controller is healthy: `kubectl get pods -n agent-sandbox-system`
3. **Image pull failure** — the runner container image can't be pulled (check pod events)
4. **Resource constraints** — insufficient CPU/memory on the cluster for the requested resources

**Debug**:

```bash
# Check the AgentTask status
kubectl get agenttask <task-name> -n shepherd-system -o yaml

# Check the SandboxClaim
kubectl get sandboxclaim -n shepherd-system

# Check pod events
kubectl describe pod -l agents.x-k8s.io/sandbox-claim=<claim-name> -n shepherd-system
```

## Callback Not Delivered

**Symptom**: Task completes, but the GitHub adapter never receives the callback (no comment posted on the issue).

**Cause**: Check the `Notified` condition on the AgentTask:

```bash
kubectl get agenttask <task-name> -n shepherd-system -o jsonpath='{.status.conditions}'
```

| Condition Reason | Meaning |
|-----------------|---------|
| `CallbackPending` | Callback hasn't been attempted yet |
| `CallbackSent` | Callback was delivered successfully |
| `CallbackFailed` | Callback delivery failed |

**Common causes of `CallbackFailed`**:

1. **HMAC mismatch** — the `SHEPHERD_CALLBACK_SECRET` doesn't match between the API server and adapter
2. **Network unreachable** — the callback URL is not reachable from the API server pod
3. **Adapter not running** — the GitHub adapter deployment is down

**Fix**: Verify that `SHEPHERD_CALLBACK_SECRET` is identical on both the API server and adapter. Check that the callback URL resolves from within the cluster.

## golangci-lint Failures

**Symptom**: `make lint-fix` fails with unused code errors.

**Cause**: golangci-lint v2 is strict about unused functions, variables, and imports. This commonly happens when scaffolding code before it's wired to routes.

**Fix**: Only add functions in the phase where they're actually called. If you're building incrementally, defer unused code to later commits.

## `go mod tidy` Removes Packages

**Symptom**: `go mod tidy` removes a dependency you just added.

**Cause**: `go mod tidy` removes packages that aren't imported by any Go source file. If you added a dependency to `go.mod` but haven't written code that imports it yet, `tidy` will remove it.

**Fix**: Add the import in the same commit as the `go.mod` change. Don't pre-add dependencies before they're used.

## Frontend Type Errors After API Changes

**Symptom**: TypeScript errors in the web frontend after modifying `api/openapi.yaml`.

**Cause**: The TypeScript types in `web/src/lib/api.d.ts` are auto-generated from the OpenAPI spec. They're now out of sync.

**Fix**:

```bash
make web-gen-types
make web-check
```

Never hand-edit `api.d.ts` — always regenerate it.

## Next Steps

- [Configuration Reference](setup/configuration/) — full list of flags and environment variables
- [Contributing](contributing/) — development workflow and conventions
