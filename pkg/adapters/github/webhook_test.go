// pkg/adapters/github/webhook_test.go
package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractTaskDescription(t *testing.T) {
	tests := []struct {
		name, body, expected string
	}{
		{"simple mention", "@shepherd fix the null pointer", "fix the null pointer"},
		{"mention in middle", "Hey team, @shepherd please fix this bug", "please fix this bug"},
		{"no mention", "This is a regular comment", ""},
		{"mention at end", "@shepherd", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if result := extractTaskDescription(tt.body); result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestVerifySignature(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)
	body := []byte(`{"test": "data"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name, signature string
		valid           bool
	}{
		{"valid signature", validSig, true},
		{"invalid signature", "sha256=invalid", false},
		{"missing prefix", hex.EncodeToString(mac.Sum(nil)), false},
		{"empty signature", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if result := handler.verifySignature(body, tt.signature); result != tt.valid {
				t.Errorf("expected %v, got %v", tt.valid, result)
			}
		})
	}
}

func TestHandleWebhook_IssueComment(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)

	event := IssueCommentEvent{
		Action: "created",
		Issue:  Issue{Number: 123, Title: "Bug report", Body: "Something is broken"},
	}
	event.Comment.Body = "@shepherd fix this issue"
	event.Repository.FullName = "org/repo"
	event.Repository.CloneURL = "https://github.com/org/repo.git"

	body, _ := json.Marshal(event)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issue_comment")

	w := httptest.NewRecorder()
	handler.HandleWebhook(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected status %d, got %d", http.StatusAccepted, w.Code)
	}
}

func TestHandleWebhook_InvalidSignature(t *testing.T) {
	handler := NewWebhookHandler("secret", nil)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	req.Header.Set("X-GitHub-Event", "issue_comment")

	w := httptest.NewRecorder()
	handler.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestHandleWebhook_NonCreatedAction(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)

	actions := []string{"edited", "deleted"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			event := IssueCommentEvent{
				Action: action,
				Issue:  Issue{Number: 123, Title: "Bug report", Body: "Something is broken"},
			}
			event.Comment.Body = "@shepherd fix this issue"
			event.Repository.FullName = "org/repo"
			event.Repository.CloneURL = "https://github.com/org/repo.git"

			body, _ := json.Marshal(event)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
			req.Header.Set("X-Hub-Signature-256", signature)
			req.Header.Set("X-GitHub-Event", "issue_comment")

			w := httptest.NewRecorder()
			handler.HandleWebhook(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
			}
		})
	}
}

func TestHandleWebhook_NoShepherdMention(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)

	event := IssueCommentEvent{
		Action: "created",
		Issue:  Issue{Number: 123, Title: "Bug report", Body: "Something is broken"},
	}
	event.Comment.Body = "Just a regular comment without the bot mention"
	event.Repository.FullName = "org/repo"
	event.Repository.CloneURL = "https://github.com/org/repo.git"

	body, _ := json.Marshal(event)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issue_comment")

	w := httptest.NewRecorder()
	handler.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestHandleWebhook_EmptyDescription(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)

	event := IssueCommentEvent{
		Action: "created",
		Issue:  Issue{Number: 123, Title: "Bug report", Body: "Something is broken"},
	}
	event.Comment.Body = "@shepherd"
	event.Repository.FullName = "org/repo"
	event.Repository.CloneURL = "https://github.com/org/repo.git"

	body, _ := json.Marshal(event)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issue_comment")

	w := httptest.NewRecorder()
	handler.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestHandleWebhook_UnknownEventType(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)

	eventTypes := []string{"push", "pull_request", "release"}
	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			body := []byte(`{"action": "created"}`)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
			req.Header.Set("X-Hub-Signature-256", signature)
			req.Header.Set("X-GitHub-Event", eventType)

			w := httptest.NewRecorder()
			handler.HandleWebhook(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
			}
		})
	}
}

func TestHandleWebhook_MalformedJSON(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil)

	body := []byte(`{"action": "created", "invalid json`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issue_comment")

	w := httptest.NewRecorder()
	handler.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}
