package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NissesSenap/shepherd/pkg/runner"
)

// mockCall records a single command invocation.
type mockCall struct {
	Name string
	Args []string
	Opts ExecOptions
}

// mockExecutor records calls and returns pre-configured results.
type mockExecutor struct {
	calls   []mockCall
	results []*ExecResult // returned in order; last one repeats
	errs    []error       // returned in order; last one repeats
	idx     int
}

func (m *mockExecutor) Run(_ context.Context, name string, args []string, opts ExecOptions) (*ExecResult, error) {
	m.calls = append(m.calls, mockCall{Name: name, Args: args, Opts: opts})

	i := m.idx
	if i >= len(m.results) {
		i = len(m.results) - 1
	}
	if i >= len(m.errs) {
		i = len(m.errs) - 1
	}
	m.idx++

	var res *ExecResult
	if i >= 0 && i < len(m.results) {
		res = m.results[i]
	}
	var err error
	if i >= 0 && i < len(m.errs) {
		err = m.errs[i]
	}

	// If StreamStdout is set, feed the result's Stdout line-by-line
	if opts.StreamStdout != nil && res != nil && len(res.Stdout) > 0 {
		for line := range strings.SplitSeq(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
			opts.StreamStdout([]byte(line))
		}
	}

	return res, err
}

func newTestTask() runner.TaskData {
	return runner.TaskData{
		TaskID:      "task-123",
		APIURL:      "http://api:8081",
		Description: "Fix the login bug",
		Context:     "The login form crashes when email contains a plus sign",
		SourceURL:   "https://github.com/org/repo/issues/42",
		RepoURL:     "https://github.com/org/repo",
		RepoRef:     "main",
	}
}

// setupConfigDir creates a temporary config dir with stub CC config files.
func setupConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(`# Test`), 0o644))
	return dir
}

func TestRunCloneAndInvoke(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configDir := setupConfigDir(t)
	workDir := t.TempDir()

	// Pre-create the repo dir that "git clone" would create
	repoDir := filepath.Join(workDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	// Simulate stream-json NDJSON output from Claude Code
	ccOutput := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Analyzing..."}]}}
{"type":"result","session_id":"sess-123","num_turns":2,"total_cost_usd":0.12}`

	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0},                           // git clone
			{ExitCode: 0},                           // git checkout -b
			{ExitCode: 0, Stdout: []byte(ccOutput)}, // claude
		},
		errs: []error{nil, nil, nil},
	}

	gr := &GoRunner{
		workDir:   workDir,
		configDir: configDir,
		logger:    logr.Discard(),
		execCmd:   mock,
	}

	task := newTestTask()
	result, err := gr.Run(context.Background(), task, "ghp_test_token")
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Verify git clone was called with correct args
	require.GreaterOrEqual(t, len(mock.calls), 3)
	cloneCall := mock.calls[0]
	assert.Equal(t, "git", cloneCall.Name)
	assert.Equal(t, "clone", cloneCall.Args[0])
	assert.Contains(t, cloneCall.Args[1], "--branch")
	assert.Equal(t, "main", cloneCall.Args[2])
	// URL contains token
	assert.Contains(t, cloneCall.Args[3], "x-access-token:ghp_test_token@github.com")

	// Verify branch creation
	branchCall := mock.calls[1]
	assert.Equal(t, "git", branchCall.Name)
	assert.Equal(t, []string{"checkout", "-b", "shepherd/task-123"}, branchCall.Args)
	assert.Equal(t, repoDir, branchCall.Opts.Dir)

	// Verify claude invocation uses stream-json
	claudeCall := mock.calls[2]
	assert.Equal(t, "claude", claudeCall.Name)
	assert.Contains(t, claudeCall.Args, "-p")
	assert.Contains(t, claudeCall.Args, "--dangerously-skip-permissions")
	assert.Contains(t, claudeCall.Args, "--output-format")
	assert.Contains(t, claudeCall.Args, "stream-json")
	assert.Equal(t, repoDir, claudeCall.Opts.Dir)

	// Verify StreamStdout callback was set for the claude command
	assert.NotNil(t, claudeCall.Opts.StreamStdout)

	// Verify env vars passed to claude
	envMap := make(map[string]string)
	for _, e := range claudeCall.Opts.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	assert.Equal(t, "http://api:8081", envMap["SHEPHERD_API_URL"])
	assert.Equal(t, "task-123", envMap["SHEPHERD_TASK_ID"])
	assert.Equal(t, "main", envMap["SHEPHERD_BASE_REF"])
	assert.Equal(t, "ghp_test_token", envMap["GH_TOKEN"])
	assert.Equal(t, "1", envMap["DISABLE_AUTOUPDATER"])
	assert.Equal(t, "true", envMap["CI"])

	// Verify task-context.md was written to home dir (not repo dir)
	home, _ := os.UserHomeDir()
	contextContent, err := os.ReadFile(filepath.Join(home, "task-context.md"))
	require.NoError(t, err)
	assert.Equal(t, "The login form crashes when email contains a plus sign", string(contextContent))

	// Verify CC config was staged
	_, err = os.Stat(filepath.Join(home, ".claude", "settings.json"))
	assert.NoError(t, err)
}

func TestRunCloneFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configDir := setupConfigDir(t)
	workDir := t.TempDir()

	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 128, Stderr: []byte("fatal: repository not found")},
		},
		errs: []error{nil},
	}

	gr := &GoRunner{
		workDir:   workDir,
		configDir: configDir,
		logger:    logr.Discard(),
		execCmd:   mock,
	}

	task := newTestTask()
	_, err := gr.Run(context.Background(), task, "ghp_test_token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloning repo")
	assert.Contains(t, err.Error(), "repository not found")
}

func TestRunCCNonZeroExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configDir := setupConfigDir(t)
	workDir := t.TempDir()

	// Pre-create the repo dir
	repoDir := filepath.Join(workDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0}, // git clone
			{ExitCode: 0}, // git checkout -b
			{ExitCode: 1, Stderr: []byte("claude code error")}, // claude
		},
		errs: []error{nil, nil, nil},
	}

	gr := &GoRunner{
		workDir:   workDir,
		configDir: configDir,
		logger:    logr.Discard(),
		execCmd:   mock,
	}

	task := newTestTask()
	_, err := gr.Run(context.Background(), task, "ghp_test_token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude exited with code 1")
}

func TestRunCommandExecutorError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configDir := setupConfigDir(t)
	workDir := t.TempDir()

	mock := &mockExecutor{
		results: []*ExecResult{nil},
		errs:    []error{fmt.Errorf("command not found: git")},
	}

	gr := &GoRunner{
		workDir:   workDir,
		configDir: configDir,
		logger:    logr.Discard(),
		execCmd:   mock,
	}

	task := newTestTask()
	_, err := gr.Run(context.Background(), task, "ghp_test_token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git clone")
}

// mockEventPoster records PostEvents calls for testing.
type mockEventPoster struct {
	calls []any
}

func (m *mockEventPoster) PostEvents(_ context.Context, _ string, events any) error {
	m.calls = append(m.calls, events)
	return nil
}

func TestRunWithEventPosting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configDir := setupConfigDir(t)
	workDir := t.TempDir()

	repoDir := filepath.Join(workDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	ccOutput := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Thinking..."}]}}`,
		`{"type":"assistant","message":{"content":[` +
			`{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"main.go"}}]}}`,
		`{"type":"user","message":{"content":[` +
			`{"type":"tool_result","tool_use_id":"toolu_1","content":"package main"}]}}`,
		`{"type":"result","session_id":"sess-1","num_turns":1,"total_cost_usd":0.05}`,
	}, "\n")

	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0},
			{ExitCode: 0},
			{ExitCode: 0, Stdout: []byte(ccOutput)},
		},
		errs: []error{nil, nil, nil},
	}

	poster := &mockEventPoster{}
	gr := &GoRunner{
		workDir:     workDir,
		configDir:   configDir,
		logger:      logr.Discard(),
		execCmd:     mock,
		eventPoster: poster,
	}

	task := newTestTask()
	result, err := gr.Run(context.Background(), task, "ghp_test_token")
	require.NoError(t, err)
	assert.True(t, result.Success)

	// Verify events were posted: thinking, tool_call, tool_result (result message doesn't produce events)
	assert.Len(t, poster.calls, 3, "expected 3 event batches: thinking, tool_call, tool_result")
}

func TestBuildPrompt(t *testing.T) {
	task := newTestTask()
	prompt := buildPrompt(task)

	assert.Contains(t, prompt, "Fix the login bug")
	assert.Contains(t, prompt, "https://github.com/org/repo/issues/42")
	assert.Contains(t, prompt, "~/task-context.md")
	assert.Contains(t, prompt, "pull request")
	assert.Contains(t, prompt, "Stay focused")
}

func TestTokenCloneURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		token   string
		want    string
	}{
		{
			name:    "without .git suffix",
			repoURL: "https://github.com/org/repo",
			token:   "ghp_abc123",
			want:    "https://x-access-token:ghp_abc123@github.com/org/repo.git",
		},
		{
			name:    "with .git suffix",
			repoURL: "https://github.com/org/repo.git",
			token:   "ghp_abc123",
			want:    "https://x-access-token:ghp_abc123@github.com/org/repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tokenCloneURL(tt.repoURL, tt.token)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
