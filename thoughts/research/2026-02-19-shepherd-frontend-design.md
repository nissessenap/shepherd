---
date: 2026-02-19T23:30:00+01:00
researcher: claude
git_commit: 741a8c03549cdaf118aeee18efd5604711e9c386
branch: stripe_minons
repository: NissesSenap/shepherd_init
topic: "Shepherd Web UI: frontend design, UX flows, technology stack, and testing strategy"
tags: [research, frontend, ui, ux, design, react, svelte, vite, playwright, websocket, streaming, testing]
status: complete
last_updated: 2026-02-19
last_updated_by: claude
---

# Research: Shepherd Web UI Design

**Date**: 2026-02-19T23:30:00+01:00
**Researcher**: claude
**Git Commit**: 741a8c03549cdaf118aeee18efd5604711e9c386
**Branch**: stripe_minons
**Repository**: NissesSenap/shepherd_init

## Research Question

How should the Shepherd web UI look and behave? What technology stack should be used, how should it integrate with the existing Go API, and how should it be tested? Inspired by Stripe Minions and Ramp Inspect, designed for real-time agent visibility via WebSocket streaming.

## Decisions Made During Research

These decisions were made through interview with the project owner:

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Deployment model | Separate container | Clean separation, static files served by Caddy/nginx |
| Target users | Both individual dev AND platform/SRE team | Like Stripe Minions -- personal list + team visibility |
| Real-time streaming | Essential from day one | WebSocket as designed in streaming architecture doc |
| UI scope | Monitor first, create tasks later | Ship read-only monitoring, add task creation as follow-up |
| Auth | None for MVP | Port-forward access, OAuth planned for future |
| Language | TypeScript required | Project owner preference |
| Dependency philosophy | Minimal | Avoid unnecessary third-party libs, prefer writing small functions |

## Summary

This document synthesizes research from six specialized agents (UX specialist, visual designer, frontend architect, senior frontend engineer, QA specialist, and web researcher) into a complete frontend design specification for Shepherd.

**Key conclusions:**

1. **Framework**: React 19 + Vite 7 (SPA, no SSR) is recommended over Svelte 5 based on CNCF ecosystem alignment (every K8s tool uses React), ecosystem maturity, and the Go team's ability to find answers. Svelte 5 is a viable alternative if the team prefers its developer experience.

2. **Architecture**: Two pages only -- task list and task detail. URL-driven state for all filters. WebSocket streaming for real-time events. ~5 production npm dependencies.

3. **Design**: Dark-mode-first developer tool aesthetic. Information-dense but clear. Event stream is the hero component. PR link is the most prominent element for completed tasks.

4. **Testing**: Vitest for unit/component tests, Playwright for E2E with `page.routeWebSocket()` for WebSocket testing. OpenAPI-generated TypeScript types for contract testing.

5. **Security**: The September 2025 chalk/debug npm supply chain attack (2.6B weekly downloads compromised) and the December 2025 React2Shell CVE (CVSS 10.0 RCE in RSC) validate the minimal-dependency approach. Pure SPAs are unaffected by React2Shell.

## Detailed Findings

### 1. Technology Stack Recommendation

#### Framework Decision: React 19 + Vite 7

Two of six agents recommended different frameworks (Svelte vs React). After cross-referencing with web research findings, React is the primary recommendation.

**Why React wins for Shepherd:**

| Factor | React | Svelte 5 |
|--------|-------|----------|
| CNCF ecosystem | Every K8s tool uses React (Headlamp, ArgoCD, Grafana, Backstage) | Zero CNCF adoption |
| TanStack Query | Stable v5 adapter, 12M downloads/week | v5 adapter "buggy and unreliable" with Svelte 5 runes; v6 adapter just released |
| Go team support | Largest community, most StackOverflow answers | Smaller community, fewer resources |
| Hiring | Largest talent pool | Growing but smaller |
| CVE-2025-55182 | Does NOT affect SPAs (only RSC) | N/A |
| Bundle size | ~45 kB runtime | ~2 kB runtime |
| Learning curve | Medium (hooks, JSX) | Low (HTML-like templates) |

**Why Svelte is a viable alternative:** If the team values developer experience and minimal bundle size over ecosystem alignment, Svelte 5 + SvelteKit is a strong choice. Its template syntax feels natural to Go developers, it has the smallest runtime, and the runes API is explicit. The TanStack Query v6 Svelte adapter addresses the friction issues.

**The recommendation is React, but either works. The architecture described below is framework-agnostic.**

#### Complete Recommended Stack

| Category | Choice | Prod Deps | Rationale |
|----------|--------|-----------|-----------|
| Framework | React 19.2 + ReactDOM | 2 | CNCF standard, vast ecosystem, patched for CVE-2025-55182 |
| Build Tool | Vite 7 | 0 (dev) | De facto standard, instant HMR, trivial proxy config |
| Routing | React Router v7 | 1 | 2 routes total, URL-driven filters |
| Data Fetching | TanStack Query v5 | 1 | Caching, polling, loading states out of the box |
| State Management | useReducer + TanStack cache | 0 | No global store needed for 2-page monitoring app |
| WebSocket | Custom typed wrapper (~60-80 lines) | 0 | Dynamic reconnect URL (`?after=N`) requires custom logic |
| Styling | CSS Modules or Tailwind CSS | 0-1 | Scoped styles, dark mode via CSS custom properties |
| Syntax Highlighting | Shiki | 1 | VS Code grammars, lazy-loaded, highest quality |
| Linting/Formatting | Biome 2.x | 0 (dev) | Single binary, 10-25x faster than ESLint+Prettier |
| Unit Testing | Vitest | 0 (dev) | Native Vite integration, Jest-compatible API |
| Component Testing | Testing Library | 0 (dev) | User-centric queries, framework-agnostic |
| E2E Testing | Playwright 1.58 | 0 (dev) | Stable WebSocket testing via `page.routeWebSocket()` |
| Container | Caddy 2 (alpine) | N/A | Simple config, automatic HTTPS, SPA fallback |
| **Total production deps** | | **~5** | react, react-dom, react-router, @tanstack/react-query, shiki |

#### npm Security Context (Why Minimal Dependencies Matters)

The project owner's instinct about minimizing dependencies is validated by 2025-2026 events:

- **September 2025**: `chalk`, `debug`, `ansi-styles` and 15 other packages (2.6B weekly downloads collectively) were compromised via maintainer account phishing. Malicious versions were live for ~2 hours. These are transitive deps in virtually every JS project.
- **December 2025**: CVE-2025-55182 ("React2Shell"), CVSS 10.0 -- unauthenticated RCE in React Server Components. Actively exploited by nation-state groups within hours. **Does NOT affect SPAs** (only RSC).
- **January 2026**: Six zero-day vulnerabilities in npm, pnpm, and Bun package manager runtimes themselves.

**Practical defenses:**

- Lock files + hash verification (commit `package-lock.json`)
- `npm install --ignore-scripts` in CI
- Pin exact versions, don't auto-update same-day
- Minimize dependencies (every transitive dep is a supply chain node)

### 2. User Experience Design

#### User Personas

**Alex -- Individual Developer**

- Triggered `@shepherd fix the null pointer` on a GitHub issue
- Wants to know: Is it running? What is it doing? Did it create a PR?
- Mental model: "I asked for help, I want to check on progress"
- Primary flow: open Shepherd UI → find my task → watch it work → click PR link

**Sam -- Platform/SRE Team Lead**

- Manages Shepherd deployment across the org
- Wants to know: How many tasks are running? Any failures? Resource usage?
- Mental model: "Dashboard for the fleet of agents"
- Primary flow: open Shepherd UI → scan summary stats → filter by repo/fleet → investigate failures

#### Information Architecture

Two pages. That's it.

```
/tasks                     → Task list with filtering
/tasks/{taskID}            → Task detail with live streaming or results
```

Every meaningful state is encoded in the URL:

```
/tasks                              → All tasks, default sort
/tasks?active=true                  → Active tasks only
/tasks?repo=org/repo                → Filtered by repo
/tasks?repo=org/repo&active=true    → Combined filters
/tasks?fleet=gpu-agents             → Filtered by fleet
/tasks/task-abc-123                 → Task detail
```

URLs are shareable. Pasting a URL in Slack takes the recipient directly to the right view. Filters survive page refresh because they're in query params.

No separate "dashboard" page for Sam. The task list with summary stats at the top serves both personas. Alex ignores the stats bar. Sam uses it.

#### Core User Flows

**Flow 1: Developer checks on triggered task**

```
1. Developer opens /tasks (bookmark or Slack link)
2. Default filter: active=true (show running/pending tasks)
3. Finds their task by repo name in the list
4. Clicks task row → navigates to /tasks/{taskID}
5. If Running: sees live event stream
6. If Succeeded: sees PR link prominently
7. If Failed: sees error message with last agent action
```

**Flow 2: Developer monitors a running agent in real-time**

```
1. Task detail page loads, REST fetch gets task metadata
2. Phase is "Running" → WebSocket connection established
3. "LIVE" indicator appears with green dot
4. Events stream in one by one:
   - thinking events: muted text showing agent reasoning
   - tool_call events: highlighted with tool name badge (Read, Edit, Bash)
   - tool_result events: attached to their tool call, show success/failure
   - error events: red background, expanded by default
5. Auto-scroll keeps newest events visible
6. If user scrolls up: "N new events" pill appears at bottom
7. Task completes → badge transitions to Succeeded/Failed
8. PR card slides into view (if succeeded)
9. WebSocket closes gracefully
```

**Flow 3: Platform team reviews all tasks**

```
1. Sam opens /tasks (no filters)
2. Summary stats bar shows: 12 active, 3 pending, 47 completed today, 2 failed
3. Applies filters: repo dropdown, fleet dropdown, status toggles
4. Sorts by: newest first (default), or by duration
5. Clicks a failed task to investigate
6. Error callout shows timeout reason + last agent action
7. Event log shows last events before failure (expanded by default for failed tasks)
```

**Flow 4: User investigates a failed task**

```
1. Task detail shows red "Failed" or "TimedOut" badge
2. Error callout box at top: "Task timed out after 30m0s"
3. "Last agent action" in the error box: bash: npm run build (running 4m 12s)
4. Event log defaults to showing LAST events (opposite of succeeded which shows first/last)
5. Failing tool call's output is expanded by default
6. [copy] button on error box for pasting into Slack/GitHub
```

### 3. Visual Design System

#### Design Principles

1. **Transparency Over Abstraction**: Show what the agent is doing. Developers distrust black boxes.
2. **Temporal Clarity**: Make time, sequence, and current state immediately legible.
3. **Density Without Clutter**: Higher information density than consumer apps. Use typography and color for hierarchy instead of whitespace.
4. **Keyboard-First, Mouse-Compatible**: Primary users live in terminals. `j`/`k` navigation, `Enter` to open, `Escape` to go back.
5. **Quiet Until Urgent**: Restrained palette. Color and visual weight reserved for states needing attention (errors, disconnection).

#### Color System (Dark Mode Default)

```
Background:
  primary:    #0d1117  (near-black, like GitHub dark)
  secondary:  #161b22  (slightly lighter, for cards/panels)
  elevated:   #1c2128  (dropdowns, hover states)
  terminal:   #0a0e13  (code blocks, distinctly darker)

Text:
  primary:    #e6edf3  (off-white, high contrast)
  secondary:  #8b949e  (medium gray, metadata)
  muted:      #6e7681  (dim gray, timestamps)

Status:
  pending:    amber     (waiting)
  running:    blue      (active, with subtle pulse)
  succeeded:  green     (done, PR created)
  failed:     red       (error)
  timed-out:  orange    (exceeded deadline)
  cancelled:  gray      (stopped)

Accent:     bright blue   (interactive elements, links)
```

Status colors always paired with icons AND text labels -- never rely on color alone (colorblind accessibility).

#### Typography

```
UI text:    system font stack (matches GitHub, zero download)
            -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif

Monospace:  JetBrains Mono (optional web font), falling back to system monospace
            "JetBrains Mono", SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace

Headings:   H1: 24px, H2: 18px, H3: 15px (only 3 levels needed)
Body:       14px (developer-tool sweet spot)
Small:      12px (metadata, timestamps)
Code:       13px monospace
```

#### Layout

```
No persistent sidebar. Header-based navigation only.
Main content max-width: 1200px (task list), 1400px (task detail)
Task list: table-row hybrid (flexbox rows, not cards)
Task detail: CSS Grid -- 70% event stream (left) + 30% metadata panel (right, sticky)
Breakpoints: 768px (mobile), 1024px (tablet), 1200px (desktop)
```

### 4. ASCII Wireframes

#### Task List View

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  SHEPHERD    Tasks                                    cluster: prod-us-east  │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  12 active    3 pending    47 completed today    2 failed                    │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ [Active ▾]  [All repos ▾]  [All fleets ▾]     Search: [___________]  │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  STATUS   REPO              TASK                      DURATION   FLEET      │
│  ──────── ───────────────── ─────────────────────────  ────────  ─────────  │
│  ● Run    org/api-server    Fix auth middleware err..   4m 12s   default    │
│  ● Run    org/web-client    Add dark mode toggle to..   2m 01s   default    │
│  ● Run    org/ml-pipeline   Refactor data loader fo..   8m 45s   gpu-pool   │
│  ◐ Pend   org/api-server    Update OpenAPI spec for..      22s   default    │
│  ◐ Pend   org/docs          Fix broken links in mig..      15s   default    │
│  ✓ Done   org/api-server    Add retry logic to paym..  12m 03s   default    │
│  ✓ Done   org/ml-pipeline   Fix memory leak in batc..   6m 44s   gpu-pool   │
│  ✗ Fail   org/web-client    Migrate from webpack to..  30m 00s   default    │
│  ✗ Fail   org/api-server    Add GraphQL subscriptio..  15m 22s   default    │
│  ✓ Done   org/docs          Update API reference fo..   3m 11s   default    │
│                                                                              │
│  Showing 10 of 62 tasks                                    [Load more]      │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**

- Summary stats bar at top provides Sam's (SRE) at-a-glance view
- Status uses both icon AND color: Running (blue dot), Pending (half dot, amber), Succeeded (green check), Failed (red X)
- Task description truncated to one line. Full text on hover and detail page
- Duration shows elapsed time for active tasks (updated every second client-side) and total duration for completed tasks
- "Load more" instead of pagination. URL supports `?page=2` for direct linking
- Filter dropdowns preserve state in URL query params

#### Task Detail View -- Running (Live Monitoring)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  SHEPHERD    Tasks                                    cluster: prod-us-east  │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Tasks / task-abc-123                                                        │
│                                                                              │
│  ┌─────────────────────────────────────────────────────┬──────────────────┐  │
│  │                                                     │                  │  │
│  │  LIVE ◉  Event Log                    42 events    │  ● Running       │  │
│  │  ───────────────────────────────────────────────    │  4m 12s elapsed  │  │
│  │                                                     │                  │  │
│  │  12:30:03  thinking                                 │  Repo:           │  │
│  │            Analyzing the codebase structure to      │  org/api-server  │  │
│  │            understand how auth middleware is...      │  @ main          │  │
│  │                                                     │                  │  │
│  │  12:30:05  Read  src/middleware/auth.go              │  Task:           │  │
│  │            Read 156 lines                     ✓    │  Fix auth middle │  │
│  │            [▶ input] [▶ output: 156 lines]          │  ware error      │  │
│  │                                                     │  handling for    │  │
│  │  12:30:08  Read  src/middleware/auth_test.go         │  expired tokens  │  │
│  │            Read 203 lines                     ✓    │                  │  │
│  │            [▶ input] [▶ output: 203 lines]          │  Source:         │  │
│  │                                                     │  issues/142      │  │
│  │  12:30:12  thinking                                 │  #comment-98765  │  │
│  │            The error handling in auth.go line 45    │                  │  │
│  │            catches the token expiry but returns     │  Fleet: default  │  │
│  │            a generic 500 instead of 401...          │                  │  │
│  │                                                     │                  │  │
│  │  12:30:15  Edit  src/middleware/auth.go              │                  │  │
│  │            Modified lines 42-58 (+8/-3)       ✓    │                  │  │
│  │            ┌─────────────────────────────────┐      │                  │  │
│  │            │ @@ -42,3 +42,8 @@                │      │                  │  │
│  │            │ - return fmt.Errorf("token err") │      │                  │  │
│  │            │ + if errors.Is(err, jwt.Expired)  │      │                  │  │
│  │            │ +   return &AuthError{Code: 401} │      │                  │  │
│  │            └─────────────────────────────────┘      │                  │  │
│  │            [▶ full diff]                             │                  │  │
│  │                                                     │                  │  │
│  │  12:30:20  Bash  go test ./src/middleware/...        │                  │  │
│  │            Tests passed (exit 0, 2.3s)        ✓    │                  │  │
│  │            [▶ output: 12 lines]                      │                  │  │
│  │                                                     │                  │  │
│  │  ┌───────────────────────────────────────────┐      │                  │  │
│  │  │  ◉ Agent is working...                     │      │                  │  │
│  │  └───────────────────────────────────────────┘      │                  │  │
│  │                                                     │                  │  │
│  └─────────────────────────────────────────────────────┴──────────────────┘  │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**

- "LIVE" indicator with pulsing dot makes it obvious this is real-time
- 70/30 split: event stream (left) is the hero, metadata panel (right) is sticky
- Timestamps left-aligned, creating a timeline feel
- Tool calls color-coded by category: file ops (blue), shell (amber), search (green)
- Diffs shown inline for edit operations (highest-value information)
- `[▶ input]` and `[▶ output]` are expandable sections
- Thinking events are visually lighter (gray text vs white)
- "Agent is working..." bar at bottom anchors the live state

#### Task Detail View -- Succeeded (Result)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  SHEPHERD    Tasks                                    cluster: prod-us-east  │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Tasks / task-abc-123                                                        │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │  ✓ Succeeded                                       12m 03s duration   │  │
│  │  Repo: org/api-server @ main                                           │  │
│  │  Task: Fix auth middleware error handling for expired tokens            │  │
│  │  Completed: 2026-02-19 12:42:06 UTC                                    │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │  Pull Request                                                          │  │
│  │                                                                        │  │
│  │  #42  Fix auth middleware error handling for expired tokens             │  │
│  │  org/api-server    +32 -8 across 3 files       [ Open in GitHub → ]   │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  Event Log                                                 67 events        │
│  ────────────────────────────────────────────────────────────────────────    │
│                                                                              │
│  12:30:03  thinking    Analyzing the codebase structure...                   │
│  12:30:05  Read        src/middleware/auth.go                          ✓    │
│  ...                                                                         │
│  (collapsed: 60 events)                        [Show all 67 events]         │
│  ...                                                                         │
│  12:41:50  Bash        git push origin shepherd/task-abc-123          ✓    │
│  12:42:01  completed   Task completed successfully. PR #42 created.         │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**

- PR card is the hero element -- impossible to miss, "Open in GitHub" is primary CTA
- `+32 -8 across 3 files` gives quick sense of PR size
- Event log present but middle portion collapsed for completed tasks
- First few and last few events shown; "Show all 67 events" expands full log
- No "LIVE" indicator -- replaced by static "Event Log" header

#### Task Detail View -- Failed

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  SHEPHERD    Tasks                                    cluster: prod-us-east  │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Tasks / task-def-456                                                        │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │  ✗ Failed                                          30m 00s duration   │  │
│  │  Repo: org/web-client @ main                                           │  │
│  │  Task: Migrate from webpack to vite bundler                            │  │
│  │  Completed: 2026-02-19 13:15:00 UTC                                    │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌─ ERROR ───────────────────────────────────────────────────────────────┐  │
│  │  Task timed out after 30m0s                                            │  │
│  │                                                                        │  │
│  │  The agent exceeded the maximum allowed execution time.                │  │
│  │  Last agent action: bash: npm run build (running 4m 12s)               │  │
│  │                                                                [copy]  │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  Event Log (showing last 10 events)                        134 events       │
│  ────────────────────────────────────────────────────────────────────────    │
│                                                                              │
│  13:12:15  Bash   npm run build                                       ✗    │
│            TIMEOUT - process terminated after 4m 12s                         │
│            ┌──────────────────────────────────────────────────────┐          │
│            │ ERROR in ./src/legacy/components/DataGrid.jsx         │          │
│            │ Module not found: Can't resolve './data-grid.css'     │          │
│            └──────────────────────────────────────────────────────┘          │
│                                                                              │
│  13:14:48  error   Task timed out after 30m0s                                │
│                                                                              │
│                                                [Show all 134 events]        │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**

- Error callout uses red/danger color scheme (red left border, light red background)
- "Last agent action" gives immediate debugging context without scrolling
- Event log defaults to showing LAST events for failed tasks
- Failing tool call's output expanded by default
- `[copy]` button for pasting error into Slack/GitHub

### 5. UI-to-API Data Flows

#### Flow A: Page Load → Task List

```
Browser                    Frontend                   API Server (:8080)
  |                          |                          |
  |  GET /tasks              |                          |
  |------------------------->|                          |
  |                          |  GET /api/v1/tasks?      |
  |                          |      active=true         |
  |                          |------------------------->|
  |                          |                          |  k8sClient.List(ctx, &taskList)
  |                          |                          |  filter IsTerminal() in-memory
  |                          |  200 OK                  |
  |                          |  [{id, status, repo}...] |
  |                          |<-------------------------|
  |  Render task cards       |                          |
  |<-------------------------|                          |
  |                          |                          |
  |       ... 30s later (background poll) ...           |
  |                          |                          |
  |                          |  GET /api/v1/tasks?      |
  |                          |      active=true         |
  |                          |------------------------->|
  |                          |  200 OK (diff renders)   |
  |<-------------------------|<-------------------------|
```

Filters are URL-driven. `useSearchParams()` reads filter state, passes to query function. TanStack Query caches by query key `["tasks", {repo, fleet, active}]`. Background poll every 30 seconds keeps list fresh.

#### Flow B: Task Detail with Live WebSocket Streaming

```
Browser                    Frontend                   API Server (:8080)
  |                          |                          |
  |  Navigate to             |                          |
  |  /tasks/task-abc123      |                          |
  |------------------------->|                          |
  |                          |  GET /api/v1/tasks/      |
  |                          |      task-abc123         |
  |                          |------------------------->|
  |                          |  200 OK {phase:Running}  |
  |                          |<-------------------------|
  |  Render metadata         |                          |
  |  Show "Connecting..."    |  Phase=Running → open WS |
  |                          |                          |
  |                          |  WS Upgrade:             |
  |                          |  /api/v1/tasks/          |
  |                          |    task-abc123/events     |
  |                          |------------------------->|
  |                          |  101 Switching Protocols  |
  |  Show "LIVE" indicator   |<-------------------------|
  |                          |                          |
  |                          |  {type:"task_event",     |
  |                          |   data:{seq:1,           |
  |                          |   type:"thinking",...}}  |
  |  Render thinking event   |<-------------------------|
  |                          |                          |
  |                          |  {type:"task_event",     |
  |                          |   data:{seq:2,           |
  |                          |   type:"tool_call",      |
  |                          |   tool:"Read",...}}      |
  |  Render tool_call event  |<-------------------------|
  |                          |                          |
  |          ... events stream in ...                   |
  |                          |                          |
  |                          |  {type:"task_complete",  |
  |                          |   data:{taskID:...,      |
  |                          |   prURL:"...pull/42"}}   |
  |                          |<-------------------------|
  |                          |  WS close (1000)         |
  |                          |  Refetch task via REST   |
  |                          |------------------------->|
  |                          |  200 OK {phase:Succeeded,|
  |                          |   prURL:"...pull/42"}    |
  |  Update badge, show PR   |<-------------------------|
```

#### Flow C: WebSocket Reconnection After Disconnect

```
Browser                    Frontend                   API Server (:8080)
  |                          |                          |
  |  Viewing running task    |  WS connected            |
  |  Events streaming        |  lastSequence = 47       |
  |                          |                          |
  |                          |  -- connection drops --   |
  |                          |                          |
  |  Show "Reconnecting..."  |  Not code 1000 →         |
  |                          |  schedule reconnect      |
  |                          |                          |
  |       ... 1s + jitter delay ...                     |
  |                          |                          |
  |                          |  WS Upgrade:             |
  |                          |  /api/v1/tasks/          |
  |                          |    task-abc123/events?    |
  |                          |    after=47              |
  |                          |------------------------->|
  |                          |  101 Switching Protocols  |
  |  Show "LIVE" indicator   |<-------------------------|
  |                          |                          |
  |                          |  {seq:48, ...}  replay   |
  |                          |  {seq:49, ...}  replay   |
  |                          |  {seq:50, ...}  live     |
  |  Render missed events    |<-------------------------|
  |  (user sees no gap)      |                          |
```

Reconnection uses exponential backoff: 1s, 2s, 4s, 8s, 16s (max 30s) plus jitter. After 5 failed attempts, shows "Disconnected" with manual retry button. Falls back to polling REST every 10s for status changes.

#### Flow D: Filter Change on Task List

```
Browser                    Frontend                   API Server (:8080)
  |                          |                          |
  |  Type "myorg/myrepo"     |                          |
  |  in repo filter          |                          |
  |------------------------->|                          |
  |                          |  300ms debounce...       |
  |                          |  URL → /tasks?repo=      |
  |                          |  myorg/myrepo&active=true|
  |                          |                          |
  |  (Previous results stay  |  GET /api/v1/tasks?      |
  |   visible during fetch)  |    repo=myorg/myrepo&    |
  |                          |    active=true           |
  |                          |------------------------->|
  |                          |  200 OK [filtered]       |
  |  Replace list            |<-------------------------|
```

### 6. Component Architecture

#### Component Tree

```
App
├── TaskListPage (/tasks)
│   ├── FilterBar           -- Status toggles, repo dropdown, search, sort
│   │   └── (URL params)    -- Filters stored in URL search params
│   ├── SummaryStats        -- "12 active, 3 pending, 47 completed, 2 failed"
│   └── TaskCard[]          -- One per task in the list
│       └── StatusBadge     -- Colored pill: Pending/Running/Succeeded/Failed
│
└── TaskDetailPage (/tasks/:taskID)
    ├── Breadcrumb          -- "Tasks / task-abc-123"
    ├── TaskMetadata        -- Repo, description, source URL, timing
    │   └── StatusBadge
    ├── PRCard              -- (if Succeeded) Hero element with GitHub link
    ├── ErrorCallout        -- (if Failed) Red box with error + last action
    ├── ConnectionIndicator -- (if Running) Green/yellow/red dot
    └── EventStream         -- Scrollable list of events
        └── EventItem[]     -- One per event
            ├── ThinkingEvent   -- Muted text, agent reasoning
            ├── ToolCallEvent   -- Tool badge + input summary + expandable details
            ├── ToolResultEvent -- Success/failure indicator + output
            └── ErrorEvent      -- Red highlight, expanded by default
```

#### Event Rendering by Type

| Event Type | Visual Treatment | Default State |
|-----------|-----------------|---------------|
| `thinking` | Muted text, brain icon, dimmer color | Collapsed if > 3 lines |
| `tool_call` | Teal left border, tool name badge (Read, Edit, Bash, Grep), input summary | Expanded (one-line summary) |
| `tool_result` | Nested under tool_call, success ✓ or failure ✗ indicator, output preview | Collapsed (click to expand) |
| `error` | Red background tint, red border, error icon | Expanded always |

Tool calls color-coded by category:

- **File operations** (Read, Edit, Write): blue
- **Shell operations** (Bash): amber/yellow
- **Search operations** (Grep, Glob): green
- **Other**: gray

### 7. WebSocket Integration Architecture

```
                    +─────────────+
                    │   ws.ts     │  Raw WebSocket wrapper (~60-80 lines TS)
                    │  (generic)  │  Knows: connect, reconnect, parse JSON
                    +──────+──────+
                           │ onMessage(parsed)
                           │ onStateChange(state)
                           v
               +───────────────────────+
               │  useTaskEvents hook   │  Application logic
               │  (task-specific)      │  Knows: sequences, event accumulation,
               │                       │  when to connect/disconnect based on phase
               +───────────+───────────+
                           │ returns {events, streamPhase, connectionState}
                           v
               +───────────────────────+
               │  TaskDetailPage       │  Rendering
               │    ├── EventStream    │  Knows: how to display events
               │    │   └── EventItem  │  Knows: how to render one event type
               │    └── ConnectionInd  │  Knows: green/yellow/red dot
               +───────────────────────+
```

**State managed by useReducer:**

```
{
  events: TaskEvent[]           // accumulated, append-only
  lastSequence: number          // highest sequence number seen
  streamPhase: "idle" | "connecting" | "streaming" | "completed" | "error"
  connectionState: "connecting" | "connected" | "reconnecting" | "disconnected"
}
```

**State transitions:**

```
idle → CONNECT_REQUESTED → connecting
connecting → CONNECTED → streaming
streaming → EVENT_RECEIVED → streaming (append event, update lastSequence)
streaming → GAP_DETECTED → connecting (reconnect with ?after=lastSequence)
streaming → TASK_COMPLETE → completed (close WS, refetch task via REST)
streaming → CONNECTION_LOST → connecting (reconnect with backoff)
connecting → CONNECTION_FAILED (after max retries) → error
error → RETRY_REQUESTED → connecting (manual retry resets backoff)
```

**Sequence gap detection:** If incoming `event.sequence > lastSequence + 1`, a gap exists. Close WebSocket, reconnect with `?after={lastSequence}`. Server replays from in-memory ring buffer. Reducer deduplicates events with `sequence <= lastSequence`.

### 8. Testing Strategy

#### Testing Pyramid

| Layer | Share | Purpose |
|-------|-------|---------|
| Unit tests (Vitest) | 50% | WebSocket reconnection logic, event parsing, state derivation, URL construction, time formatting |
| Component tests (Vitest + Testing Library) | 25% | Individual components render correctly given props/state, event stream ordering, loading/error/empty states |
| Integration tests (Playwright, mocked WS) | 15% | Multi-component flows with route transitions, WebSocket mock server delivering scripted events |
| E2E tests (Playwright, real Go API) | 10% | Full stack: create task via API, watch it flow through states, verify UI reflects reality |

#### Unit Testing Priorities

**Highest priority** (most critical, hardest to debug in production):

1. WebSocket reconnection: backoff timer, retry counter, `?after=` parameter calculation
2. Event sequence validation: gap detection, deduplication, ordering
3. Status derivation: maps CRD condition reasons to UI phases (mirrors Go `extractStatus`)

#### Playwright E2E Strategy

**WebSocket testing**: Playwright 1.58 has stable `page.routeWebSocket()` API for intercepting and mocking WebSocket connections. This enables deterministic testing of real-time streaming without a real WebSocket server.

**Key test scenarios** (10 tests maximum):

1. Task list loads and displays tasks with correct status badges
2. Clicking a task navigates to detail view
3. Live events stream in during a running task (mock WS sends events with delays)
4. Task completes and shows PR link
5. Filtering tasks by repository (URL params update)
6. WebSocket reconnects after network interruption (mock disconnect + reconnect)
7. Empty state when no tasks exist
8. Deep link to a specific task works
9. Browser back button preserves filter state
10. Accessibility scan passes (axe-core)

**Test environment**: Real Go API server (built with `make build`, pointed at envtest) + static frontend served by dev server or `npx serve`. Docker Compose available for CI.

**Test data seeding**: Create tasks via `POST /api/v1/tasks` (public port 8080). Transition states via `POST /api/v1/tasks/{id}/status` (internal port 8081). This matches how the real system works.

#### API Contract Testing

**Recommended**: Generate OpenAPI spec from Go types (swaggo/swag or oapi-codegen) → generate TypeScript types with `openapi-typescript` → CI step fails if generated output differs from committed types. Eliminates type drift between Go and TypeScript permanently.

#### CI Integration

```yaml
# GitHub Actions workflow
name: Frontend Tests
on:
  pull_request:
    paths: ['web/**', 'pkg/api/types.go', 'pkg/api/server.go']
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - Build Go API server (make build)
      - Install frontend deps (npm ci)
      - Build frontend (npm run build)
      - Install Playwright browsers (chromium only)
      - Start API server (envtest backend)
      - Serve frontend (npx serve dist)
      - Run Vitest (unit + component)
      - Run Playwright (E2E)
      - Upload test report + traces on failure
```

### 9. Empty States and Edge Cases

| State | What the user sees |
|-------|-------------------|
| No tasks exist | Centered message: "No tasks yet. Tasks are created when you mention @shepherd in a GitHub issue comment." with example |
| Task is Pending | "Waiting for sandbox to be provisioned... typically takes 10-30s." If > 2 min: "Waiting longer than usual." |
| WebSocket disconnects | Banner: "⚠ Connection lost. Reconnecting... [attempt 2/5]" → falls back to REST polling if all retries fail |
| Task was cancelled | "This task was cancelled." with duration before cancel. Event log from before cancellation shown below |
| Filtered list returns nothing | "No tasks match your filters." with dismissible filter chips and "Clear all filters" button |
| Task completes while viewing | Badge transitions from Running (blue) to Succeeded (green). PR card slides in. No modal, no confetti |

### 10. Design Tokens

```
Spacing scale:     4, 8, 12, 16, 24, 32, 48 px (4px base unit)
Border radius:     sm: 4px, md: 6px, lg: 8px, pill: 9999px
Elevation:         none, subtle (dropdowns), medium (modals), strong (overlays)
Z-index layers:    base(0), sticky(10), header(20), dropdown(30), modal(50), tooltip(60)
Animation:         fast: 150ms, normal: 200ms, slow: 300ms, pulse: 2000ms
Focus ring:        2px solid accent color, 2px offset
Content max-width: 1200px (list), 1400px (detail)
Event stream:      70% width, 2px left border for tool calls
Metadata panel:    30% width, sticky positioning
```

### 11. Container Image

Multi-stage Dockerfile with Caddy:

```
Stage 1 (build):  node:22-alpine, npm ci, npm run build
Stage 2 (serve):  caddy:2-alpine, copy built assets to /srv
```

Caddyfile (~5 lines):

```
:8080
root * /srv
file_server
try_files {path} /index.html
header /assets/* Cache-Control "public, max-age=31536000, immutable"
```

Final image: ~45 MB. Caddy handles SPA routing, static file serving, and can proxy `/api/*` to the backend service for development parity.

### 12. Keyboard Shortcuts (Progressive Enhancement)

| Key | Action |
|-----|--------|
| `j` / `k` | Move between tasks in list (vim-style) |
| `Enter` | Open selected task detail |
| `Escape` / `Backspace` | Return to task list |
| `e` | Expand all events in detail view |
| `c` | Collapse all events |
| `G` | Scroll to bottom / resume auto-scroll |
| `/` | Focus search input |

## Code References

- `pkg/api/server.go:175-200` -- Dual-port server setup (8080 public, 8081 internal)
- `pkg/api/types.go:20-25` -- Event type constants (started, progress, completed, failed)
- `pkg/api/types.go:59-68` -- TaskResponse struct (the primary type the UI consumes)
- `pkg/api/types.go:71-77` -- TaskStatusSummary struct (phase, message, prURL, error)
- `pkg/api/handler_tasks.go:238-279` -- List tasks with label-based filtering (repo, issue, fleet, active)
- `api/v1alpha1/conditions.go:19-39` -- Condition reasons (Pending, Running, Succeeded, Failed, TimedOut, Cancelled)

## Architecture Documentation

### Current API Surface (What the Frontend Consumes)

**Public port 8080:**

| Method | Path | Purpose |
|--------|------|---------|
| GET | /healthz | Health check |
| GET | /readyz | Readiness check |
| POST | /api/v1/tasks | Create task |
| GET | /api/v1/tasks | List tasks (?repo, ?issue, ?fleet, ?active) |
| GET | /api/v1/tasks/{taskID} | Get single task |

**Planned (from streaming architecture research):**

| Method | Path | Purpose |
|--------|------|---------|
| GET (WS) | /api/v1/tasks/{taskID}/events | WebSocket event stream |

**Internal port 8081 (runner-only, for test seeding):**

| Method | Path | Purpose |
|--------|------|---------|
| POST | /api/v1/tasks/{taskID}/status | Update task status |
| GET | /api/v1/tasks/{taskID}/data | Get task data |
| GET | /api/v1/tasks/{taskID}/token | Get GitHub token |

### Backend Gaps the Frontend Depends On

These are planned but not implemented:

1. **WebSocket endpoint** (`GET /api/v1/tasks/{taskID}/events`) -- entire streaming infrastructure
2. **Runner stream-json parsing** -- runner uses `--output-format json` (single blob), needs `stream-json`
3. **Event POST endpoint** (`POST /api/v1/tasks/{taskID}/events` on port 8081)
4. **`coder/websocket` Go dependency** -- not in go.mod yet

The frontend can be built before these exist. The REST-only task detail view works immediately with the existing `GET /api/v1/tasks/{taskID}` endpoint. WebSocket streaming is additive.

## Historical Context (from thoughts/)

- `thoughts/research/2026-02-18-agent-visibility-streaming-architecture.md` -- WebSocket architecture, event schema, EventHub design, transport protocol comparison (SSE vs WebSocket). Chose WebSocket for native auth headers and bidirectional upgrade path.
- `thoughts/research/2026-01-27-shepherd-design.md` -- Main design doc. CRD specification, component responsibilities, data flow, job specification.
- `thoughts/research/2026-01-31-background-agents-session-management-learnings.md` -- Research into Ramp's session management, WebSocket connections, interactive sessions.
- `thoughts/research/2026-02-08-real-runner-image-design.md` -- Runner `stream-json` output format, future real-time progress streaming.

## Related Research

- [Stripe Minions Blog](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents) -- Web UI visibility, repo-based task listing
- [Ramp Inspect Blog](https://builders.ramp.com/post/why-we-built-our-background-agent) -- Real-time streaming, statistics dashboard, multiplayer sessions
- [CVE-2025-55182 React2Shell](https://react.dev/blog/2025/12/03/critical-security-vulnerability-in-react-server-components) -- Critical RCE in RSC, does NOT affect SPAs
- [chalk/debug npm compromise](https://www.sonatype.com/blog/npm-chalk-and-debug-packages-hit-in-software-supply-chain-attack) -- 2.6B weekly downloads compromised Sept 2025
- [Kubernetes Dashboard ARCHIVED](https://github.com/kubernetes/dashboard) -- Replaced by Headlamp (React + Go)
- [Headlamp (new K8s Dashboard)](https://github.com/kubernetes-sigs/headlamp) -- React + TypeScript, CNCF sig-ui
- [coder/websocket Go library](https://github.com/coder/websocket) -- v1.8.14, context-first API, no JS client needed
- [Playwright WebSocketRoute API](https://playwright.dev/docs/api/class-websocketroute) -- Stable WS testing
- [Biome 2.3](https://biomejs.dev/blog/biome-v2-3/) -- Svelte/Vue formatting support

## Open Questions

1. **React vs Svelte final decision**: This document recommends React based on ecosystem alignment, but Svelte's DX advantages are real. The project owner should try both (scaffold a simple task list page in each) before committing.
Svelte, [see](./2026-02-24-svelte5-frontend-rules-tanstack-evaluation.md).

2. **Tailwind vs CSS Modules**: Both work. Tailwind adds one dependency but reduces context-switching between files. CSS Modules add zero dependencies. Try both approaches on 2-3 components and see what the team prefers.

3. **OpenAPI generation tooling**: Which Go tool to use for OpenAPI spec generation (swaggo/swag vs oapi-codegen vs manual spec)? This affects the contract testing pipeline.

4. **Monorepo vs separate repo**: Should the frontend live in the same repo as the Go backend (monorepo) or a separate repo? Monorepo simplifies CI (contract tests run on same PR) but mixes Go and Node tooling.

5. **Cost/token display**: The streaming architecture research mentions `total_cost_usd` in the result message. Should this be shown in the UI? Where? This is valuable for FinOps but may need to be behind a feature flag.
We can add it later

6. **Multi-task monitoring view**: The streaming architecture research (section 12) describes a future CLI with multi-task progress bars. Should the web UI have a similar view? This would be a third page type.
You can't have a progress bar since we don't know how long an LLM will take. We can only have an status bar, in-progress, waiting to start running, completed, failed (maybe some other).
An overview of all the jobs would be nice, but I think that is already planned.

7. **Event persistence**: Currently events are in-memory only (lost on API restart). Should completed task events be persisted for post-mortem analysis? This is an API-side decision that affects whether the frontend can show historical events.
Yes, In the long run but for the MVP we can keep everything in memory.
