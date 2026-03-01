---
title: API Reference
weight: 2
---

Shepherd exposes a REST API on two ports with different access scopes.

## Two-Port Architecture

| Port | Scope | Audience | Endpoints |
|------|-------|----------|-----------|
| **8080** | Public | Web UI, external clients | Health probes, task CRUD, WebSocket event streaming |
| **8081** | Internal | Runner pods (cluster-only) | Status updates, event posting, task data, token generation |

In a typical deployment, port 8080 is exposed via a Service (or Ingress), while port 8081 is restricted to in-cluster traffic using a NetworkPolicy. Runner pods communicate exclusively with port 8081.

## Interactive API Documentation

The full OpenAPI specification is rendered below using Swagger UI. You can explore all endpoints, view request/response schemas, and try out requests.

{{< swagger src="/openapi.yaml" >}}

## WebSocket Event Streaming

The `GET /api/v1/tasks/{taskID}/events` endpoint upgrades to a WebSocket connection for real-time event streaming.

### Connection

```
ws://localhost:8080/api/v1/tasks/{taskID}/events
```

### Reconnection

Use the `?after=N` query parameter to resume from a specific sequence number:

```
ws://localhost:8080/api/v1/tasks/{taskID}/events?after=42
```

The server replays any buffered events with `sequence > 42`, then continues streaming live events. The API maintains an in-memory ring buffer of 1000 events per task.

### Message Envelope

The server sends JSON messages in this format:

```json
{
  "type": "task_event",
  "taskID": "my-task-abc123",
  "event": {
    "sequence": 1,
    "timestamp": "2026-03-01T10:00:01Z",
    "type": "thinking",
    "summary": "Analyzing the codebase"
  }
}
```

When a task reaches a terminal state, the server sends a completion message:

```json
{
  "type": "task_complete",
  "taskID": "my-task-abc123",
  "status": {
    "phase": "Succeeded",
    "message": "Task completed successfully",
    "prURL": "https://github.com/org/repo/pull/123"
  }
}
```

## Error Codes

| Code | Meaning | Common Causes |
|------|---------|---------------|
| **400** | Bad Request | Invalid JSON body, missing required fields, invalid query parameters |
| **404** | Not Found | Task ID doesn't exist in the namespace |
| **409** | Conflict | Token already issued for this task (one-time use) |
| **410** | Gone | Task is in a terminal state (completed, failed, timed out) — data and events are no longer writable |
| **413** | Payload Too Large | Compressed context exceeds the size limit |
| **415** | Unsupported Media Type | `Content-Type` is not `application/json` |
| **502** | Bad Gateway | API server cannot reach the Kubernetes API |
| **503** | Service Unavailable | GitHub App not configured (token endpoint), or server not ready |

## Next Steps

- [Custom Runners](../custom-runners/) — implement the 5-step runner protocol
- [Configuration Reference](../../setup/configuration/) — CLI flags, env vars, and CRD fields
- [Architecture Overview](../../architecture/overview/) — how the API server fits into the system
