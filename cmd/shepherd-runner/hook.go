package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/NissesSenap/shepherd/pkg/runner"
)

const (
	eventFailed    = "failed"
	eventCompleted = "completed"
)

// HookInput is the JSON data CC passes to hooks on stdin.
// Note: CC does NOT pass result data — only metadata. Artifact verification
// must be done by inspecting git state and PR existence.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	StopHookActive bool   `json:"stop_hook_active"`
}

type HookCmd struct{}

func (c *HookCmd) Run() error {
	ctx := context.Background()
	logger := log.FromContext(ctx, "component", "hook")
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}

	return runHook(ctx, logger, os.Stdin, &osExecutor{}, os.Getenv)
}

// runHook contains the testable hook logic. Dependencies are injected for testing.
func runHook(
	ctx context.Context, logger logr.Logger, stdin io.Reader,
	exec CommandExecutor, getenv func(string) string,
) error {
	// 1. Read hook JSON from stdin
	data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	var input HookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("parsing hook input: %w", err)
	}

	// 2. Check stop_hook_active — if true, exit early (prevent infinite loop)
	if input.StopHookActive {
		logger.Info("stop_hook_active is true, exiting to prevent infinite loop")
		return nil
	}

	// 3. Read env vars
	apiURL := getenv("SHEPHERD_API_URL")
	taskID := getenv("SHEPHERD_TASK_ID")
	if apiURL == "" || taskID == "" {
		return fmt.Errorf("SHEPHERD_API_URL and SHEPHERD_TASK_ID must be set")
	}

	logger = logger.WithValues("taskID", taskID)

	// 4. Verify artifacts
	client := runner.NewClient(apiURL)
	event, message, details := verifyArtifacts(ctx, logger, exec, input.CWD, taskID)

	// 5. Report status to API
	if err := client.ReportStatus(ctx, taskID, event, message, details); err != nil {
		// On network errors reaching API: exit silently (let Go entrypoint be the safety net)
		logger.Error(err, "failed to report status, entrypoint will handle fallback")
		return nil
	}

	logger.Info("reported status", "event", event, "message", message)
	return nil
}

// verifyArtifacts checks git state and PR existence to determine task outcome.
func verifyArtifacts(
	ctx context.Context, logger logr.Logger, exec CommandExecutor,
	cwd, taskID string,
) (event, message string, details map[string]any) {
	// Check if any changes were made: git diff --quiet HEAD
	diffArgs := []string{"diff", "--quiet", "HEAD"}
	res, err := exec.Run(ctx, "git", diffArgs, ExecOptions{Dir: cwd})
	if err != nil {
		logger.Error(err, "failed to run git diff")
		return eventFailed, "failed to check git state", nil
	}

	if res.ExitCode == 0 {
		// No changes made
		return eventFailed, "no changes made", nil
	}

	// Changes exist — check if a PR was created
	branch := "shepherd/" + taskID
	prArgs := []string{
		"pr", "list", "--head", branch,
		"--json", "url", "--jq", ".[0].url",
	}
	res, err = exec.Run(ctx, "gh", prArgs, ExecOptions{Dir: cwd})
	if err != nil {
		logger.Error(err, "failed to run gh pr list")
		return eventFailed, "changes made but failed to check PR status", nil
	}

	prURL := strings.TrimSpace(string(res.Stdout))
	if prURL == "" {
		return eventFailed, "changes made but no PR created", nil
	}

	return eventCompleted, "task completed", map[string]any{"pr_url": prURL}
}
