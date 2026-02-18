package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hookInput(active bool, cwd string) io.Reader {
	input := HookInput{
		SessionID:      "sess-1",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            cwd,
		HookEventName:  "Stop",
		StopHookActive: active,
	}
	data, _ := json.Marshal(input)
	return strings.NewReader(string(data))
}

func makeGetenv(apiURL, taskID string) func(string) string {
	return func(key string) string {
		switch key {
		case "SHEPHERD_API_URL":
			return apiURL
		case "SHEPHERD_TASK_ID":
			return taskID
		case "SHEPHERD_BASE_REF":
			return "main"
		default:
			return ""
		}
	}
}

func TestHookStopHookActive(t *testing.T) {
	mock := &mockExecutor{
		results: []*ExecResult{},
		errs:    []error{},
	}

	err := runHook(
		context.Background(), logr.Discard(),
		hookInput(true, "/tmp"), mock,
		makeGetenv("http://api:8081", "task-1"),
	)
	require.NoError(t, err)

	// No commands should have been executed
	assert.Empty(t, mock.calls)
}

func TestHookNoChanges(t *testing.T) {
	// gh pr list returns empty (no PR), git rev-list returns "0" (no commits)
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("")},    // gh pr list (no PR)
			{ExitCode: 0, Stdout: []byte("0\n")}, // git rev-list --count
		},
		errs: []error{nil, nil},
	}

	// Set up a mock API server to capture the status report
	var reportedEvent, reportedMessage string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		reportedEvent = req["event"].(string)
		reportedMessage = req["message"].(string)
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()

	err := runHook(
		context.Background(), logr.Discard(),
		hookInput(false, "/tmp/repo"), mock,
		makeGetenv(apiServer.URL, "task-1"),
	)
	require.NoError(t, err)

	assert.Equal(t, "failed", reportedEvent)
	assert.Equal(t, "no changes made", reportedMessage)

	// Verify PR check then commit check
	require.Len(t, mock.calls, 2)
	assert.Equal(t, "gh", mock.calls[0].Name)
	assert.Contains(t, mock.calls[0].Args, "--head")
	assert.Contains(t, mock.calls[0].Args, "shepherd/task-1")
	assert.Equal(t, "git", mock.calls[1].Name)
	assert.Equal(t, []string{"rev-list", "--count", "origin/main..HEAD"}, mock.calls[1].Args)
	assert.Equal(t, "/tmp/repo", mock.calls[1].Opts.Dir)
}

func TestHookChangesNoPR(t *testing.T) {
	// gh pr list returns empty (no PR), git rev-list returns "2" (commits exist)
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("")},    // gh pr list (no PR)
			{ExitCode: 0, Stdout: []byte("2\n")}, // git rev-list --count (2 commits)
		},
		errs: []error{nil, nil},
	}

	var reportedEvent, reportedMessage string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		reportedEvent = req["event"].(string)
		reportedMessage = req["message"].(string)
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()

	err := runHook(
		context.Background(), logr.Discard(),
		hookInput(false, "/tmp/repo"), mock,
		makeGetenv(apiServer.URL, "task-1"),
	)
	require.NoError(t, err)

	assert.Equal(t, "failed", reportedEvent)
	assert.Equal(t, "changes made but no PR created", reportedMessage)

	// Verify PR check then commit check
	require.Len(t, mock.calls, 2)
	assert.Equal(t, "gh", mock.calls[0].Name)
	assert.Contains(t, mock.calls[0].Args, "--head")
	assert.Contains(t, mock.calls[0].Args, "shepherd/task-1")
	assert.Equal(t, "git", mock.calls[1].Name)
	assert.Equal(t, []string{"rev-list", "--count", "origin/main..HEAD"}, mock.calls[1].Args)
}

func TestHookPRCreated(t *testing.T) {
	// gh pr list returns URL — PR found, no need to check commits
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("https://github.com/org/repo/pull/42\n")}, // gh pr list
		},
		errs: []error{nil},
	}

	var reportedEvent, reportedMessage string
	var reportedDetails map[string]any
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		reportedEvent = req["event"].(string)
		reportedMessage = req["message"].(string)
		if d, ok := req["details"].(map[string]any); ok {
			reportedDetails = d
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()

	err := runHook(
		context.Background(), logr.Discard(),
		hookInput(false, "/tmp/repo"), mock,
		makeGetenv(apiServer.URL, "task-1"),
	)
	require.NoError(t, err)

	assert.Equal(t, "completed", reportedEvent)
	assert.Equal(t, "task completed", reportedMessage)
	assert.Equal(t, "https://github.com/org/repo/pull/42", reportedDetails["pr_url"])

	// Only PR check was needed — no commit check
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "gh", mock.calls[0].Name)
}

func TestHookMissingEnvVars(t *testing.T) {
	tests := []struct {
		name   string
		apiURL string
		taskID string
	}{
		{name: "missing API URL", apiURL: "", taskID: "task-1"},
		{name: "missing task ID", apiURL: "http://api:8081", taskID: ""},
		{name: "both missing", apiURL: "", taskID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockExecutor{
				results: []*ExecResult{},
				errs:    []error{},
			}

			err := runHook(
				context.Background(), logr.Discard(),
				hookInput(false, "/tmp"), mock,
				makeGetenv(tt.apiURL, tt.taskID),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SHEPHERD_API_URL and SHEPHERD_TASK_ID must be set")
			assert.Empty(t, mock.calls)
		})
	}
}

func TestHookAPINetworkError(t *testing.T) {
	// PR exists, but API is unreachable (transport error)
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("https://github.com/org/repo/pull/42\n")}, // gh pr list
		},
		errs: []error{nil},
	}

	// Use an unreachable URL — closed server
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	apiServer.Close() // Close immediately so connections fail

	err := runHook(
		context.Background(), logr.Discard(),
		hookInput(false, "/tmp/repo"), mock,
		makeGetenv(apiServer.URL, "task-1"),
	)
	// Should not return an error — exits silently on transport failure
	require.NoError(t, err)
}

func TestHookAPIHTTPError(t *testing.T) {
	// PR exists, but API returns 500 (HTTP error, not transport)
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("https://github.com/org/repo/pull/42\n")}, // gh pr list
		},
		errs: []error{nil},
	}

	// API returns 500
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer apiServer.Close()

	err := runHook(
		context.Background(), logr.Discard(),
		hookInput(false, "/tmp/repo"), mock,
		makeGetenv(apiServer.URL, "task-1"),
	)
	// Should return an error — HTTP errors are not swallowed
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API rejected status report")
}
