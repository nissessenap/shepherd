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
	// git rev-list --count returns "0" = no commits on branch
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("0\n")}, // git rev-list --count
		},
		errs: []error{nil},
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

	// Verify git rev-list was called
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "git", mock.calls[0].Name)
	assert.Equal(t, []string{"rev-list", "--count", "HEAD", "^HEAD@{upstream}"}, mock.calls[0].Args)
	assert.Equal(t, "/tmp/repo", mock.calls[0].Opts.Dir)
}

func TestHookChangesNoPR(t *testing.T) {
	// git rev-list --count returns "2" = commits exist, gh pr list returns empty
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("2\n")}, // git rev-list --count (2 commits)
			{ExitCode: 0, Stdout: []byte("")},    // gh pr list (no PR)
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

	// Verify both commands were called
	require.Len(t, mock.calls, 2)
	assert.Equal(t, "git", mock.calls[0].Name)
	assert.Equal(t, "gh", mock.calls[1].Name)
	assert.Contains(t, mock.calls[1].Args, "--head")
	assert.Contains(t, mock.calls[1].Args, "shepherd/task-1")
}

func TestHookPRCreated(t *testing.T) {
	// git rev-list --count returns "1" = commits exist, gh pr list returns URL
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("1\n")},                                   // git rev-list --count
			{ExitCode: 0, Stdout: []byte("https://github.com/org/repo/pull/42\n")}, // gh pr list
		},
		errs: []error{nil, nil},
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
	// git rev-list shows commits, PR exists, but API is unreachable
	mock := &mockExecutor{
		results: []*ExecResult{
			{ExitCode: 0, Stdout: []byte("1\n")},                                   // git rev-list --count
			{ExitCode: 0, Stdout: []byte("https://github.com/org/repo/pull/42\n")}, // gh pr list
		},
		errs: []error{nil, nil},
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
	// Should not return an error — exits silently on network failure
	require.NoError(t, err)
}
