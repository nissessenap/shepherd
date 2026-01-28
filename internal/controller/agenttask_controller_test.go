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
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

var _ = Describe("AgentTask Controller", func() {
	const resourceNamespace = "default"

	var reconciler *AgentTaskReconciler

	BeforeEach(func() {
		reconciler = &AgentTaskReconciler{
			Client:             k8sClient,
			Scheme:             k8sClient.Scheme(),
			Recorder:           events.NewFakeRecorder(10),
			AllowedRunnerImage: "shepherd-runner:latest",
			RunnerSecretName:   "shepherd-runner-app-key",
			InitImage:          "shepherd-init:latest",
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

	cleanupJob := func(name, namespace string) {
		nn := client.ObjectKey{Name: name, Namespace: namespace}
		job := &batchv1.Job{}
		if err := k8sClient.Get(ctx, nn, job); err == nil {
			propagation := metav1.DeletePropagationBackground
			_ = k8sClient.Delete(ctx, job, &client.DeleteOptions{
				PropagationPolicy: &propagation,
			})
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
			Expect(cond.Message).To(Equal("Waiting for job to start"))
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

	Context("When Job lifecycle is managed", func() {
		var (
			taskName string
			taskNN   types.NamespacedName
			testIdx  int
		)

		BeforeEach(func() {
			testIdx++
			taskName = fmt.Sprintf("test-job-%d", testIdx)
			taskNN = types.NamespacedName{Name: taskName, Namespace: resourceNamespace}
		})

		AfterEach(func() {
			// Clean up Jobs first (owned by the task, but belt-and-suspenders)
			var task toolkitv1alpha1.AgentTask
			if err := k8sClient.Get(ctx, taskNN, &task); err == nil {
				if task.Status.JobName != "" {
					cleanupJob(task.Status.JobName, resourceNamespace)
				}
				// Also clean up by expected name pattern in case status wasn't set
				expectedJobName := fmt.Sprintf("%s-%d-job", taskName, task.Generation)
				cleanupJob(expectedJobName, resourceNamespace)
			}
			cleanupTask(taskName, resourceNamespace)
		})

		reconcileToPending := func() {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		}

		reconcileToRunning := func() string {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			_ = result

			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Status.JobName).NotTo(BeEmpty(), "JobName should be set after Job creation")
			return task.Status.JobName
		}

		It("should create a Job on second reconcile and set Running", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()

			By("Second reconcile — creates Job")
			jobName := reconcileToRunning()

			By("Verifying Job exists")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			By("Verifying Job uses AllowedRunnerImage")
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal("shepherd-runner:latest"))

			By("Verifying task-files volume exists")
			volumeNames := make([]string, len(job.Spec.Template.Spec.Volumes))
			for i, v := range job.Spec.Template.Spec.Volumes {
				volumeNames[i] = v.Name
			}
			Expect(volumeNames).To(ContainElement("task-files"))

			By("Verifying runner mounts /task read-only")
			runnerMounts := job.Spec.Template.Spec.Containers[0].VolumeMounts
			var taskFilesMount *corev1.VolumeMount
			for i := range runnerMounts {
				if runnerMounts[i].Name == "task-files" {
					taskFilesMount = &runnerMounts[i]
					break
				}
			}
			Expect(taskFilesMount).NotTo(BeNil(), "runner should mount task-files volume")
			Expect(taskFilesMount.MountPath).To(Equal("/task"))
			Expect(taskFilesMount.ReadOnly).To(BeTrue(), "runner should have read-only access to task-files")

			By("Verifying runner has file path env vars instead of inline description")
			runnerEnv := job.Spec.Template.Spec.Containers[0].Env
			Expect(envVarValue(runnerEnv, "SHEPHERD_TASK_FILE")).To(Equal("/task/description.txt"))
			Expect(envVarValue(runnerEnv, "SHEPHERD_CONTEXT_FILE")).To(Equal("/task/context.txt"))
			Expect(envVarValue(runnerEnv, "SHEPHERD_TASK_DESCRIPTION")).To(BeEmpty(),
				"runner should not have SHEPHERD_TASK_DESCRIPTION — reads from file instead")

			By("Verifying task status")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Status.JobName).To(Equal(jobName))
			Expect(task.Status.StartTime).NotTo(BeNil())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonRunning))
			Expect(cond.Message).To(Equal("Job created"))
		})

		It("should set Succeeded when Job completes", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Simulating Job completion")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.CompletionTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobSuccessCriteriaMet,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionTrue,
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("Reconciling after Job completion")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying task is Succeeded")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonSucceeded))
			Expect(task.Status.CompletionTime).NotTo(BeNil())
		})

		It("should set Failed when Job fails with application error", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Simulating Job failure with a failed pod (application error)")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			// Create a Pod matching the Job's template labels so classifyJobFailure
			// sees a pod with a non-OOM, non-eviction failure (→ application error).
			podLabels := make(map[string]string)
			maps.Copy(podLabels, job.Spec.Template.Labels)
			podLabels["batch.kubernetes.io/job-name"] = job.Name
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName + "-pod",
					Namespace: resourceNamespace,
					Labels:    podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "runner", Image: "shepherd-runner:latest"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })
			pod.Status = corev1.PodStatus{
				Phase: corev1.PodFailed,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "runner",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
							},
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "BackoffLimitExceeded",
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("Reconciling after Job failure")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying task is Failed")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonFailed))
			Expect(task.Status.CompletionTime).NotTo(BeNil())
			Expect(task.Status.Result.Error).To(Equal("BackoffLimitExceeded"))
		})

		It("should include REPO_REF env var when ref is set", func() {
			task := &toolkitv1alpha1.AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      taskName,
					Namespace: resourceNamespace,
				},
				Spec: toolkitv1alpha1.AgentTaskSpec{
					Repo: toolkitv1alpha1.RepoSpec{
						URL: "https://github.com/test-org/test-repo.git",
						Ref: "feature-branch",
					},
					Task: toolkitv1alpha1.TaskSpec{
						Description: "Test task with ref",
						Context:     "Issue body with relevant details for the LLM",
					},
					Callback: toolkitv1alpha1.CallbackSpec{
						URL: "https://example.com/callback",
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Verifying Job env vars include ref")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			initEnv := job.Spec.Template.Spec.InitContainers[0].Env
			runnerEnv := job.Spec.Template.Spec.Containers[0].Env

			Expect(envVarValue(initEnv, "REPO_REF")).To(Equal("feature-branch"))
			Expect(envVarValue(runnerEnv, "SHEPHERD_REPO_REF")).To(Equal("feature-branch"))
		})

		It("should not re-create Job if it already exists", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Third reconcile — Job already exists, should check status")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying only one Job exists")
			var jobList batchv1.JobList
			Expect(k8sClient.List(ctx, &jobList, client.InNamespace(resourceNamespace),
				client.MatchingLabels{"shepherd.io/task": taskName})).To(Succeed())
			Expect(jobList.Items).To(HaveLen(1))
			Expect(jobList.Items[0].Name).To(Equal(jobName))
		})

		It("should retry on infrastructure failure (no pods) and requeue", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Simulating infrastructure failure — Job failed with no pods")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "Job failed",
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("Reconciling — should retry (delete Job, set annotation, requeue)")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue for retry")

			By("Verifying retry count annotation")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Annotations).To(HaveKeyWithValue("shepherd.io/retry-count", "1"))

			By("Verifying task is NOT terminal (still running)")
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown), "task should still be in-progress during retry")
		})

		It("should mark Failed after max infrastructure retries", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Setting retry count to max (3)")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			if task.Annotations == nil {
				task.Annotations = make(map[string]string)
			}
			task.Annotations["shepherd.io/retry-count"] = "3"
			Expect(k8sClient.Update(ctx, &task)).To(Succeed())

			By("Simulating another infrastructure failure")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "Job failed",
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("Reconciling — should fail permanently")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying task is Failed")
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonFailed))
			Expect(task.Status.Result.Error).To(ContainSubstring("Infrastructure failure after 3 retries"))
		})

		It("should set TimedOut when Job exceeds deadline", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Simulating timeout failure")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Reason:  "DeadlineExceeded",
					Message: "Job was active longer than specified deadline",
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("Reconciling after timeout")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying task is TimedOut")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonTimedOut))
			Expect(task.Status.Result.Error).To(Equal("Job exceeded timeout"))
		})

		It("should set Failed with OOMKilled when container is OOM killed", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			jobName := reconcileToRunning()

			By("Creating a pod with OOMKilled status")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      jobName,
			}, &job)).To(Succeed())

			podLabels := make(map[string]string)
			maps.Copy(podLabels, job.Spec.Template.Labels)
			podLabels["batch.kubernetes.io/job-name"] = job.Name
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName + "-oom-pod",
					Namespace: resourceNamespace,
					Labels:    podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "runner", Image: "shepherd-runner:latest"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })
			pod.Status = corev1.PodStatus{
				Phase: corev1.PodFailed,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "runner",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 137,
								Reason:   "OOMKilled",
							},
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			By("Simulating Job failure")
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "BackoffLimitExceeded",
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("Reconciling after OOM failure")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying task is Failed with OOM message")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())

			cond := meta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(toolkitv1alpha1.ReasonFailed))
			Expect(task.Status.Result.Error).To(Equal("Container killed: OOMKilled"))
		})

		It("should create new Job after infrastructure retry", func() {
			createAgentTask(taskName, resourceNamespace)
			reconcileToPending()
			firstJobName := reconcileToRunning()

			By("Simulating infrastructure failure — Job failed with no pods")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      firstJobName,
			}, &job)).To(Succeed())

			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
				},
				batchv1.JobCondition{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "Job failed",
				},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			By("First retry reconcile — deletes old Job, requeues")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			By("Next reconcile — creates new Job")
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: taskNN})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying new Job was created")
			var task toolkitv1alpha1.AgentTask
			Expect(k8sClient.Get(ctx, taskNN, &task)).To(Succeed())
			Expect(task.Status.JobName).To(Equal(firstJobName), "Job name should be the same since generation hasn't changed")

			// The old job was deleted and a new one created with the same name
			var newJob batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: resourceNamespace,
				Name:      firstJobName,
			}, &newJob)).To(Succeed())
		})
	})
})

func envVarValue(envVars []corev1.EnvVar, name string) string {
	for _, e := range envVars {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
