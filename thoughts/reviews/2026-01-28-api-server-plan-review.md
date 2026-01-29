# Code Review Report: API Server Implementation Plan

**Date:** 2026-01-28
**Plan:** `thoughts/plans/2026-01-28-api-server-implementation.md`
**Reviewer:** Claude Opus 4.5

---

**Summary:**

- **Verdict**: NEEDS REVISION
- **Blockers**: 4
- **High Priority Issues**: 5
- **Medium Priority Issues**: 6

---

## Blockers (Must Fix)

### 1. Plan moves `APICmd` to a new file but `main.go` edit is underspecified

**Phase 1, Step 6** proposes creating `cmd/shepherd/api.go` with a new `APICmd` struct, then says "Remove the inline `APICmd` struct and `Run` method (lines 38-44)" from `main.go`. However, the plan's `APICmd` changes the `Run` signature from `Run(globals *CLI)` to `Run(_ *CLI)` and adds new fields (`CallbackSecret`, `Namespace`). This is fine architecturally, but the plan **does not show the actual edit to `main.go`** -- it just says "remove lines 38-44." Since `main.go` also contains the `GitHubCmd` stub (lines 46-55), the plan needs to be explicit that only `APICmd` is removed, not the surrounding code.

**Suggestion:** Add the explicit diff for `main.go` showing the removal of lines 38-44 only, preserving `GitHubCmd`.

### 2. `server.go` code has compilation errors

**Phase 1, Step 5** -- The `Run()` function references `time.Second` (line 213 of the plan) but the import list does not include `"time"`. The code will not compile as written.

Similarly, Phase 2's `handler_tasks.go` calls `time.ParseDuration` and `time.RFC3339` but the import list on plan line 383 does not include `"time"`.

**Suggestion:** Add `"time"` to both import lists. Since these are pseudo-code snippets, consider adding a note that imports are illustrative and should be completed during implementation.

### 3. `handler_tasks.go` references `meta.FindStatusCondition` without correct import

**Phase 2, line 536:** `extractStatus` calls `meta.FindStatusCondition(...)` but the import list (plan lines 383-395) does not include `"k8s.io/apimachinery/pkg/api/meta"`. The `meta` package is also used in Phase 3 (`isTerminalFromStatus`) and Phase 4 (`updateTaskStatus`). The plan must specify this import.

Similarly, Phase 4's `handler_status.go` references both `meta.FindStatusCondition` and `meta.SetStatusCondition` (plan lines 948-949, 964) without importing the `meta` package, and also references `log` without showing how it's obtained (it's not a method receiver or parameter).

Phase 5's `watcher.go` references `statusMeta.SetStatusCondition` (plan line 1194), which is a completely different name from the `meta` alias used elsewhere. This is an inconsistency that will cause compilation failure.

**Suggestion:** Standardize on `"k8s.io/apimachinery/pkg/api/meta"` imported as `apimeta` (to avoid collision with the `meta` shorthand for `metav1`) across all files. Fix `statusMeta` in `watcher.go` to use the same alias.

### 4. Phase 4 `updateTaskStatus` has a double-write bug with race condition

**Plan lines 930-934 and 960-974:** The handler does a `Status().Update()` to write PR URL/error, then later does another `Status().Update()` to set the `Notified` condition. Between these two writes, the resource version has changed. The second `Status().Update()` on plan line 971 uses the same `task` object, which now has a **stale resource version** from the first write.

This will cause a conflict error on the second update in production. The plan even re-fetches in the watcher's `setNotifiedCondition` (plan line 1188-1191) to avoid exactly this problem, but fails to do the same in the handler.

**Suggestion:** Either:
- Combine both status changes into a single `Status().Update()` call, or
- Re-fetch the task between the two updates (like the watcher does), or
- Use `Status().Patch()` instead of `Update()` to avoid resource version conflicts

---

## High Priority Issues (Strongly Recommend Fixing)

### 1. Mixing controller-runtime `client.Client` with raw `dynamic.Interface` is unnecessarily complex

The plan proposes using `sigs.k8s.io/controller-runtime/pkg/client` for CRUD operations and `k8s.io/client-go/dynamic` for the informer (Phase 5). This requires two separate K8s client configurations and two different object representations (typed vs unstructured), with a manual `toAgentTask` conversion function (plan lines 1207-1218).

Controller-runtime provides `cache.Informer` that works with typed objects natively. Since the plan already imports controller-runtime and uses its client, the watcher could use a controller-runtime informer cache instead, eliminating the dynamic client, the unstructured conversion, and reducing complexity.

**Suggestion:** Consider using `ctrl.NewManager` (or a lighter-weight `cache.New`) for the informer instead of raw `dynamic.Interface`. This would let the watcher work with typed `AgentTask` objects directly and match the operator's architectural pattern.

### 2. No request body size limit

**Phase 2's `createTask` handler** decodes the request body with `json.NewDecoder(r.Body).Decode(&req)` (plan line 406) without limiting body size. The `context` field can contain arbitrary text that gets gzip-compressed. An attacker could POST a multi-GB body and exhaust memory.

**Suggestion:** Add `http.MaxBytesReader` before decoding:
```go
r.Body = http.MaxBytesReader(w, r.Body, 2<<20) // 2MB limit
```

### 3. The `CallbackSpec.URL` validation pattern `^https?://` allows `http://` callback URLs

**`agenttask_types.go:72`** allows HTTP callback URLs. The plan's HMAC signing provides integrity but the callback payload (including task details, PR URLs, errors) travels in plaintext over HTTP. For cluster-internal MVP this is documented as acceptable, but the plan should explicitly note that production deployments should enforce HTTPS-only callbacks or document the threat model.

**Suggestion:** Add a note in the plan's Security Considerations about HTTP callback URLs being acceptable for cluster-internal MVP only, and add a TODO for HTTPS enforcement in production.

### 4. `writeJSON` silently swallows encoding errors

**Plan line 505:** `json.NewEncoder(w).Encode(v)` -- if encoding fails (e.g., a field has an unsupported type), the error is silently dropped. Worse, `WriteHeader` has already been called on line 504, so the client receives a partial/empty JSON response with a 200/201 status.

**Suggestion:** Marshal to bytes first, then write:
```go
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
    data, err := json.Marshal(v)
    if err != nil {
        http.Error(w, `{"error":"internal encoding error"}`, http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    w.Write(data)
}
```

### 5. Watcher's `handleUpdate` method creates goroutine-unsafe behavior

**Plan lines 1122-1182:** `handleUpdate` is called from the informer's event handler, which runs in the informer's processing goroutine. Inside `handleUpdate`, the code calls `w.callback.send()` (plan line 1166) which makes an HTTP request with a 10-second timeout, and then calls `w.setNotifiedCondition()` which does a K8s API read+write.

Blocking the informer's event handler for up to 10+ seconds delays processing of all other events. If the callback target is slow, this creates a cascading delay.

**Suggestion:** Queue terminal events and process them in a separate goroutine (e.g., using a `workqueue.RateLimitingInterface` from client-go), or at minimum spawn a goroutine per terminal event to avoid blocking the informer. The plan should document this design decision.

---

## Medium Priority Suggestions (Consider for Follow-up)

### 1. `compressContext` returns `("", "", nil)` for empty context, but Phase 2 then checks `contextValue == ""` and replaces with `"-"`

**Plan lines 356 and 436-438:** The flow is:
1. `compressContext("")` returns `("", "", nil)`
2. `contextValue = ""`, then replaced with `"-"`
3. `encoding` remains `""` (not "gzip")

This means the CRD has `context: "-"` with `contextEncoding: ""`. The init container will try to interpret `"-"` as a literal context string, not a placeholder. The plan should clarify how the init container handles this case, or whether the API should reject requests without context entirely.

### 2. Task name generation uses `rand.String(8)` -- collision risk

**Plan line 441:** `fmt.Sprintf("task-%s", rand.String(8))` uses `k8s.io/apimachinery/pkg/util/rand`, which generates lowercase alphanumeric strings. With 8 characters from a 36-character alphabet, there are ~2.8 trillion combinations. For a small cluster this is fine, but the plan could use `GenerateName` on the `ObjectMeta` instead, which is the idiomatic K8s approach:

```go
ObjectMeta: metav1.ObjectMeta{
    GenerateName: "task-",
    Namespace:    h.namespace,
}
```

This delegates uniqueness to the API server and eliminates the collision risk entirely.

### 3. Phase 3's `listTasks` does in-memory filtering for `active=true`

**Plan lines 682-691:** The active filter fetches all tasks from the namespace and filters in memory. For MVP this is fine (as noted in "What We're NOT Doing"), but the plan should add a note that this will need server-side filtering (using field selectors or label-based state tracking) before production.

### 4. `CallbackPayload.Details` uses `map[string]interface{}`

**Plan line 820:** Using `interface{}` in Go 1.25 (which has `any` as an alias) is a style nit, but more importantly, this untyped map makes it easy to produce inconsistent callback payloads. The adapter has to guess the shape of `Details`. Consider using typed structs for known events.

### 5. Phase 5 imports `"k8s.io/apimachinery/pkg/watch"` but never uses it

**Plan line 1078:** The `watch` package is imported but no function from it appears in the watcher code. This will fail `go vet`.

### 6. Plan proposes `cmd/shepherd/api.go` as a new file but leaves `GitHubCmd` inline

**`cmd/shepherd/main.go:38-44`** defines `APICmd` inline, while `cmd/shepherd/operator.go` puts `OperatorCmd` in its own file. The plan correctly follows the `operator.go` pattern by creating `api.go`. However, it leaves `GitHubCmd` still inline in `main.go`. For consistency, note this as future cleanup (or move `GitHubCmd` now).

---

## Good Practices Observed

- **Phased implementation** with explicit pause points between phases is disciplined and review-friendly. Each phase has clear success criteria with both automated and manual verification.

- **Following existing patterns** -- the plan mirrors the `pkg/operator/operator.go` architecture (Options struct, `Run()` function, signal-based shutdown, controller-runtime logging). This reduces cognitive load.

- **HMAC callback signing** is the right approach for verifying callback integrity without per-task secrets.

- **Notified condition for dedup** is a well-designed pattern that uses the K8s status subresource as the single source of truth, avoiding external state stores.

- **"What We're NOT Doing" section** is clear and prevents scope creep.

- **Test strategy** is thorough -- table-driven tests, httptest, fake K8s client, and explicit test case lists per phase.

- **SecretRef removal** is correctly identified as unused dead code. Cleaning it up before building on top of the CRD is the right call.
