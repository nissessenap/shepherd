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

// AgentTaskReconciler reconciles a AgentTask object
type AgentTaskReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	Recorder           events.EventRecorder
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
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// TODO Phase 4: Create/monitor Job here
	log.Info("reconcile complete (job creation not yet implemented)", "task", req.NamespacedName)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
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
