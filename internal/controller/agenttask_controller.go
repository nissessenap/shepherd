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
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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
)

// AgentTaskReconciler reconciles a AgentTask object
type AgentTaskReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	Recorder           events.EventRecorder
	AllowedRunnerImage string
	RunnerSecretName   string
	InitImage          string
}

// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=toolkit.shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the AgentTask
	var task toolkitv1alpha1.AgentTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip if already in terminal state
	if isTerminal(&task) {
		log.V(1).Info("skipping reconcile for terminal task", "task", req.NamespacedName)
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
		// Use RequeueAfter instead of deprecated Requeue: true (controller-runtime v0.23+ PR #3107)
		return ctrl.Result{RequeueAfter: time.Second}, nil
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
			InitImage:          r.InitImage,
			Scheme:             r.Scheme,
		})
		if buildErr != nil {
			return r.markFailed(ctx, &task, toolkitv1alpha1.ReasonFailed,
				fmt.Sprintf("failed to build job: %v", buildErr))
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

// failureClass represents the type of Job failure.
type failureClass int

const (
	failureApplication failureClass = iota // Non-zero exit — permanent
	failureOOM                             // Exit code 137 (SIGKILL/OOM) — permanent
	failureTimeout                         // ActiveDeadlineSeconds exceeded — permanent
)

// classifyJobFailure examines a Job's Failed condition to determine the failure type.
// It relies on podFailurePolicy surfacing exit code 137 as reason "PodFailurePolicy"
// with a message containing "exit code 137".
func classifyJobFailure(cond batchv1.JobCondition) failureClass {
	switch cond.Reason {
	case "DeadlineExceeded":
		return failureTimeout
	case "PodFailurePolicy":
		if strings.Contains(cond.Message, "exit code 137") {
			return failureOOM
		}
		return failureApplication
	default:
		return failureApplication
	}
}

func (r *AgentTaskReconciler) reconcileJobStatus(ctx context.Context, task *toolkitv1alpha1.AgentTask, job *batchv1.Job) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	for _, c := range job.Status.Conditions {
		switch c.Type {
		case batchv1.JobComplete:
			if c.Status == corev1.ConditionTrue {
				return r.markSucceeded(ctx, task, "Job completed successfully")
			}
		case batchv1.JobFailed:
			if c.Status == corev1.ConditionTrue {
				fc := classifyJobFailure(c)
				switch fc {
				case failureOOM:
					return r.markFailed(ctx, task, toolkitv1alpha1.ReasonOOM,
						"Container killed: exit code 137 (OOM)")
				case failureTimeout:
					return r.markFailed(ctx, task, toolkitv1alpha1.ReasonTimedOut,
						"Job exceeded timeout")
				default:
					return r.markFailed(ctx, task, toolkitv1alpha1.ReasonFailed, c.Message)
				}
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

// SetupWithManager sets up the controller with the Manager.
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolkitv1alpha1.AgentTask{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
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
