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

package github

import (
	"sync"

	"github.com/go-logr/logr"
)

// TaskMetadata stores the GitHub context needed to post comments when
// a callback arrives for a completed task.
type TaskMetadata struct {
	Owner       string
	Repo        string
	IssueNumber int
}

// CallbackHandler handles callback notifications from the Shepherd API.
// Phase 5 adds the full ServeHTTP implementation.
type CallbackHandler struct {
	secret    string
	ghClient  *Client
	apiClient *APIClient
	log       logr.Logger

	mu    sync.RWMutex
	tasks map[string]TaskMetadata // NOTE: Entries are cleaned up in handleCallback (Phase 5)
}

// NewCallbackHandler creates a new callback handler.
func NewCallbackHandler(secret string, ghClient *Client, apiClient *APIClient, log logr.Logger) *CallbackHandler {
	return &CallbackHandler{
		secret:    secret,
		ghClient:  ghClient,
		apiClient: apiClient,
		log:       log,
		tasks:     make(map[string]TaskMetadata),
	}
}

// RegisterTask stores metadata for a task so that callback notifications
// can be routed back to the correct GitHub issue.
func (h *CallbackHandler) RegisterTask(taskID string, meta TaskMetadata) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks[taskID] = meta
}
