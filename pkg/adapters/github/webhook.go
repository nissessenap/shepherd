// pkg/adapters/github/webhook.go
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WebhookHandler handles GitHub webhooks
type WebhookHandler struct {
	secret    string
	apiClient APIClient
}

// APIClient interface for creating tasks via the API
type APIClient interface {
	CreateTask(repoURL, description, context, callbackURL string) (string, error)
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(secret string, apiClient APIClient) *WebhookHandler {
	return &WebhookHandler{secret: secret, apiClient: apiClient}
}

// IssueCommentEvent represents a GitHub issue comment event
type IssueCommentEvent struct {
	Action  string `json:"action"`
	Issue   Issue  `json:"issue"`
	Comment struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// Issue represents a GitHub issue
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// HandleWebhook handles incoming GitHub webhooks
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if !h.verifySignature(body, signature) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	switch eventType {
	case "issue_comment":
		h.handleIssueComment(w, body)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (h *WebhookHandler) handleIssueComment(w http.ResponseWriter, body []byte) {
	var event IssueCommentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if event.Action != "created" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !strings.Contains(event.Comment.Body, "@shepherd") {
		w.WriteHeader(http.StatusOK)
		return
	}

	description := extractTaskDescription(event.Comment.Body)
	if description == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	context := fmt.Sprintf("Issue #%d: %s\n\n%s", event.Issue.Number, event.Issue.Title, event.Issue.Body)
	callbackURL := fmt.Sprintf("/callback/%s/%d", event.Repository.FullName, event.Issue.Number)

	if h.apiClient != nil {
		if _, err := h.apiClient.CreateTask(event.Repository.CloneURL, description, context, callbackURL); err != nil {
			http.Error(w, "failed to create task", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
	if h.secret == "" {
		return true
	}
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func extractTaskDescription(body string) string {
	idx := strings.Index(body, "@shepherd")
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(body[idx+len("@shepherd"):])
}
