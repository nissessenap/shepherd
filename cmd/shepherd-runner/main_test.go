package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
