package runner

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthEndpoint(t *testing.T) {
	s := NewServer(nil, nil)
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
	s := NewServer(nil, nil)
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
	s := NewServer(nil, nil)
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
	s := NewServer(nil, nil)
	srv := httptest.NewServer(s.newMux())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
