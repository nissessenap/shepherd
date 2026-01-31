# PoC Sandbox Learnings

Findings from the agent-sandbox PoC (Phase 1 + Phase 2). These inform the real operator migration.

## Environment

- Kind cluster (`cve-visualizer-dev`) with agent-sandbox + extensions
- agent-sandbox v0.1.0
- ko-built images loaded into Kind

## Findings

### 1. Startup Latency

~8 seconds from SandboxClaim creation to Sandbox Ready=True. Breakdown from orchestrator logs:
- Claim created at T+0
- First poll at T+2: "DependenciesNotReady" (pod running but not ready)
- Ready at T+8

This is cold start with image already loaded in Kind. In production with image pulls, expect longer. Warm pools will be important for latency-sensitive workloads.

### 2. DNS Resolution / In-Cluster Connectivity

The headless service created by agent-sandbox has **no ports defined** — it's a pure selector-only service:

```yaml
spec:
  clusterIP: None
  selector:
    agents.x-k8s.io/sandbox-name-hash: <hash>
  # no ports
```

Despite this, FQDN resolution works from inside the cluster. A curl pod successfully reached `http://<name>.default.svc.cluster.local:8888/healthz` and got a 200 response. Headless services resolve to the pod IP directly, so the client connects to the container port on that IP.

**Implication for the operator:** Use `Sandbox.Status.ServiceFQDN` + the known container port. No workarounds needed. However, `kubectl port-forward svc/<name>` does NOT work (requires service ports). For local dev, port-forward to the pod directly: `kubectl port-forward pod/<name> 8888:8888`.

### 3. Pod Exit Behavior

Completed pods show:
- `exitCode: 0`
- `reason: Completed`
- `phase: Succeeded`
- `restartCount: 0`

`restartPolicy: Never` is fully honored. No restart loops.

### 4. Resource Cleanup

After the pod exits, **all resources persist indefinitely**:
- SandboxClaim: remains
- Sandbox: remains
- Pod (Completed): remains
- Headless Service: remains

agent-sandbox v0.1.0 `SandboxClaimSpec` does not have a `Lifecycle` field, so there is no automatic expiry. The local (unreleased) checkout of agent-sandbox does have `Lifecycle` with `ShutdownTime` and `ShutdownPolicy`.

**Implication for the operator:** The operator must explicitly delete the SandboxClaim after task completion (or failure) to free resources. Once `Lifecycle` is available in a release, we can set `ShutdownTime` as a safety net, but explicit cleanup is still the primary path.

### 5. Image Pull with ko + Kind

Works reliably with `kind load docker-image`. The sandbox template uses `imagePullPolicy: Never` to avoid pulling from a registry. No gotchas encountered.

### 6. Log Access After Exit

Pod logs are fully accessible after container exit via `kubectl logs <pod>`, as long as the pod object exists. Since nothing auto-cleans the resources, logs remain available until the operator deletes the SandboxClaim.

**Implication for the operator:** Capture logs/results before deleting the SandboxClaim. The operator should read pod logs (or the task result from the runner's response) before cleanup.

### 7. Port Conflicts with Multiple Sandboxes

No issues. Multiple sandboxes using `containerPort: 8888` coexist without conflict. Each pod gets its own IP, and the headless service resolves to the correct pod IP via the sandbox-name-hash label selector.

### 8. Concurrent Sandboxes

The orchestrator was run multiple times with different task-ids. Each created a separate SandboxClaim/Sandbox/Pod/Service. No interference between them.

### 9. Module Integration

Importing agent-sandbox types (`api/v1alpha1` and `extensions/api/v1alpha1`) is lightweight. The PoC module depends on:
- `sigs.k8s.io/agent-sandbox v0.1.0`
- `sigs.k8s.io/controller-runtime v0.22.2`
- `k8s.io/api v0.34.1`, `k8s.io/apimachinery v0.34.1`, `k8s.io/client-go v0.34.1`

No surprises. The `go mod tidy` resolved cleanly. For the main shepherd module, these deps will be additive alongside the existing operator deps.

**Watch out:** agent-sandbox v0.1.0 API is limited (no Lifecycle on SandboxClaim). The operator implementation should target whatever version includes the features we need, or work around their absence.

## Key Design Decisions for the Operator

Based on these findings:

1. **Connectivity:** Use `Sandbox.Status.ServiceFQDN` + hardcoded port. Works in-cluster.
2. **Cleanup:** Operator must delete SandboxClaim explicitly after task completion.
3. **Readiness detection:** Poll `Sandbox.Status.Conditions` for `Ready=True`. ~8s cold start baseline.
4. **Completion detection:** Poll for `Ready=False` after sending the task. The pod exits and the condition flips.
5. **Log capture:** Read pod logs before deleting the SandboxClaim.
6. **No restart concern:** `restartPolicy: Never` works correctly.
7. **Version targeting:** Plan for agent-sandbox version with `Lifecycle` support, or implement timeout-based cleanup in the operator itself.
