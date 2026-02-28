package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoint(t *testing.T) {
	assigned := make(chan TaskAssignment, 1)
	srv := httptest.NewServer(newMux(assigned))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPostTaskAccepts(t *testing.T) {
	assigned := make(chan TaskAssignment, 1)
	srv := httptest.NewServer(newMux(assigned))
	defer srv.Close()

	body := `{"taskID":"task-1","apiURL":"http://api:8080"}`
	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	ta := <-assigned
	if ta.TaskID != "task-1" {
		t.Errorf("expected taskID task-1, got %s", ta.TaskID)
	}
	if ta.APIURL != "http://api:8080" {
		t.Errorf("expected apiURL http://api:8080, got %s", ta.APIURL)
	}
}

func TestPostTaskRejectsSecond(t *testing.T) {
	assigned := make(chan TaskAssignment, 1)
	srv := httptest.NewServer(newMux(assigned))
	defer srv.Close()

	body := `{"taskID":"task-1","apiURL":"http://api:8080"}`

	// First assignment succeeds
	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first POST expected 200, got %d", resp.StatusCode)
	}

	// Second assignment is rejected (channel buffer full)
	resp, err = http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}

func TestPostTaskInvalidJSON(t *testing.T) {
	assigned := make(chan TaskAssignment, 1)
	srv := httptest.NewServer(newMux(assigned))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestExecuteTask(t *testing.T) {
	var dataRequested, statusRequested atomic.Bool
	var eventCount atomic.Int32
	var lastStatusDetails map[string]any

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/data"):
			dataRequested.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"description": "test task",
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
			eventCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/status"):
			statusRequested.Store(true)
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if d, ok := payload["details"].(map[string]any); ok {
				lastStatusDetails = d
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer api.Close()

	ta := TaskAssignment{TaskID: "test-task", APIURL: api.URL}
	err := executeTask(context.Background(), ta)
	require.NoError(t, err)
	assert.True(t, dataRequested.Load(), "should have requested task data")
	assert.True(t, statusRequested.Load(), "should have reported status")
	assert.Equal(t, int32(8), eventCount.Load(), "should have posted 8 events")
	assert.Equal(t, "https://github.com/test-org/test-repo/pull/42", lastStatusDetails["pr_url"],
		"completed status should include pr_url in details")
}
