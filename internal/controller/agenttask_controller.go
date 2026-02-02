/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// AgentTaskReconciler reconciles a AgentTask object
type AgentTaskReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   events.EventRecorder
	APIURL     string       // Internal API URL for runner task assignment
	HTTPClient *http.Client // Injectable for testing; defaults to http.DefaultClient
}

// TaskAssignment is the payload POSTed to the runner's /task endpoint.
type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
}

// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the AgentTask
	var task toolkitv1alpha1.AgentTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. If terminal → clean up SandboxClaim if still exists, then return
	if task.IsTerminal() {
		log.V(1).Info("task is terminal, checking for SandboxClaim cleanup", "task", req.NamespacedName)
		if err := r.cleanupSandboxClaim(ctx, &task); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 3. Initialize condition if not set → set Pending, requeue
	if !hasCondition(&task, toolkitv1alpha1.ConditionSucceeded) {
		setCondition(&task, metav1.Condition{
			Type:               toolkitv1alpha1.ConditionSucceeded,
			Status:             metav1.ConditionUnknown,
			Reason:             toolkitv1alpha1.ReasonPending,
			Message:            "Waiting for sandbox to start",
			ObservedGeneration: task.Generation,
		})
		task.Status.ObservedGeneration = task.Generation

		if err := r.Status().Update(ctx, &task); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating initial status: %w", err)
		}
		// events.EventRecorder uses (regarding, related, type, reason, action, note) signature
		r.Recorder.Eventf(&task, nil, "Normal", "Pending", "Reconcile", "Task accepted, waiting for sandbox creation")
		log.Info("initialized task status", "task", req.NamespacedName)
		// Use RequeueAfter instead of deprecated Requeue: true (controller-runtime v0.23+ PR #3107)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// 4. Look for existing SandboxClaim (name = task.Name)
	var claim sandboxextv1alpha1.SandboxClaim
	claimKey := client.ObjectKey{Namespace: task.Namespace, Name: task.Name}

	err := r.Get(ctx, claimKey, &claim)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("getting sandbox claim: %w", err)
	}

	// 5. No SandboxClaim → create it
	if err != nil {
		newClaim, buildErr := buildSandboxClaim(&task, sandboxConfig{
			Scheme: r.Scheme,
		})
		if buildErr != nil {
			return r.markFailed(ctx, &task, toolkitv1alpha1.ReasonFailed,
				fmt.Sprintf("failed to build sandbox claim: %v", buildErr))
		}
		if createErr := r.Create(ctx, newClaim); createErr != nil {
			return ctrl.Result{}, fmt.Errorf("creating sandbox claim: %w", createErr)
		}

		task.Status.SandboxClaimName = newClaim.Name

		if statusErr := r.Status().Update(ctx, &task); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating status after sandbox claim creation: %w", statusErr)
		}
		r.Recorder.Eventf(&task, nil, "Normal", "SandboxClaimCreated", "Reconcile", "Created sandbox claim %s", newClaim.Name)
		log.Info("created sandbox claim", "claim", newClaim.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// 6. SandboxClaim exists — check Ready condition
	readyCond := meta.FindStatusCondition(claim.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))

	succeededCond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
	isRunning := succeededCond != nil && succeededCond.Reason == toolkitv1alpha1.ReasonRunning

	// 6a. Ready=True → assign task to runner, then transition to Running
	if readyCond != nil && readyCond.Status == metav1.ConditionTrue {
		if isRunning {
			// Already Running — assignment already succeeded; check timeout.
			// (Also checked in section 7 for the edge case where Ready becomes nil/Unknown
			// while the task is Running.)
			if checkTimeout(&task) {
				log.Info("task timed out", "task", req.NamespacedName, "timeout", task.Spec.Runner.Timeout.Duration)
				if err := r.cleanupSandboxClaim(ctx, &task); err != nil {
					return ctrl.Result{}, err
				}
				return r.markFailed(ctx, &task, toolkitv1alpha1.ReasonTimedOut,
					fmt.Sprintf("Task exceeded timeout of %s", task.Spec.Runner.Timeout.Duration))
			}
			// Requeue at the timeout deadline so it fires even without external events.
			remaining := max(time.Until(task.Status.StartTime.Add(taskTimeout(&task))), time.Second)
			log.V(1).Info("sandbox ready and task already running", "claim", claim.Name, "requeueIn", remaining)
			return ctrl.Result{RequeueAfter: remaining}, nil
		}

		// GET Sandbox by name to read ServiceFQDN
		sandboxName := claim.Status.SandboxStatus.Name
		if sandboxName == "" {
			log.V(1).Info("SandboxClaim Ready but Sandbox name not yet populated, requeuing", "claim", claim.Name)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}

		var sandbox sandboxv1alpha1.Sandbox
		if err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: sandboxName}, &sandbox); err != nil {
			return ctrl.Result{}, fmt.Errorf("getting sandbox %s: %w", sandboxName, err)
		}

		if sandbox.Status.ServiceFQDN == "" {
			log.V(1).Info("Sandbox ServiceFQDN not yet available, requeuing", "sandbox", sandboxName)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}

		// POST task assignment to the runner
		assignment := TaskAssignment{
			TaskID: task.Name,
			APIURL: r.APIURL,
		}
		if err := r.assignTask(ctx, sandbox.Status.ServiceFQDN, assignment); err != nil {
			log.Error(err, "task assignment failed", "sandbox", sandboxName)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// Assignment succeeded — set Running (this IS the idempotency marker) and record StartTime
		now := metav1.Now()
		task.Status.StartTime = &now
		setCondition(&task, metav1.Condition{
			Type:               toolkitv1alpha1.ConditionSucceeded,
			Status:             metav1.ConditionUnknown,
			Reason:             toolkitv1alpha1.ReasonRunning,
			Message:            "Sandbox is ready, task assigned to runner",
			ObservedGeneration: task.Generation,
		})
		if statusErr := r.Status().Update(ctx, &task); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating status to running: %w", statusErr)
		}
		r.Recorder.Eventf(&task, nil, "Normal", "Running", "Reconcile", "Task assigned to sandbox %s", sandboxName)
		log.Info("task assigned and running", "sandbox", sandboxName, "claim", claim.Name)
		// Schedule requeue at timeout deadline.
		return ctrl.Result{RequeueAfter: taskTimeout(&task)}, nil
	}

	// 6b. Ready=False and task was previously Running → sandbox terminated
	if readyCond != nil && readyCond.Status == metav1.ConditionFalse && isRunning {
		log.Info("sandbox terminated while task was running", "claim", claim.Name, "reason", readyCond.Reason)
		return r.handleSandboxTermination(ctx, req)
	}

	// 7. Timeout check — covers the edge case where the Ready condition is temporarily
	// nil/Unknown while the task is Running (section 6a handles Ready=True).
	if isRunning && checkTimeout(&task) {
		log.Info("task timed out", "task", req.NamespacedName, "timeout", task.Spec.Runner.Timeout.Duration)
		if err := r.cleanupSandboxClaim(ctx, &task); err != nil {
			return ctrl.Result{}, err
		}
		return r.markFailed(ctx, &task, toolkitv1alpha1.ReasonTimedOut,
			fmt.Sprintf("Task exceeded timeout of %s", task.Spec.Runner.Timeout.Duration))
	}

	// 6c. Ready condition nil, False, or Unknown AND task not yet Running → still starting
	log.V(1).Info("sandbox claim not yet ready, requeuing", "claim", claim.Name)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// assignTask POSTs a task assignment to the runner's HTTP endpoint.
// Returns nil on success (200 OK or 409 Conflict), error otherwise.
// The caller handles retries via controller-runtime's RequeueAfter.
func (r *AgentTaskReconciler) assignTask(ctx context.Context, sandboxFQDN string, assignment TaskAssignment) error {
	log := logf.FromContext(ctx)
	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	body, err := json.Marshal(assignment)
	if err != nil {
		return fmt.Errorf("marshaling assignment: %w", err)
	}

	url := fmt.Sprintf("http://%s:8888/task", sandboxFQDN)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting to runner: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		// Runner already has this task (idempotent retry after crash)
		log.V(1).Info("runner already has task (409), treating as success")
		return nil
	default:
		return fmt.Errorf("runner returned %d", resp.StatusCode)
	}
}

// cleanupSandboxClaim deletes the SandboxClaim if it still exists for a terminal task.
func (r *AgentTaskReconciler) cleanupSandboxClaim(ctx context.Context, task *toolkitv1alpha1.AgentTask) error {
	if task.Status.SandboxClaimName == "" {
		return nil
	}
	log := logf.FromContext(ctx)

	var claim sandboxextv1alpha1.SandboxClaim
	claimKey := client.ObjectKey{Namespace: task.Namespace, Name: task.Status.SandboxClaimName}
	if err := r.Get(ctx, claimKey, &claim); err != nil {
		return client.IgnoreNotFound(err)
	}

	if err := r.Delete(ctx, &claim); err != nil {
		return client.IgnoreNotFound(err)
	}
	log.Info("deleted SandboxClaim for terminal task", "claim", claim.Name)
	return nil
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

// handleSandboxTermination manages the grace period when a sandbox terminates
// while the task is still Running. This gives the API time to process the
// runner's success callback before we mark the task as failed.
func (r *AgentTaskReconciler) handleSandboxTermination(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Refetch the task — the API may have updated it to terminal via callback
	var freshTask toolkitv1alpha1.AgentTask
	if err := r.Get(ctx, req.NamespacedName, &freshTask); err != nil {
		return ctrl.Result{}, fmt.Errorf("refetching task: %w", err)
	}
	if freshTask.IsTerminal() {
		log.Info("task already terminal after refetch, cleaning up SandboxClaim")
		if err := r.cleanupSandboxClaim(ctx, &freshTask); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Grace period: give the API time to process the runner's callback.
	// Use a status field to track the grace deadline.
	const graceDuration = 30 * time.Second

	if freshTask.Status.GraceDeadline != nil {
		// Grace period was already set — check if it has elapsed
		if time.Now().After(freshTask.Status.GraceDeadline.Time) {
			// Grace period elapsed — refetch claim (it may have changed during grace period),
			// classify termination reason, then clear GraceDeadline and mark failed
			var freshClaim sandboxextv1alpha1.SandboxClaim
			claimKey := client.ObjectKey{Namespace: freshTask.Namespace, Name: freshTask.Status.SandboxClaimName}
			if err := r.Get(ctx, claimKey, &freshClaim); err != nil {
				return ctrl.Result{}, fmt.Errorf("refetching claim: %w", err)
			}

			reason, message := classifyClaimTermination(&freshClaim)
			log.Info("grace period elapsed, marking task terminal", "reason", reason)

			// Clear GraceDeadline and mark failed in one status update
			now := metav1.Now()
			freshTask.Status.GraceDeadline = nil
			freshTask.Status.CompletionTime = &now
			freshTask.Status.Result.Error = message
			setCondition(&freshTask, metav1.Condition{
				Type:               toolkitv1alpha1.ConditionSucceeded,
				Status:             metav1.ConditionFalse,
				Reason:             reason,
				Message:            message,
				ObservedGeneration: freshTask.Generation,
			})
			if err := r.Status().Update(ctx, &freshTask); err != nil {
				return ctrl.Result{}, fmt.Errorf("marking failed: %w", err)
			}
			r.Recorder.Eventf(&freshTask, nil, "Warning", reason, "Reconcile", message)
			return ctrl.Result{}, nil
		}
		// Still within grace period — requeue until deadline
		remaining := time.Until(freshTask.Status.GraceDeadline.Time)
		log.V(1).Info("within grace period, requeuing", "remaining", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// First time seeing Ready=False while Running — start grace period
	graceDeadline := metav1.NewTime(time.Now().Add(graceDuration))
	freshTask.Status.GraceDeadline = &graceDeadline
	if err := r.Status().Update(ctx, &freshTask); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting grace deadline: %w", err)
	}
	log.Info("started grace period for sandbox termination")
	return ctrl.Result{RequeueAfter: graceDuration}, nil
}

// Condition reasons used by agent-sandbox controllers. These are string literals
// in agent-sandbox v0.1.0; upstream defines them as constants in later versions.
const (
	reasonSandboxExpired = "SandboxExpired"
	reasonClaimExpired   = "ClaimExpired"
)

// classifyClaimTermination inspects SandboxClaim conditions to determine the
// failure reason. SandboxExpired and ClaimExpired map to TimedOut; all others
// map to Failed.
func classifyClaimTermination(claim *sandboxextv1alpha1.SandboxClaim) (string, string) {
	readyCond := meta.FindStatusCondition(claim.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))
	if readyCond == nil {
		return toolkitv1alpha1.ReasonFailed, "SandboxClaim status unavailable"
	}
	if readyCond.Reason == reasonSandboxExpired ||
		readyCond.Reason == reasonClaimExpired {
		return toolkitv1alpha1.ReasonTimedOut, "Sandbox expired"
	}
	return toolkitv1alpha1.ReasonFailed, fmt.Sprintf("Sandbox terminated: %s", readyCond.Message)
}

const defaultTimeout = 30 * time.Minute

// taskTimeout returns the configured timeout or the default (30m).
func taskTimeout(task *toolkitv1alpha1.AgentTask) time.Duration {
	if d := task.Spec.Runner.Timeout.Duration; d > 0 {
		return d
	}
	return defaultTimeout
}

// checkTimeout returns true if the task has exceeded its timeout duration.
func checkTimeout(task *toolkitv1alpha1.AgentTask) bool {
	if task.Status.StartTime == nil {
		return false
	}
	return time.Now().After(task.Status.StartTime.Add(taskTimeout(task)))
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolkitv1alpha1.AgentTask{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&sandboxextv1alpha1.SandboxClaim{}).
		Complete(r)
}

// hasCondition returns true if the named condition exists.
func hasCondition(task *toolkitv1alpha1.AgentTask, condType string) bool {
	return meta.FindStatusCondition(task.Status.Conditions, condType) != nil
}

// setCondition sets or updates a condition on the task.
func setCondition(task *toolkitv1alpha1.AgentTask, condition metav1.Condition) {
	meta.SetStatusCondition(&task.Status.Conditions, condition)
}
