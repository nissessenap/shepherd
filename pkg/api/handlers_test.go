// pkg/api/handlers_test.go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestCreateTask_Success(t *testing.T) {
	handlers := NewHandlers()
	body := CreateTaskRequest{
		RepoURL:     "https://github.com/org/repo.git",
		Description: "Fix the bug",
		CallbackURL: "https://callback.example.com/webhook",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handlers.CreateTask(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var resp CreateTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.TaskID == "" {
		t.Error("expected task_id to be non-empty")
	}
}

func TestCreateTask_MissingRepoURL(t *testing.T) {
	handlers := NewHandlers()
	body := CreateTaskRequest{Description: "Fix the bug", CallbackURL: "https://callback.example.com"}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handlers.CreateTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestCreateTask_MissingDescription(t *testing.T) {
	handlers := NewHandlers()
	body := CreateTaskRequest{RepoURL: "https://github.com/org/repo.git", CallbackURL: "https://callback.example.com"}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handlers.CreateTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestCreateTask_MissingCallbackURL(t *testing.T) {
	handlers := NewHandlers()
	body := CreateTaskRequest{RepoURL: "https://github.com/org/repo.git", Description: "Fix the bug"}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handlers.CreateTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestGetTaskStatus(t *testing.T) {
	handlers := NewHandlers()
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{id}", handlers.GetTaskStatus)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/test-123", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp TaskStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.TaskID != "test-123" {
		t.Errorf("expected task_id test-123, got %s", resp.TaskID)
	}
}

func TestHealthCheck(t *testing.T) {
	handlers := NewHandlers()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handlers.HealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got '%s'", w.Body.String())
	}
}

func TestReadyCheck(t *testing.T) {
	handlers := NewHandlers()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handlers.ReadyCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got '%s'", w.Body.String())
	}
}
