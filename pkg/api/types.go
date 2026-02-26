/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

// Callback event types used by runners and adapters.
const (
	EventStarted   = "started"
	EventProgress  = "progress"
	EventCompleted = "completed"
	EventFailed    = "failed"
)

// CreateTaskRequest is the JSON body for POST /api/v1/tasks.
type CreateTaskRequest struct {
	Repo     RepoRequest       `json:"repo"`
	Task     TaskRequest       `json:"task"`
	Callback string            `json:"callbackURL"`
	Runner   *RunnerConfig     `json:"runner"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// RepoRequest specifies the repository for the task.
type RepoRequest struct {
	URL string `json:"url"`
	Ref string `json:"ref,omitempty"`
}

// TaskRequest specifies the task details.
type TaskRequest struct {
	Description string `json:"description"`
	Context     string `json:"context,omitempty"`
	SourceURL   string `json:"sourceURL,omitempty"`
	SourceType  string `json:"sourceType,omitempty"`
	SourceID    string `json:"sourceID,omitempty"`
}

// RunnerConfig specifies optional runner overrides.
type RunnerConfig struct {
	SandboxTemplateName string `json:"sandboxTemplateName,omitempty"`
	Timeout             string `json:"timeout,omitempty"`
	ServiceAccountName  string `json:"serviceAccountName,omitempty"`
}

// TaskResponse is the JSON response for task endpoints.
type TaskResponse struct {
	ID             string            `json:"id"`
	Namespace      string            `json:"namespace"`
	Repo           RepoRequest       `json:"repo"`
	Task           TaskRequest       `json:"task"`
	CallbackURL    string            `json:"callbackURL"`
	Status         TaskStatusSummary `json:"status"`
	CreatedAt      string            `json:"createdAt"`
	CompletionTime *string           `json:"completionTime,omitempty"`
}

// TaskStatusSummary summarizes the task's current status.
type TaskStatusSummary struct {
	Phase            string `json:"phase"`
	Message          string `json:"message"`
	SandboxClaimName string `json:"sandboxClaimName,omitempty"`
	PRURL            string `json:"prURL,omitempty"`
	Error            string `json:"error,omitempty"`
}

// StatusUpdateRequest is the JSON body from the runner for POST /api/v1/tasks/{taskID}/status.
type StatusUpdateRequest struct {
	Event   string         `json:"event"` // started, progress, completed, failed
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// CallbackPayload is the JSON body sent to adapters.
type CallbackPayload struct {
	TaskID  string         `json:"taskID"`
	Event   string         `json:"event"` // started, progress, completed, failed
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// TaskDataResponse is the JSON response for GET /api/v1/tasks/{taskID}/data.
type TaskDataResponse struct {
	Description string      `json:"description"`
	Context     string      `json:"context"`
	SourceURL   string      `json:"sourceURL,omitempty"`
	Repo        RepoRequest `json:"repo"`
}

// TokenResponse is the JSON response for GET /api/v1/tasks/{taskID}/token.
type TokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// ErrorResponse is the standard error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// TaskEvent types for streaming agent activity.
type TaskEventType string

const (
	EventTypeThinking   TaskEventType = "thinking"
	EventTypeToolCall   TaskEventType = "tool_call"
	EventTypeToolResult TaskEventType = "tool_result"
	EventTypeError      TaskEventType = "error"
)

// TaskEvent represents a single agent activity event.
type TaskEvent struct {
	Sequence  int64            `json:"sequence"`
	Timestamp string           `json:"timestamp"`
	Type      TaskEventType    `json:"type"`
	Summary   string           `json:"summary"`
	Tool      string           `json:"tool,omitempty"`
	Input     map[string]any   `json:"input,omitempty"`
	Output    *TaskEventOutput `json:"output,omitempty"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
}

// TaskEventOutput contains the result of a tool execution.
type TaskEventOutput struct {
	Success bool   `json:"success"`
	Summary string `json:"summary,omitempty"`
}

// WSMessage is a WebSocket message envelope (server → client).
type WSMessage struct {
	Type string `json:"type"` // "task_event" or "task_complete"
	Data any    `json:"data"`
}

// TaskCompleteData is sent when a task reaches a terminal state.
type TaskCompleteData struct {
	TaskID string `json:"taskID"`
	Status string `json:"status"`
	PRURL  string `json:"prURL,omitempty"`
	Error  string `json:"error,omitempty"`
}

// PostEventRequest is the JSON body for POST /api/v1/tasks/{taskID}/events.
type PostEventRequest struct {
	Events []TaskEvent `json:"events"`
}
