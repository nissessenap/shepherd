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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/go-logr/logr"

	"github.com/NissesSenap/shepherd/pkg/api"
)

// TaskMetadata stores the GitHub context needed to post comments when
// a callback arrives for a completed task.
type TaskMetadata struct {
	Owner       string
	Repo        string
	IssueNumber int
}

// CallbackHandler handles callback notifications from the Shepherd API.
type CallbackHandler struct {
	secret    string
	ghClient  *Client
	apiClient *APIClient
	log       logr.Logger

	// In-memory cache for fast lookup; API fallback handles restarts
	mu    sync.RWMutex
	tasks map[string]TaskMetadata
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

// ServeHTTP handles callback requests from the Shepherd API.
func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body with 1MB limit
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.log.Error(err, "failed to read callback body")
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Verify HMAC signature
	signature := r.Header.Get("X-Shepherd-Signature")
	if !h.verifySignature(body, signature) {
		h.log.Info("callback signature verification failed")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Parse payload
	var payload api.CallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.log.Error(err, "failed to parse callback payload")
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	h.log.Info("received callback", "taskID", payload.TaskID, "event", payload.Event)

	// Handle the callback
	h.handleCallback(r.Context(), &payload)

	w.WriteHeader(http.StatusOK)
}

// verifySignature verifies the HMAC-SHA256 signature from the API.
func (h *CallbackHandler) verifySignature(body []byte, signature string) bool {
	if h.secret == "" {
		return true // No verification if no secret
	}

	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// resolveTaskMetadata looks up task metadata from cache, falling back to
// the Shepherd API if not found (e.g., after a restart).
func (h *CallbackHandler) resolveTaskMetadata(ctx context.Context, taskID string) (TaskMetadata, bool) {
	// Check in-memory cache first
	h.mu.RLock()
	meta, ok := h.tasks[taskID]
	h.mu.RUnlock()
	if ok {
		return meta, true
	}

	// Fallback: query the Shepherd API for task details
	if h.apiClient == nil {
		h.log.Info("no API client configured, cannot recover task metadata", "taskID", taskID)
		return TaskMetadata{}, false
	}

	task, err := h.apiClient.GetTask(ctx, taskID)
	if err != nil {
		h.log.Error(err, "failed to fetch task from API for callback", "taskID", taskID)
		return TaskMetadata{}, false
	}

	// Parse owner/repo/issue from sourceURL (e.g., "https://github.com/org/repo/issues/42")
	meta, err = parseSourceURL(task.Task.SourceURL)
	if err != nil {
		h.log.Error(err, "failed to parse sourceURL from task", "taskID", taskID, "sourceURL", task.Task.SourceURL)
		return TaskMetadata{}, false
	}

	// Cache for future callbacks on the same task
	h.RegisterTask(taskID, meta)
	h.log.Info("recovered task metadata from API",
		"taskID", taskID, "owner", meta.Owner, "repo", meta.Repo, "issue", meta.IssueNumber)
	return meta, true
}

// parseSourceURL extracts owner, repo, and issue number from a GitHub issue URL.
// Expected format: https://github.com/{owner}/{repo}/issues/{number}
func parseSourceURL(sourceURL string) (TaskMetadata, error) {
	if sourceURL == "" {
		return TaskMetadata{}, fmt.Errorf("empty sourceURL")
	}
	u, err := url.Parse(sourceURL)
	if err != nil {
		return TaskMetadata{}, fmt.Errorf("invalid sourceURL: %w", err)
	}
	// Path: /owner/repo/issues/42
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "issues" {
		return TaskMetadata{}, fmt.Errorf("unexpected sourceURL format: %s", sourceURL)
	}
	issueNumber, err := strconv.Atoi(parts[3])
	if err != nil {
		return TaskMetadata{}, fmt.Errorf("invalid issue number in sourceURL: %w", err)
	}
	return TaskMetadata{
		Owner:       parts[0],
		Repo:        parts[1],
		IssueNumber: issueNumber,
	}, nil
}

// handleCallback processes the callback and posts appropriate GitHub comments.
func (h *CallbackHandler) handleCallback(ctx context.Context, payload *api.CallbackPayload) {
	// Look up task metadata (cache + API fallback)
	meta, ok := h.resolveTaskMetadata(ctx, payload.TaskID)
	if !ok {
		h.log.Info("unable to resolve task metadata, cannot post comment", "taskID", payload.TaskID)
		return
	}

	var comment string
	switch payload.Event {
	case api.EventCompleted:
		prURL := ""
		if v, ok := payload.Details["prURL"].(string); ok {
			prURL = v
		}
		if prURL != "" {
			comment = formatCompleted(prURL)
		} else {
			comment = "Shepherd completed the task successfully."
		}

	case api.EventFailed:
		errorMsg := payload.Message
		if v, ok := payload.Details["error"].(string); ok && v != "" {
			errorMsg = v
		}
		comment = formatFailed(errorMsg)

	case api.EventStarted, api.EventProgress:
		// Don't post comments for intermediate events
		h.log.V(1).Info("ignoring intermediate event", "event", payload.Event)
		return

	default:
		h.log.Info("unknown callback event type", "event", payload.Event)
		return
	}

	// Clean up task metadata for terminal events
	if payload.Event == api.EventCompleted || payload.Event == api.EventFailed {
		h.mu.Lock()
		delete(h.tasks, payload.TaskID)
		h.mu.Unlock()
	}

	if err := h.ghClient.PostComment(ctx, meta.Owner, meta.Repo, meta.IssueNumber, comment); err != nil {
		h.log.Error(err, "failed to post callback comment",
			"taskID", payload.TaskID,
			"event", payload.Event,
		)
	}
}
