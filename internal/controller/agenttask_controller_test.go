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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

var _ = Describe("AgentTask Controller", func() {
	const (
		resourceName      = "test-resource"
		resourceNamespace = "default"
	)

	var (
		reconciler         *AgentTaskReconciler
		typeNamespacedName types.NamespacedName
	)

	BeforeEach(func() {
		typeNamespacedName = types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		reconciler = &AgentTaskReconciler{
			Client:             k8sClient,
			Scheme:             k8sClient.Scheme(),
			Recorder:           events.NewFakeRecorder(10),
			AllowedRunnerImage: "shepherd-runner:latest",
			RunnerSecretName:   "shepherd-runner-app-key",
		}
	})

	AfterEach(func() {
		resource := &toolkitv1alpha1.AgentTask{}
		err := k8sClient.Get(ctx, typeNamespacedName, resource)
		if err == nil {
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		}
	})

	createAgentTask := func(name, namespace string) {
		task := &toolkitv1alpha1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: toolkitv1alpha1.AgentTaskSpec{
				Repo: toolkitv1alpha1.RepoSpec{
					URL: "https://github.com/test-org/test-repo.git",
				},
				Task: toolkitv1alpha1.TaskSpec{
					Description: "Test task for reconciler",
				},
				Callback: toolkitv1alpha1.CallbackSpec{
					URL: "https://example.com/callback",
				},
			},
		}
		Expect(k8sClient.Create(ctx, task)).To(Succeed())
	}

	Context("When reconciling a new AgentTask", func() {
		It("should set Pending condition on first reconcile", func() {
			createAgentTask(resourceName, resourceNamespace)

			By("Reconciling the created resource")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue after setting initial status")

			By("Verifying the Pending condition is set")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, typeNamespacedName, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonPending))
			Expect(cond.Message).To(Equal("Waiting for job to start"))
			Expect(task.Status.ObservedGeneration).To(Equal(task.Generation))
		})
	})

	Context("When reconciling a terminal AgentTask", func() {
		It("should not reconcile a Succeeded task", func() {
			createAgentTask(resourceName, resourceNamespace)

			By("Manually setting the task to Succeeded")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, typeNamespacedName, &task)).To(Succeed())
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonSucceeded,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should return immediately")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("should not reconcile a Failed task", func() {
			createAgentTask(resourceName, resourceNamespace)

			By("Manually setting the task to Failed")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, typeNamespacedName, &task)).To(Succeed())
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionFalse,
				Reason: toolkitv1alpha1.ReasonFailed,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should return immediately")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("When reconciling a deleted AgentTask", func() {
		It("should return without error for a non-existent resource", func() {
			result, err := reconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "does-not-exist",
					Namespace: resourceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("When reconciling an already-pending AgentTask", func() {
		It("should not re-initialize the condition", func() {
			createAgentTask(resourceName, resourceNamespace)

			By("First reconcile — sets Pending condition")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			By("Second reconcile — condition already exists, should not requeue")
			result, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero(), "should not requeue when condition already set")

			By("Verifying condition is still Pending")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, typeNamespacedName, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonPending))
		})
	})
})
