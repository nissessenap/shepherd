// internal/controller/agenttask_controller.go
package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shepherdv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// AgentTaskReconciler reconciles an AgentTask object
type AgentTaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shepherd.shepherd.io,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shepherd.shepherd.io,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shepherd.shepherd.io,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the AgentTask
	task := &shepherdv1alpha1.AgentTask{}
	if err := r.Get(ctx, req.NamespacedName, task); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Initialize conditions if empty
	if len(task.Status.Conditions) == 0 {
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Accepted",
			Status:             metav1.ConditionTrue,
			Reason:             "ValidationPassed",
			Message:            "Task validated and accepted",
			ObservedGeneration: task.Generation,
		})
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Running",
			Status:             metav1.ConditionFalse,
			Reason:             "Pending",
			Message:            "Waiting for job to start",
			ObservedGeneration: task.Generation,
		})
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Succeeded",
			Status:             metav1.ConditionFalse,
			Reason:             "InProgress",
			Message:            "",
			ObservedGeneration: task.Generation,
		})
		if err := r.Status().Update(ctx, task); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if job already exists
	if task.Status.JobName != "" {
		return r.reconcileJob(ctx, task)
	}

	// Create the job
	job := r.buildJob(task)
	if err := ctrl.SetControllerReference(task, job, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Creating job", "job", job.Name)
	if err := r.Create(ctx, job); err != nil {
		if errors.IsAlreadyExists(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	// Update status with job name
	task.Status.JobName = job.Name
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:               "Running",
		Status:             metav1.ConditionTrue,
		Reason:             "JobStarted",
		Message:            fmt.Sprintf("Job %s started", job.Name),
		ObservedGeneration: task.Generation,
	})
	if err := r.Status().Update(ctx, task); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) reconcileJob(ctx context.Context, task *shepherdv1alpha1.AgentTask) (ctrl.Result, error) {
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Status.JobName}, job); err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:               "Succeeded",
				Status:             metav1.ConditionFalse,
				Reason:             "JobDeleted",
				Message:            "Job was deleted",
				ObservedGeneration: task.Generation,
			})
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:               "Running",
				Status:             metav1.ConditionFalse,
				Reason:             "JobDeleted",
				Message:            "Job was deleted",
				ObservedGeneration: task.Generation,
			})
			return ctrl.Result{}, r.Status().Update(ctx, task)
		}
		return ctrl.Result{}, err
	}

	if job.Status.Succeeded > 0 {
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Succeeded",
			Status:             metav1.ConditionTrue,
			Reason:             "JobCompleted",
			Message:            "Job completed successfully",
			ObservedGeneration: task.Generation,
		})
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Running",
			Status:             metav1.ConditionFalse,
			Reason:             "JobCompleted",
			Message:            "Job completed successfully",
			ObservedGeneration: task.Generation,
		})
		return ctrl.Result{}, r.Status().Update(ctx, task)
	}

	if job.Status.Failed > 0 {
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Succeeded",
			Status:             metav1.ConditionFalse,
			Reason:             "JobFailed",
			Message:            "Job failed",
			ObservedGeneration: task.Generation,
		})
		meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               "Running",
			Status:             metav1.ConditionFalse,
			Reason:             "JobFailed",
			Message:            "Job failed",
			ObservedGeneration: task.Generation,
		})
		task.Status.Result.Error = "Job failed"
		return ctrl.Result{}, r.Status().Update(ctx, task)
	}

	return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) buildJob(task *shepherdv1alpha1.AgentTask) *batchv1.Job {
	jobName := fmt.Sprintf("%s-job", task.Name)

	// Convert timeout to seconds for ActiveDeadlineSeconds
	timeoutSeconds := int64(task.Spec.Runner.Timeout.Duration.Seconds())

	env := []corev1.EnvVar{
		{Name: "SHEPHERD_TASK_ID", Value: task.Name},
		{Name: "SHEPHERD_REPO_URL", Value: task.Spec.Repo.URL},
		{Name: "SHEPHERD_TASK_DESCRIPTION", Value: task.Spec.Task.Description},
	}

	// Add context if provided
	if task.Spec.Task.Context != "" {
		env = append(env, corev1.EnvVar{
			Name:  "SHEPHERD_TASK_CONTEXT",
			Value: task.Spec.Task.Context,
		})
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: task.Namespace,
			Labels: map[string]string{
				"shepherd.io/task": task.Name,
			},
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &timeoutSeconds,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: task.Spec.Runner.Image,
							Env:   env,
						},
					},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shepherdv1alpha1.AgentTask{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
