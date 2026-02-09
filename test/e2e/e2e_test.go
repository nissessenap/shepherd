//go:build e2e
// +build e2e

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

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NissesSenap/shepherd/test/utils"
)

// namespace where the project is deployed in
const namespace = "shepherd-system"

// apiURL is the NodePort-exposed API address for lifecycle tests
const apiURL = "http://localhost:30080"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("ensuring manager namespace exists")
		cmd := exec.Command("kubectl", "create", "ns", namespace, "--dry-run=client", "-o", "yaml")
		nsYAML, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		applyCmd := exec.Command("kubectl", "apply", "-f", "-")
		applyCmd.Stdin = strings.NewReader(nsYAML)
		_, err = utils.Run(applyCmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy-test")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("verifying agent-sandbox controller is available")
		cmd = exec.Command("kubectl", "rollout", "status",
			"statefulset/agent-sandbox-controller",
			"-n", "agent-sandbox-system", "--timeout=2m")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "agent-sandbox controller not available")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("undeploying the controller-manager")
		cmd := exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}

			By("Fetching AgentTask status")
			cmd = exec.Command("kubectl", "get", "agenttask", "-n", namespace, "-o", "yaml")
			agentTaskOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "AgentTask status:\n%s\n", agentTaskOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get AgentTask status: %s\n", err)
			}

			By("Fetching SandboxClaim status")
			cmd = exec.Command("kubectl", "get", "sandboxclaim", "-n", namespace, "-o", "yaml")
			claimOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "SandboxClaim status:\n%s\n", claimOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get SandboxClaim status: %s\n", err)
			}

			By("Fetching Sandbox status")
			cmd = exec.Command("kubectl", "get", "sandbox", "-n", namespace, "-o", "yaml")
			sandboxOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Sandbox status:\n%s\n", sandboxOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Sandbox status: %s\n", err)
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	Context("AgentTask Lifecycle", func() {
		var taskName string

		BeforeAll(func() {
			By("waiting for API to be ready")
			verifyAPIReady := func(g Gomega) {
				resp, err := http.Get(apiURL + "/healthz")
				g.Expect(err).NotTo(HaveOccurred())
				defer resp.Body.Close()
				g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			}
			Eventually(verifyAPIReady, 30*time.Second, 2*time.Second).Should(Succeed())

			By("creating the AgentTask via the public API")
			reqBody := `{
				"repo": {"url": "https://github.com/test-org/test-repo.git", "ref": "main"},
				"task": {
					"description": "E2E lifecycle test task",
					"context": "This is an e2e test to validate the full task lifecycle"
				},
				"callbackURL": "https://example.com/callback",
				"runner": {
					"sandboxTemplateName": "e2e-runner",
					"timeout": "5m"
				}
			}`
			resp, err := http.Post(
				apiURL+"/api/v1/tasks",
				"application/json",
				strings.NewReader(reqBody),
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to POST task to API")
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusCreated),
				"Expected 201 Created from API")

			var taskResp struct {
				ID string `json:"id"`
			}
			Expect(json.NewDecoder(resp.Body).Decode(&taskResp)).To(Succeed())
			Expect(taskResp.ID).NotTo(BeEmpty(), "API should return a task ID")
			taskName = taskResp.ID
			GinkgoWriter.Printf("Created task: %s\n", taskName)
		})

		AfterAll(func() {
			if taskName != "" {
				By("cleaning up the AgentTask")
				cmd := exec.Command("kubectl", "delete", "agenttask", taskName,
					"-n", namespace, "--ignore-not-found")
				_, _ = utils.Run(cmd)

				By("cleaning up the SandboxClaim")
				cmd = exec.Command("kubectl", "delete", "sandboxclaim", taskName,
					"-n", namespace, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
		})

		It("should create a SandboxClaim for the task", func() {
			verifySandboxClaim := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "sandboxclaim", taskName,
					"-n", namespace, "-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal(taskName))
			}
			Eventually(verifySandboxClaim, 30*time.Second, time.Second).Should(Succeed())
		})

		It("should reach Running state when sandbox is ready", func() {
			verifyRunning := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "agenttask", taskName,
					"-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Succeeded")].reason}`)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"))
			}
			Eventually(verifyRunning, 3*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("should reach Succeeded state after runner completes", func() {
			verifySucceeded := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "agenttask", taskName,
					"-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Succeeded")].status}`)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifySucceeded, 3*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("should have runner pod logs showing task execution", func() {
			verifyRunnerLogs := func(g Gomega) {
				// Find the runner pod by label selector
				cmd := exec.Command("kubectl", "get", "pods",
					"-n", namespace,
					"-l", fmt.Sprintf("shepherd.dev/task=%s", taskName),
					"-o", "jsonpath={.items[0].metadata.name}")
				podName, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(podName).NotTo(BeEmpty())

				// Get the logs
				cmd = exec.Command("kubectl", "logs", podName, "-n", namespace)
				logs, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(logs).To(ContainSubstring("task data fetched"))
				g.Expect(logs).To(ContainSubstring("status reported"))
			}
			Eventually(verifyRunnerLogs, 3*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("should set Notified condition after terminal state", func() {
			verifyNotified := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "agenttask", taskName,
					"-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Notified")].reason}`)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// CallbackSent if example.com responds 2xx, CallbackFailed otherwise — either is valid
				g.Expect(output).To(SatisfyAny(
					Equal("CallbackSent"),
					Equal("CallbackFailed"),
				))
			}
			Eventually(verifyNotified, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should clean up the SandboxClaim after terminal state", func() {
			verifyClaimDeleted := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "sandboxclaim", taskName,
					"-n", namespace, "--no-headers")
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "SandboxClaim should be deleted")
			}
			Eventually(verifyClaimDeleted, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})
})
