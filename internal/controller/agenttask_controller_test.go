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
