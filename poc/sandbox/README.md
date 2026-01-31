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

1. Build:

   ```bash
   make build-orchestrator
   ```

2. Run (requires kubeconfig):

   ```bash
   ./bin/orchestrator \
     --template=poc-runner \
     --namespace=default \
     --task-id=orchestrated-001 \
     --message="Hello from orchestrator"
   ```

## Cleanup

```bash
make clean
```
