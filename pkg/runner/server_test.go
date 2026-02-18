package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoint(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(s.newMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestTaskAccepted(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(s.newMux())
	defer srv.Close()

	body := `{"taskID":"task-1","apiURL":"http://api:8081"}`
	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	ta := <-s.assigned
	assert.Equal(t, "task-1", ta.TaskID)
	assert.Equal(t, "http://api:8081", ta.APIURL)
}

func TestTaskRejectsSecond(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(s.newMux())
	defer srv.Close()

	body := `{"taskID":"task-1","apiURL":"http://api:8081"}`

	// First assignment succeeds
	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Second assignment is rejected (channel buffer full)
	resp, err = http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestTaskInvalidJSON(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(s.newMux())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTaskMissingFields(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(s.newMux())
	defer srv.Close()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing taskID",
			body: `{"apiURL":"http://api:8081"}`,
		},
		{
			name: "missing apiURL",
			body: `{"taskID":"task-1"}`,
		},
		{
			name: "empty taskID",
			body: `{"taskID":"","apiURL":"http://api:8081"}`,
		},
		{
			name: "empty apiURL",
			body: `{"taskID":"task-1","apiURL":""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

// Mock implementations for executeTask tests

type mockRunner struct {
	result *Result
	err    error
}

func (m *mockRunner) Run(ctx context.Context, task TaskData, token string) (*Result, error) {
	return m.result, m.err
}

type mockAPIClient struct {
	taskData     *TaskData
	taskDataErr  error
	token        string
	tokenExpires time.Time
	tokenErr     error
	statusCalls  []statusCall
	statusErr    error
}

type statusCall struct {
	taskID  string
	event   string
	message string
}

func (m *mockAPIClient) FetchTaskData(ctx context.Context, taskID string) (*TaskData, error) {
	return m.taskData, m.taskDataErr
}

func (m *mockAPIClient) FetchToken(ctx context.Context, taskID string) (string, time.Time, error) {
	return m.token, m.tokenExpires, m.tokenErr
}

func (m *mockAPIClient) ReportStatus(
	ctx context.Context, taskID string, event, message string, details map[string]any,
) error {
	m.statusCalls = append(m.statusCalls, statusCall{taskID: taskID, event: event, message: message})
	return m.statusErr
}

func TestExecuteTaskHappyPath(t *testing.T) {
	mockClient := &mockAPIClient{
		taskData: &TaskData{
			TaskID:      "task-1",
			Description: "fix bug",
			Context:     "some context",
			RepoURL:     "https://github.com/org/repo",
			RepoRef:     "main",
		},
		token:        "ghs_test_token",
		tokenExpires: time.Now().Add(time.Hour),
		statusErr:    nil,
	}
	mockRun := &mockRunner{
		result: &Result{
			Success: true,
			PRURL:   "https://github.com/org/repo/pull/1",
			Message: "PR created",
		},
		err: nil,
	}

	s := NewServer(mockRun, WithClient(mockClient))
	ta := TaskAssignment{
		TaskID: "task-1",
		APIURL: "http://api:8081",
	}

	err := s.executeTask(context.Background(), ta)
	require.NoError(t, err)

	// Verify status calls: started + fallback completed
	require.Len(t, mockClient.statusCalls, 2)
	assert.Equal(t, "task-1", mockClient.statusCalls[0].taskID)
	assert.Equal(t, "started", mockClient.statusCalls[0].event)
	assert.Equal(t, "completed", mockClient.statusCalls[1].event)
	assert.Equal(t, "PR created", mockClient.statusCalls[1].message)
}

func TestExecuteTaskFetchDataFails(t *testing.T) {
	mockClient := &mockAPIClient{
		taskData:    nil,
		taskDataErr: assert.AnError,
		statusErr:   nil,
	}
	mockRun := &mockRunner{}

	s := NewServer(mockRun, WithClient(mockClient))
	ta := TaskAssignment{
		TaskID: "task-1",
		APIURL: "http://api:8081",
	}

	err := s.executeTask(context.Background(), ta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetching task data")

	// Verify status calls: started + failed
	require.Len(t, mockClient.statusCalls, 2)
	assert.Equal(t, "started", mockClient.statusCalls[0].event)
	assert.Equal(t, "failed", mockClient.statusCalls[1].event)
}

func TestExecuteTaskFetchTokenFails(t *testing.T) {
	mockClient := &mockAPIClient{
		taskData: &TaskData{
			TaskID:      "task-1",
			Description: "fix bug",
			RepoURL:     "https://github.com/org/repo",
		},
		tokenErr:  assert.AnError,
		statusErr: nil,
	}
	mockRun := &mockRunner{}

	s := NewServer(mockRun, WithClient(mockClient))
	ta := TaskAssignment{
		TaskID: "task-1",
		APIURL: "http://api:8081",
	}

	err := s.executeTask(context.Background(), ta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetching token")

	// Verify status calls: started + failed
	require.Len(t, mockClient.statusCalls, 2)
	assert.Equal(t, "started", mockClient.statusCalls[0].event)
	assert.Equal(t, "failed", mockClient.statusCalls[1].event)
}

func TestExecuteTaskRunnerFails(t *testing.T) {
	mockClient := &mockAPIClient{
		taskData: &TaskData{
			TaskID:      "task-1",
			Description: "fix bug",
			RepoURL:     "https://github.com/org/repo",
		},
		token:        "ghs_test_token",
		tokenExpires: time.Now().Add(time.Hour),
		statusErr:    nil,
	}
	mockRun := &mockRunner{
		result: nil,
		err:    assert.AnError,
	}

	s := NewServer(mockRun, WithClient(mockClient))
	ta := TaskAssignment{
		TaskID: "task-1",
		APIURL: "http://api:8081",
	}

	err := s.executeTask(context.Background(), ta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running task")

	// Verify status calls: started + failed
	require.Len(t, mockClient.statusCalls, 2)
	assert.Equal(t, "started", mockClient.statusCalls[0].event)
	assert.Equal(t, "failed", mockClient.statusCalls[1].event)
}

func TestExecuteTaskStartedReportFails(t *testing.T) {
	mockClient := &mockAPIClient{
		taskData: &TaskData{
			TaskID:      "task-1",
			Description: "fix bug",
			RepoURL:     "https://github.com/org/repo",
		},
		token:        "ghs_test_token",
		tokenExpires: time.Now().Add(time.Hour),
		statusErr:    assert.AnError, // ReportStatus will fail
	}
	mockRun := &mockRunner{
		result: &Result{
			Success: true,
			PRURL:   "https://github.com/org/repo/pull/1",
			Message: "PR created",
		},
		err: nil,
	}

	s := NewServer(mockRun, WithClient(mockClient))
	ta := TaskAssignment{
		TaskID: "task-1",
		APIURL: "http://api:8081",
	}

	// Task should still proceed despite started report failure (non-fatal)
	err := s.executeTask(context.Background(), ta)
	require.NoError(t, err)

	// Verify status calls were attempted (started + fallback completed, both fail but non-fatal)
	require.Len(t, mockClient.statusCalls, 2)
	assert.Equal(t, "started", mockClient.statusCalls[0].event)
	assert.Equal(t, "completed", mockClient.statusCalls[1].event)
}
