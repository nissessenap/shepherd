# PoC: Agent-Sandbox Entrypoint

Validates the agent-sandbox integration for shepherd.

## Prerequisites

- Kind cluster with agent-sandbox installed (with extensions enabled)
- ko installed
- kubectl configured

## Quick Start

### Phase 1: Manual Validation

1. Build and load image into Kind:

   ```bash
   make kind-load
   ```

2. Update `manifests/sandbox-template.yaml` image to match ko output.

3. Deploy:

   ```bash
   make deploy
   ```

4. Wait for sandbox to be ready:

   ```bash
   make status
   ```

5. Port-forward to test (or exec into a pod in the cluster):

   ```bash
   # Option A: port-forward the headless service
   kubectl port-forward pod/poc-test-task 8888:8888

   # Option B: exec into a debug pod
   kubectl run -it --rm curl --image=curlimages/curl -- sh
   curl http://poc-test-task.default.svc.cluster.local:8888/healthz
   ```

6. Send a task:

   ```bash
   curl -X POST http://localhost:8888/task \
     -H 'Content-Type: application/json' \
     -d '{"taskID":"test-001","message":"Hello from PoC"}'
   ```

7. Check logs:

   ```bash
   make logs
   ```

### Phase 2: Orchestrator

1. Ensure the SandboxTemplate is deployed (from Phase 1):

   ```bash
   kubectl apply -f manifests/sandbox-template.yaml
   ```

2. Build and run with default settings (uses `--local` for Kind port-forwarding):

   ```bash
   make orchestrate
   ```

3. Or build and run manually with custom options:

   ```bash
   make build-orchestrator
   ./bin/orchestrator \
     --template=poc-runner \
     --namespace=default \
     --task-id=orchestrated-001 \
     --message="Hello from orchestrator" \
     --local
   ```

   Flags:
   - `--template`: SandboxTemplate name (default: `poc-runner`)
   - `--namespace`: Namespace (default: `default`)
   - `--task-id`: Task ID to send (default: `poc-task-001`)
   - `--message`: Task message (default: `Hello from orchestrator`)
   - `--timeout`: Overall timeout (default: `5m`)
   - `--local`: Use kubectl port-forward instead of cluster DNS (required for Kind)

## Cleanup

```bash
make clean
```
