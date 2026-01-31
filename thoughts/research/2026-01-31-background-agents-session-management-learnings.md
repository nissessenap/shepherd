---
date: 2026-01-31T13:30:22+01:00
researcher: claude
git_commit: 9eeb4c25653f69a3c76b278f47602c6bc318e2b6
branch: review_sandbox
repository: shepherd_init
topic: "Learnings from ColeMurray/background-agents session management design for Shepherd"
tags: [research, session-management, background-agents, open-inspect, ramp, durable-objects, websocket, sandbox-lifecycle, collaboration]
status: complete
last_updated: 2026-01-31
last_updated_by: claude
---

# Research: Learnings from ColeMurray/background-agents Session Management Design

**Date**: 2026-01-31T13:30:22+01:00
**Researcher**: claude
**Git Commit**: 9eeb4c25653f69a3c76b278f47602c6bc318e2b6
**Branch**: review_sandbox
**Repository**: shepherd_init

## Research Question

The open-source project [ColeMurray/background-agents](https://github.com/ColeMurray/background-agents) (also called "Open-Inspect") builds on Ramp's Inspect design and focuses heavily on session management. What design learnings can Shepherd take from its session management architecture, particularly as Shepherd considers adding interactive sessions in the future?

## Summary

Background-agents implements a sophisticated three-layer architecture: **control plane** (Cloudflare Workers + Durable Objects), **data plane** (Modal sandboxes), and **client layer** (web, Slack, browser extension). The session management is centered on a `SessionDO` Durable Object that serves as a stateful coordinator — managing WebSocket connections, message queues, sandbox lifecycle, participant presence, and artifact tracking, all backed by SQLite.

The most transferable learnings for Shepherd fall into six areas:

1. **Session as a first-class stateful entity** with a well-defined lifecycle (created → active → completed → archived)
2. **Message queue pattern** for sequential prompt processing within a session
3. **Sandbox lifecycle management** with pure decision functions separated from side effects
4. **Multi-client collaboration** via WebSocket with presence tracking
5. **Warm sandbox pools** with snapshot-based restore for near-instant startup
6. **Bridge pattern** for sandbox-to-control-plane communication

Shepherd's K8s-native architecture differs fundamentally (CRDs as state, Jobs as execution), but several of these patterns can be adapted — particularly the session lifecycle model, message queuing, and the clean separation of lifecycle decisions from execution.

## Background-Agents Architecture Overview

### Three-Layer Design

```text
┌─────────────────────────────────────────────┐
│                Client Layer                  │
│  Web (Next.js) │ Slack │ Chrome Extension    │
│           ↕ WebSocket / REST ↕               │
├─────────────────────────────────────────────┤
│              Control Plane                   │
│  Cloudflare Workers + Durable Objects        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ Router   │  │ Auth     │  │ SessionDO│  │
│  │ (chi-    │  │ (GitHub  │  │ (per-    │  │
│  │  like)   │  │  OAuth)  │  │  session)│  │
│  └──────────┘  └──────────┘  └──────────┘  │
│           ↕ WebSocket ↕                      │
├─────────────────────────────────────────────┤
│                Data Plane                    │
│  Modal Sandboxes (containerized envs)        │
│  ┌──────────┐  ┌──────────┐                 │
│  │ Manager  │  │ Bridge   │                 │
│  │ (Python) │  │ (Agent↔  │                 │
│  │          │  │  Control)│                 │
│  └──────────┘  └──────────┘                 │
└─────────────────────────────────────────────┘
```

### Technology Choices

| Layer | Technology | Purpose |
|-------|-----------|---------|
| Control plane | Cloudflare Workers + Durable Objects | Stateful session coordination, WebSocket, SQLite |
| Data plane | Modal | Containerized sandboxes with filesystem snapshots |
| Agent runtime | OpenCode | LLM agent execution inside sandboxes |
| Frontend | Next.js | Web client |
| IaC | Terraform | Multi-cloud deployment (Cloudflare, Vercel, Modal) |

## Detailed Findings

### 1. Session as a First-Class Entity

Background-agents models a session as a rich stateful object with its own lifecycle, not just a reference to a running container.

**Session states**: `created` → `active` → `completed` → `archived`

**What a session tracks** (SQLite tables inside the Durable Object):

| Table | Purpose |
|-------|---------|
| `session` | Core metadata: repo, branch, SHA, model config, status |
| `participants` | Users in the session with encrypted GitHub tokens |
| `messages` | Ordered prompts with source attribution (web, slack, extension, github) |
| `events` | Agent activity: tool calls, tokens, errors, git sync |
| `artifacts` | Outputs: PRs, screenshots, preview URLs |
| `sandbox` | Execution environment state: Modal IDs, snapshots, heartbeat |
| `ws_client_mapping` | WebSocket recovery after hibernation |

**Key insight**: The session is the unit of coordination, not the sandbox. A session can outlive individual sandbox instances (via snapshots and restores). Multiple sandboxes may serve a single session over its lifetime.

**Mapping to Shepherd**: Shepherd's current `AgentTask` CRD is a single-execution entity (create → run → complete). For interactive sessions, Shepherd would need a `Session` CRD (or similar) that acts as the long-lived coordinator, with `AgentTask` instances representing individual prompts or work items within that session.

### 2. Message Queue Pattern

Sessions process prompts sequentially via a message queue:

```text
User submits prompt → message stored with status "pending"
    ↓
processMessageQueue() checks:
  - Is sandbox connected? If not, spawn one
  - Is another message "processing"? If so, wait
    ↓
Mark message as "processing" → send to sandbox
    ↓
Sandbox streams events (tool_call, token, error, etc.)
    ↓
"execution_complete" event → mark message "completed"
    ↓
Process next pending message
```

**Message statuses**: `pending` → `processing` → `completed` | `failed`

**Key insight**: This is essentially a per-session work queue with exactly-once processing semantics. It ensures prompts are handled in order and prevents concurrent execution within a single session.

**Mapping to Shepherd**: In the current batch model, each `AgentTask` is independent. For interactive sessions, a similar queue could be implemented:
- As a list of messages in a `Session` CRD status (simple, K8s-native)
- As a separate work queue service (if scale demands it)
- Via the API server maintaining per-session queues in memory, backed by CRD state

### 3. Sandbox Lifecycle Management

This is the most architecturally interesting part. The lifecycle manager uses **pure decision functions** that return decisions (no side effects), and the manager then executes those decisions through injected dependencies.

**Decision functions** (pure, testable):
- `evaluateCircuitBreaker()` — Should we attempt to spawn? (tracks consecutive failures)
- `evaluateSpawnDecision()` — Spawn fresh, restore from snapshot, or wait?
- `evaluateInactivityTimeout()` — Timeout, extend, or schedule next check?
- `evaluateHeartbeatHealth()` — Is the sandbox responsive?
- `evaluateWarmDecision()` — Should we proactively warm a sandbox?

**Sandbox states**: `pending` → `spawning` → `connecting` → `active` → `stale` | `stopped` | `failed`

**Injected dependencies** (side-effect boundaries):

| Dependency | Purpose |
|-----------|---------|
| `SandboxProvider` | Create, restore, snapshot sandboxes |
| `SandboxStorage` | Read/write persistent state |
| `SandboxBroadcaster` | Send messages to connected clients |
| `WebSocketManager` | Manage sandbox WebSocket connections |
| `AlarmScheduler` | Schedule timeout/heartbeat checks |
| `IdGenerator` | Generate unique identifiers |

**Key insight**: Separating "what should happen" (pure functions) from "how to make it happen" (injected dependencies) makes the lifecycle logic testable without mocking infrastructure. The decision functions can be unit-tested with plain data.

**Mapping to Shepherd**: This pattern maps cleanly to Go interfaces:
```go
// Pure decision (no side effects)
type SpawnDecision struct {
    Action    SpawnAction // Spawn, RestoreSnapshot, Wait
    Reason    string
    SnapshotID string
}

func EvaluateSpawnDecision(state SandboxState, config LifecycleConfig) SpawnDecision

// Side-effect boundary
type SandboxProvider interface {
    Create(ctx context.Context, config SandboxConfig) (SandboxHandle, error)
    Restore(ctx context.Context, snapshotID string) (SandboxHandle, error)
    Snapshot(ctx context.Context, sandboxID string) (string, error)
}
```

### 4. Multi-Client Collaboration

Sessions support multiple simultaneous users ("multiplayer"):

**Participant model**:
- Each participant has a WebSocket token (SHA-256 hashed in storage, plain token returned once)
- Encrypted GitHub OAuth tokens stored per participant (for PR attribution)
- Presence tracking: status, avatar, last-seen timestamp

**WebSocket message types**:

| Direction | Message Type | Purpose |
|-----------|-------------|---------|
| Client → Server | `subscribe` | Authenticate and join session |
| Client → Server | `prompt` | Submit a user prompt |
| Client → Server | `stop` | Stop current execution |
| Client → Server | `typing` | Typing indicator |
| Client → Server | `presence` | Presence update |
| Server → Client | `subscribed` | Confirm subscription |
| Server → Client | `sandbox_event` | Agent activity stream |
| Server → Client | `presence_sync` | Presence state broadcast |
| Server → Client | `artifact` | New artifact notification |

**Hibernation recovery**: Cloudflare Durable Objects can hibernate (evict from memory). The `ws_client_mapping` table allows reconstructing the in-memory client map when the DO wakes up. WebSockets survive hibernation via Cloudflare's hibernation API.

**Key insight**: The WebSocket token model (generate, hash-store, return-once) provides session-scoped authentication without requiring the main auth flow for each connection. Commit attribution uses individual GitHub tokens, ensuring PRs show the correct author.

**Mapping to Shepherd**: For interactive sessions, Shepherd's API server could implement a WebSocket endpoint per session. In K8s, this maps to:
- API server maintains WebSocket connections to clients
- API server communicates with runner pods via the existing callback mechanism (or a bidirectional WebSocket)
- Session CRD tracks participants
- For hibernation equivalence, K8s pod restarts + CRD state provides similar recovery

### 5. Warm Sandbox Pools and Snapshots

Background-agents uses two strategies for fast sandbox startup:

**Warm pools** (Modal-specific):
- `maintain_warm_pool()` keeps N ready sandboxes per repository
- `cleanup_stale_pools()` terminates sandboxes older than 30 minutes
- When a session needs a sandbox, claim from pool → near-instant
- Warm decision triggered when user starts typing (proactive)

**Filesystem snapshots** (Modal-specific):
- After execution completes, snapshot the sandbox filesystem
- Next prompt in the same session restores from snapshot
- Preserves: repo state, session data, cached artifacts
- No git re-clone needed between prompts

**Lifecycle flow with snapshots**:
```text
Session prompt 1:
  Cold start (or warm pool) → clone repo → execute → snapshot

Session prompt 2:
  Restore from snapshot → execute → snapshot

Session prompt 3:
  Restore from snapshot → execute → snapshot
  ...

Inactivity timeout:
  Snapshot → terminate sandbox (save resources)
  Next prompt → restore from snapshot (resume where left off)
```

**Key insight**: Snapshots are what make interactive sessions viable. Without them, each prompt would require a fresh sandbox setup (clone, install deps, etc.). With snapshots, the session's working state persists even when the sandbox is shut down.

**Mapping to Shepherd**: This is where the `kubernetes-sigs/agent-sandbox` research (see `thoughts/research/2026-01-30-agent-sandbox-integration.md`) becomes directly relevant. Agent-sandbox's warm pools and future snapshot capabilities provide the K8s-native equivalent:
- `SandboxWarmPool` CRD → pre-warmed runner pods
- GKE Pod Snapshots (limited preview) → filesystem persistence between prompts
- CRIU checkpointing (upstream alpha) → full process state preservation

### 6. Bridge Pattern (Sandbox ↔ Control Plane)

The `AgentBridge` class in the sandbox establishes a persistent WebSocket back to the control plane:

**Connection management**:
- Authenticates via sandbox ID and session ID headers
- Exponential backoff reconnection (base 2.0, max 60s)
- Terminal on 401/403/404/410 (no reconnect)
- 30-second heartbeat interval

**Command handling** (control plane → sandbox):
- `prompt` — Execute user prompt via OpenCode, stream results
- `stop` — Halt current execution
- `snapshot` — Trigger filesystem snapshot
- `shutdown` — Terminate sandbox
- `push` — Git push with provided token

**Event streaming** (sandbox → control plane):
- Agent activity streamed back via the WebSocket
- Events typed: `tool_call`, `tool_result`, `token`, `error`, `git_sync`, `execution_complete`, `heartbeat`, `push_complete`

**Git identity per prompt**: The bridge configures git identity based on the prompt author, ensuring commits are attributed correctly in multiplayer sessions.

**Key insight**: The bridge is a bidirectional command/event channel. The sandbox doesn't need to know about HTTP APIs or REST endpoints — it just processes commands and emits events over a single WebSocket.

**Mapping to Shepherd**: Shepherd's current design uses a callback URL (runner → API) for status updates. For bidirectional communication, the runner could:
- Establish a WebSocket to the API server (similar to the bridge pattern)
- Or use the existing callback for events + API polling for commands (simpler, works with Jobs)
- The bridge pattern is more natural with long-lived sandboxes than with ephemeral Jobs

## Comparison: Background-Agents vs Shepherd

| Aspect | Background-Agents | Shepherd (Current) | Shepherd (Future w/ Sessions) |
|--------|-------------------|--------------------|-----------------------------|
| **Execution model** | Long-running sandbox, multiple prompts | Ephemeral Job, single task | Hybrid: Jobs for batch, Sandboxes for sessions |
| **State storage** | SQLite in Durable Object | K8s CRD | CRD for session + task state |
| **Session lifecycle** | created→active→completed→archived | N/A (task lifecycle only) | Similar lifecycle on Session CRD |
| **Sandbox management** | Modal (cloud-managed containers) | K8s Jobs (or agent-sandbox) | agent-sandbox CRDs with warm pools |
| **Communication** | WebSocket (bidirectional) | HTTP callback (unidirectional) | WebSocket or enhanced callbacks |
| **Client interfaces** | Web, Slack, Chrome extension | GitHub adapter (planned) | GitHub, Slack, CLI, Web (future) |
| **Collaboration** | Multi-user per session | Single trigger per task | Multi-user via Session CRD |
| **Snapshots** | Modal filesystem snapshots | N/A | agent-sandbox / GKE Pod Snapshots |
| **Warm pools** | Modal warm pools | N/A | SandboxWarmPool CRD |
| **Authentication** | GitHub OAuth per user | GitHub App per installation | Both (App for ops, OAuth for attribution) |
| **Infrastructure** | Cloudflare + Modal + Vercel | K8s-native (single cluster) | K8s-native |

## Key Learnings for Shepherd

### Learning 1: Session CRD Design

If Shepherd adds interactive sessions, a `Session` CRD would be the natural K8s-native equivalent of the `SessionDO` Durable Object. It would track:

```yaml
apiVersion: toolkit.shepherd.io/v1alpha1
kind: Session
metadata:
  name: session-abc123
spec:
  repo:
    url: "https://github.com/org/repo.git"
    ref: "main"
  runner:
    sandboxTemplateName: "shepherd-go-runner"
  inactivityTimeout: 10m
  maxDuration: 4h
status:
  phase: Active  # Created, Active, Completed, Archived
  sandboxName: ""
  snapshotID: ""
  participants:
    - username: "edvin"
      joinedAt: "2026-01-31T10:00:00Z"
  messageCount: 5
  lastActivityTime: "2026-01-31T10:30:00Z"
```

Individual prompts could remain as `AgentTask` CRDs with an `ownerReference` to the Session, or be tracked as status entries within the Session CRD itself.

### Learning 2: Pure Decision Functions for Lifecycle

The pattern of separating lifecycle decisions from side effects is universally applicable and particularly well-suited to Go's interface-based design. Shepherd's operator already follows a similar pattern (reconciler decides, then acts), but the explicit decision-function approach would make sandbox lifecycle management more testable.

### Learning 3: Incremental Adoption Path

Background-agents is a complete system (control plane + data plane + clients). Shepherd doesn't need to adopt it wholesale. The learnings map to Shepherd's existing roadmap:

1. **Now (batch mode)**: Current AgentTask + Job design works. No sessions needed.
2. **Next (enhanced batch)**: Add agent-sandbox integration (warm pools, templates). Runner-pull model.
3. **Later (interactive)**: Add Session CRD, WebSocket communication, message queuing, snapshots.

Each step builds on the previous without requiring a rewrite.

### Learning 4: WebSocket Token Model

The generate-hash-store-return-once pattern for WebSocket authentication is a simple, secure approach. Each session participant gets a scoped token that authenticates their WebSocket connection without requiring the full OAuth flow per connection.

### Learning 5: Git Identity Per Prompt

For multiplayer sessions, configuring git identity per prompt (not per session) ensures correct commit attribution. This is a detail that's easy to overlook but important for code review and audit trails.

### Learning 6: Proactive Sandbox Warming

Triggering sandbox warming when a user starts typing (before they submit) reduces perceived latency. In Shepherd's K8s context, this could translate to:
- Claiming a sandbox from a warm pool when a user opens a session
- Pre-pulling runner images to nodes with warm pool pods

## Architecture Patterns Worth Adopting

### Pattern: Command/Event Protocol

Background-agents defines a clean bidirectional protocol between control plane and sandbox. Commands flow down (prompt, stop, snapshot, shutdown, push), events flow up (tool_call, tool_result, token, error, execution_complete, heartbeat).

This pattern is worth adopting regardless of transport (WebSocket, gRPC, or HTTP long-poll). It cleanly separates the concerns:
- Control plane decides WHAT to do
- Sandbox/runner decides HOW to do it
- Events provide observability without tight coupling

### Pattern: Circuit Breaker for Spawn

Tracking consecutive spawn failures and implementing backoff prevents resource waste when infrastructure is degraded. Simple to implement, high value.

### Pattern: Heartbeat + Inactivity Dual Timeout

Two independent timeout mechanisms serve different purposes:
- **Heartbeat**: "Is the sandbox alive?" (health check)
- **Inactivity**: "Is anyone using this session?" (resource management)

Both are relevant for Shepherd's future session management.

## Code References (background-agents)

- `packages/control-plane/src/session/durable-object.ts` — SessionDO: main session coordinator
- `packages/control-plane/src/session/schema.ts` — SQLite schema (6 tables + indexes)
- `packages/control-plane/src/session/types.ts` — Database row types and command types
- `packages/control-plane/src/sandbox/lifecycle/manager.ts` — SandboxLifecycleManager with DI
- `packages/control-plane/src/sandbox/lifecycle/decisions.ts` — Pure decision functions
- `packages/control-plane/src/router.ts` — API router with 20 endpoints
- `packages/control-plane/src/realtime/` — WebSocket event utilities
- `packages/shared/src/types.ts` — Shared type definitions (statuses, events, messages)
- `packages/modal-infra/src/sandbox/manager.py` — Sandbox creation, warming, snapshots
- `packages/modal-infra/src/sandbox/bridge.py` — AgentBridge (sandbox ↔ control plane)

## Links

- [ColeMurray/background-agents](https://github.com/ColeMurray/background-agents) — Source repository
- [Getting Started Guide](https://github.com/ColeMurray/background-agents/blob/main/docs/GETTING_STARTED.md) — Deployment guide
- [Ramp's Background Agent](https://builders.ramp.com/post/why-we-built-our-background-agent) — Original inspiration
- [Spotify's Background Coding Agent](https://engineering.atspotify.com/2025/11/spotifys-background-coding-agent-part-1) — Parallel inspiration

## Historical Context (from thoughts/)

- `thoughts/research/2026-01-30-agent-sandbox-integration.md` — Research on kubernetes-sigs/agent-sandbox integration for Shepherd. Directly relevant: warm pools, snapshots, SandboxTemplate, and the `ExecutionBackend` interface all connect to the learnings here. The runner-pull model described there aligns with background-agents' bridge pattern.
- `thoughts/plans/2026-01-28-api-server-implementation.md` — API server plan. Phase 4 (runner callbacks) is where session management patterns would integrate.
- `docs/plans/2026-01-27-shepherd-design.md` — Main design doc. "Interactive sessions" listed as a future goal.

## Open Questions

1. **Session CRD vs API-only state**: Should session state live in a CRD (K8s-native, declarative) or in the API server's memory/database (more flexible, lower latency)? Background-agents uses Durable Objects (essentially in-process state with persistence). A CRD provides durability but introduces reconciliation latency.
2. **WebSocket in K8s**: How to handle WebSocket connections to an API server behind a K8s Service/Ingress? Sticky sessions or connection affinity may be needed. Alternatively, use Server-Sent Events (SSE) for server→client and REST for client→server.
3. **Message ordering guarantees**: Background-agents uses sequential processing per session. With K8s CRDs (eventually consistent), ensuring strict message ordering requires careful design — possibly using the API server as the ordering point rather than CRD-based queuing.
4. **Snapshot portability**: Modal's snapshots are platform-specific. If Shepherd targets generic K8s, snapshot support depends on cluster capabilities (GKE Pod Snapshots, CRIU, or agent-sandbox's future deep hibernation). What's the fallback for clusters without snapshot support?
5. **Multi-cluster sessions**: Background-agents is single-region (Cloudflare edge + Modal). If Shepherd sessions span clusters or regions, how does the session coordinator maintain state consistency?
