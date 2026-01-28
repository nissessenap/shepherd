# Shepherd K8s Operator Implementation Plan

## Overview

Implement the Shepherd K8s operator — the component that watches `AgentTask` CRDs, creates and monitors K8s Jobs, and updates CRD status. The operator is purely K8s-internal (no external HTTP calls). This is the foundational component that everything else builds on.

## Current State Analysis

Clean slate. No code exists — only design docs at `docs/research/2026-01-27-shepherd-design.md`.

## Desired End State

A working K8s operator that:
- Defines the `AgentTask` CRD (`toolkit.shepherd.io/v1alpha1`)
- Reconciles `AgentTask` resources by creating K8s Jobs
- Updates CRD status conditions through the full lifecycle (Pending → Running → Succeeded/Failed)
- Handles failure modes: infrastructure failures (retry), application failures (mark failed), OOM, timeout
- Is started via `shepherd operator` Kong CLI subcommand
- Has comprehensive unit and envtest integration tests

### How to Verify

- `make generate && make manifests` produces CRD YAML
- `make test` passes all unit and envtest tests
- `shepherd operator` starts and connects to a K8s cluster
- Creating an `AgentTask` CRD results in a Job being created
- Job completion/failure updates CRD status correctly

## What We're NOT Doing

- REST API server (`shepherd api`)
- GitHub adapter (`shepherd github`)
- `shepherd all` mode (intentional deviation from design doc — deferred, not dropped. Can be added later by composing the three Run functions with errgroup.)
- Helm charts (separate plan)
- Runner/init container images
- Callback notifications to adapters (API responsibility)
- CRD status informer for terminal state notifications (API responsibility)
- ko build configuration

## Implementation Approach

Use kubebuilder CLI to scaffold, then customize. Each phase builds on the previous and is independently testable. The Kong CLI wraps kubebuilder's controller-runtime Manager via a `pkg/operator` package.

---

## Phase 1: Kubebuilder Scaffolding + CRD Types

### Overview

Scaffold the project with kubebuilder and define the complete `AgentTask` CRD types matching the design doc.

### Changes Required

#### 1. Kubebuilder Scaffolding

Run from project root:

```bash
# Install latest stable kubebuilder if not present
# https://book.kubebuilder.io/quick-start

kubebuilder init --domain shepherd.io --repo github.com/NissesSenap/shepherd

kubebuilder create api --group toolkit --version v1alpha1 --kind AgentTask --resource --controller
```

This produces:
- `PROJECT` — kubebuilder project metadata
- `go.mod` / `go.sum` — Go module with controller-runtime
- `cmd/main.go` — default entrypoint (replaced in Phase 2)
- `api/v1alpha1/agenttask_types.go` — CRD type stubs
- `api/v1alpha1/groupversion_info.go` — scheme registration
- `internal/controller/agenttask_controller.go` — reconciler stub
- `internal/controller/suite_test.go` — envtest test suite
- `config/` — Kustomize manifests (CRD, RBAC, manager, etc.)
- `Makefile` — build, test, generate targets

#### 2. CRD Types

**File**: `api/v1alpha1/agenttask_types.go`

Replace the kubebuilder-generated stubs with the full types from the design:

```go
package v1alpha1

import (
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=`.status.result.prUrl`,priority=1
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type AgentTask struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   AgentTaskSpec   `json:"spec,omitempty"`
    Status AgentTaskStatus `json:"status,omitempty"`
}

type AgentTaskSpec struct {
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repo is immutable"
    Repo     RepoSpec     `json:"repo"`
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="task is immutable"
    Task     TaskSpec     `json:"task"`
    Callback CallbackSpec `json:"callback"`
    Runner   RunnerSpec   `json:"runner,omitempty"`
}

type RepoSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Pattern=`^https://`
    URL string `json:"url"`
    Ref string `json:"ref,omitempty"`
}

type TaskSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Description     string `json:"description"`
    Context         string `json:"context,omitempty"`
    ContextEncoding string `json:"contextEncoding,omitempty"`
    ContextURL      string `json:"contextUrl,omitempty"`
}

type CallbackSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Pattern=`^https?://`
    URL       string                    `json:"url"`
    SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`
}

type RunnerSpec struct {
    // +kubebuilder:default="shepherd-runner:latest"
    Image              string                      `json:"image,omitempty"`
    Timeout            metav1.Duration             `json:"timeout,omitempty"`
    ServiceAccountName string                      `json:"serviceAccountName,omitempty"`
    Resources          corev1.ResourceRequirements `json:"resources,omitempty"`
}
// Note: metav1.Duration cannot use kubebuilder default markers (struct type).
// Default timeout (30m) is applied in the reconciler/buildJob code instead.

type AgentTaskStatus struct {
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
    StartTime          *metav1.Time       `json:"startTime,omitempty"`
    CompletionTime     *metav1.Time       `json:"completionTime,omitempty"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    JobName            string             `json:"jobName,omitempty"`
    Result             TaskResult         `json:"result,omitempty"`
}

type TaskResult struct {
    PRUrl string `json:"prUrl,omitempty"`
    Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
type AgentTaskList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []AgentTask `json:"items"`
}

func init() {
    SchemeBuilder.Register(&AgentTask{}, &AgentTaskList{})
}
```

#### 3. Condition Constants

**File**: `api/v1alpha1/conditions.go` (new file)

```go
package v1alpha1

const (
    // ConditionSucceeded is the primary condition type (run-to-completion pattern).
    ConditionSucceeded = "Succeeded"

    // Reasons for ConditionSucceeded
    ReasonPending   = "Pending"
    ReasonRunning   = "Running"
    ReasonSucceeded = "Succeeded"
    ReasonFailed    = "Failed"
    ReasonTimedOut  = "TimedOut"
    ReasonCancelled = "Cancelled"
)
```

#### 4. Generate and Verify

```bash
make generate   # deepcopy functions
make manifests  # CRD YAML + RBAC
```

### Success Criteria

#### Automated Verification:
- [x] `make generate` succeeds (deepcopy generated)
- [x] `make manifests` succeeds (CRD YAML generated in `config/crd/bases/`)
- [x] `go build ./...` compiles
- [x] `go vet ./...` passes
- [x] CRD YAML contains all spec/status fields, print columns, and validation rules

#### Manual Verification:
- [ ] Review generated CRD YAML matches design doc schema
- [ ] CEL validation rules present for immutable fields

**Pause for manual review before Phase 2.**

---

## Phase 2: Kong CLI + Operator Wrapper

### Overview

Replace kubebuilder's default `cmd/main.go` with a Kong CLI entrypoint. Create `pkg/operator` wrapper. Stub all three subcommands, implement `operator`.

### Changes Required

#### 1. Add Kong Dependency

```bash
go get github.com/alecthomas/kong
```

#### 2. Kong CLI Entrypoint

**File**: `cmd/shepherd/main.go` (new file, replaces `cmd/main.go`)

```go
package main

import (
    "fmt"
    "os"

    "github.com/alecthomas/kong"
    "sigs.k8s.io/controller-runtime/pkg/log"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"
    zapraw "go.uber.org/zap/zapcore"
)

type CLI struct {
    API      APICmd      `cmd:"" help:"Run API server"`
    Operator OperatorCmd `cmd:"" help:"Run K8s operator"`
    GitHub   GitHubCmd   `cmd:"" help:"Run GitHub adapter"`

    LogLevel int  `help:"Log level (0=info, 1=debug)" default:"0"`
    DevMode  bool `help:"Enable development mode logging" default:"false"`
}

type APICmd struct {
    ListenAddr string `help:"API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
}

func (c *APICmd) Run(globals *CLI) error {
    return fmt.Errorf("api server not implemented yet")
}

type GitHubCmd struct {
    ListenAddr    string `help:"GitHub adapter listen address" default:":8082" env:"SHEPHERD_GITHUB_ADDR"`
    WebhookSecret string `help:"GitHub webhook secret" env:"SHEPHERD_GITHUB_WEBHOOK_SECRET"`
    AppID         int64  `help:"GitHub App ID" env:"SHEPHERD_GITHUB_APP_ID"`
    PrivateKey    string `help:"Path to GitHub App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY"`
}

func (c *GitHubCmd) Run(globals *CLI) error {
    return fmt.Errorf("github adapter not implemented yet")
}

func main() {
    cli := CLI{}
    ctx := kong.Parse(&cli,
        kong.Name("shepherd"),
        kong.Description("Background coding agent orchestrator"),
    )

    // Configure logging
    logger := zap.New(
        zap.UseDevMode(cli.DevMode),
        zap.Level(zapraw.Level(-cli.LogLevel)),
    )
    log.SetLogger(logger)

    err := ctx.Run(&cli)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
}
```

#### 3. Operator Command

**File**: `cmd/shepherd/operator.go` (new file)

```go
package main

import (
    "github.com/NissesSenap/shepherd/pkg/operator"
)

type OperatorCmd struct {
    MetricsAddr        string `help:"Metrics address" default:":9090" env:"SHEPHERD_METRICS_ADDR"`
    HealthAddr         string `help:"Health probe address" default:":8081" env:"SHEPHERD_HEALTH_ADDR"`
    LeaderElection     bool   `help:"Enable leader election" default:"false" env:"SHEPHERD_LEADER_ELECTION"`
    AllowedRunnerImage string `help:"Allowed runner image (full registry path including tag)" required:"" env:"SHEPHERD_RUNNER_IMAGE"`
    RunnerSecretName   string `help:"GitHub App private key secret name" default:"shepherd-runner-app-key" env:"SHEPHERD_RUNNER_SECRET"`
}

func (c *OperatorCmd) Run(globals *CLI) error {
    return operator.Run(operator.Options{
        MetricsAddr:        c.MetricsAddr,
        HealthAddr:         c.HealthAddr,
        LeaderElection:     c.LeaderElection,
        AllowedRunnerImage: c.AllowedRunnerImage,
        RunnerSecretName:   c.RunnerSecretName,
    })
}
```

#### 4. Operator Package

**File**: `pkg/operator/operator.go` (new file)

```go
package operator

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "k8s.io/apimachinery/pkg/runtime"
    utilruntime "k8s.io/apimachinery/pkg/util/runtime"
    clientgoscheme "k8s.io/client-go/kubernetes/scheme"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/healthz"
    "sigs.k8s.io/controller-runtime/pkg/metrics/server"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
    "github.com/NissesSenap/shepherd/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
    utilruntime.Must(clientgoscheme.AddToScheme(scheme))
    utilruntime.Must(toolkitv1alpha1.AddToScheme(scheme))
}

type Options struct {
    MetricsAddr        string
    HealthAddr         string
    LeaderElection     bool
    AllowedRunnerImage string
    RunnerSecretName   string
}

func Run(opts Options) error {
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    log := ctrl.Log.WithName("operator")

    mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
        Scheme: scheme,
        Metrics: server.Options{
            BindAddress: opts.MetricsAddr,
        },
        HealthProbeBindAddress: opts.HealthAddr,
        LeaderElection:         opts.LeaderElection,
        LeaderElectionID:       "shepherd-operator",
    })
    if err != nil {
        return fmt.Errorf("creating manager: %w", err)
    }

    if err := (&controller.AgentTaskReconciler{
        Client:             mgr.GetClient(),
        Scheme:             mgr.GetScheme(),
        Recorder:           mgr.GetEventRecorder("shepherd-operator"), // new API (not deprecated GetEventRecorderFor)
        AllowedRunnerImage: opts.AllowedRunnerImage,
        RunnerSecretName:   opts.RunnerSecretName,
    }).SetupWithManager(mgr); err != nil {
        return fmt.Errorf("setting up controller: %w", err)
    }

    if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
        return fmt.Errorf("setting up healthz: %w", err)
    }
    if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
        return fmt.Errorf("setting up readyz: %w", err)
    }

    log.Info("starting operator")
    if err := mgr.Start(ctx); err != nil {
        return fmt.Errorf("running manager: %w", err)
    }
    return nil
}
```

#### 5. Update Controller Struct

**File**: `internal/controller/agenttask_controller.go`

Update the kubebuilder-generated reconciler to include the event recorder and config fields:

```go
type AgentTaskReconciler struct {
    client.Client
    Scheme             *runtime.Scheme
    Recorder           events.EventRecorder // k8s.io/client-go/tools/events (new API, via mgr.GetEventRecorder())
    AllowedRunnerImage string
    RunnerSecretName   string
}
```

#### 6. Delete Old Entrypoint

Delete `cmd/main.go` (the kubebuilder-generated one).

#### 7. Update Makefile

Update the `Makefile` to point to the new entrypoint. Change the build target from `cmd/main.go` to `cmd/shepherd/`:

```makefile
# In the existing Makefile, update:
# - Binary name and path references from cmd/ to cmd/shepherd/
```

### Success Criteria

#### Automated Verification:
- [x] `go build ./cmd/shepherd/` compiles
- [x] `./shepherd --help` shows api, operator, github subcommands
- [x] `./shepherd api` returns "not implemented yet" error
- [x] `./shepherd github` returns "not implemented yet" error
- [x] `make test` still passes (envtest suite)

#### Manual Verification:
- [ ] `./shepherd operator` starts and attempts to connect to a K8s cluster (will fail without kubeconfig, that's fine — verify it prints the right startup log)

**Pause for manual review before Phase 3.**

---

## Phase 3: Reconciler Skeleton

### Overview

Implement the reconcile loop that watches `AgentTask` resources and manages status conditions. No Job creation yet — this phase establishes the condition state machine and status update patterns.

### Changes Required

#### 1. Reconciler Implementation

**File**: `internal/controller/agenttask_controller.go`

```go
package controller

import (
    "context"
    "fmt"

    batchv1 "k8s.io/api/batch/v1"
    "k8s.io/apimachinery/pkg/api/meta"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/client-go/tools/events"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

type AgentTaskReconciler struct {
    client.Client
    Scheme             *runtime.Scheme
    Recorder           events.EventRecorder // new API via mgr.GetEventRecorder()
    AllowedRunnerImage string
    RunnerSecretName   string
}

// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // Fetch the AgentTask
    var task toolkitv1alpha1.AgentTask
    if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Skip if already in terminal state
    if isTerminal(&task) {
        return ctrl.Result{}, nil
    }

    // Initialize condition if not set
    if !hasCondition(&task, toolkitv1alpha1.ConditionSucceeded) {
        setCondition(&task, metav1.Condition{
            Type:               toolkitv1alpha1.ConditionSucceeded,
            Status:             metav1.ConditionUnknown,
            Reason:             toolkitv1alpha1.ReasonPending,
            Message:            "Waiting for job to start",
            ObservedGeneration: task.Generation,
        })
        task.Status.ObservedGeneration = task.Generation

        if err := r.Status().Update(ctx, &task); err != nil {
            return ctrl.Result{}, fmt.Errorf("updating initial status: %w", err)
        }
        r.Recorder.Eventf(&task, nil, "Normal", "Pending", "Reconcile", "Task accepted, waiting for job creation")
        log.Info("initialized task status", "task", req.NamespacedName)
        // Note: ctrl.Result{Requeue: true} is deprecated in controller-runtime v0.23+.
        // Use RequeueAfter with a minimal duration instead.
        return ctrl.Result{RequeueAfter: time.Second}, nil
    }

    // TODO Phase 4: Create/monitor Job here
    log.Info("reconcile complete (job creation not yet implemented)", "task", req.NamespacedName)

    return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&toolkitv1alpha1.AgentTask{}).
        Owns(&batchv1.Job{}).
        Complete(r)
}

// isTerminal returns true if the task has reached a terminal condition.
func isTerminal(task *toolkitv1alpha1.AgentTask) bool {
    cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
    if cond == nil {
        return false
    }
    return cond.Status != metav1.ConditionUnknown
}

// hasCondition returns true if the named condition exists.
func hasCondition(task *toolkitv1alpha1.AgentTask, condType string) bool {
    return meta.FindStatusCondition(task.Status.Conditions, condType) != nil
}

// setCondition sets or updates a condition on the task.
func setCondition(task *toolkitv1alpha1.AgentTask, condition metav1.Condition) {
    meta.SetStatusCondition(&task.Status.Conditions, condition)
}
```

#### 2. Unit Tests

**File**: `internal/controller/agenttask_controller_test.go` (new file)

```go
package controller

import (
    "testing"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "github.com/stretchr/testify/assert"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestIsTerminal(t *testing.T) {
    tests := []struct {
        name     string
        task     *toolkitv1alpha1.AgentTask
        expected bool
    }{
        {
            name:     "no conditions",
            task:     &toolkitv1alpha1.AgentTask{},
            expected: false,
        },
        {
            name: "pending (unknown)",
            task: taskWithCondition(metav1.ConditionUnknown, toolkitv1alpha1.ReasonPending),
            expected: false,
        },
        {
            name: "running (unknown)",
            task: taskWithCondition(metav1.ConditionUnknown, toolkitv1alpha1.ReasonRunning),
            expected: false,
        },
        {
            name: "succeeded (true)",
            task: taskWithCondition(metav1.ConditionTrue, toolkitv1alpha1.ReasonSucceeded),
            expected: true,
        },
        {
            name: "failed (false)",
            task: taskWithCondition(metav1.ConditionFalse, toolkitv1alpha1.ReasonFailed),
            expected: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            assert.Equal(t, tt.expected, isTerminal(tt.task))
        })
    }
}

func taskWithCondition(status metav1.ConditionStatus, reason string) *toolkitv1alpha1.AgentTask {
    task := &toolkitv1alpha1.AgentTask{}
    setCondition(task, metav1.Condition{
        Type:   toolkitv1alpha1.ConditionSucceeded,
        Status: status,
        Reason: reason,
    })
    return task
}
```

#### 3. Integration Tests (envtest)

**File**: `internal/controller/integration_test.go` (new file)

Use the kubebuilder-generated `suite_test.go` setup. Add tests:

```go
package controller

import (
    . "github.com/onsi/gomega"
    // ... envtest imports

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// Test: Creating an AgentTask sets Pending condition
// Test: AgentTask with Succeeded=True is not reconciled again
// Test: AgentTask with Succeeded=False is not reconciled again
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes (all unit + envtest tests)
- [x] `go vet ./...` clean
- [x] Unit tests cover: isTerminal, hasCondition, setCondition helpers
- [x] envtest: creating AgentTask results in Pending condition being set

#### Manual Verification:
- [ ] Review reconciler logic matches the condition state machine from design doc

**Pause for manual review before Phase 4.**

---

## Phase 4: Job Creation

### Overview

The reconciler creates K8s Jobs from `AgentTask` resources. Jobs include an init container (GitHub auth) and main container (runner). The reconciler updates CRD status based on Job state.

### Changes Required

#### 1. Job Builder

**File**: `internal/controller/job_builder.go` (new file)

Build the Job spec from an AgentTask:

```go
package controller

import (
    "fmt"
    "time"

    batchv1 "k8s.io/api/batch/v1"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

    toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// jobConfig holds operator-level configuration needed to build Jobs.
type jobConfig struct {
    AllowedRunnerImage string
    RunnerSecretName   string
    Scheme             *runtime.Scheme
}

const defaultTimeout = 30 * time.Minute

func buildJob(task *toolkitv1alpha1.AgentTask, cfg jobConfig) (*batchv1.Job, error) {
    // Validate runner image against operator-configured allowlist.
    // For MVP, the operator admin sets SHEPHERD_RUNNER_IMAGE and only that
    // image is permitted. The spec.runner.image field is ignored — the admin
    // controls what runs in the cluster.
    if cfg.AllowedRunnerImage == "" {
        return nil, fmt.Errorf("no allowed runner image configured (set SHEPHERD_RUNNER_IMAGE)")
    }

    // Job name includes generation to avoid collision on delete/recreate
    jobName := fmt.Sprintf("%s-%d-job", task.Name, task.Generation)

    // backoffLimit: 0 — no K8s-level retries, operator handles retries
    backoffLimit := int32(0)

    // activeDeadlineSeconds from spec.runner.timeout, defaulting to 30m
    timeout := task.Spec.Runner.Timeout.Duration
    if timeout == 0 {
        timeout = defaultTimeout
    }
    activeDeadlineSecs := int64(timeout.Seconds())

    // Build init container env — include ref if specified
    initEnv := []corev1.EnvVar{
        {Name: "REPO_URL", Value: task.Spec.Repo.URL},
    }
    if task.Spec.Repo.Ref != "" {
        initEnv = append(initEnv, corev1.EnvVar{Name: "REPO_REF", Value: task.Spec.Repo.Ref})
    }

    // Build main container env — include ref if specified
    runnerEnv := []corev1.EnvVar{
        {Name: "SHEPHERD_TASK_ID", Value: task.Name},
        {Name: "SHEPHERD_REPO_URL", Value: task.Spec.Repo.URL},
        {Name: "SHEPHERD_TASK_DESCRIPTION", Value: task.Spec.Task.Description},
        {Name: "SHEPHERD_CALLBACK_URL", Value: task.Spec.Callback.URL},
    }
    if task.Spec.Repo.Ref != "" {
        runnerEnv = append(runnerEnv, corev1.EnvVar{Name: "SHEPHERD_REPO_REF", Value: task.Spec.Repo.Ref})
    }

    job := &batchv1.Job{
        ObjectMeta: metav1.ObjectMeta{
            Name:      jobName,
            Namespace: task.Namespace,
            Labels: map[string]string{
                "shepherd.io/task": task.Name,
            },
        },
        Spec: batchv1.JobSpec{
            BackoffLimit:          &backoffLimit,
            ActiveDeadlineSeconds: &activeDeadlineSecs,
            Template: corev1.PodTemplateSpec{
                ObjectMeta: metav1.ObjectMeta{
                    Labels: map[string]string{
                        "shepherd.io/task": task.Name,
                    },
                },
                Spec: corev1.PodSpec{
                    ServiceAccountName: task.Spec.Runner.ServiceAccountName,
                    RestartPolicy:      corev1.RestartPolicyNever,
                    InitContainers: []corev1.Container{
                        {
                            Name:         "github-auth",
                            Image:        "shepherd-init:latest",
                            Env:          initEnv,
                            VolumeMounts: []corev1.VolumeMount{
                                {Name: "github-creds", MountPath: "/creds"},
                                {Name: "runner-app-key", MountPath: "/secrets/runner-app-key", ReadOnly: true},
                            },
                        },
                    },
                    Containers: []corev1.Container{
                        {
                            Name:      "runner",
                            Image:     cfg.AllowedRunnerImage,
                            Env:       runnerEnv,
                            Resources: task.Spec.Runner.Resources,
                            VolumeMounts: []corev1.VolumeMount{
                                {Name: "github-creds", MountPath: "/creds", ReadOnly: true},
                            },
                        },
                    },
                    Volumes: []corev1.Volume{
                        {
                            Name: "github-creds",
                            VolumeSource: corev1.VolumeSource{
                                EmptyDir: &corev1.EmptyDirVolumeSource{},
                            },
                        },
                        {
                            Name: "runner-app-key",
                            VolumeSource: corev1.VolumeSource{
                                Secret: &corev1.SecretVolumeSource{
                                    SecretName: cfg.RunnerSecretName,
                                },
                            },
                        },
                    },
                },
            },
        },
    }

    // Set owner reference so Job is garbage-collected with the AgentTask
    if err := controllerutil.SetControllerReference(task, job, cfg.Scheme); err != nil {
        return nil, err
    }

    return job, nil
}
```

#### 2. Reconciler Updates

**File**: `internal/controller/agenttask_controller.go`

Replace the Phase 3 TODO with Job creation and monitoring:

```go
func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    var task toolkitv1alpha1.AgentTask
    if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if isTerminal(&task) {
        return ctrl.Result{}, nil
    }

    // Initialize condition
    if !hasCondition(&task, toolkitv1alpha1.ConditionSucceeded) {
        // ... same as Phase 3
    }

    // Look for existing Job (name includes generation to avoid collision on delete/recreate)
    var job batchv1.Job
    jobName := fmt.Sprintf("%s-%d-job", task.Name, task.Generation)
    jobKey := client.ObjectKey{Namespace: task.Namespace, Name: jobName}

    err := r.Get(ctx, jobKey, &job)
    if client.IgnoreNotFound(err) != nil {
        return ctrl.Result{}, fmt.Errorf("getting job: %w", err)
    }

    if err != nil {
        // Job doesn't exist — create it
        newJob, buildErr := buildJob(&task, jobConfig{
            AllowedRunnerImage: r.AllowedRunnerImage,
            RunnerSecretName:   r.RunnerSecretName,
            Scheme:             r.Scheme,
        })
        if buildErr != nil {
            return ctrl.Result{}, fmt.Errorf("building job: %w", buildErr)
        }
        if createErr := r.Create(ctx, newJob); createErr != nil {
            return ctrl.Result{}, fmt.Errorf("creating job: %w", createErr)
        }

        task.Status.JobName = newJob.Name
        setCondition(&task, metav1.Condition{
            Type:               toolkitv1alpha1.ConditionSucceeded,
            Status:             metav1.ConditionUnknown,
            Reason:             toolkitv1alpha1.ReasonRunning,
            Message:            "Job created",
            ObservedGeneration: task.Generation,
        })
        now := metav1.Now()
        task.Status.StartTime = &now

        if statusErr := r.Status().Update(ctx, &task); statusErr != nil {
            return ctrl.Result{}, fmt.Errorf("updating status after job creation: %w", statusErr)
        }
        r.Recorder.Eventf(&task, nil, "Normal", "JobCreated", "Reconcile", "Created job %s", newJob.Name)
        log.Info("created job", "job", newJob.Name)
        return ctrl.Result{}, nil
    }

    // Job exists — check its status
    return r.reconcileJobStatus(ctx, &task, &job)
}

func (r *AgentTaskReconciler) reconcileJobStatus(ctx context.Context, task *toolkitv1alpha1.AgentTask, job *batchv1.Job) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // Check for Job completion conditions
    for _, c := range job.Status.Conditions {
        switch c.Type {
        case batchv1.JobComplete:
            if c.Status == corev1.ConditionTrue {
                return r.markSucceeded(ctx, task, "Job completed successfully")
            }
        case batchv1.JobFailed:
            if c.Status == corev1.ConditionTrue {
                // Phase 5 will add infrastructure vs application failure distinction
                return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed, c.Message)
            }
        }
    }

    log.V(1).Info("job still running", "job", job.Name)
    return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) markSucceeded(ctx context.Context, task *toolkitv1alpha1.AgentTask, message string) (ctrl.Result, error) {
    now := metav1.Now()
    task.Status.CompletionTime = &now
    setCondition(task, metav1.Condition{
        Type:               toolkitv1alpha1.ConditionSucceeded,
        Status:             metav1.ConditionTrue,
        Reason:             toolkitv1alpha1.ReasonSucceeded,
        Message:            message,
        ObservedGeneration: task.Generation,
    })
    if err := r.Status().Update(ctx, task); err != nil {
        return ctrl.Result{}, fmt.Errorf("marking succeeded: %w", err)
    }
    r.Recorder.Eventf(task, nil, "Normal", "Succeeded", "Reconcile", message)
    return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) markFailed(ctx context.Context, task *toolkitv1alpha1.AgentTask, reason, message string) (ctrl.Result, error) {
    now := metav1.Now()
    task.Status.CompletionTime = &now
    task.Status.Result.Error = message
    setCondition(task, metav1.Condition{
        Type:               toolkitv1alpha1.ConditionSucceeded,
        Status:             metav1.ConditionFalse,
        Reason:             reason,
        Message:            message,
        ObservedGeneration: task.Generation,
    })
    if err := r.Status().Update(ctx, task); err != nil {
        return ctrl.Result{}, fmt.Errorf("marking failed: %w", err)
    }
    r.Recorder.Eventf(task, nil, "Warning", reason, "Reconcile", message)
    return ctrl.Result{}, nil
}
```

#### 3. Job Builder Tests

**File**: `internal/controller/job_builder_test.go` (new file)

```go
// Test: Job name matches task name + generation + "-job"
// Test: backoffLimit is 0
// Test: activeDeadlineSeconds derived from timeout
// Test: activeDeadlineSeconds defaults to 30m when timeout is zero
// Test: init container has REPO_URL and REPO_REF env vars
// Test: main container has SHEPHERD_* env vars including SHEPHERD_REPO_REF
// Test: REPO_REF / SHEPHERD_REPO_REF omitted when spec.repo.ref is empty
// Test: runner image uses AllowedRunnerImage, NOT spec.runner.image
// Test: buildJob returns error when AllowedRunnerImage is empty
// Test: owner reference set correctly
// Test: resources from spec applied to container
// Test: volumes and mounts configured correctly
// Test: runner-app-key secret name comes from config (RunnerSecretName)
```

#### 4. Integration Tests

Extend envtest tests:

```go
// Test: Creating AgentTask creates a Job
// Test: Job completion → task Succeeded=True
// Test: Job failure → task Succeeded=False
// Test: Job name stored in status.jobName (includes generation)
// Test: startTime set when Job created
// Test: completionTime set when Job finishes
// Test: Job uses AllowedRunnerImage, not spec.runner.image
// Test: Job env vars include REPO_REF when spec.repo.ref is set
```

### Success Criteria

#### Automated Verification:
- [x] `make test` passes all tests
- [x] `go vet ./...` clean
- [x] Job builder unit tests cover all spec fields
- [x] envtest: AgentTask → Job → Succeeded lifecycle works
- [x] envtest: AgentTask → Job → Failed lifecycle works

#### Manual Verification:
- [ ] Review Job spec matches design doc (init container, main container, volumes)
- [ ] Owner references ensure garbage collection

**Pause for manual review before Phase 4.5.**

---

## Phase 4.5: Init Container Context Decompression

### Overview

Extend the init container's responsibilities and the Job spec to handle gzip-decompressed context. The design doc specifies that the API gzip-compresses the context field before creating the CRD. Currently `SHEPHERD_TASK_DESCRIPTION` is passed as a plain env var to the runner, which won't scale to large prompts or compressed context. The init container already runs before the main container and has write access to a shared volume — extend it to also write task input files.

### Rationale

- **Design doc compliance**: `docs/research/2026-01-27-shepherd-design.md` line 335 specifies gzip compression of the context field
- **Env var size limits**: Large prompts + context can exceed practical env var sizes (~1-2MB due to pod spec in etcd)
- **File-based input is standard**: More robust than env vars for multi-line text with special characters
- **Init container already exists**: Zero additional pod overhead — just extend its responsibilities

### Changes Required

#### 1. Update Job Builder

**File**: `internal/controller/job_builder.go`

Changes:
- Remove `SHEPHERD_TASK_DESCRIPTION` from runner container env vars
- Add `TASK_DESCRIPTION`, `TASK_CONTEXT`, and `CONTEXT_ENCODING` to init container env vars
- Add `SHEPHERD_TASK_FILE=/task/description.txt` and `SHEPHERD_CONTEXT_FILE=/task/context.txt` to runner container env vars
- Add `task-files` emptyDir volume, mounted read-write in init container and read-only in runner

Init container env changes:
```go
initEnv := []corev1.EnvVar{
    {Name: "REPO_URL", Value: task.Spec.Repo.URL},
    {Name: "TASK_DESCRIPTION", Value: task.Spec.Task.Description},
}
if task.Spec.Repo.Ref != "" {
    initEnv = append(initEnv, corev1.EnvVar{Name: "REPO_REF", Value: task.Spec.Repo.Ref})
}
if task.Spec.Task.Context != "" {
    initEnv = append(initEnv, corev1.EnvVar{
        Name: "TASK_CONTEXT", Value: task.Spec.Task.Context,
    })
}
if task.Spec.Task.ContextEncoding != "" {
    initEnv = append(initEnv, corev1.EnvVar{
        Name: "CONTEXT_ENCODING", Value: task.Spec.Task.ContextEncoding,
    })
}
```

Runner container env changes:
```go
// Remove SHEPHERD_TASK_DESCRIPTION, add file paths instead:
{Name: "SHEPHERD_TASK_FILE", Value: "/task/description.txt"},
{Name: "SHEPHERD_CONTEXT_FILE", Value: "/task/context.txt"},
```

Volume changes:
```yaml
volumes:
  - name: task-files
    emptyDir: {}

initContainers:
  - name: github-auth
    volumeMounts:
      - name: task-files
        mountPath: /task

containers:
  - name: runner
    volumeMounts:
      - name: task-files
        mountPath: /task
        readOnly: true
```

#### 2. Update Job Builder Tests

**File**: `internal/controller/job_builder_test.go`

- Verify init container env includes `TASK_DESCRIPTION` and `TASK_CONTEXT` (when set)
- Verify runner env includes `SHEPHERD_TASK_FILE` and `SHEPHERD_CONTEXT_FILE`
- Verify runner env does NOT include `SHEPHERD_TASK_DESCRIPTION`
- Verify `task-files` volume exists and is mounted correctly (init: read-write, runner: read-only)
- Verify `TASK_CONTEXT` and `CONTEXT_ENCODING` are omitted when spec fields are empty

#### 3. Update Integration Tests

**File**: `internal/controller/agenttask_controller_test.go`

- Verify created Job has the `task-files` volume
- Verify runner container mounts `/task` read-only

### Out of Scope

- Init container binary implementation (separate image build plan)
- Runner image entrypoint updates (separate image build plan)
- This phase only updates the Job spec generation in the operator

### Phase Ordering

This phase must complete before implementing the API, since the API creates CRDs with gzip context and the Job spec must match the expected contract.

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests
- [ ] `go vet ./...` clean
- [ ] Job builder unit tests verify init container receives `TASK_DESCRIPTION`, `TASK_CONTEXT`, `CONTEXT_ENCODING`
- [ ] Job builder unit tests verify runner receives `SHEPHERD_TASK_FILE` and `SHEPHERD_CONTEXT_FILE`
- [ ] Job builder unit tests verify runner does NOT receive `SHEPHERD_TASK_DESCRIPTION`
- [ ] Job builder unit tests verify `task-files` volume mounts

#### Manual Verification:
- [ ] Review Job spec matches updated contract (init writes files, runner reads files)

**Pause for manual review before Phase 5.**

---

## Phase 5: Failure Handling + Retry Logic

### Overview

Implement the full failure handling strategy: distinguish infrastructure failures (pod missing/evicted → retry with new Job) from application failures (non-zero exit → mark failed). Handle timeout and OOM.

### Changes Required

#### 1. Failure Classification

**File**: `internal/controller/failure.go` (new file)

```go
package controller

import (
    batchv1 "k8s.io/api/batch/v1"
    corev1 "k8s.io/api/core/v1"
)

type failureType int

const (
    failureNone           failureType = iota
    failureInfrastructure             // pod missing, evicted — retryable
    failureApplication                // non-zero exit — permanent
    failureOOM                        // OOMKilled — permanent
    failureTimeout                    // activeDeadlineSeconds exceeded — permanent
)

// classifyJobFailure examines a failed Job and its Pods to determine
// whether the failure is retryable (infrastructure) or permanent (application).
func classifyJobFailure(job *batchv1.Job, pods []corev1.Pod) failureType {
    // Check for timeout (DeadlineExceeded)
    for _, c := range job.Status.Conditions {
        if c.Type == batchv1.JobFailed && c.Reason == "DeadlineExceeded" {
            return failureTimeout
        }
    }

    // No pods found — infrastructure failure (node loss, eviction)
    if len(pods) == 0 {
        return failureInfrastructure
    }

    // Check pod status
    for _, pod := range pods {
        // Evicted pods
        if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == "Evicted" {
            return failureInfrastructure
        }

        // Check container statuses for OOM
        for _, cs := range pod.Status.ContainerStatuses {
            if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
                return failureOOM
            }
        }
        for _, cs := range pod.Status.InitContainerStatuses {
            if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
                return failureOOM
            }
        }
    }

    // Pod exists with failure — application error
    return failureApplication
}
```

#### 2. Retry Logic in Reconciler

Update `reconcileJobStatus` to use failure classification:

```go
const maxInfraRetries = 3

func (r *AgentTaskReconciler) reconcileJobStatus(ctx context.Context, task *toolkitv1alpha1.AgentTask, job *batchv1.Job) (ctrl.Result, error) {
    // ... check for completion (same as Phase 4)

    // On failure, classify and act
    for _, c := range job.Status.Conditions {
        if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
            // List pods owned by this Job
            pods, err := r.listJobPods(ctx, job)
            if err != nil {
                return ctrl.Result{}, fmt.Errorf("listing job pods: %w", err)
            }

            ft := classifyJobFailure(job, pods)
            switch ft {
            case failureInfrastructure:
                return r.retryJob(ctx, task, job, "Infrastructure failure: pod missing or evicted")
            case failureOOM:
                return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed, "Container killed: OOMKilled")
            case failureTimeout:
                return r.markFailed(ctx, task, toolkitv1alpha1.ReasonTimedOut, "Job exceeded timeout")
            default:
                return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed, c.Message)
            }
        }
    }

    return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) retryJob(ctx context.Context, task *toolkitv1alpha1.AgentTask, oldJob *batchv1.Job, reason string) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // Count retries from annotations
    retries := getRetryCount(task)
    if retries >= maxInfraRetries {
        return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed,
            fmt.Sprintf("Infrastructure failure after %d retries: %s", retries, reason))
    }

    // Delete old Job
    if err := r.Delete(ctx, oldJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
        return ctrl.Result{}, fmt.Errorf("deleting failed job: %w", err)
    }

    // Increment retry count
    setRetryCount(task, retries+1)
    if err := r.Update(ctx, task); err != nil {
        return ctrl.Result{}, fmt.Errorf("updating retry count: %w", err)
    }

    r.Recorder.Eventf(task, nil, "Warning", "RetryingJob", "Reconcile",
        "Retrying after infrastructure failure (attempt %d/%d): %s", retries+1, maxInfraRetries, reason)
    log.Info("retrying job after infrastructure failure", "attempt", retries+1, "reason", reason)

    // Requeue — next reconcile will create a new Job
    // Note: ctrl.Result{Requeue: true} is deprecated in controller-runtime v0.23+.
    // Use RequeueAfter with a minimal duration instead.
    return ctrl.Result{RequeueAfter: time.Second}, nil
}

// Retry count stored in annotation to survive reconciler restarts.
const retryAnnotation = "shepherd.io/retry-count"

func getRetryCount(task *toolkitv1alpha1.AgentTask) int {
    // Parse from annotation
}

func setRetryCount(task *toolkitv1alpha1.AgentTask, count int) {
    // Set annotation
}
```

#### 3. Pod Listing Helper

```go
func (r *AgentTaskReconciler) listJobPods(ctx context.Context, job *batchv1.Job) ([]corev1.Pod, error) {
    var podList corev1.PodList
    if err := r.List(ctx, &podList,
        client.InNamespace(job.Namespace),
        client.MatchingLabels(job.Spec.Selector.MatchLabels),
    ); err != nil {
        return nil, err
    }
    return podList.Items, nil
}
```

#### 4. RBAC Update

Add pod list permission to the controller:

```go
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
```

Then `make manifests` to regenerate RBAC.

#### 5. Failure Classification Tests

**File**: `internal/controller/failure_test.go` (new file)

```go
// Test: No pods → failureInfrastructure
// Test: Evicted pod → failureInfrastructure
// Test: OOMKilled container → failureOOM
// Test: OOMKilled init container → failureOOM
// Test: DeadlineExceeded → failureTimeout
// Test: Normal failure (exit code 1) → failureApplication
```

#### 6. Retry Integration Tests

```go
// Test: Infrastructure failure → Job deleted, new Job created on requeue
// Test: Infrastructure failure after max retries → marked Failed
// Test: Application failure → marked Failed immediately (no retry)
// Test: OOM → marked Failed immediately
// Test: Timeout → marked TimedOut
// Test: Retry count persisted in annotation
```

### Success Criteria

#### Automated Verification:
- [ ] `make test` passes all tests
- [ ] `make manifests` generates updated RBAC with pod list permission
- [ ] Unit tests cover all failure classification cases
- [ ] envtest: infrastructure failure triggers retry
- [ ] envtest: max retries exceeded marks task failed
- [ ] envtest: application failure marks task failed immediately
- [ ] envtest: timeout marks task timed out

#### Manual Verification:
- [ ] Review retry logic matches design doc (infra → retry, app → fail, OOM → fail, timeout → fail)
- [ ] Verify retry count annotation survives reconciler restarts
- [ ] Confirm `backoffLimit: 0` means K8s doesn't retry on its own

---

## Phase 6: Ko Build + Build Smoke Test

### Overview

Set up ko builds and a build smoke test target. No e2e/chainsaw tests — envtest integration tests provide sufficient coverage for the operator's K8s interactions (real API server, CRD validation, RBAC, status subresource). E2e tests in kind would require mocking external dependencies (GitHub auth, runner containers) without testing meaningful additional operator logic.

### Rationale for Dropping E2E

- **envtest already uses a real API server** (etcd + kube-apiserver) — not mocks
- **Jobs can't run meaningfully in kind** without a real GitHub App, runner image, and callback endpoint — you'd be testing mock containers, not the operator
- **The operator's logic is condition-based** — it reacts to Job status conditions, which envtest can simulate directly by setting Job status
- **RBAC correctness** is verified by `make manifests` generating the ClusterRole from kubebuilder markers; runtime RBAC issues surface immediately on first reconcile in any real cluster
- **Real e2e** should happen in a staging environment with actual GitHub integration, not a hermetic kind cluster with fakes

### Changes Required

#### 1. Makefile Targets

Add to the kubebuilder-generated `Makefile`:

```makefile
## Ko
KO_VERSION ?= v0.17.1
KO ?= $(LOCALBIN)/ko-$(KO_VERSION)
KO_DOCKER_REPO ?= ko.local/nissessenap/shepherd

.PHONY: ko
ko: $(KO)
$(KO): $(LOCALBIN)
	$(call go-install-tool,$(KO),github.com/google/ko,$(KO_VERSION))

.PHONY: ko-build-local
ko-build-local: ko
	$(KO) build --sbom=none --bare cmd/shepherd/main.go

.PHONY: build-smoke
build-smoke: ko-build-local manifests kustomize ## Verify ko build + kustomize render
	$(KUSTOMIZE) build config/default > /dev/null
	@echo "Build smoke test passed: ko image built, kustomize renders cleanly"
```

### Success Criteria

#### Automated Verification:

- [ ] `make ko-build-local` builds the binary successfully
- [ ] `make build-smoke` builds image and verifies kustomize renders cleanly
- [ ] No chainsaw, kind, or e2e infrastructure needed

#### Manual Verification:

- [ ] `make build-smoke` exits 0

---

## Makefile Cleanup Note

Kubebuilder generates a Makefile with many targets. After scaffolding (Phase 1), review and identify targets that are unnecessary for this project. Candidates to evaluate:

- **Docker targets** (`docker-build`, `docker-push`, `docker-buildx`) — replaced by ko. Remove or comment out.
- **`deploy`/`undeploy`** — may keep for dev convenience, but `make deploy-e2e` is the primary deployment path for testing.
- **`catalog-*` targets** — OLM catalog targets. Not needed unless publishing to OperatorHub. Remove.
- **`bundle-*` targets** — OLM bundle targets. Same as above. Remove.

Keep:
- `make generate` — deepcopy generation
- `make manifests` — CRD + RBAC YAML generation
- `make test` — envtest unit/integration tests
- `make fmt` / `make vet` / `make lint` — code quality
- `make install` / `make uninstall` — CRD install into cluster
- Tool download targets (`controller-gen`, `envtest`, `kustomize`, `golangci-lint`)

This cleanup should happen during Phase 1 after scaffolding, but doesn't need to be exhaustive — just remove the obviously wrong targets (docker, OLM) and leave the rest.

---

## Testing Strategy

### Unit Tests (testify)
- Condition helpers: `isTerminal`, `hasCondition`, `setCondition`
- Job builder: all fields correctly mapped
- Failure classifier: all failure types correctly identified
- Retry count: annotation parsing and setting

### Integration Tests (envtest + gomega)
- Full lifecycle: AgentTask → Pending → Running → Succeeded
- Full lifecycle: AgentTask → Pending → Running → Failed
- Retry: infrastructure failure → delete Job → create new Job → succeed
- Terminal state: no re-reconciliation after Succeeded/Failed
- Idempotency: reconciling same state doesn't create duplicate Jobs

### Build Smoke Test
- `make build-smoke`: verifies ko builds and kustomize renders cleanly
- No e2e/chainsaw tests — envtest covers K8s API interactions with a real API server
- Real e2e testing deferred to staging environment with actual GitHub integration

### Test Patterns
- Use `Eventually()` from gomega for async assertions in envtest
- Use table-driven tests with testify for unit tests
- Use `fake.NewClientBuilder()` for unit tests that need a client

## Performance Considerations

- `Owns(&batchv1.Job{})` ensures Job events trigger reconciliation without polling
- Leader election ensures single-active operator (no duplicate Job creation)
- Terminal state short-circuit avoids unnecessary work

## References

- Design doc: `docs/research/2026-01-27-shepherd-design.md`
- Kubebuilder book: https://book.kubebuilder.io/
- controller-runtime: https://pkg.go.dev/sigs.k8s.io/controller-runtime
- Tekton TaskRun conditions pattern: https://tekton.dev/docs/pipelines/taskruns/
