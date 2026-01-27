---
date: 2026-01-27T12:00:00+01:00
researcher: Claude
git_commit: 64392b29c0031630f81163b547d65c4fb02fa4ae
branch: plan
repository: NissesSenap/shepherd
topic: "Package choices, binary architecture, CRD design, and pre-implementation decisions for Shepherd"
tags: [research, architecture, packages, crd-design, kubebuilder, go]
status: complete
last_updated: 2026-01-27
last_updated_by: Claude
---

# Research: Package Choices, Binary Architecture, CRD Design, and Pre-Implementation Decisions

**Date**: 2026-01-27T12:00:00+01:00
**Researcher**: Claude
**Git Commit**: 64392b29c0031630f81163b547d65c4fb02fa4ae
**Branch**: plan
**Repository**: NissesSenap/shepherd

## Research Questions

1. What Go packages should we use?
2. Is a single binary practical given kubebuilder's opinions?
3. Is testify a good choice for unit tests?
4. Is zap still standard in kubebuilder?
5. Should we use Kong or Cobra for CLI?
6. Which API framework should we use?
7. How should the CRD be designed in more detail?
8. What else should we think through before a detailed plan?

## Summary

This research covers the key technical decisions needed before implementing Shepherd. The findings support most of the current design with some important refinements, particularly around CRD design (replacing `status.events[]` with proper conditions), CLI framework choice (Cobra over Kong), and additional fields the CRD needs.

---

## 1. Single Binary vs Separate Binaries with Kubebuilder

### The Core Tension

Kubebuilder is opinionated about project layout: `cmd/main.go` at the root, `api/` for CRD types, `internal/controller/` for reconcilers, `config/` for manifests. The kubebuilder docs explicitly warn: "It is not recommended to deviate from the proposed layout unless you know what you are doing."

### How Similar Projects Handle This

| Project | Approach | Notes |
|---------|----------|-------|
| **Loki/Mimir** | Single binary, `-target` flag | Not kubebuilder projects; custom Go projects |
| **Flux** | Separate controllers per concern | Each controller is its own kubebuilder project |
| **ArgoCD** | Monolithic with gRPC/REST | Custom project structure, not kubebuilder |
| **Tekton** | Standard K8s API patterns | Uses API aggregation |

### Recommendation: Hybrid Approach

**Use kubebuilder for the operator portion, keep non-operator components in separate `pkg/` packages, but build into a single binary.**

The key insight: kubebuilder's opinions primarily govern the *operator* portion (CRD types, controllers, generated manifests). The REST API and GitHub adapter are standard Go HTTP servers that don't need kubebuilder scaffolding.

**Practical structure:**
```
shepherd/
├── cmd/shepherd/main.go       # Custom entrypoint (replaces kubebuilder's cmd/main.go)
├── api/v1alpha1/              # Kubebuilder-managed CRD types
├── internal/controller/       # Kubebuilder-managed controllers
├── config/                    # Kubebuilder-managed manifests
├── pkg/
│   ├── shepherd/              # Module orchestrator (custom)
│   ├── api/                   # REST API server (custom, chi-based)
│   ├── operator/              # Operator module wrapper (custom)
│   └── adapters/github/       # GitHub adapter (custom)
├── PROJECT                    # Kubebuilder project file
└── Makefile                   # Extended kubebuilder Makefile
```

**Tradeoffs accepted:**
- Kubebuilder upgrades may require manual `cmd/main.go` reconciliation
- Binary is larger than separate binaries
- All components share the dependency tree

**Tradeoffs gained:**
- Single Docker image, simpler CI/CD
- Proven pattern (Loki, Mimir, Cortex)
- Flexible deployment (different K8s Deployments with different `args`)

### The existing implementation plan already follows this pattern, which is sound.

---

## 2. CLI Framework: Kong vs Cobra

### Cobra (Kubebuilder Default)

- Used by Kubernetes, Hugo, GitHub CLI
- Kubebuilder scaffolds with Cobra; generated `cmd/main.go` uses it
- Over 35,000 GitHub stars, dominant in K8s ecosystem

### Kong

- Struct-based, declarative approach
- Smaller, easier to understand
- Developer sentiment favors it for new projects
- Not used in K8s ecosystem

### Verdict: Use Cobra

**Rationale:**
1. Kubebuilder already sets it up - mixing would add confusion
2. Controller-runtime's zap flags use `flag.CommandLine` - Cobra integrates with this; Kong would require adaptation
3. Consistent with K8s ecosystem conventions
4. The `-target` flag pattern works naturally with Cobra subcommands or flags

However, the existing implementation plan uses `flag.FlagSet` directly (not Cobra), which is even simpler and perfectly fine for the current needs. You don't actually need Cobra *or* Kong unless you want subcommands. The `flag` package is sufficient for flag-based configuration.

**Recommendation: Start with `flag` (as the current plan does). If you later need subcommands, add Cobra. Skip Kong entirely.**

---

## 3. API Framework

### Comparison

| Framework | K8s Ecosystem Usage | Zap Integration | Prometheus | Testing w/ testify |
|-----------|-------------------|-----------------|------------|-------------------|
| **chi** | Used by some operators | treastech/logger, chi-logger | chi-prometheus | Excellent (standard httptest) |
| **net/http (Go 1.22+)** | Universal | Manual | Manual | Excellent |
| **gin** | Not used in operators | gin-contrib/zap | Available | Good (gin.TestMode) |
| **echo** | Rare in operators | echo-zap-middleware | Built-in | Good |
| **Connect-Go** | Emerging | Via interceptors | Via interceptors | Different paradigm |

### Recommendation: chi

**chi** is the right choice for Shepherd:

1. **Lightweight and idiomatic** - Built on `net/http`, composable middleware
2. **Good ecosystem fit** - Used alongside controller-runtime in projects like Flux's artifact server
3. **Testing** - Works with standard `httptest.NewRecorder()` + testify assertions
4. **Middleware** - Good zap and prometheus middleware packages available
5. **The existing implementation plan already uses chi** - this is validated

**Pattern for running chi alongside controller-runtime in the same binary:**
```go
// The operator module uses controller-runtime's Manager
// The API module runs chi as a standalone http.Server
// Both are started via errgroup in shepherd.go
```

This is cleaner than trying to bolt REST endpoints onto controller-runtime's webhook server, which is designed for admission webhooks only.

---

## 4. Logging: Zap

### Current State

- **Yes, zap is still the default** in kubebuilder/controller-runtime (2025-2026)
- Controller-runtime uses `logr` interface with `zapr` backend
- Import: `sigs.k8s.io/controller-runtime/pkg/log/zap`

### How to Share Logger Across Components

```go
// In main.go, set the global logger once:
logger := zap.New(zap.UseFlagOptions(&opts))
log.SetLogger(logger)

// In any component, access via:
logger := log.Log.WithName("rest-api")
```

**Important gotcha:** Must call `SetLogger` within 30 seconds of startup or it defaults to `NullLogSink`.

### CLI Flags Provided by controller-runtime's zap package

- `--zap-devel` - Development mode (console encoder, debug level)
- `--zap-encoder` - json or console
- `--zap-log-level` - debug, info, error, or integer
- `--zap-stacktrace-level` - When to capture stack traces

### Recommendation

Use zap everywhere. Set logger once in `main.go`, create named child loggers per module. The existing plan should add zap initialization to `cmd/shepherd/main.go`.

---

## 5. Testing: testify

### Compatibility with Kubebuilder

- **testify works fine with envtest** - no inherent conflicts
- Kubebuilder scaffolds tests with Ginkgo/Gomega by default
- testify is used by some K8s ecosystem projects (Oracle MySQL Operator, others)

### Recommendation: testify for unit tests, consider Gomega's Eventually for async

**Use testify for:**
- Unit tests with mocked/faked clients
- API handler tests
- Webhook handler tests
- Helper function tests

**Be aware:**
- Kubebuilder's scaffolded `suite_test.go` uses Ginkgo - you can replace it or keep it for envtest integration tests
- For async K8s operations (reconciler integration tests), Gomega's `Eventually()` is genuinely useful; testify doesn't have a built-in equivalent
- You can use `testify/assert` + a simple polling helper for async tests, or mix testify with Gomega selectively

**Import:**
```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)
```

---

## 6. CRD Design - Detailed Analysis

### Current Design Issues

The existing plan has `status.events[]` alongside `status.conditions[]`. This is **not idiomatic** in Kubernetes.

**Conditions** express current state; **Events** are separate K8s resources (kind: Event), not embedded in status. No well-designed CRD embeds an events array in status.

### Recommended Condition Design

Follow the kpt convention + Tekton patterns:

**Primary condition: `Ready`** (or `Succeeded` for run-to-completion resources like TaskRun)

Since AgentTask is a run-to-completion resource (like Tekton TaskRun), use **`Succeeded`** as the primary condition:

```yaml
status:
  conditions:
    - type: Succeeded
      status: "Unknown"     # True, False, or Unknown
      reason: Running        # CamelCase enum
      message: "Agent is analyzing codebase"
      lastTransitionTime: "2026-01-27T10:00:00Z"
      observedGeneration: 1
```

**Reason values for Succeeded condition:**
| status | reason | meaning |
|--------|--------|---------|
| Unknown | Pending | Waiting for job to start |
| Unknown | Running | Job is executing |
| True | Succeeded | Task completed, PR created |
| False | Failed | Task failed |
| False | TimedOut | Timeout exceeded |
| False | Cancelled | User cancelled |

### Fields to Add

**To Spec:**

1. **Resource limits** for the Job:
```go
type RunnerSpec struct {
    Image     string            `json:"image,omitempty"`
    Timeout   metav1.Duration   `json:"timeout,omitempty"`
    Resources corev1.ResourceRequirements `json:"resources,omitempty"`
    ServiceAccountName string  `json:"serviceAccountName,omitempty"`
}
```

2. **SecretRef** should be a proper `corev1.SecretKeySelector`:
```go
type CallbackSpec struct {
    URL       string                      `json:"url"`
    SecretRef *corev1.SecretKeySelector   `json:"secretRef,omitempty"`
}
```

3. **Repo ref** (branch/tag):
```go
type RepoSpec struct {
    URL string `json:"url"`
    Ref string `json:"ref,omitempty"` // branch, tag, or SHA
}
```

**To Status:**

1. **ObservedGeneration** - Tracks which spec version was reconciled:
```go
ObservedGeneration int64 `json:"observedGeneration,omitempty"`
```

2. **StartTime/CompletionTime** (like Tekton):
```go
StartTime      *metav1.Time `json:"startTime,omitempty"`
CompletionTime *metav1.Time `json:"completionTime,omitempty"`
```

3. **Remove `events[]`** - Use K8s Events API instead (`r.Recorder.Event(task, "Normal", "JobCreated", "...")`)

### Spec Immutability

Use **CEL validation rules** (available since K8s 1.25):

```go
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repo is immutable"
Repo RepoSpec `json:"repo"`

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="task is immutable"
Task TaskSpec `json:"task"`
```

This avoids needing a validating webhook for immutability.

### Printer Columns

Add useful kubectl output:

```go
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=`.status.result.prUrl`,priority=1
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
```

### etcd Size Limits

- Per-object limit: 1.5 MiB
- Practical recommendation: Keep under 100 KB
- `spec.task.context` could exceed this for large issues

**Mitigation options:**
1. Store only a summary + URL in the CRD: `contextUrl: "https://github.com/org/repo/issues/123"`
2. Reference a ConfigMap: `contextRef: {configMapKeyRef: {name: ..., key: ...}}`
3. Truncate with link to full context

---

## 7. Other Important Packages

### GitHub Integration

| Package | Import | Purpose |
|---------|--------|---------|
| go-github | `github.com/google/go-github/v81/github` | GitHub API client |
| ghinstallation | `github.com/bradleyfalzon/ghinstallation/v2` | GitHub App auth |

**Note:** go-github uses calendar versioning. The existing plan pins v68; current is v81. Pin to latest at time of implementation.

### Webhook Signature Verification

Implement manually using `crypto/hmac` + `crypto/sha256` + `crypto/subtle.ConstantTimeCompare`. The existing plan already does this correctly. Use `X-Hub-Signature-256` (SHA-256), not the deprecated `X-Hub-Signature` (SHA-1).

### Configuration

| Option | Best For | Notes |
|--------|----------|-------|
| `flag` (stdlib) | Simple flag-based config | Already in the plan, sufficient for MVP |
| `envconfig` | K8s env-var config | Good for 12-factor; `github.com/kelseyhightower/envconfig` |
| `koanf` | Complex multi-source config | Better than viper (313% smaller binary, no key lowercasing) |
| `viper` | Cobra companion | Popular but bloated, force-lowercases keys |

**Recommendation:** Start with `flag` for MVP. Add `envconfig` if you need env var overrides (common in K8s). Avoid viper.

### Prometheus Metrics

Controller-runtime provides built-in metrics (reconcile counts, durations, workqueue). Register custom metrics via:

```go
import "sigs.k8s.io/controller-runtime/pkg/metrics"

var tasksCreated = prometheus.NewCounter(...)

func init() {
    metrics.Registry.MustRegister(tasksCreated)
}
```

For the REST API (chi), use `github.com/766b/chi-prometheus` middleware.

### Other Dependencies

| Package | Purpose |
|---------|---------|
| `golang.org/x/sync/errgroup` | Goroutine management (already in plan) |
| `sigs.k8s.io/controller-runtime` | K8s operator framework |
| `k8s.io/client-go` | K8s API client (transitive via controller-runtime) |
| `github.com/go-chi/chi/v5` | HTTP router |

---

## 8. Additional Design Considerations Before Implementation

### 8.1 Callback Authentication

The current design mentions HMAC signatures for API-to-adapter callbacks (`X-Shepherd-Signature`), but the internal Job-to-API callback has "no auth required" since it uses cluster-internal networking. This is acceptable for MVP but consider:
- Network policies to restrict which pods can call the API
- A simple shared secret for Job-to-API calls as defense in depth

### 8.2 Runner Image Allowlist

The design mentions "pre-approved runner images" but doesn't specify enforcement. Consider:
- A ConfigMap or CRD field listing allowed image patterns
- Webhook validation to reject tasks with unapproved images
- For MVP: hardcode a default and validate in the controller

### 8.3 Task Deduplication

What happens if someone comments `@shepherd fix this` twice on the same issue? Consider:
- Generating deterministic task names from repo + issue number
- Checking for existing active tasks before creating new ones
- Or just allowing duplicates and letting users manage

### 8.4 Graceful Shutdown of Long-Running Jobs

When a task is cancelled or times out:
- How does the controller signal the running Job to stop?
- K8s Job `activeDeadlineSeconds` handles timeout
- For cancellation: delete the Job (controller-runtime owner references handle cleanup)

### 8.5 Init Container Token Scope

The init container generates a GitHub installation token. Consider:
- Token expiry (1 hour) vs task timeout (30 minutes default)
- Token permissions: should be scoped to only the target repo
- How to pass the token securely (shared volume is in the plan)

### 8.6 Observability from Day One

Consider adding from the start:
- Structured logging with correlation IDs (task ID in all log lines)
- Prometheus metrics for key operations (tasks created, jobs started, jobs completed/failed)
- Health/readiness probes (already in the plan)

### 8.7 Testing Strategy

The current plan has unit tests with faked clients. Consider also planning for:
- **envtest integration tests** - Test controller against a real API server (local)
- **testify + envtest** - Use testify assertions in envtest tests (with a polling helper for Eventually-style assertions)
- **Table-driven tests** - The webhook tests already use this pattern; apply consistently

### 8.8 Error Handling in the Reconciler

The current controller has some gaps:
- No rate limiting on requeue
- No distinction between transient and permanent errors
- Consider using `ctrl.Result{RequeueAfter: time.Second * 30}` for transient failures

### 8.9 Namespace Strategy

The design implies a single `shepherd` namespace. Consider:
- Should AgentTasks be namespace-scoped (current) or cluster-scoped?
- Namespace-scoped is simpler and supports multi-tenancy later
- The operator needs RBAC to create Jobs in the task's namespace

### 8.10 Helm vs Kustomize

Kubebuilder generates Kustomize manifests. The plan adds Helm charts. Consider:
- Maintain both (Kustomize for operator CRDs via kubebuilder, Helm for deployment)?
- Or just Helm?
- Recommendation: Use kubebuilder's Kustomize for CRD generation only (`make manifests`), Helm for actual deployment

---

## Recommended Package List (go.mod)

```go
require (
    // Core K8s
    sigs.k8s.io/controller-runtime  // latest compatible with your K8s target
    k8s.io/apimachinery             // transitive
    k8s.io/client-go                // transitive

    // HTTP
    github.com/go-chi/chi/v5

    // GitHub
    github.com/google/go-github/v81
    github.com/bradleyfalzon/ghinstallation/v2

    // Observability
    github.com/prometheus/client_golang  // transitive via controller-runtime

    // Concurrency
    golang.org/x/sync                    // for errgroup

    // Testing
    github.com/stretchr/testify
)
```

---

## Code References

- `docs/plans/2026-01-25-shepherd-design.md` - Original architecture design
- `docs/plans/2026-01-26-shepherd-implementation.md` - Existing implementation plan

## Open Questions

1. **go-github version** - Plan uses v68, current is v81. Pin to latest at implementation time.
2. **CRD group naming** - Plan uses `shepherd.io` as the group, but kubebuilder generates `shepherd.shepherd.io`. Consider using just `shepherd.io` as the domain and a different group name, or accept the stuttering.
3. **Ginkgo vs testify for envtest** - The plan's unit tests use raw `testing.T`, which is fine. But envtest integration tests benefit from Gomega's `Eventually()`. Decide whether to add Gomega as a test dependency or write a polling helper.
4. **Runner image distribution** - ko builds the shepherd binary. The runner image (with Claude Code) needs a separate Dockerfile. This is out of scope for MVP but needs planning.
