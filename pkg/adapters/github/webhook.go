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

	"github.com/go-logr/logr"
	gh "github.com/google/go-github/v75/github"
)

// shepherdMentionRegex matches @shepherd mentions but not email-style patterns
// (e.g., user@shepherd.io). Requires start-of-string or whitespace before the @.
var shepherdMentionRegex = regexp.MustCompile(`(?i)(?:^|\s)@shepherd\b`)

// WebhookHandler handles incoming GitHub webhooks.
type WebhookHandler struct {
	secret   string
	ghClient *Client
	log      logr.Logger
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(secret string, ghClient *Client, log logr.Logger) *WebhookHandler {
	return &WebhookHandler{
		secret:   secret,
		ghClient: ghClient,
		log:      log,
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

// processTask is a placeholder for the task creation workflow (Phase 4).
func (h *WebhookHandler) processTask(_ context.Context, event *gh.IssueCommentEvent, description string) {
	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()
	issueNumber := event.GetIssue().GetNumber()
	repoFullName := event.GetRepo().GetFullName()

	repoLabel := strings.ReplaceAll(repoFullName, "/", "-")
	issueLabel := fmt.Sprintf("%d", issueNumber)

	h.log.Info("would process task",
		"owner", owner,
		"repo", repo,
		"issue", issueNumber,
		"repoLabel", repoLabel,
		"issueLabel", issueLabel,
		"description", description,
	)
}
