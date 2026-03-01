# Async Event Posting: Fire-and-Forget vs Buffered Channel

**Date**: 2026-03-01
**Context**: PR #41 / Issue #37 — preventing `PostEvents` from blocking the runner's stdout pipe reader
**Status**: Decision pending

## Problem

`PostEvents` is an HTTP POST to the API server (a separate K8s pod, not localhost). If the API is slow or unreachable, the synchronous call blocks the `StreamStdout` callback, which stalls the `bufio.Scanner` loop, which fills the 64KB kernel pipe buffer, which blocks Claude Code's stdout writes — stalling the agent entirely.

The HTTP client has a 30s timeout, so a single stuck call freezes the agent for up to 30 seconds.

## Option A: Fire-and-Forget Goroutine

```go
go func() {
    if postErr := eventPoster.PostEvents(ctx, task.TaskID, events); postErr != nil {
        log.Info("failed to post events", "error", postErr)
    }
}()
```

### Pros

- **Minimal complexity**: 5 lines, no new types, no channel, no WaitGroup, no drain logic
- **Pipe never blocks**: each callback returns immediately regardless of HTTP latency
- **Easy to understand**: obvious what it does, no concurrency patterns to reason about
- **Goroutines are cheap**: thousands of outstanding goroutines are fine in Go
- **Bounded lifetime**: goroutines die when `ctx` is cancelled (pod shutdown) or HTTP call completes/times out

### Cons

- **Out-of-order delivery**: if POST N is slow and POST N+1 is fast, events arrive at the API out of sequence. The EventHub appends in arrival order without sorting. The frontend drops events where `sequence <= lastSequence`, so out-of-order events are silently lost
  - *Mitigation*: add `sort.Slice` by sequence in EventHub before appending (small server-side change)
  - *Likelihood*: low on a stable cluster network — HTTP to the same endpoint over a single TCP connection is nearly always FIFO
- **No backpressure**: if the API is down for 30s and Claude Code produces 500 lines, 500 goroutines are spawned, each holding a request in-flight until timeout. Memory usage: ~4KB stack per goroutine + request body ≈ ~2-4MB total. Not dangerous, but unbounded
- **No flush guarantee**: when `Run` returns, in-flight POSTs may still be executing. Events posted after the task is marked complete could be ignored by the API or race with status updates
  - *Practical impact*: low — the last few events are cosmetic (frontend already has the result)
- **Harder to test drop behavior**: no channel to observe; would need to mock PostEvents and track concurrent calls

## Option B: Buffered Channel + Posting Goroutine (current PR #41)

```go
eventCh = make(chan []api.TaskEvent, 32)
wg.Go(func() {
    for events := range eventCh {
        if postErr := eventPoster.PostEvents(ctx, task.TaskID, events); postErr != nil {
            log.Info("failed to post events", "error", postErr)
        }
    }
})

// In callback:
select {
case eventCh <- events:
default:
    log.Info("event channel full, dropping events", "count", len(events))
}

// After execCmd.Run:
close(eventCh)
wg.Wait()
```

### Pros

- **Preserves event order**: single consumer goroutine posts sequentially, so events always arrive at the API in sequence order
- **Backpressure with graceful degradation**: buffer absorbs bursts (32 batches); excess is dropped rather than spawning unbounded goroutines
- **Flush guarantee**: `close(eventCh)` + `wg.Wait()` ensures all buffered events are posted before `Run` returns — no racing with task completion
- **Observable drops**: the `default` branch logs when events are dropped, giving clear signal that the API can't keep up
- **Testable**: channel mechanics are deterministic and testable (see `TestRunAsyncEventPostingDropsWhenFull`)

### Cons

- **More complex**: ~30 lines of new code, WaitGroup, channel, drain logic, new test mock (`firstCallSlowPoster`)
- **Drain can block**: if the API is slow during drain, `wg.Wait()` blocks `Run` from returning. With 32 buffered batches and 30s timeout, worst case is ~16 minutes
  - *Mitigation*: add `if ctx.Err() != nil { continue }` in the goroutine loop to skip posting when context is cancelled
- **Fixed buffer size**: 32 is a magic number; too small wastes events under burst, too large delays backpressure signal. In practice 32 is fine for this workload
- **Channel buffer size (32) drops events under sustained backpressure**: same as fire-and-forget losing events, just with a different mechanism (channel full vs out-of-order)

## Summary

| Concern                  | Fire-and-Forget        | Buffered Channel       |
|--------------------------|------------------------|------------------------|
| Pipe blocking            | Solved                 | Solved                 |
| Event ordering           | Not guaranteed         | Guaranteed             |
| Backpressure             | Unbounded goroutines   | Bounded buffer + drop  |
| Flush on exit            | No                     | Yes                    |
| Complexity               | Minimal (~5 lines)     | Moderate (~30 lines)   |
| Drain blocking risk      | None                   | Up to 16min worst case |
| Testability              | Harder                 | Straightforward        |

## Recommendation

Events are best-effort and the agent must never stall for them. Both options solve the core problem (pipe blocking). The choice comes down to whether event ordering and flush guarantees matter enough to justify the added complexity.

If out-of-order loss is acceptable (or mitigated server-side with a sort in EventHub), fire-and-forget is the pragmatic choice. If ordered delivery and clean shutdown matter, the buffered channel is the right tool.
