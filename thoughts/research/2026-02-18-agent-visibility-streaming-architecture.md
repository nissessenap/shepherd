---
date: 2026-02-18T14:00:00+01:00
researcher: claude
git_commit: 6c1523a6de6a46547abe502701bc2ea09a115068
branch: stripe_minons
repository: NissesSenap/shepherd_init
topic: "Agent visibility and streaming architecture: real-time task monitoring inspired by Stripe Minions"
tags: [research, codebase, streaming, sse, visibility, stripe, minions, claude-code, opencode, goose, architecture]
status: complete
last_updated: 2026-02-18
last_updated_by: claude
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
| API→Consumer protocol | SSE for MVP | Real-time UX, ~200 lines more than polling, deprecate when WebSocket arrives for interactive sessions |
| Runner→API protocol | REST POST (agent-agnostic schema) | Runner is producer, POST is fire-and-forget, anyone can implement |
| Document scope | Architecture only | Implementation phases come as a separate plan |

## Summary

Shepherd currently has no streaming or real-time visibility infrastructure. The status system supports four events (`started`, `progress`, `completed`, `failed`) via synchronous HTTP POST callbacks. To achieve Stripe Minions-style visibility, Shepherd needs three additions: (1) an agent-agnostic event schema that the runner POSTs to the API, (2) an in-memory event hub in the API that fans out to subscribers, and (3) an SSE endpoint that web UI and CLI clients consume.

The architecture is designed around a clean split: the runner parses agent-specific output (Claude Code's `stream-json`) and translates it into agent-agnostic events. The API never knows which agent produced the events. This allows future runners to use OpenCode, Goose, or any other agent without API changes.

Claude Code's `--output-format stream-json` provides rich NDJSON streaming with assistant messages (containing `tool_use` blocks), user messages (containing `tool_result` blocks), and metadata (`total_cost_usd`, `num_turns`, `session_id`). The Go runner entrypoint reads this stdout line-by-line, extracts turn-level events, and POSTs them to the API.

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
                                            SSE                │
                    ┌──────────────────────────────────────────┘
                    │
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
7. EventHub fans out to all SSE subscribers for that task
8. Web UI / CLI connected via SSE receive events in real-time
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

### 7. API Streaming Endpoint (SSE)

#### Endpoint

```
GET /api/v1/tasks/{taskID}/events
Accept: text/event-stream

Query parameters:
  after=<sequence>    Resume from a specific sequence number (for reconnection)
  token=<auth_token>  Authentication (SSE doesn't support custom headers via EventSource)
```

#### SSE Event Format

```
event: task_event
data: {"sequence":1,"timestamp":"...","type":"tool_call","summary":"Reading src/auth.go","tool":"Read"}

event: task_event
data: {"sequence":2,"timestamp":"...","type":"tool_result","summary":"package auth...","tool":"Read"}

event: task_complete
data: {"taskID":"task-abc123","status":"completed","prURL":"https://github.com/org/repo/pull/42"}

event: keepalive
data: {}
```

- `task_event`: Individual turn-level events during execution
- `task_complete`: Terminal event, signals end of stream. Mirrors existing `completed`/`failed` semantics.
- `keepalive`: Sent every 15-30 seconds to prevent connection timeout

#### Reconnection

SSE has built-in reconnection via `Last-Event-ID` header. The API uses the `sequence` field as the event ID:

```
id: 5
event: task_event
data: {"sequence":5,...}
```

On reconnect, the client sends `Last-Event-ID: 5` and the API replays events with `sequence > 5` from the in-memory buffer.

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
| **API Server** | Add `/events` SSE endpoint, add EventHub, add `POST /events` handler on internal port | Existing status handler, callback system, watcher, CRD management |
| **Runner** | Add stream-json parsing, add event POST loop | HTTP server on :8888, task assignment protocol, token/data fetch |
| **Operator** | Nothing | Sandbox lifecycle, CRD reconciliation |
| **GitHub Adapter** | Nothing | Webhook handling, comment posting, callback reception |
| **CRD** | Nothing for MVP (events are in-memory only) | All existing fields and conditions |

The visibility system is purely additive. It runs alongside the existing status/callback system without modifying it. Terminal status reporting continues through the established `POST /status` → `callbackSender` → adapter flow.

### 11. Future: Interactive Sessions and WebSocket

When interactive sessions become a priority, the architecture evolves:

```
Current (one-shot, read-only):
  Runner → POST events → API → SSE → Web UI (read-only)

Future (interactive):
  Runner → WebSocket → API → WebSocket → Web UI (bidirectional)
  Web UI → API → Runner: send user messages, instructions
  Runner → API → Web UI: agent events, responses
```

At that point:
- SSE endpoint is deprecated in favor of WebSocket
- The EventHub evolves into a session manager
- The agent needs a server mode (not just `-p` CLI) — this is where OpenCode's `serve`/`attach` model becomes attractive
- The `TaskEvent` schema remains the same; only the transport changes

This is why the agent-agnostic event schema is important: it decouples the transport (SSE now, WebSocket later) from the event semantics.

### 12. Future: CLI Multi-Runner Feedback

A future `shepherd run` CLI command that triggers multiple tasks needs aggregate progress:

```
$ shepherd run --tasks tasks.yaml
Task 1: fix-auth       [████░░░░░░] Reading src/auth.go
Task 2: update-deps    [██████░░░░] Running go test ./...
Task 3: add-logging    [░░░░░░░░░░] Waiting for sandbox
```

This is naturally supported by the SSE architecture:
- CLI opens one SSE connection per task (or a single multiplexed connection via `GET /api/v1/events?tasks=id1,id2,id3`)
- Each SSE event includes `taskID`
- CLI renders a TUI with per-task progress (libraries: bubbletea, lipgloss)

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
Runner  ──POST /events───→  API EventHub  ──SSE──→  Web UI / CLI (real-time)
        ──POST /status───→  API           ──POST──→  Adapter (terminal only, unchanged)
```

The event stream is a parallel, additive channel. It does not replace the existing status/callback system. Terminal status continues through the established flow.

## Historical Context (from thoughts/)

- `thoughts/research/2026-01-31-background-agents-session-management-learnings.md` — Deep research into ColeMurray/background-agents ("Open-Inspect") session management. Covers WebSocket connections, Durable Object-based session coordination, interactive sessions, sandbox lifecycle. References OpenCode as agent runtime. Most directly relevant to future interactive sessions.
- `thoughts/research/2026-02-08-real-runner-image-design.md` — Section on `stream-json` output format notes future use for real-time progress streaming, live task monitoring UI, and saving CC output for audit. Identifies `--output-format stream-json` as the path to visibility.
- `thoughts/plans/2026-02-09-real-runner-image.md` — Runner implementation plan explicitly lists "No real-time progress streaming" as out of scope, noting it as future work.
- `thoughts/research/2026-01-25-shepherd-intial-arch.md` — Lists "interactive sessions" as Phase 2 goal and "OpenCode/SDK integration" as future direction.
- `thoughts/research/2026-01-27-shepherd-design.md` — Main design doc. References Ramp's Background Agent architecture as inspiration.
- `thoughts/research/2026-02-01-shepherd-sandbox-architecture.md` — Notes K8s Events as observability mechanism, references session management learnings doc.

## Related Research

- [Stripe Minions: One-shot, end-to-end coding agents](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents) — Part 1 only; Part 2 (technical implementation) not yet published
- [Ramp: Why We Built Our Own Background Agent](https://builders.ramp.com/post/why-we-built-our-background-agent) — OpenCode in Modal sandboxes, Durable Objects for sessions
- [Claude Code headless mode docs](https://code.claude.com/docs/en/headless) — `-p` flag, output formats
- [Claude Agent SDK streaming output](https://platform.claude.com/docs/en/agent-sdk/streaming-output) — Full stream-json message taxonomy
- [block/goose architecture](https://block.github.io/goose/docs/goose-architecture/) — Three-layer design, SSE notifications
- [OpenCode server mode docs](https://opencode.ai/docs/server/) — REST + SSE server, `attach` for remote TUI
- [OpenCode Go SDK](https://pkg.go.dev/github.com/sst/opencode-sdk-go) — Go client for OpenCode server
- [ColeMurray/background-agents](https://github.com/ColeMurray/background-agents) — Ramp's open-source blueprint

## Open Questions

1. **Event endpoint authentication**: The internal port (8081) is currently unauthenticated (NetworkPolicy protected). The SSE endpoint on the public port (8080) needs authentication. Bearer token in query param? API key? This ties into the broader Shepherd auth story which doesn't exist yet.

2. **Event buffer size**: The in-memory ring buffer per task needs a cap. A typical CC session produces 5-50 turns, so 1000 events is generous. But should we cap by count or by memory? A single large `Bash` output could be significant even after truncation.

3. **Multi-replica event fan-out**: When Shepherd runs multiple API replicas, events posted to replica A are invisible to SSE subscribers on replica B. Solutions (Redis, NATS, K8s events) add infrastructure. When does this become a priority?

4. **Event schema versioning**: The `TaskEvent` schema will evolve. How do we handle backward compatibility? Options: version field in events, content negotiation on SSE endpoint, or just break things (MVP approach).

5. **Truncation policy**: How aggressively should the runner truncate tool inputs/outputs? Too aggressive loses debugging value. Too permissive overwhelms the API and UI. Should truncation be configurable?

6. **SSE vs fetch streaming**: Browser `EventSource` doesn't support custom headers. The alternative is `fetch()` with `ReadableStream`, which supports headers but requires more client code. Which should the web UI use? If we use query param tokens, `EventSource` is simpler. If we want proper `Authorization` headers, `fetch()` streaming is needed.

7. **When to evaluate OpenCode**: OpenCode's server-first architecture is compelling for interactive sessions. At what point should Shepherd prototype with OpenCode alongside Claude Code? When interactive sessions enter active planning?

8. **Cost observability**: CC reports `total_cost_usd` in the result message. Should this be exposed in the SSE stream as a final event? Stored in the CRD? Both? This is valuable for budget tracking and FinOps.
