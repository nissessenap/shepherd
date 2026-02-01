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
	"context"
	"fmt"
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
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// AgentTaskReconciler reconciles a AgentTask object
type AgentTaskReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Recorder             events.EventRecorder
	AllowedRunnerImage   string
	RunnerSecretName     string
	InitImage            string
	GithubAppID          int64
	GithubInstallationID int64
	GithubAPIURL         string
}

// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the AgentTask
	var task toolkitv1alpha1.AgentTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip if already in terminal state
	if task.IsTerminal() {
		log.V(1).Info("skipping reconcile for terminal task", "task", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Initialize condition if not set
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
		r.Recorder.Eventf(&task, nil, "Normal", "Pending", "Reconcile", "Task accepted, waiting for sandbox creation")
		log.Info("initialized task status", "task", req.NamespacedName)
		// Use RequeueAfter instead of deprecated Requeue: true (controller-runtime v0.23+ PR #3107)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Look for existing SandboxClaim (name = task.Name)
	var claim sandboxextv1alpha1.SandboxClaim
	claimKey := client.ObjectKey{Namespace: task.Namespace, Name: task.Name}

	err := r.Get(ctx, claimKey, &claim)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("getting sandbox claim: %w", err)
	}

	if err != nil {
		// SandboxClaim doesn't exist — create it
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
		now := metav1.Now()
		task.Status.StartTime = &now

		if statusErr := r.Status().Update(ctx, &task); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating status after sandbox claim creation: %w", statusErr)
		}
		r.Recorder.Eventf(&task, nil, "Normal", "SandboxClaimCreated", "Reconcile", "Created sandbox claim %s", newClaim.Name)
		log.Info("created sandbox claim", "claim", newClaim.Name)
		return ctrl.Result{}, nil
	}

	// SandboxClaim exists — status tracking deferred to Phase 3
	log.V(1).Info("sandbox claim exists, status tracking pending", "claim", claim.Name)
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
