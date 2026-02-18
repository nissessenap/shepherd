package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"

	"github.com/NissesSenap/shepherd/pkg/runner"
)

// ExecOptions configures command execution.
type ExecOptions struct {
	Dir   string   // Working directory
	Env   []string // Environment variables (KEY=VALUE)
	Stdin io.Reader
}

// ExecResult holds the outcome of a command execution.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// CommandExecutor abstracts os/exec for testing.
type CommandExecutor interface {
	// Run executes a command. Returns ExecResult for command outcomes (including non-zero exit).
	// Returns error only for non-command failures (context cancel, command not found).
	Run(ctx context.Context, name string, args []string, opts ExecOptions) (*ExecResult, error)
}

// osExecutor implements CommandExecutor using os/exec.
type osExecutor struct{}

func (e *osExecutor) Run(ctx context.Context, name string, args []string, opts ExecOptions) (*ExecResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Env != nil {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ExecResult{
		Stdout: []byte(stdout.String()),
		Stderr: []byte(stderr.String()),
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		// Non-exit error (context cancel, command not found, etc.)
		return nil, err
	}

	return result, nil
}

// GoRunner implements runner.TaskRunner for coding tasks.
type GoRunner struct {
	workDir   string // e.g., /workspace
	configDir string // e.g., /etc/shepherd (baked-in CC config)
	logger    logr.Logger
	execCmd   CommandExecutor
}

// ccOutput is the JSON output from `claude -p --output-format json`.
type ccOutput struct {
	SessionID    string  `json:"session_id"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Result       string  `json:"result"`
}

func (r *GoRunner) Run(ctx context.Context, task runner.TaskData, token string) (*runner.Result, error) {
	log := r.logger.WithValues("taskID", task.TaskID)

	// 0. Copy baked-in CC config from configDir to ~/.claude/
	if err := r.stageConfig(); err != nil {
		return nil, fmt.Errorf("staging config: %w", err)
	}

	// 1. Clone repo using token in URL
	repoDir, err := r.cloneRepo(ctx, log, task, token)
	if err != nil {
		return nil, fmt.Errorf("cloning repo: %w", err)
	}

	// 2. Create working branch: shepherd/{taskID}
	branch := "shepherd/" + task.TaskID
	res, err := r.execCmd.Run(ctx, "git", []string{"checkout", "-b", branch}, ExecOptions{Dir: repoDir})
	if err != nil {
		return nil, fmt.Errorf("creating branch: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("git checkout -b failed (exit %d): %s", res.ExitCode, string(res.Stderr))
	}
	log.Info("created branch", "branch", branch)

	// 3. Write task context to ~/task-context.md (outside the repo to avoid polluting it)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	contextPath := filepath.Join(home, "task-context.md")
	if err := os.WriteFile(contextPath, []byte(task.Context), 0o644); err != nil {
		return nil, fmt.Errorf("writing task context: %w", err)
	}

	// 4. Build env vars for hook
	env := []string{
		"SHEPHERD_API_URL=" + task.APIURL,
		"SHEPHERD_TASK_ID=" + task.TaskID,
		"SHEPHERD_BASE_REF=" + task.RepoRef,
		"GH_TOKEN=" + token,
		"DISABLE_AUTOUPDATER=1",
		"CI=true",
	}

	// 5. Build prompt
	prompt := buildPrompt(task)

	// 6. Invoke Claude Code
	log.Info("invoking claude code")
	ccArgs := []string{
		"-p", prompt,
		"--dangerously-skip-permissions",
		"--output-format", "json",
		"--max-turns", "50",
		"--max-budget-usd", "10.00",
	}
	res, err = r.execCmd.Run(ctx, "claude", ccArgs, ExecOptions{
		Dir: repoDir,
		Env: env,
	})
	if err != nil {
		return nil, fmt.Errorf("invoking claude: %w", err)
	}

	// 7. Parse JSON output and log metrics
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("claude exited with code %d: %s", res.ExitCode, string(res.Stderr))
	}

	var output ccOutput
	if err := json.Unmarshal(res.Stdout, &output); err != nil {
		log.Info("could not parse claude output as JSON", "error", err)
	} else {
		log.Info("claude finished",
			"sessionID", output.SessionID,
			"numTurns", output.NumTurns,
			"totalCostUSD", output.TotalCostUSD,
		)
	}

	// 8. Return Result — the hook handles success/failure detection
	return &runner.Result{
		Success: true,
		Message: "claude code completed",
	}, nil
}

// stageConfig copies baked-in CC config from configDir to ~/.claude/.
func (r *GoRunner) stageConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}

	files := []struct{ src, dst string }{
		{filepath.Join(r.configDir, "settings.json"), filepath.Join(claudeDir, "settings.json")},
		{filepath.Join(r.configDir, "CLAUDE.md"), filepath.Join(claudeDir, "CLAUDE.md")},
	}
	for _, f := range files {
		data, err := os.ReadFile(f.src)
		if err != nil {
			return fmt.Errorf("reading %s: %w", f.src, err)
		}
		if err := os.WriteFile(f.dst, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.dst, err)
		}
	}
	return nil
}

// cloneRepo clones the repository with the token embedded in the URL.
func (r *GoRunner) cloneRepo(ctx context.Context, log logr.Logger, task runner.TaskData, token string) (string, error) {
	cloneURL, err := tokenCloneURL(task.RepoURL, token)
	if err != nil {
		return "", fmt.Errorf("building clone URL: %w", err)
	}

	// Determine clone directory name from repo URL
	repoName := filepath.Base(task.RepoURL)
	repoName = strings.TrimSuffix(repoName, ".git")
	repoDir := filepath.Join(r.workDir, repoName)

	args := []string{"clone"}
	if task.RepoRef != "" {
		args = append(args, "--branch", task.RepoRef)
	}
	args = append(args, cloneURL, repoDir)

	log.Info("cloning repo", "repoDir", repoDir)
	res, err := r.execCmd.Run(ctx, "git", args, ExecOptions{})
	if err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	if res.ExitCode != 0 {
		// Sanitize stderr to avoid leaking the token in logs
		sanitized := strings.ReplaceAll(string(res.Stderr), token, "***")
		return "", fmt.Errorf("git clone failed (exit %d): %s", res.ExitCode, sanitized)
	}

	return repoDir, nil
}

// tokenCloneURL embeds the token into a GitHub HTTPS URL.
// Input:  https://github.com/owner/repo or https://github.com/owner/repo.git
// Output: https://x-access-token:{token}@github.com/owner/repo.git
func tokenCloneURL(repoURL, token string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("parsing repo URL: %w", err)
	}
	u.User = url.UserPassword("x-access-token", token)
	if !strings.HasSuffix(u.Path, ".git") {
		u.Path += ".git"
	}
	return u.String(), nil
}
