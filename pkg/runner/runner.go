package runner

import "context"

// TaskAssignment is the payload sent by the operator when assigning a task.
type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
}

// TaskData holds the fetched task information for the runner.
type TaskData struct {
	TaskID      string
	APIURL      string
	Description string
	Context     string
	SourceURL   string
	RepoURL     string
	RepoRef     string
}

// Result holds the outcome of a task execution.
type Result struct {
	Success bool
	PRURL   string
	Message string
}

// TaskRunner is implemented by language-specific runners.
type TaskRunner interface {
	Run(ctx context.Context, task TaskData, token string) (*Result, error)
}
