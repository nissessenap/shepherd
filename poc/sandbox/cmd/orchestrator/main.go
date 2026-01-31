package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(scheme))
	utilruntime.Must(extensionsv1alpha1.AddToScheme(scheme))
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	templateName := flag.String("template", "poc-runner", "SandboxTemplate name")
	namespace := flag.String("namespace", "default", "Namespace")
	taskID := flag.String("task-id", "poc-task-001", "Task ID to send")
	message := flag.String("message", "Hello from orchestrator", "Task message")
	timeout := flag.Duration("timeout", 5*time.Minute, "Overall timeout")
	localMode := flag.Bool("local", false, "Use kubectl port-forward instead of cluster DNS")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Build K8s client
	cfg := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error("failed to create k8s client", "error", err)
		os.Exit(1)
	}

	claimName := fmt.Sprintf("poc-%s", *taskID)

	// Step 1: Create SandboxClaim
	// Note: agent-sandbox v0.1.0 SandboxClaimSpec doesn't have Lifecycle field.
	// The claim won't auto-expire; clean up manually or via the orchestrator context timeout.
	log.Info("creating SandboxClaim", "name", claimName, "template", *templateName)
	claim := &extensionsv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: *namespace,
		},
		Spec: extensionsv1alpha1.SandboxClaimSpec{
			TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
				Name: *templateName,
			},
		},
	}

	if err := k8sClient.Create(ctx, claim); err != nil {
		log.Error("failed to create SandboxClaim", "error", err)
		os.Exit(1)
	}
	log.Info("SandboxClaim created", "name", claimName)

	// Step 2: Wait for Sandbox to be Ready
	log.Info("waiting for Sandbox to become Ready...")
	sandboxFQDN, err := waitForSandboxReady(ctx, k8sClient, claimName, *namespace, log)
	if err != nil {
		log.Error("sandbox did not become ready", "error", err)
		os.Exit(1)
	}
	log.Info("sandbox is ready", "fqdn", sandboxFQDN)

	// Step 3: Determine target URL and optionally start port-forward
	targetHost := sandboxFQDN
	var portForwardCmd *exec.Cmd

	if *localMode {
		log.Info("local mode: starting kubectl port-forward")
		svcName := claimName // SandboxClaim name matches the service name
		portForwardCmd = exec.CommandContext(ctx, "kubectl", "port-forward",
			fmt.Sprintf("svc/%s", svcName), "8888:8888",
			"-n", *namespace)
		portForwardCmd.Stdout = os.Stdout
		portForwardCmd.Stderr = os.Stderr

		if err := portForwardCmd.Start(); err != nil {
			log.Error("failed to start port-forward", "error", err)
			os.Exit(1)
		}
		defer func() {
			if portForwardCmd.Process != nil {
				portForwardCmd.Process.Kill()
			}
		}()

		// Give port-forward time to establish
		time.Sleep(3 * time.Second)
		targetHost = "localhost"
		log.Info("port-forward established", "target", "localhost:8888")
	}

	// Step 4: POST task to runner
	log.Info("sending task to runner", "taskID", *taskID, "target", targetHost)
	if err := sendTask(ctx, targetHost, *taskID, *message, log); err != nil {
		log.Error("failed to send task", "error", err)
		os.Exit(1)
	}

	log.Info("task sent successfully! Check pod logs for execution output.")

	// Step 5: Wait for pod to complete
	log.Info("waiting for pod to finish...")
	if err := waitForPodCompletion(ctx, k8sClient, claimName, *namespace, log); err != nil {
		log.Error("pod did not complete cleanly", "error", err)
		os.Exit(1)
	}

	log.Info("PoC complete! Sandbox ran task successfully.")
}

func waitForSandboxReady(ctx context.Context, c client.Client, name, ns string, log *slog.Logger) (string, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			// Get the Sandbox (created by the claim controller)
			var sandbox sandboxv1alpha1.Sandbox
			if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &sandbox); err != nil {
				log.Info("sandbox not found yet, waiting...")
				continue
			}

			// Check Ready condition
			readyCond := apimeta.FindStatusCondition(sandbox.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))
			if readyCond == nil {
				log.Info("sandbox exists but no Ready condition yet")
				continue
			}

			if readyCond.Status == metav1.ConditionTrue {
				return sandbox.Status.ServiceFQDN, nil
			}

			log.Info("sandbox not ready", "reason", readyCond.Reason, "message", readyCond.Message)
		}
	}
}

func sendTask(ctx context.Context, host, taskID, message string, log *slog.Logger) error {
	url := fmt.Sprintf("http://%s:8888/task", host)

	body := map[string]string{
		"taskID":  taskID,
		"message": message,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling task: %w", err)
	}

	// Retry a few times — the pod might be ready but the HTTP server
	// hasn't started serving yet
	var lastErr error
	for i := range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			log.Info("POST failed, retrying", "attempt", i+1, "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusAccepted {
			log.Info("task accepted by runner", "statusCode", resp.StatusCode)
			return nil
		}

		lastErr = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		log.Info("unexpected response, retrying", "status", resp.StatusCode)
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("failed after retries: %w", lastErr)
}

func waitForPodCompletion(ctx context.Context, c client.Client, sandboxName, ns string, log *slog.Logger) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var sandbox sandboxv1alpha1.Sandbox
			if err := c.Get(ctx, client.ObjectKey{Name: sandboxName, Namespace: ns}, &sandbox); err != nil {
				return fmt.Errorf("getting sandbox: %w", err)
			}

			// When the pod exits, the Ready condition changes
			readyCond := apimeta.FindStatusCondition(sandbox.Status.Conditions, string(sandboxv1alpha1.SandboxConditionReady))
			if readyCond == nil {
				continue
			}

			// If no longer ready and reason is pod-related, the container exited
			if readyCond.Status == metav1.ConditionFalse {
				log.Info("sandbox no longer ready — pod likely completed",
					"reason", readyCond.Reason, "message", readyCond.Message)
				return nil
			}

			log.Info("pod still running...")
		}
	}
}
