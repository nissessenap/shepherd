---
date: 2026-02-18T14:00:00+01:00
researcher: claude
git_commit: 6c1523a6de6a46547abe502701bc2ea09a115068
branch: stripe_minons
repository: NissesSenap/shepherd_init
topic: "Agent visibility and streaming architecture: real-time task monitoring inspired by Stripe Minions"
tags: [research, codebase, streaming, sse, websocket, visibility, stripe, minions, claude-code, opencode, goose, architecture, gateway-api, envoy-gateway]
status: complete
last_updated: 2026-02-18
last_updated_by: claude
last_updated_note: "Added transport protocol comparison (SSE vs WebSocket vs REST polling), Kubernetes Gateway API / Envoy Gateway proxy configuration, updated recommendation from SSE to WebSocket"
---

# Research: Agent Visibility and Streaming Architecture

**Date**: 2026-02-18T14:00:00+01:00
**Researcher**: claude
**Git Commit**: 6c1523a6de6a46547abe502701bc2ea09a115068
**Branch**: stripe_minons
**Repository**: NissesSenap/shepherd_init

## Research Question

How can Shepherd provide real-time visibility into what coding agents are doing — similar to Stripe's Minions web interface — while keeping the architecture agent-agnostic, API-first, and compatible with future interactive sessions? Should Shepherd continue using Claude Code or switch to an open-source agent like OpenCode or Goose?

## Decisions Made During Research

These decisions were made through interview with the project owner:

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Visibility granularity | Turn-level (tool calls as discrete steps) | Simpler, lower bandwidth, sufficient for monitoring |
| Primary consumers | API first, then Web UI > CLI | API-first enables multiple frontends |
| GitHub comments | No per-step comments | Too chatty, no real value |
| Event persistence | Live-only for MVP | Events in-memory, lost on restart |
| Agent tool | Stay with Claude Code | Already building runner, simpler short-term |
| API→Consumer protocol | WebSocket (coder/websocket) | ~5 lines more than SSE server-side, native auth header support, no migration cost when interactive sessions arrive. SSE would need to be deprecated later. |
| Runner→API protocol | REST POST (agent-agnostic schema) | Runner is producer, POST is fire-and-forget, anyone can implement |
| Document scope | Architecture only | Implementation phases come as a separate plan |

## Summary

Shepherd currently has no streaming or real-time visibility infrastructure. The status system supports four events (`started`, `progress`, `completed`, `failed`) via synchronous HTTP POST callbacks. To achieve Stripe Minions-style visibility, Shepherd needs three additions: (1) an agent-agnostic event schema that the runner POSTs to the API, (2) an in-memory event hub in the API that fans out to subscribers, and (3) a WebSocket endpoint that web UI and CLI clients consume.

The architecture is designed around a clean split: the runner parses agent-specific output (Claude Code's `stream-json`) and translates it into agent-agnostic events. The API never knows which agent produced the events. This allows future runners to use OpenCode, Goose, or any other agent without API changes.

Claude Code's `--output-format stream-json` provides rich NDJSON streaming with assistant messages (containing `tool_use` blocks), user messages (containing `tool_result` blocks), and metadata (`total_cost_usd`, `num_turns`, `session_id`). The Go runner entrypoint reads this stdout line-by-line, extracts turn-level events, and POSTs them to the API.

For the API→consumer transport, WebSocket (via `coder/websocket`) is chosen over SSE. The server-side complexity difference is negligible (~5 lines), WebSocket supports native `Authorization` headers (SSE's `EventSource` API cannot set custom headers), and WebSocket provides a direct upgrade path to bidirectional communication for future interactive sessions — avoiding a costly SSE→WebSocket migration later. See section 7 for the full transport protocol comparison.

## Detailed Findings

### 1. What Stripe Minions Provides (and What We Know)

Stripe's Minions system (Part 1 blog, February 2026) uses a fork of [block/goose](https://github.com/block/goose) with custom orchestration that interleaves agent loops with deterministic git/lint/test steps. Engineers invoke tasks via Slack, CLI, web UI, or embedded integrations. Agents run in isolated "devboxes" (pre-warmed in 10 seconds).

The key visibility feature: **"Engineers can monitor minion decisions and actions through a web interface during execution or afterward."** This is the entirety of Part 1's disclosure on the UI. Part 2 (covering technical implementation) has not been published as of 2026-02-18.

From Goose's upstream architecture, the `goosed` server implements SSE-based streaming with these notification types:

| Notification | Description |
|-------------|-------------|
| `AgentMessageChunk` | Streaming agent text output |
| `AgentThoughtChunk` | Streaming reasoning/thinking content |
| `ToolCall` | Tool invocation started |
| `ToolCallUpdate` | Tool completion or failure |

Whether Stripe's fork uses the same SSE protocol is unknown, but the upstream architecture provides the foundation.

**Key architectural differences from Shepherd:**

| Aspect | Stripe Minions | Shepherd |
|--------|---------------|----------|
| Agent tool | Goose fork (Rust, Apache-2.0) | Claude Code (proprietary) |
| Sandbox | "Devbox" (pre-warmed, identical to dev machines) | Kubernetes pods via agent-sandbox CRD |
| Orchestration | Agent loop interleaved with deterministic steps | Runner Go entrypoint wraps CC invocation |
| Tool access | 400+ MCP tools via centralized "Toolshed" | CC's built-in tools + repo CLAUDE.md |
| Streaming | SSE from goosed server (likely) | None currently |
| Status | Web UI (live + retrospective) | GitHub issue comments (terminal only) |

### 2. Current Shepherd Status System

The current system uses synchronous HTTP POST callbacks with no streaming:

**Events**: `started`, `progress`, `completed`, `failed` (defined in `pkg/api/types.go:20-25`)

**Runner→API**: `POST /api/v1/tasks/{taskID}/status` on internal port 8081 (`pkg/api/handler_status.go:36`). Accepts `StatusUpdateRequest{Event, Message, Details}`.

**API→Adapter**: `callbackSender.send()` (`pkg/api/callback.go:51-86`) forwards `CallbackPayload{TaskID, Event, Message, Details}` to the adapter's registered callback URL with HMAC-SHA256 signature.

**Safety net**: `statusWatcher` (`pkg/api/watcher.go`) runs a controller-runtime informer on AgentTask CRDs. If the operator marks a task terminal but no callback was sent, the watcher claims it and fires the callback. Uses a `ConditionNotified` state machine (`CallbackPending` → `CallbackSent`/`CallbackFailed`) to prevent duplicates.

**No streaming infrastructure exists** — no WebSocket, SSE, or event buffering anywhere in the codebase.

### 3. Claude Code's stream-json Output

Claude Code's `--output-format stream-json` emits NDJSON (one JSON object per line) to stdout. Without `--include-partial-messages`, the output consists of turn-level messages:

```
SDKSystemMessage       (type: "system", subtype: "init")      — session start
SDKAssistantMessage    (type: "assistant")                     — Claude's response (text + tool_use)
SDKUserMessage         (type: "user")                          — tool results
SDKResultMessage       (type: "result")                        — final summary
```

**Assistant messages contain tool calls:**

```json
{
  "type": "assistant",
  "session_id": "sess_abc123",
  "message": {
    "content": [
      {"type": "text", "text": "Let me read the file..."},
      {"type": "tool_use", "id": "toolu_01ABC", "name": "Read", "input": {"file_path": "/src/auth.go"}}
    ],
    "stop_reason": "tool_use",
    "usage": {"input_tokens": 500, "output_tokens": 42}
  }
}
```

**User messages contain tool results:**

```json
{
  "type": "user",
  "message": {
    "content": [
      {"type": "tool_result", "tool_use_id": "toolu_01ABC", "content": "package auth\n\nfunc Login()..."}
    ]
  }
}
```

**Result message (always last):**

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "total_cost_usd": 0.34,
  "num_turns": 4,
  "duration_ms": 3400,
  "session_id": "abc-123"
}
```

This gives Shepherd everything needed for turn-level visibility: which tools were called, what inputs were given, what results came back, and final cost/turn metadata.

**Important limitation**: With extended thinking enabled (`--betas interleaved-thinking`), `stream_event` messages are NOT emitted — only complete `AssistantMessage` objects after each turn. This does not affect turn-level visibility (which uses complete messages anyway) but would matter for future token-level streaming.

### 4. Agent Tool Landscape

#### Claude Code (Current Choice)

- **License**: Proprietary (source-available CLI, Commercial ToS SDK)
- **Language**: TypeScript CLI, Python/TypeScript SDK wrappers
- **Streaming**: `--output-format stream-json` (NDJSON) — richest structured output of the three
- **Container support**: First-class (`-p` mode, no GUI dependency)
- **Extensibility**: MCP servers, hooks (PreToolUse, PostToolUse, Stop, SessionStart, SessionEnd), subagents
- **Lock-in**: Claude models only (via Anthropic API, Bedrock, Vertex, or Azure AI Foundry)
- **Production use**: Shepherd, Coinbase Claudebot
- **SDK**: `claude_agent_sdk` (Python/TypeScript) spawns `claude` subprocess, communicates via stream-json

#### OpenCode (Ramp's Choice)

- **License**: MIT
- **Language**: Go (Bubble Tea TUI, chi HTTP, SQLite persistence)
- **Streaming**: SSE from HTTP server (`/event`), NDJSON via ACP stdio
- **Container support**: `opencode serve` runs headless; `opencode run -p "..."` for one-shot
- **Extensibility**: MCP + LSP + custom agents + REST API clients
- **Lock-in**: None (75+ LLM providers)
- **Production use**: Ramp Inspect (~30% of PRs)
- **SDK**: Go SDK (`github.com/sst/opencode-sdk-go`), TypeScript SDK (from OpenAPI spec)
- **Key differentiator**: Server-first design — TUI is just a client. `opencode attach <url>` connects remote TUI to a running daemon. This is architecturally closest to what Shepherd needs for interactive sessions.

#### Goose (Stripe's Choice)

- **License**: Apache-2.0
- **Language**: Rust (58.7%), TypeScript (33.3%)
- **Streaming**: SSE via `goosed` server, migrating to ACP-over-HTTP
- **Container support**: Dockerfile in repo, headless mode via `goose run -t "..."`
- **Extensibility**: MCP extensions, custom distributions (Stripe's fork model)
- **Lock-in**: None (any LLM provider)
- **Production use**: Stripe Minions (1000+ PRs/week)
- **Key differentiator**: Fork-and-customize model with first-class MCP support and 30.6k GitHub stars

#### Comparison for Shepherd's Needs

| Capability | Claude Code | OpenCode | Goose |
|-----------|-------------|----------|-------|
| Turn-level event extraction | Excellent (structured NDJSON) | Good (SSE events) | Good (SSE events) |
| Go ecosystem fit | Subprocess wrapper | Native Go | Rust binary + CLI |
| Interactive sessions (future) | No (CLI only, no server mode) | Yes (`opencode serve` + `attach`) | Yes (`goosed` server) |
| Agent-agnostic API | N/A (it IS the agent) | N/A | N/A |
| Provider flexibility | Claude only | Any LLM | Any LLM |
| Maturity for headless use | High (well-documented `-p` mode) | Medium (server mode newer) | Medium (headless tutorial exists) |

**Why staying with Claude Code works for now**: The runner already translates CC-specific output into agent-agnostic API events. The API never sees CC's stream-json directly. Swapping the agent later means changing only the runner image, not the API or consumers. The main risk is vendor lock-in to Claude models, which is an acceptable trade-off given Shepherd's current stage.

**When to reconsider**: If interactive sessions become a priority, OpenCode's `serve`/`attach` architecture is a more natural fit than wrapping CC's CLI. OpenCode's Go SDK also fits better in Shepherd's Go codebase. This is a future decision point, not a current blocker.

### 5. Target Architecture: Agent Visibility

#### Component Diagram

```
┌─────────────┐     POST /api/v1/tasks/{id}/events     ┌──────────────┐
│   Runner    │ ──────────────────────────────────────→ │              │
│  (in pod)   │     (agent-agnostic event schema)       │  Shepherd    │
│             │                                         │  API Server  │
│ CC stream-  │     POST /api/v1/tasks/{id}/status      │  (:8080/     │
│ json stdout │ ──────────────────────────────────────→ │   :8081)     │
│ → Go parser │     (existing: started/completed/failed)│              │
└─────────────┘                                         │  ┌────────┐  │
                                                        │  │ Event  │  │
                                                        │  │  Hub   │  │
                                                        │  │(in-mem)│  │
                                                        │  └───┬────┘  │
                                                        │      │       │
                                                        └──────┼───────┘
                                         WebSocket             │
                    ┌──────────────────────────────────────────┘
                    │   (coder/websocket, bidirectional-ready)
            ┌───────┴───────┐
            │               │
     ┌──────▼──────┐  ┌─────▼─────┐
     │   Web UI    │  │    CLI    │
     │ (future)    │  │ (future)  │
     └─────────────┘  └───────────┘
```

#### Data Flow

```
1. Runner receives task assignment (existing: POST /task from operator)
2. Runner starts CC: claude -p "..." --output-format stream-json
3. Runner reads CC stdout line-by-line (NDJSON)
4. For each SDKAssistantMessage with tool_use blocks:
   → Runner extracts tool name, input summary, timestamps
   → Runner POSTs agent-agnostic TaskEvent to API: POST /api/v1/tasks/{id}/events
5. For each SDKUserMessage with tool_result blocks:
   → Runner extracts result summary (truncated), status
   → Runner POSTs TaskEvent to API
6. API receives TaskEvent, writes to in-memory EventHub
7. EventHub fans out to all WebSocket subscribers for that task
8. Web UI / CLI connected via WebSocket receive events in real-time
9. On CC completion, runner reports terminal status via existing
   POST /api/v1/tasks/{id}/status (unchanged)
```

### 6. Agent-Agnostic Event Schema

The event schema is designed so that any agent (Claude Code, OpenCode, Goose, or custom) can produce it. The API only knows about this schema, never about CC's stream-json.

```go
// TaskEvent represents a single observable action during task execution.
// This schema is agent-agnostic — any runner implementation can produce it.
type TaskEvent struct {
    // Sequence is a monotonically increasing counter per task.
    // Allows consumers to detect gaps and order events.
    Sequence int64 `json:"sequence"`

    // Timestamp is when the event occurred in the runner.
    Timestamp time.Time `json:"timestamp"`

    // Type classifies the event for UI rendering.
    Type TaskEventType `json:"type"`

    // Summary is a human-readable description of the event.
    // Examples: "Reading file src/auth.go", "Running go test ./..."
    Summary string `json:"summary"`

    // Tool is the name of the tool invoked (if applicable).
    // Examples: "Read", "Edit", "Bash", "Write", "Glob", "Grep"
    Tool string `json:"tool,omitempty"`

    // Input is a condensed representation of the tool's input.
    // For Read: the file path. For Bash: the command. For Edit: file + summary.
    // Truncated to a reasonable size by the runner.
    Input map[string]any `json:"input,omitempty"`

    // Output is a condensed representation of the tool's result.
    // Truncated by the runner to avoid overwhelming the API.
    Output *TaskEventOutput `json:"output,omitempty"`

    // Metadata holds agent-specific information that the API passes through
    // without interpretation. Useful for debugging and observability.
    Metadata map[string]any `json:"metadata,omitempty"`
}

type TaskEventType string

const (
    // EventTypeThinking indicates the agent is reasoning (text output without tool calls).
    EventTypeThinking TaskEventType = "thinking"

    // EventTypeToolCall indicates a tool invocation has started.
    EventTypeToolCall TaskEventType = "tool_call"

    // EventTypeToolResult indicates a tool invocation has completed.
    EventTypeToolResult TaskEventType = "tool_result"

    // EventTypeError indicates an error during execution (non-terminal).
    EventTypeError TaskEventType = "error"
)

type TaskEventOutput struct {
    // Success indicates whether the tool call succeeded.
    Success bool `json:"success"`

    // Summary is a truncated representation of the output.
    Summary string `json:"summary,omitempty"`
}
```

#### Mapping CC stream-json → TaskEvent

| CC Message | TaskEvent Type | Summary | Tool | Input |
|-----------|----------------|---------|------|-------|
| `assistant` with `text` content | `thinking` | First 200 chars of text | — | — |
| `assistant` with `tool_use` content | `tool_call` | `"Reading src/auth.go"` | `Read` | `{"file_path": "src/auth.go"}` |
| `user` with `tool_result` content | `tool_result` | First 200 chars of result | (from matching tool_call) | — |
| Non-zero exit or `is_error: true` | `error` | Error message | — | — |

The runner maintains a map of `tool_use_id` → `tool_name` to correlate tool results back to their tool calls.

### 7. Transport Protocol Comparison: WebSocket vs SSE vs REST Polling

Three options were evaluated for the API→consumer streaming transport. The runner→API transport is always REST POST regardless of this choice (runner is the event producer, not a subscriber).

#### Server-Side Complexity (Go + chi)

| Component | REST Polling | SSE | WebSocket (coder/websocket) | WebSocket (gorilla) |
|-----------|-------------|-----|---------------------------|-------------------|
| Hub/broker (fan-out) | ~80 lines (ring buffer + GET) | ~50 lines | ~50 lines | ~50 lines |
| Handler | ~30 lines | ~30 lines | ~35 lines | ~80 lines |
| Ping/pong keepalive | N/A | 3 lines (comment) | 0 (automatic via `CloseRead`) | ~30 lines (manual ticker) |
| Protocol upgrade | None | None | `websocket.Accept()` | `Upgrader{}` struct |
| **Total server-side** | **~110 lines** | **~85 lines** | **~90 lines** | **~160 lines** |

With `coder/websocket` (formerly `nhooyr/websocket`, adopted by Coder August 2024), the server-side gap vs SSE is ~5 lines. The library handles ping/pong automatically via `c.CloseRead(ctx)` — no manual ticker goroutine needed. Context-first API integrates naturally with chi middleware.

`gorilla/websocket` is more established (~24.5k stars, ~190k dependents) but requires manual ping/pong management (~30 lines), explicit `Upgrader` configuration, and has uncertain long-term maintenance (re-archived, then un-archived, still seeking maintainers as of v1.5.3, June 2024).

#### Client-Side Complexity

| Concern | REST Polling | SSE (EventSource) | SSE (fetch stream) | WebSocket |
|---------|-------------|-------------------|-------------------|-----------|
| Basic connection | ~15 lines | ~5 lines | ~15 lines | ~10 lines |
| Reconnection | Built-in (just poll again) | Built-in (`Last-Event-ID`) | Manual (~15 lines) | Manual (~15 lines) |
| Auth headers | Native | **Cannot set** (spec limitation) | Native | Native |
| Message type dispatch | Manual | Named events built-in | Manual | Manual |
| **Total JS** | **~30 lines** | **~5 lines** | **~30 lines** | **~25 lines** |

The browser `EventSource` API is the simplest client but **cannot set custom HTTP headers** — this is a specification-level limitation. Authentication requires workarounds: query parameter tokens (appear in logs/history), cookies (same-origin only), or replacing `EventSource` with `fetch()` + `ReadableStream` (which can set headers but loses automatic reconnection, bringing the client complexity to ~30 lines — same as WebSocket).

#### Feature Comparison

| Feature | REST Polling | SSE | WebSocket |
|---------|-------------|-----|-----------|
| Latency | Poll interval (1-5s) | Real-time | Real-time |
| Direction | Client→Server only | Server→Client only | Bidirectional |
| Auth headers | Yes | No (EventSource) / Yes (fetch) | Yes |
| Auto-reconnect | N/A | Yes (EventSource) | No (manual) |
| Proxy compatibility | Universal | Universal (HTTP chunked) | Requires Upgrade header forwarding |
| HTTP/2 multiplexing | Yes | Yes | No (dedicated TCP connection) |
| Future interactive sessions | No (need new transport) | No (need new transport) | Yes (add read loop, ~15 lines) |
| Chi middleware compatibility | Full | Full | Full (coder/websocket), footgun with gorilla + `middleware.Timeout` |

#### Authentication: The Decisive Factor

For a production API serving a web UI, authentication is non-negotiable. SSE with `EventSource` forces one of these workarounds:

1. **Query param token** (`?token=jwt...`) — tokens in server logs, browser history, URL bars
2. **Cookie-based auth** — works for same-origin web UI, fails for CLI clients and cross-origin
3. **`fetch()` + `ReadableStream`** — supports headers but loses auto-reconnect, adds ~25 lines of client code

WebSocket avoids all of this. The upgrade request is a standard HTTP request with full header support:
```javascript
const ws = new WebSocket("wss://api.example.com/ws/tasks/abc123", {
  headers: { "Authorization": "Bearer " + token }  // works natively
});
```

#### Upgrade Path to Bidirectional

Going from **read-only WebSocket to bidirectional** (for future interactive sessions):
- Remove `c.CloseRead(ctx)` call
- Add a goroutine that calls `c.Read(ctx)` in a loop
- Wire reads to application logic
- **Delta: ~15-20 lines of Go**

Going from **SSE to bidirectional**:
- Replace entire SSE broker + handler with WebSocket hub + handler
- Update all client code
- Update proxy/gateway configuration
- **Delta: ~200+ lines changed across API and clients**

#### Recommendation

**Use `coder/websocket` from day one.** The upfront cost vs SSE is negligible (~5 lines server-side, ~20 lines client-side for reconnect logic). The benefits are significant: native auth headers, no SSE→WebSocket migration cost, and a clean path to bidirectional interactive sessions. The only trade-off is manual client reconnection (~15 lines of JS), which is well-documented.

### 7a. API WebSocket Endpoint

#### Endpoint

```
GET /api/v1/tasks/{taskID}/events
Connection: Upgrade
Upgrade: websocket
Authorization: Bearer <token>

Query parameters:
  after=<sequence>    Resume from a specific sequence number (for reconnection)
```

#### WebSocket Message Format

All messages are JSON. Server→client messages:

```json
{"type":"task_event","data":{"sequence":1,"timestamp":"...","type":"tool_call","summary":"Reading src/auth.go","tool":"Read"}}

{"type":"task_event","data":{"sequence":2,"timestamp":"...","type":"tool_result","summary":"package auth...","tool":"Read"}}

{"type":"task_complete","data":{"taskID":"task-abc123","status":"completed","prURL":"https://github.com/org/repo/pull/42"}}
```

- `task_event`: Individual turn-level events during execution
- `task_complete`: Terminal event, signals end of stream. Server closes WebSocket with `StatusNormalClosure` after sending this.

Ping/pong keepalive is handled automatically by `coder/websocket` via `c.CloseRead(ctx)`.

#### Reconnection

Client implements reconnection with the `after` query parameter:

```javascript
function connect(taskID, lastSequence) {
  const url = `wss://api.example.com/api/v1/tasks/${taskID}/events?after=${lastSequence}`;
  const ws = new WebSocket(url);
  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === "task_event") lastSequence = msg.data.sequence;
    handleEvent(msg);
  };
  ws.onclose = () => setTimeout(() => connect(taskID, lastSequence), 1000 + Math.random() * 2000);
}
```

On reconnect, the API replays events with `sequence > after` from the in-memory buffer, then continues live streaming.

### 7b. Kubernetes Gateway API and Proxy Configuration

Shepherd targets Kubernetes-native deployments. The Kubernetes Gateway API (successor to the now-retiring Ingress API) is the standard for external traffic routing. This section covers what's needed for WebSocket and SSE through Gateway API implementations, with focus on Envoy Gateway.

#### Ingress-nginx Retirement

Ingress-nginx is being **fully retired** (not just deprecated):

| Milestone | Date |
|-----------|------|
| Retirement announced | November 11, 2025 |
| Kubernetes Steering Committee warning | January 29, 2026 |
| Final EOL, repositories go read-only | **March 31, 2026** |

After March 2026: no releases, no bug fixes, no security patches. ~50% of cloud-native environments use ingress-nginx (Datadog research), making Gateway API migration urgent. Shepherd should only document Gateway API patterns, not legacy Ingress.

**Sources**: [Official retirement blog](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/) | [Steering Committee statement (Jan 2026)](https://kubernetes.io/blog/2026/01/29/ingress-nginx-statement/)

#### Gateway API WebSocket Support

Since Gateway API **v1.2** (October 2024, Standard channel), WebSocket support is declared via `appProtocol` on the Kubernetes Service:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: shepherd-api
spec:
  ports:
    - name: http
      port: 8080
      targetPort: 8080
      appProtocol: kubernetes.io/ws  # Tells gateway: this backend speaks WebSocket
```

The HTTPRoute itself is identical for WebSocket and regular HTTP — no special fields needed. The protocol distinction lives on the Service, not the route.

**Important**: The spec states that absence of `appProtocol` does NOT mean WebSocket is disabled. Most implementations pass `Upgrade` headers through by default. The field is a hint, not a toggle.

**Source**: [Gateway API v1.2 blog](https://kubernetes.io/blog/2024/11/21/gateway-api-v1-2/) | [GEP-1911: Backend Protocol Selection](https://gateway-api.sigs.k8s.io/geps/gep-1911/)

#### Gateway API SSE Support

SSE is plain HTTP with `Content-Type: text/event-stream` and chunked transfer encoding. No protocol upgrade, no `appProtocol` needed. The only concern is **timeouts** — SSE connections never end, so the default route timeout must be disabled.

#### Envoy Gateway (gateway.envoyproxy.io)

Envoy Gateway is the reference Gateway API implementation built on Envoy proxy.

**WebSocket**: Enabled by default on ALL HTTPRoutes. Envoy Gateway's translator automatically adds `upgradeConfigs: [{upgradeType: websocket}]` to every dynamic route. Zero configuration needed for basic WebSocket.

**Known caveat**: WebSocket + SecurityPolicy (JWT auth or ExtAuth) has had issues ([issue #4976](https://github.com/envoyproxy/gateway/issues/4976)). Verify against your Envoy Gateway version before combining JWT validation with WebSocket routes.

**Timeouts**: Envoy's defaults will kill long-lived connections. Three timeout levels interact:

| Timeout | Default | Impact | Fix |
|---------|---------|--------|-----|
| HTTPRoute `timeouts.request` | 15s | Kills WebSocket/SSE after 15s | Set `"0s"` |
| HTTPRoute `timeouts.backendRequest` | 15s | Kills backend connection | Set `"0s"` |
| Envoy HCM `stream_idle_timeout` | 5 min | Kills idle connections | `ClientTrafficPolicy` |

**Complete Envoy Gateway configuration for Shepherd's WebSocket endpoint:**

```yaml
# HTTPRoute: disable timeout for WebSocket connections
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: shepherd-api-ws
  namespace: shepherd
spec:
  parentRefs:
    - name: eg
      namespace: envoy-gateway-system
  hostnames:
    - "api.shepherd.example.com"
  rules:
    # WebSocket endpoint for event streaming
    - matches:
        - path:
            type: PathPrefix
            value: /api/v1/tasks
          headers:
            - name: Upgrade
              value: websocket
      backendRefs:
        - name: shepherd-api
          port: 8080
      timeouts:
        request: "0s"          # REQUIRED: disable 15s route timeout
        backendRequest: "0s"   # REQUIRED: disable backend-leg timeout
    # Regular REST API (default timeouts are fine)
    - matches:
        - path:
            type: PathPrefix
            value: /api/v1
      backendRefs:
        - name: shepherd-api
          port: 8080
---
# ClientTrafficPolicy: configure idle timeout for the Gateway
# Affects how long a WebSocket/SSE connection can sit with no data
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: ClientTrafficPolicy
metadata:
  name: shepherd-client-policy
  namespace: envoy-gateway-system
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: eg
  timeout:
    http:
      # Idle timeout: connection closed if no data flows for this duration.
      # For WebSocket with periodic keepalive pings (coder/websocket handles
      # this automatically), set this > ping interval. 1 hour is generous.
      idleTimeout: "3600s"
```

**Sources**: [Envoy Gateway Extension Types](https://gateway.envoyproxy.io/v1.4/api/extension_types/) | [HTTP Timeouts task](https://gateway.envoyproxy.io/docs/tasks/traffic/http-timeouts/) | [ClientTrafficPolicy](https://gateway.envoyproxy.io/latest/tasks/traffic/client-traffic-policy/) | [Issue #4859: upgradeType](https://github.com/envoyproxy/gateway/issues/4859) | [Issue #7806: idle timeout autoconfig fix](https://github.com/envoyproxy/gateway/issues/7806)

#### Other Gateway API Implementations

| Implementation | WebSocket | SSE | Notes |
|---------------|-----------|-----|-------|
| **Envoy Gateway** | Auto-enabled, zero config | Works, disable timeouts | Reference implementation |
| **Istio** | Auto-detected via Envoy sidecars | Works, disable timeouts | Most WebSocket docs still use VirtualService, not HTTPRoute |
| **Cilium** | Works (uses Envoy data plane) | Works | Had intermittent WS issues in 1.17.x ([#40822](https://github.com/cilium/cilium/issues/40822)), fixed |
| **NGINX Gateway Fabric** | Auto-detected via headers | Works | HTTPRoute `timeouts` field **not supported** yet ([#2164](https://github.com/nginx/nginx-gateway-fabric/issues/2164)) — needs global config for WS/SSE timeout |

#### SSE-Specific Gateway Configuration (for reference)

If SSE were chosen instead, the HTTPRoute configuration is simpler (no `Upgrade` header matching needed) but the timeout configuration is identical:

```yaml
# SSE only needs timeout disabled — no appProtocol, no header matching
rules:
  - matches:
      - path:
          type: PathPrefix
          value: /api/v1/tasks
    backendRefs:
      - name: shepherd-api
        port: 8080
    timeouts:
      request: "0s"
      backendRequest: "0s"
```

The key difference: SSE needs no proxy awareness of the protocol (it's just HTTP), while WebSocket needs the proxy to forward `Upgrade` headers. With Envoy Gateway this is automatic, but with other implementations it may require explicit configuration.

#### Why WebSocket Still Wins for Shepherd

Despite SSE's simpler proxy story, WebSocket is the right choice because:
1. Envoy Gateway (Shepherd's target) enables WebSocket automatically — no config overhead
2. Gateway API v1.2 standardizes `appProtocol: kubernetes.io/ws` — portable across implementations
3. Auth headers work natively — no SSE `EventSource` workarounds
4. No migration cost when interactive sessions arrive
5. The timeout configuration (`request: "0s"`, `idleTimeout`) is identical for both protocols

### 8. In-Memory Event Hub

The Event Hub is a per-task, in-memory pub/sub system inside the API server.

```go
// EventHub manages per-task event streams for SSE fan-out.
type EventHub struct {
    mu    sync.RWMutex
    tasks map[string]*taskStream
}

// taskStream holds events and subscribers for a single task.
type taskStream struct {
    mu          sync.RWMutex
    events      []TaskEvent        // ring buffer, capped at e.g. 1000 events
    subscribers map[string]chan TaskEvent  // subscriber ID → channel
    done        bool               // true when task reaches terminal state
}
```

**Lifecycle**:
1. Created lazily when the first event arrives or the first subscriber connects
2. Events appended to ring buffer and fanned out to all subscriber channels
3. Marked `done` when a terminal status event arrives
4. Cleaned up after a configurable TTL (e.g., 5 minutes after completion) to allow late subscribers to catch up

**Scaling consideration**: This is single-process. Multiple API replicas would each have their own EventHub and only serve events for tasks whose runners happen to POST to that replica. For MVP (single API replica), this is fine. For multi-replica, options include:
- **Sticky routing**: Runner always POSTs to the same API replica (simple, fragile)
- **Redis pub/sub**: Events flow through Redis, all replicas subscribe (adds dependency)
- **K8s shared informer**: Store events in CRD status and use informer (etcd size limits)

Multi-replica is explicitly out of scope for MVP.

### 9. Runner-Side Event Extraction

The Go runner entrypoint reads CC's stdout line-by-line and translates each NDJSON line into zero or more `TaskEvent` POSTs:

```go
// processStreamLine handles one line of CC's stream-json output.
// Returns zero or more TaskEvents to send to the API.
func processStreamLine(line []byte, toolMap map[string]string, seq *int64) []TaskEvent {
    var msg map[string]any
    json.Unmarshal(line, &msg)

    switch msg["type"] {
    case "assistant":
        // Extract text blocks → EventTypeThinking
        // Extract tool_use blocks → EventTypeToolCall (record tool_use_id → name in toolMap)
    case "user":
        // Extract tool_result blocks → EventTypeToolResult (look up tool name from toolMap)
    case "result":
        // Log total_cost_usd, num_turns, session_id for observability
        // Don't generate a TaskEvent — terminal status goes through existing status endpoint
    }
}
```

The runner POSTs events individually as they arrive (not batched) to minimize latency. The API's event endpoint is on the internal port (8081), same as the existing status endpoint.

**Truncation**: The runner truncates `Input` and `Output.Summary` to prevent large payloads. For example:
- `Bash` command input: first 500 chars
- `Read` file content result: first 200 chars + `"... (truncated)"`
- `Edit` input: file path + old/new string lengths

### 10. Existing System — What Changes, What Doesn't

| Component | Changes | Doesn't Change |
|-----------|---------|----------------|
| **API Server** | Add `/events` WebSocket endpoint, add EventHub, add `POST /events` handler on internal port | Existing status handler, callback system, watcher, CRD management |
| **Runner** | Add stream-json parsing, add event POST loop | HTTP server on :8888, task assignment protocol, token/data fetch |
| **Operator** | Nothing | Sandbox lifecycle, CRD reconciliation |
| **GitHub Adapter** | Nothing | Webhook handling, comment posting, callback reception |
| **CRD** | Nothing for MVP (events are in-memory only) | All existing fields and conditions |

The visibility system is purely additive. It runs alongside the existing status/callback system without modifying it. Terminal status reporting continues through the established `POST /status` → `callbackSender` → adapter flow.

### 11. Future: Interactive Sessions

Since WebSocket is already the transport for read-only monitoring, the upgrade path to interactive sessions is incremental:

```
Current (one-shot, read-only):
  Runner → POST events → API → WebSocket (server→client only) → Web UI

Future (interactive):
  Runner ↔ WebSocket ↔ API ↔ WebSocket ↔ Web UI (bidirectional)
  Web UI → API → Runner: send user messages, instructions, cancellation
  Runner → API → Web UI: agent events, responses, prompts
```

What changes:
- **API WebSocket handler**: Remove `c.CloseRead(ctx)`, add a read loop (~15 lines of Go). Wire client messages to application logic.
- **Runner→API**: Upgrade from REST POST to WebSocket (or keep REST POST for events + add a separate WebSocket for commands). Design decision deferred.
- **EventHub**: Evolves into a session manager with bidirectional message routing.
- **Agent**: Needs a server mode (not just `-p` CLI) — this is where OpenCode's `serve`/`attach` model becomes attractive. Claude Code's `-p` mode is one-shot and cannot accept follow-up messages.
- **`TaskEvent` schema**: Remains the same for server→client events. New message types added for client→server commands.

The critical advantage of starting with WebSocket: **no transport migration**. The same connection, same endpoint, same client code — just add bidirectional messages.

### 12. Future: CLI Multi-Runner Feedback

A future `shepherd run` CLI command that triggers multiple tasks needs aggregate progress:

```
$ shepherd run --tasks tasks.yaml
Task 1: fix-auth       [████░░░░░░] Reading src/auth.go
Task 2: update-deps    [██████░░░░] Running go test ./...
Task 3: add-logging    [░░░░░░░░░░] Waiting for sandbox
```

This is naturally supported by the WebSocket architecture:
- CLI opens one WebSocket connection per task (or a single multiplexed connection via `GET /api/v1/events?tasks=id1,id2,id3`)
- Each WebSocket message includes `taskID`
- CLI renders a TUI with per-task progress (libraries: bubbletea, lipgloss)
- Go has excellent WebSocket client support via `coder/websocket` (same library as the server)

The same EventHub serves both web UI and CLI consumers without any changes to the streaming protocol.

## Code References

- `pkg/api/types.go:20-25` — Current event type constants
- `pkg/api/handler_status.go:36` — Status update handler entry point
- `pkg/api/handler_status.go:78-97` — Terminal event deduplication via ConditionNotified
- `pkg/api/callback.go:51-86` — Callback sender with HMAC-SHA256
- `pkg/api/watcher.go:44-49` — Status watcher struct (informer-based safety net)
- `pkg/api/watcher.go:91-198` — Terminal transition handler with atomic claim
- `pkg/api/handler_data.go:33-72` — Task data endpoint (runner-facing)
- `pkg/api/handler_token.go:34-105` — Token endpoint with one-time-use protection
- `pkg/api/server.go:174-200` — Dual-port server setup (8080 public, 8081 internal)
- `api/v1alpha1/agenttask_types.go:106-126` — AgentTaskStatus struct
- `api/v1alpha1/conditions.go` — Condition type constants and reasons
- `pkg/adapters/github/callback.go:78-114` — Adapter callback handler
- `pkg/adapters/github/callback.go:197-250` — Callback event routing and comment posting

## Architecture Documentation

### Current Communication Pattern

```
Runner  ──POST /status──→  API  ──POST /callback──→  Adapter  ──GitHub API──→  Issue Comment
                             │
                             └──(watcher informer)──→  fallback callback if runner crashes
```

All communication is synchronous HTTP POST. No streaming, no pub/sub, no event buffering.

### Proposed Communication Pattern (Additive)

```
Runner  ──POST /events───→  API EventHub  ──WebSocket──→  Web UI / CLI (real-time)
        ──POST /status───→  API           ──POST───────→  Adapter (terminal only, unchanged)
```

The event stream is a parallel, additive channel. It does not replace the existing status/callback system. Terminal status continues through the established flow. WebSocket is served via `coder/websocket` on the public port (8080), using `c.CloseRead(ctx)` for write-only mode initially, with a clear upgrade path to bidirectional when interactive sessions arrive.

## Historical Context (from thoughts/)

- `thoughts/research/2026-01-31-background-agents-session-management-learnings.md` — Deep research into ColeMurray/background-agents ("Open-Inspect") session management. Covers WebSocket connections, Durable Object-based session coordination, interactive sessions, sandbox lifecycle. References OpenCode as agent runtime. Most directly relevant to future interactive sessions.
- `thoughts/research/2026-02-08-real-runner-image-design.md` — Section on `stream-json` output format notes future use for real-time progress streaming, live task monitoring UI, and saving CC output for audit. Identifies `--output-format stream-json` as the path to visibility.
- `thoughts/plans/2026-02-09-real-runner-image.md` — Runner implementation plan explicitly lists "No real-time progress streaming" as out of scope, noting it as future work.
- `thoughts/research/2026-01-25-shepherd-intial-arch.md` — Lists "interactive sessions" as Phase 2 goal and "OpenCode/SDK integration" as future direction.
- `thoughts/research/2026-01-27-shepherd-design.md` — Main design doc. References Ramp's Background Agent architecture as inspiration.
- `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md` — Notes K8s Events as observability mechanism, references session management learnings doc.

## Open Questions

1. **Event endpoint authentication**: The internal port (8081) is currently unauthenticated (NetworkPolicy protected). The WebSocket endpoint on the public port (8080) needs authentication. With WebSocket, standard `Authorization: Bearer` headers work — but the auth story for Shepherd doesn't exist yet. Bearer tokens? API keys? OIDC?

2. **Event buffer size**: The in-memory ring buffer per task needs a cap. A typical CC session produces 5-50 turns, so 1000 events is generous. But should we cap by count or by memory? A single large `Bash` output could be significant even after truncation.

3. **Multi-replica event fan-out**: When Shepherd runs multiple API replicas, events posted to replica A are invisible to WebSocket subscribers on replica B. Solutions (Redis, NATS, K8s events) add infrastructure. When does this become a priority?

4. **Event schema versioning**: The `TaskEvent` schema will evolve. How do we handle backward compatibility? Options: version field in events, content negotiation on WebSocket endpoint, or just break things (MVP approach).

5. **Truncation policy**: How aggressively should the runner truncate tool inputs/outputs? Too aggressive loses debugging value. Too permissive overwhelms the API and UI. Should truncation be configurable?

6. **When to evaluate OpenCode**: OpenCode's server-first architecture is compelling for interactive sessions. At what point should Shepherd prototype with OpenCode alongside Claude Code? When interactive sessions enter active planning?

7. **Cost observability**: CC reports `total_cost_usd` in the result message. Should this be exposed in the WebSocket stream as a final event? Stored in the CRD? Both? This is valuable for budget tracking and FinOps.

8. **Envoy Gateway + JWT + WebSocket**: There are known issues with Envoy Gateway's SecurityPolicy (JWT/ExtAuth) combined with WebSocket routes ([issue #4976](https://github.com/envoyproxy/gateway/issues/4976)). If Shepherd uses Envoy Gateway's built-in JWT validation, this needs testing. Alternative: handle JWT validation in the Shepherd API server itself.

9. **WebSocket client library for CLI**: The future `shepherd` CLI needs a WebSocket client. `coder/websocket` works as both server and client in Go, so the same dependency serves both. But should the CLI use raw WebSocket or a thin SDK wrapper?

## Related Research

- [Stripe Minions: One-shot, end-to-end coding agents](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents) — Part 1 only; Part 2 (technical implementation) not yet published
- [Ramp: Why We Built Our Own Background Agent](https://builders.ramp.com/post/why-we-built-our-background-agent) — OpenCode in Modal sandboxes, Durable Objects for sessions
- [Claude Code headless mode docs](https://code.claude.com/docs/en/headless) — `-p` flag, output formats
- [Claude Agent SDK streaming output](https://platform.claude.com/docs/en/agent-sdk/streaming-output) — Full stream-json message taxonomy
- [block/goose architecture](https://block.github.io/goose/docs/goose-architecture/) — Three-layer design, SSE notifications
- [OpenCode server mode docs](https://opencode.ai/docs/server/) — REST + SSE server, `attach` for remote TUI
- [OpenCode Go SDK](https://pkg.go.dev/github.com/sst/opencode-sdk-go) — Go client for OpenCode server
- [ColeMurray/background-agents](https://github.com/ColeMurray/background-agents) — Ramp's open-source blueprint
- [coder/websocket](https://github.com/coder/websocket) — Go WebSocket library, context-first API, formerly nhooyr/websocket
- [gorilla/websocket](https://github.com/gorilla/websocket) — Established Go WebSocket library, v1.5.3
- [Gateway API v1.2: WebSockets, Timeouts, Retries](https://kubernetes.io/blog/2024/11/21/gateway-api-v1-2/) — Standard WebSocket support via appProtocol
- [GEP-1911: Backend Protocol Selection](https://gateway-api.sigs.k8s.io/geps/gep-1911/) — Gateway API protocol specification
- [Envoy Gateway HTTP Timeouts](https://gateway.envoyproxy.io/docs/tasks/traffic/http-timeouts/) — Timeout configuration for long-lived connections
- [Envoy Gateway ClientTrafficPolicy](https://gateway.envoyproxy.io/latest/tasks/traffic/client-traffic-policy/) — Idle timeout configuration
- [Ingress-nginx retirement announcement](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/) — EOL March 31, 2026
- [Kubernetes Steering Committee statement on ingress-nginx](https://kubernetes.io/blog/2026/01/29/ingress-nginx-statement/) — Migration urgency
