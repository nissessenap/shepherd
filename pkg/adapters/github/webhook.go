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
	"regexp"
	"strings"

	"github.com/NissesSenap/shepherd/pkg/api"
	"github.com/go-logr/logr"
	gh "github.com/google/go-github/v75/github"
)

// shepherdMentionRegex matches @shepherd mentions but not email-style patterns
// (e.g., user@shepherd.io). Requires start-of-string or whitespace before the @.
var shepherdMentionRegex = regexp.MustCompile(`(?i)(?:^|\s)@shepherd\b`)

// WebhookHandler handles incoming GitHub webhooks.
type WebhookHandler struct {
	secret                 string
	ghClient               *Client
	apiClient              *APIClient
	callbackHandler        *CallbackHandler
	callbackURL            string
	defaultSandboxTemplate string
	log                    logr.Logger
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(
	secret string,
	ghClient *Client,
	apiClient *APIClient,
	callbackHandler *CallbackHandler,
	callbackURL string,
	defaultSandboxTemplate string,
	log logr.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		secret:                 secret,
		ghClient:               ghClient,
		apiClient:              apiClient,
		callbackHandler:        callbackHandler,
		callbackURL:            callbackURL,
		defaultSandboxTemplate: defaultSandboxTemplate,
		log:                    log,
	}
}

// ServeHTTP handles webhook requests.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body with 10MB limit
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		h.log.Error(err, "failed to read webhook body")
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Verify signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if !h.verifySignature(body, signature) {
		h.log.Info("webhook signature verification failed")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Route by event type
	eventType := r.Header.Get("X-GitHub-Event")
	h.log.V(1).Info("received webhook", "event", eventType)

	switch eventType {
	case "issue_comment":
		h.handleIssueComment(r.Context(), body)
	case "ping":
		h.log.Info("received ping webhook")
	default:
		h.log.V(1).Info("ignoring event type", "event", eventType)
	}

	w.WriteHeader(http.StatusOK)
}

// verifySignature verifies the GitHub webhook signature using HMAC-SHA256.
func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
	if h.secret == "" {
		return true // No verification if no secret configured
	}

	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// handleIssueComment processes issue_comment events.
func (h *WebhookHandler) handleIssueComment(ctx context.Context, body []byte) {
	var event gh.IssueCommentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.log.Error(err, "failed to parse issue_comment event")
		return
	}

	// Only process new comments (not edits or deletes)
	if event.GetAction() != "created" {
		return
	}

	// Check for @shepherd mention
	commentBody := event.GetComment().GetBody()
	if !shepherdMentionRegex.MatchString(commentBody) {
		return
	}

	// Extract task description from comment
	description := strings.TrimSpace(shepherdMentionRegex.ReplaceAllString(commentBody, ""))
	if description == "" {
		description = "Work on this issue"
	}

	h.log.Info("processing @shepherd mention",
		"repo", event.GetRepo().GetFullName(),
		"issue", event.GetIssue().GetNumber(),
		"user", event.GetComment().GetUser().GetLogin(),
	)

	h.processTask(ctx, &event, description)
}

// maxContextSize is the soft limit for context passed to the API.
// The API's etcd limit is ~1.4MB compressed; 1MB uncompressed provides
// safe headroom since gzip typically achieves 3-5x compression on text.
const maxContextSize = 1_000_000 // 1MB

// processTask handles the task creation workflow.
func (h *WebhookHandler) processTask(ctx context.Context, event *gh.IssueCommentEvent, description string) {
	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()
	issueNumber := event.GetIssue().GetNumber()
	repoFullName := event.GetRepo().GetFullName()
	issueURL := event.GetIssue().GetHTMLURL()
	repoURL := event.GetRepo().GetCloneURL()

	// Format label values
	repoLabel := strings.ReplaceAll(repoFullName, "/", "-")
	issueLabel := fmt.Sprintf("%d", issueNumber)

	// Check for active tasks (deduplication)
	activeTasks, err := h.apiClient.GetActiveTasks(ctx, repoLabel, issueLabel)
	if err != nil {
		h.log.Error(err, "failed to check for active tasks")
		// Continue anyway - better to potentially create duplicate than fail silently
	}

	if len(activeTasks) > 0 {
		task := activeTasks[0]
		h.log.Info("task already running", "taskID", task.ID, "status", task.Status.Phase)

		if commentErr := h.ghClient.PostComment(ctx, owner, repo, issueNumber,
			formatAlreadyRunning(task.ID, task.Status.Phase)); commentErr != nil {
			h.log.Error(commentErr, "failed to post already-running comment")
		}
		return
	}

	// Build context from issue body and comments
	issueBody := event.GetIssue().GetBody()
	taskContext := h.buildContext(ctx, owner, repo, issueNumber, issueBody)

	// Create task
	createReq := api.CreateTaskRequest{
		Repo: api.RepoRequest{
			URL: repoURL,
		},
		Task: api.TaskRequest{
			Description: description,
			Context:     taskContext,
			SourceURL:   issueURL,
			SourceType:  "issue",
			SourceID:    issueLabel,
		},
		Callback: h.callbackURL,
		Runner: &api.RunnerConfig{
			SandboxTemplateName: h.defaultSandboxTemplate,
		},
		Labels: map[string]string{
			"shepherd.io/repo":  repoLabel,
			"shepherd.io/issue": issueLabel,
		},
	}

	taskResp, err := h.apiClient.CreateTask(ctx, createReq)
	if err != nil {
		h.log.Error(err, "failed to create task")
		if commentErr := h.ghClient.PostComment(ctx, owner, repo, issueNumber,
			formatFailed("Failed to create task: "+err.Error())); commentErr != nil {
			h.log.Error(commentErr, "failed to post error comment")
		}
		return
	}

	h.log.Info("created task", "taskID", taskResp.ID)

	// Register task metadata for callback handling
	h.callbackHandler.RegisterTask(taskResp.ID, TaskMetadata{
		Owner:       owner,
		Repo:        repo,
		IssueNumber: issueNumber,
	})

	// Post acknowledgment comment
	if commentErr := h.ghClient.PostComment(ctx, owner, repo, issueNumber,
		formatAcknowledge(taskResp.ID)); commentErr != nil {
		h.log.Error(commentErr, "failed to post acknowledgment comment")
	}
}

// buildContext assembles the context string from issue body and comments.
// Truncates if the total context exceeds maxContextSize.
func (h *WebhookHandler) buildContext(
	ctx context.Context, owner, repo string, issueNumber int, issueBody string,
) string {
	var sb strings.Builder
	sb.WriteString("## Issue Description\n\n")
	sb.WriteString(issueBody)
	sb.WriteString("\n\n")

	// Fetch comments
	comments, err := h.ghClient.ListIssueComments(ctx, owner, repo, issueNumber)
	if err != nil {
		h.log.Error(err, "failed to fetch issue comments")
		return sb.String()
	}

	if len(comments) > 0 {
		sb.WriteString("## Comments\n\n")
		for _, c := range comments {
			entry := fmt.Sprintf("**%s** wrote:\n\n%s\n\n---\n\n", c.GetUser().GetLogin(), c.GetBody())
			if sb.Len()+len(entry) > maxContextSize {
				sb.WriteString("\n\n--- Context truncated due to size limit ---\n")
				h.log.Info("context truncated", "issue", issueNumber, "size", sb.Len())
				break
			}
			sb.WriteString(entry)
		}
	}

	return sb.String()
}
