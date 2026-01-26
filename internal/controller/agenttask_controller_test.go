// internal/controller/agenttask_controller_test.go
package controller

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	shepherdv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestAgentTaskReconciler_CreatesJob(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = shepherdv1alpha1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)

	task := &shepherdv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task",
			Namespace: "default",
		},
		Spec: shepherdv1alpha1.AgentTaskSpec{
			Repo:     shepherdv1alpha1.RepoSpec{URL: "https://github.com/org/repo.git"},
			Task:     shepherdv1alpha1.TaskSpec{Description: "Fix the bug"},
			Callback: shepherdv1alpha1.CallbackSpec{URL: "https://callback.example.com"},
			Runner:   shepherdv1alpha1.RunnerSpec{Image: "shepherd-runner:latest"},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(task).
		Build()

	reconciler := &AgentTaskReconciler{Client: client, Scheme: scheme}

	// First reconcile - initialize conditions
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-task", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Second reconcile - create job
	_, err = reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-task", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Verify job was created
	job := &batchv1.Job{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "test-task-job", Namespace: "default"}, job)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}

	if job.Spec.Template.Spec.Containers[0].Image != "shepherd-runner:latest" {
		t.Errorf("expected image shepherd-runner:latest, got %s", job.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestAgentTaskReconciler_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = shepherdv1alpha1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &AgentTaskReconciler{Client: client, Scheme: scheme}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "non-existent", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Requeue {
		t.Error("should not requeue for non-existent task")
	}
}

func TestBuildJob(t *testing.T) {
	reconciler := &AgentTaskReconciler{}
	task := &shepherdv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "my-task", Namespace: "shepherd"},
		Spec: shepherdv1alpha1.AgentTaskSpec{
			Repo:   shepherdv1alpha1.RepoSpec{URL: "https://github.com/test/repo.git"},
			Task:   shepherdv1alpha1.TaskSpec{Description: "Test description"},
			Runner: shepherdv1alpha1.RunnerSpec{Image: "custom-runner:v1"},
		},
	}

	job := reconciler.buildJob(task)

	if job.Name != "my-task-job" {
		t.Errorf("expected job name my-task-job, got %s", job.Name)
	}
	if job.Namespace != "shepherd" {
		t.Errorf("expected namespace shepherd, got %s", job.Namespace)
	}
	if job.Spec.Template.Spec.Containers[0].Image != "custom-runner:v1" {
		t.Errorf("expected image custom-runner:v1, got %s", job.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestAgentTaskReconciler_JobSucceeded(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = shepherdv1alpha1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)

	task := &shepherdv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-task",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: shepherdv1alpha1.AgentTaskSpec{
			Repo:     shepherdv1alpha1.RepoSpec{URL: "https://github.com/org/repo.git"},
			Task:     shepherdv1alpha1.TaskSpec{Description: "Fix the bug"},
			Callback: shepherdv1alpha1.CallbackSpec{URL: "https://callback.example.com"},
			Runner:   shepherdv1alpha1.RunnerSpec{Image: "shepherd-runner:latest"},
		},
		Status: shepherdv1alpha1.AgentTaskStatus{
			JobName: "test-task-job",
			Conditions: []metav1.Condition{
				{
					Type:               "Accepted",
					Status:             metav1.ConditionTrue,
					Reason:             "ValidationPassed",
					Message:            "Task validated and accepted",
					ObservedGeneration: 1,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "Running",
					Status:             metav1.ConditionTrue,
					Reason:             "JobStarted",
					Message:            "Job test-task-job started",
					ObservedGeneration: 1,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "Succeeded",
					Status:             metav1.ConditionFalse,
					Reason:             "InProgress",
					Message:            "",
					ObservedGeneration: 1,
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task-job",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Succeeded: 1,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task, job).
		WithStatusSubresource(task).
		Build()

	reconciler := &AgentTaskReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-task", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Verify task status was updated
	updatedTask := &shepherdv1alpha1.AgentTask{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "test-task", Namespace: "default"}, updatedTask)
	if err != nil {
		t.Fatalf("failed to get updated task: %v", err)
	}

	// Check Succeeded condition
	succeededCond := findCondition(updatedTask.Status.Conditions, "Succeeded")
	if succeededCond == nil {
		t.Fatal("Succeeded condition not found")
	}
	if succeededCond.Status != metav1.ConditionTrue {
		t.Errorf("expected Succeeded condition to be True, got %s", succeededCond.Status)
	}
	if succeededCond.Reason != "JobCompleted" {
		t.Errorf("expected Succeeded reason to be JobCompleted, got %s", succeededCond.Reason)
	}
	if succeededCond.ObservedGeneration != 1 {
		t.Errorf("expected ObservedGeneration to be 1, got %d", succeededCond.ObservedGeneration)
	}

	// Check Running condition
	runningCond := findCondition(updatedTask.Status.Conditions, "Running")
	if runningCond == nil {
		t.Fatal("Running condition not found")
	}
	if runningCond.Status != metav1.ConditionFalse {
		t.Errorf("expected Running condition to be False, got %s", runningCond.Status)
	}
}

func TestAgentTaskReconciler_JobFailed(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = shepherdv1alpha1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)

	task := &shepherdv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-task",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: shepherdv1alpha1.AgentTaskSpec{
			Repo:     shepherdv1alpha1.RepoSpec{URL: "https://github.com/org/repo.git"},
			Task:     shepherdv1alpha1.TaskSpec{Description: "Fix the bug"},
			Callback: shepherdv1alpha1.CallbackSpec{URL: "https://callback.example.com"},
			Runner:   shepherdv1alpha1.RunnerSpec{Image: "shepherd-runner:latest"},
		},
		Status: shepherdv1alpha1.AgentTaskStatus{
			JobName: "test-task-job",
			Conditions: []metav1.Condition{
				{
					Type:               "Accepted",
					Status:             metav1.ConditionTrue,
					Reason:             "ValidationPassed",
					Message:            "Task validated and accepted",
					ObservedGeneration: 1,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "Running",
					Status:             metav1.ConditionTrue,
					Reason:             "JobStarted",
					Message:            "Job test-task-job started",
					ObservedGeneration: 1,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "Succeeded",
					Status:             metav1.ConditionFalse,
					Reason:             "InProgress",
					Message:            "",
					ObservedGeneration: 1,
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task-job",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Failed: 1,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task, job).
		WithStatusSubresource(task).
		Build()

	reconciler := &AgentTaskReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-task", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Verify task status was updated
	updatedTask := &shepherdv1alpha1.AgentTask{}
	err = client.Get(context.Background(), types.NamespacedName{Name: "test-task", Namespace: "default"}, updatedTask)
	if err != nil {
		t.Fatalf("failed to get updated task: %v", err)
	}

	// Check Succeeded condition
	succeededCond := findCondition(updatedTask.Status.Conditions, "Succeeded")
	if succeededCond == nil {
		t.Fatal("Succeeded condition not found")
	}
	if succeededCond.Status != metav1.ConditionFalse {
		t.Errorf("expected Succeeded condition to be False, got %s", succeededCond.Status)
	}
	if succeededCond.Reason != "JobFailed" {
		t.Errorf("expected Succeeded reason to be JobFailed, got %s", succeededCond.Reason)
	}
	if succeededCond.ObservedGeneration != 1 {
		t.Errorf("expected ObservedGeneration to be 1, got %d", succeededCond.ObservedGeneration)
	}

	// Check Running condition
	runningCond := findCondition(updatedTask.Status.Conditions, "Running")
	if runningCond == nil {
		t.Fatal("Running condition not found")
	}
	if runningCond.Status != metav1.ConditionFalse {
		t.Errorf("expected Running condition to be False, got %s", runningCond.Status)
	}

	// Check error is recorded
	if updatedTask.Status.Result.Error != "Job failed" {
		t.Errorf("expected error to be 'Job failed', got %s", updatedTask.Status.Result.Error)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
