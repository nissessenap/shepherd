// pkg/api/handlers.go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Handlers contains HTTP handlers for the API
type Handlers struct {
	// K8s client will be injected during integration
}

// NewHandlers creates new API handlers
func NewHandlers() *Handlers {
	return &Handlers{}
}

// CreateTask handles POST /api/v1/tasks
func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RepoURL == "" {
		h.writeError(w, http.StatusBadRequest, "repo_url is required")
		return
	}
	if req.Description == "" {
		h.writeError(w, http.StatusBadRequest, "description is required")
		return
	}
	if req.CallbackURL == "" {
		h.writeError(w, http.StatusBadRequest, "callback_url is required")
		return
	}

	// TODO: Create AgentTask CRD (will be wired during integration)
	taskID := "task-placeholder"

	h.writeJSON(w, http.StatusCreated, CreateTaskResponse{TaskID: taskID})
}

// GetTaskStatus handles GET /api/v1/tasks/{id}
func (h *Handlers) GetTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		h.writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	// TODO: Fetch AgentTask CRD status (will be wired during integration)
	h.writeJSON(w, http.StatusOK, TaskStatusResponse{TaskID: taskID, Status: "pending"})
}

// UpdateTaskStatus handles POST /api/v1/tasks/{id}/status
func (h *Handlers) UpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		h.writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	var req StatusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TODO: Update AgentTask CRD status and notify callback
	w.WriteHeader(http.StatusAccepted)
}

// HealthCheck handles GET /healthz
func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// ReadyCheck handles GET /readyz
func (h *Handlers) ReadyCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handlers) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *Handlers) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, ErrorResponse{Error: message})
}
