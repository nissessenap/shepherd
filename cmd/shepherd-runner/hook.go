package main

import (
	"context"
	"encoding/json"
	"errors"
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
	event, message, details := verifyArtifacts(ctx, logger, exec, input.CWD, taskID, getenv)

	// 5. Report status to API
	if err := client.ReportStatus(ctx, taskID, event, message, details); err != nil {
		var httpErr *runner.HTTPStatusError
		if errors.As(err, &httpErr) {
			// API rejected the request — this is a real error, not a network issue
			return fmt.Errorf("API rejected status report (HTTP %d): %w", httpErr.StatusCode, err)
		}
		// Transport-level error (network unreachable, timeout, etc.) — exit silently
		// and let the Go entrypoint be the safety net
		logger.Error(err, "failed to report status, entrypoint will handle fallback")
		return nil
	}

	logger.Info("reported status", "event", event, "message", message)
	return nil
}

// verifyArtifacts checks PR existence and git state to determine task outcome.
// PR check comes first because it's the definitive success signal and is
// unaffected by whether commits have been pushed to the remote.
func verifyArtifacts(
	ctx context.Context, logger logr.Logger, exec CommandExecutor,
	cwd, taskID string, getenv func(string) string,
) (event, message string, details map[string]any) {
	branch := "shepherd/" + taskID

	// 1. Check PR first — most definitive signal of success.
	// After push, git rev-list --not --remotes returns 0 (commits are on remote),
	// so checking PR first avoids a false "no changes" on the happy path.
	logger.Info("checking for pull request", "branch", branch)
	prArgs := []string{
		"pr", "list", "--head", branch,
		"--json", "url", "--jq", ".[0].url",
	}
	res, err := exec.Run(ctx, "gh", prArgs, ExecOptions{Dir: cwd})
	if err != nil {
		logger.Error(err, "failed to run gh pr list")
		// Fall through to commit check
	} else {
		prURL := strings.TrimSpace(string(res.Stdout))
		if prURL != "" {
			return eventCompleted, "task completed", map[string]any{"pr_url": prURL}
		}
	}

	// 2. No PR — check if commits were made on the branch.
	// Compare against origin/{baseRef} so the count is correct even after push.
	baseRef := getenv("SHEPHERD_BASE_REF")
	if baseRef == "" {
		baseRef = "main"
	}
	logger.Info("checking for commits", "baseRef", baseRef)
	revArgs := []string{"rev-list", "--count", "origin/" + baseRef + "..HEAD"}
	res, err = exec.Run(ctx, "git", revArgs, ExecOptions{Dir: cwd})
	if err != nil {
		logger.Error(err, "failed to check commit count")
		return eventFailed, "failed to check git state", nil
	}

	commitCount := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || commitCount == "0" {
		return eventFailed, "no changes made", nil
	}

	return eventFailed, "changes made but no PR created", nil
}
