// pkg/api/types.go
package api

// CreateTaskRequest is the request body for POST /api/v1/tasks
type CreateTaskRequest struct {
	RepoURL     string `json:"repo_url"`
	Description string `json:"description"`
	Context     string `json:"context,omitempty"`
	CallbackURL string `json:"callback_url"`
}

// CreateTaskResponse is the response for POST /api/v1/tasks
type CreateTaskResponse struct {
	TaskID string `json:"task_id"`
}

// StatusUpdateRequest is the request body for POST /api/v1/tasks/{id}/status
type StatusUpdateRequest struct {
	Event   string            `json:"event"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// TaskStatusResponse is the response for GET /api/v1/tasks/{id}
type TaskStatusResponse struct {
	TaskID  string            `json:"task_id"`
	Status  string            `json:"status"`
	Message string            `json:"message,omitempty"`
	Result  map[string]string `json:"result,omitempty"`
}

// ErrorResponse is returned for errors
type ErrorResponse struct {
	Error string `json:"error"`
}
