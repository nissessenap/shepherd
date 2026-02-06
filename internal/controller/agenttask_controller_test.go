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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
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
			APIURL:   "http://shepherd-api.shepherd.svc.cluster.local:8081",
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

	cleanupSandbox := func(name, namespace string) {
		nn := client.ObjectKey{Name: name, Namespace: namespace}
		sb := &sandboxv1alpha1.Sandbox{}
		if err := k8sClient.Get(ctx, nn, sb); err == nil {
			_ = k8sClient.Delete(ctx, sb)
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
		)

		BeforeEach(func() {
			taskName = fmt.Sprintf("test-claim-%s", rand.String(8))
			taskNN = types.NamespacedName{Name: taskName, Namespace: resourceNamespace}
		})

		AfterEach(func() {
			cleanupSandbox(taskName+"-sandbox", resourceNamespace)
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

		// createSandboxForClaim creates a Sandbox resource to simulate the
		// agent-sandbox controller. Returns the sandbox name.
		createSandboxForClaim := func(claimName string) string {
			const serviceFQDN = "test-sandbox.default.svc.cluster.local"
			sandboxName := claimName + "-sandbox"

			sandbox := &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sandboxName,
					Namespace: resourceNamespace,
				},
				Spec: sandboxv1alpha1.SandboxSpec{
					PodTemplate: sandboxv1alpha1.PodTemplate{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "runner", Image: "test-runner:latest"},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, sandbox)).To(Succeed())

			// Set ServiceFQDN on the Sandbox status
			sandbox.Status.ServiceFQDN = serviceFQDN
			meta.SetStatusCondition(&sandbox.Status.Conditions, metav1.Condition{
				Type:   string(sandboxv1alpha1.SandboxConditionReady),
				Status: metav1.ConditionTrue,
				Reason: "Ready",
			})
			Expect(k8sClient.Status().Update(ctx, sandbox)).To(Succeed())

			return sandboxName
		}

		setClaimReadyWithSandbox := func(claimName, sandboxName string) {
			var claim sandboxextv1alpha1.SandboxClaim
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)).To(Succeed())

			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
				Type:   string(sandboxv1alpha1.SandboxConditionReady),
				Status: metav1.ConditionTrue,
				Reason: "TestSetup",
			})
			claim.Status.SandboxStatus.Name = sandboxName
			Expect(k8sClient.Status().Update(ctx, &claim)).To(Succeed())
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

		// setupRunnerMock creates an httptest server on a specific port and configures
		// the reconciler to use it. Returns the server (caller must close) and the handler func
		// that was registered.
		setupRunnerMock := func(handler http.HandlerFunc) (*httptest.Server, string) {
			server := httptest.NewServer(handler)
			// Extract host:port from test server to use as the "FQDN"
			host, port, err := net.SplitHostPort(server.Listener.Addr().String())
			Expect(err).NotTo(HaveOccurred())
			_ = port

			reconciler.HTTPClient = server.Client()
			// We need to override the URL construction in assignTask.
			// Since assignTask builds "http://<fqdn>:8888/task", we need the mock
			// on port 8888. Instead, we'll use a custom transport to redirect.
			transport := &rewriteTransport{
				base:      server.Client().Transport,
				targetURL: server.URL,
			}
			reconciler.HTTPClient = &http.Client{Transport: transport}

			return server, host
		}

		// reconcileToRunning takes the task through Pending → Claimed → Running
		// using a runner mock that accepts assignments.
		reconcileToRunning := func(claimName string) {
			sandboxName := createSandboxForClaim(claimName)
			setClaimReadyWithSandbox(claimName, sandboxName)

			server, _ := setupRunnerMock(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
			})
			defer server.Close()

			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should schedule requeue for timeout enforcement")

			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
			Expect(task.Status.StartTime).NotTo(BeNil(), "StartTime should be set when task becomes Running")
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
			Expect(task.Status.StartTime).To(BeNil(), "StartTime should not be set until task is Running")
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

		It("should POST assignment to runner when SandboxClaim Ready=True", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			sandboxName := createSandboxForClaim(claimName)
			setClaimReadyWithSandbox(claimName, sandboxName)

			By("Setting up runner mock to capture the assignment POST")
			var receivedAssignment TaskAssignment
			var receivedContentType string
			server, _ := setupRunnerMock(func(w http.ResponseWriter, r *http.Request) {
				receivedContentType = r.Header.Get("Content-Type")
				_ = json.NewDecoder(r.Body).Decode(&receivedAssignment)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
			})
			defer server.Close()

			By("Reconciling — should POST assignment and transition to Running")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should schedule requeue for timeout enforcement")

			By("Verifying POST body contains taskID and apiURL")
			// Fetch the task to get its name
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(receivedAssignment.TaskID).To(Equal(task.Name))
			Expect(receivedAssignment.APIURL).To(Equal("http://shepherd-api.shepherd.svc.cluster.local:8081"))
			Expect(receivedContentType).To(Equal("application/json"))

			By("Verifying Running condition")
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
			Expect(cond.Message).To(Equal("Sandbox is ready, task assigned to runner"))
		})

		It("should set Running after successful assignment", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			sandboxName := createSandboxForClaim(claimName)
			setClaimReadyWithSandbox(claimName, sandboxName)

			server, _ := setupRunnerMock(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			defer server.Close()

			By("Reconciling — should transition to Running and schedule timeout requeue")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should schedule requeue for timeout enforcement")

			By("Verifying Running condition")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
		})

		It("should not re-assign if already Running", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			reconcileToRunning(claimName)

			By("Setting up runner mock that tracks calls")
			var callCount atomic.Int32
			server, _ := setupRunnerMock(func(w http.ResponseWriter, r *http.Request) {
				callCount.Add(1)
				w.WriteHeader(http.StatusOK)
			})
			defer server.Close()

			By("Reconciling again — already Running, should not POST but should schedule timeout requeue")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should schedule requeue for timeout enforcement")
			Expect(callCount.Load()).To(Equal(int32(0)), "should not POST to runner when already Running")

			By("Verifying condition is still Running")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
		})

		It("should treat 409 from runner as success", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			sandboxName := createSandboxForClaim(claimName)
			setClaimReadyWithSandbox(claimName, sandboxName)

			By("Setting up runner mock that returns 409 Conflict")
			server, _ := setupRunnerMock(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "task already assigned", http.StatusConflict)
			})
			defer server.Close()

			By("Reconciling — should treat 409 as success and set Running")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should schedule requeue for timeout enforcement")

			By("Verifying Running condition")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
		})

		It("should requeue on transient assignment failure", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			sandboxName := createSandboxForClaim(claimName)
			setClaimReadyWithSandbox(claimName, sandboxName)

			By("Setting up runner mock that always returns 500")
			server, _ := setupRunnerMock(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "internal error", http.StatusInternalServerError)
			})
			defer server.Close()

			By("Reconciling — should requeue after assignment failure")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue on transient failure")

			By("Verifying task is NOT Running (assignment failed)")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).NotTo(Equal(toolkitv1alpha1.ReasonRunning))
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

		It("should requeue when SandboxClaim Ready but Sandbox name not populated", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Setting SandboxClaim Ready=True but without SandboxStatus.Name")
			setClaimReady(claimName, true)

			By("Reconciling — should requeue waiting for sandbox name")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})

		It("should requeue with grace period when SandboxClaim Ready=False and task Running", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Transitioning to Running via successful assignment")
			reconcileToRunning(claimName)

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

			By("First reconcile — should start grace period and requeue")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue for grace period")

			By("Verifying GraceDeadline is set in status")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Status.GraceDeadline).NotTo(BeNil())

			By("Verifying task is still Running (not yet Failed)")
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
		})

		It("should mark Failed after grace period if task still Running", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Transitioning to Running via successful assignment")
			reconcileToRunning(claimName)

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

			By("Setting an already-expired grace deadline to simulate elapsed grace period")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			expiredDeadline := metav1.NewTime(time.Now().Add(-1 * time.Minute))
			task.Status.GraceDeadline = &expiredDeadline
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — grace period elapsed, should mark Failed")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying Failed condition")
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonFailed))
			Expect(cond.Message).To(ContainSubstring("Sandbox terminated"))
			Expect(task.Status.CompletionTime).NotTo(BeNil())
			Expect(task.Status.Result.Error).To(ContainSubstring("Sandbox terminated"))
			Expect(task.Status.GraceDeadline).To(BeNil(), "GraceDeadline should be cleared")
		})

		It("should not mark Failed if API set Succeeded during grace period", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Transitioning to Running via successful assignment")
			reconcileToRunning(claimName)

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

			By("Simulating API already setting task to Succeeded (runner callback)")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonSucceeded,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should see terminal state on refetch and clean up")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying task is still Succeeded (not Failed)")
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonSucceeded))
		})

		// Note: The test "should mark TimedOut when timeout exceeded" was removed
		// because timeout enforcement is now delegated to agent-sandbox via
		// SandboxClaim.Lifecycle.ShutdownTime. The timeout behavior is tested
		// via the ClaimExpired and SandboxExpired tests below.

		It("should mark TimedOut when SandboxExpired reason on SandboxClaim", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Transitioning to Running via successful assignment")
			reconcileToRunning(claimName)

			By("Simulating SandboxClaim Ready=False with SandboxExpired reason")
			var claim sandboxextv1alpha1.SandboxClaim
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)).To(Succeed())
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
				Type:    string(sandboxv1alpha1.SandboxConditionReady),
				Status:  metav1.ConditionFalse,
				Reason:  reasonSandboxExpired,
				Message: "sandbox lifetime exceeded",
			})
			Expect(k8sClient.Status().Update(ctx, &claim)).To(Succeed())

			By("Setting an already-expired grace deadline")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			expiredDeadline := metav1.NewTime(time.Now().Add(-1 * time.Minute))
			task.Status.GraceDeadline = &expiredDeadline
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should classify as TimedOut")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying TimedOut condition")
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonTimedOut))
			Expect(cond.Message).To(Equal("Sandbox expired"))
		})

		It("should mark TimedOut when ClaimExpired reason on SandboxClaim", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Transitioning to Running via successful assignment")
			reconcileToRunning(claimName)

			By("Simulating SandboxClaim Ready=False with ClaimExpired reason")
			var claim sandboxextv1alpha1.SandboxClaim
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)).To(Succeed())
			meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
				Type:    string(sandboxv1alpha1.SandboxConditionReady),
				Status:  metav1.ConditionFalse,
				Reason:  reasonClaimExpired,
				Message: "claim lifetime exceeded",
			})
			Expect(k8sClient.Status().Update(ctx, &claim)).To(Succeed())

			By("Setting an already-expired grace deadline")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			expiredDeadline := metav1.NewTime(time.Now().Add(-1 * time.Minute))
			task.Status.GraceDeadline = &expiredDeadline
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should classify as TimedOut")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying TimedOut condition")
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonTimedOut))
			Expect(cond.Message).To(Equal("Sandbox expired"))
		})

		It("should delete SandboxClaim when task already succeeded via API callback", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			claimName := reconcileToClaimed()

			By("Transitioning to Running via successful assignment")
			reconcileToRunning(claimName)

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
				Message: "Sandbox shut down",
			})
			Expect(k8sClient.Status().Update(ctx, &claim)).To(Succeed())

			By("Simulating API setting task to Succeeded")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			now := metav1.Now()
			task.Status.CompletionTime = &now
			meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
				Type:   toolkitv1alpha1.ConditionSucceeded,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonSucceeded,
			})
			Expect(k8sClient.Status().Update(ctx, &task)).To(Succeed())

			By("Reconciling — should detect terminal task and clean up SandboxClaim")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying SandboxClaim is deleted")
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      claimName,
			}, &claim)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed(), "SandboxClaim should be deleted")

			By("Verifying task is still Succeeded")
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonSucceeded))
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
	})
})

// rewriteTransport rewrites all requests to target a test server URL,
// allowing assignTask() to build its normal "http://<fqdn>:8888/task" URL
// while actually hitting the httptest server.
type rewriteTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to the test server
	parsed, err := url.Parse(t.targetURL)
	if err != nil {
		return nil, fmt.Errorf("parsing target URL: %w", err)
	}
	req.URL.Scheme = parsed.Scheme
	req.URL.Host = parsed.Host
	return t.base.RoundTrip(req)
}
