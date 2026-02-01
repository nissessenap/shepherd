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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	sandboxextv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

var _ = Describe("AgentTask Controller", func() {
	const resourceNamespace = "default"

	var reconciler *AgentTaskReconciler

	BeforeEach(func() {
		reconciler = &AgentTaskReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(10),
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
					Context:     "Issue body with relevant details for the LLM",
				},
				Callback: toolkitv1alpha1.CallbackSpec{
					URL: "https://example.com/callback",
				},
				Runner: toolkitv1alpha1.RunnerSpec{
					SandboxTemplateName: "test-template",
				},
			},
		}
		Expect(k8sClient.Create(ctx, task)).To(Succeed())
	}

	cleanupTask := func(name, namespace string) {
		nn := types.NamespacedName{Name: name, Namespace: namespace}
		resource := &toolkitv1alpha1.AgentTask{}
		if err := k8sClient.Get(ctx, nn, resource); err == nil {
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		}
	}

	cleanupClaim := func(name, namespace string) {
		nn := client.ObjectKey{Name: name, Namespace: namespace}
		claim := &sandboxextv1alpha1.SandboxClaim{}
		if err := k8sClient.Get(ctx, nn, claim); err == nil {
			_ = k8sClient.Delete(ctx, claim)
		}
	}

	Context("When reconciling a new AgentTask", func() {
		const name = "test-new"

		AfterEach(func() {
			cleanupTask(name, resourceNamespace)
		})

		It("should set Pending condition on first reconcile", func() {
			createAgentTask(name, resourceNamespace)
			nn := types.NamespacedName{Name: name, Namespace: resourceNamespace}

			By("Reconciling the created resource")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue after setting initial status")

			By("Verifying the Pending condition is set")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, nn, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonPending))
			Expect(cond.Message).To(Equal("Waiting for sandbox to start"))
			Expect(task.Status.ObservedGeneration).To(Equal(task.Generation))
		})
	})

	Context("When reconciling a terminal AgentTask", func() {
		const name = "test-terminal"

		AfterEach(func() {
			cleanupTask(name, resourceNamespace)
		})

		It("should not reconcile a Succeeded task", func() {
			createAgentTask(name, resourceNamespace)
			nn := types.NamespacedName{Name: name, Namespace: resourceNamespace}

			By("Manually setting the task to Succeeded")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, nn, &task)).To(Succeed())
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonSucceeded,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should return immediately")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("should not reconcile a Failed task", func() {
			createAgentTask(name, resourceNamespace)
			nn := types.NamespacedName{Name: name, Namespace: resourceNamespace}

			By("Manually setting the task to Failed")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, nn, &task)).To(Succeed())
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionFalse,
				Reason: toolkitv1alpha1.ReasonFailed,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should return immediately")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("When reconciling a deleted AgentTask", func() {
		It("should return without error for a non-existent resource", func() {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "does-not-exist",
					Namespace: resourceNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("When SandboxClaim lifecycle is managed", func() {
		var (
			taskName string
			taskNN   types.NamespacedName
			testIdx  int
		)

		BeforeEach(func() {
			testIdx++
			taskName = fmt.Sprintf("test-claim-%d", testIdx)
			taskNN = types.NamespacedName{Name: taskName, Namespace: resourceNamespace}
		})

		AfterEach(func() {
			cleanupClaim(taskName, resourceNamespace)
			cleanupTask(taskName, resourceNamespace)
		})

		reconcileToPending := func() {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		}

		reconcileToClaimed := func() string {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			_ = result

			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Status.SandboxClaimName).NotTo(BeEmpty(), "SandboxClaimName should be set after claim creation")
			return task.Status.SandboxClaimName
		}

		setClaimReady := func(claimName string, ready bool) {
			var claim sandboxextv1alpha1.SandboxClaim
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)).To(Succeed())

			status := metav1.ConditionFalse
			if ready {
				status = metav1.ConditionTrue
			}
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
				Type:   string(sandboxv1alpha1.SandboxConditionReady),
				Status: status,
				Reason: "TestSetup",
			})
			Expect(k8sClient.Status().Update(ctx, &claim)).To(Succeed())
		}

		It("should create a SandboxClaim on second reconcile", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()

			By("Second reconcile — creates SandboxClaim")
			claimName := reconcileToClaimed()

			By("Verifying SandboxClaim exists")
			var claim sandboxextv1alpha1.SandboxClaim
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)).To(Succeed())

			By("Verifying SandboxClaim template ref")
			Expect(claim.Spec.TemplateRef.Name).To(Equal("test-template"))

			By("Verifying SandboxClaim labels")
			Expect(claim.Labels["shepherd.io/task"]).To(Equal(taskName))

			By("Verifying SandboxClaim owner reference")
			Expect(claim.OwnerReferences).To(HaveLen(1))
			Expect(claim.OwnerReferences[0].Kind).To(Equal("AgentTask"))
			Expect(claim.OwnerReferences[0].Name).To(Equal(taskName))

			By("Verifying task status")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Status.SandboxClaimName).To(Equal(claimName))
			Expect(task.Status.StartTime).NotTo(BeNil())
		})

		It("should not re-create SandboxClaim if it already exists", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Third reconcile — SandboxClaim already exists, should not error")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			// Requeues because claim is not yet ready
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			By("Verifying only one SandboxClaim exists")
			var claimList sandboxextv1alpha1.SandboxClaimList
			Expect(k8sClient.List(ctx, &claimList, client.InNamespace(resourceNamespace),
				client.MatchingLabels{"shepherd.io/task": taskName})).To(Succeed())
			Expect(claimList.Items).To(HaveLen(1))
			Expect(claimList.Items[0].Name).To(Equal(claimName))
		})

		It("should set Running when SandboxClaim Ready=True", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Simulating SandboxClaim Ready=True")
			setClaimReady(claimName, true)

			By("Reconciling — should transition to Running")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying Running condition")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
			Expect(cond.Message).To(Equal("Sandbox is ready, task is running"))
		})

		It("should requeue when SandboxClaim not yet ready", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			reconcileToClaimed()

			By("Reconciling — SandboxClaim has no Ready condition yet")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue when claim not ready")
		})

		It("should mark Failed when SandboxClaim Ready=False and task was Running", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Setting SandboxClaim Ready=True to trigger Running")
			setClaimReady(claimName, true)
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())

			By("Simulating SandboxClaim Ready=False (sandbox terminated)")
			var claim sandboxextv1alpha1.SandboxClaim
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)).To(Succeed())
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
				Type:    string(sandboxv1alpha1.SandboxConditionReady),
				Status:  metav1.ConditionFalse,
				Reason:  "SandboxNotReady",
				Message: "Sandbox pod terminated unexpectedly",
			})
			Expect(k8sClient.Status().Update(ctx, &claim)).To(Succeed())

			By("Reconciling — should mark Failed")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying Failed condition")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonFailed))
			Expect(cond.Message).To(ContainSubstring("Sandbox terminated"))
			Expect(task.Status.CompletionTime).NotTo(BeNil())
			Expect(task.Status.Result.Error).To(ContainSubstring("Sandbox terminated"))
		})

		It("should delete SandboxClaim on terminal state", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Manually setting the task to Succeeded (simulating API callback)")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonSucceeded,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should clean up SandboxClaim")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying SandboxClaim is deleted")
			var claim sandboxextv1alpha1.SandboxClaim
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed(), "SandboxClaim should be deleted")
		})

		It("should not error when Running and SandboxClaim already Ready", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Setting SandboxClaim Ready=True and transitioning to Running")
			setClaimReady(claimName, true)
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling again — already Running, should be a no-op")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying condition is still Running")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
		})
	})
})
