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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gh "github.com/google/go-github/v75/github"
	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	testAPITasksPath   = "/api/v1/tasks"
	testGHCommentsPath = "/api/v3/repos/org/repo/issues/42/comments"
)

func signedRequest(t *testing.T, secret string, body []byte, event string) *http.Request {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", event)
	return req
}

func TestWebhookHandler_SignatureVerification(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil, nil, nil, "", "default", ctrl.Log.WithName("test"))

	t.Run("valid signature", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		assert.True(t, handler.verifySignature(body, sig))
	})

	t.Run("invalid signature", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		assert.False(t, handler.verifySignature(body, "sha256=invalid"))
	})

	t.Run("missing prefix", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		assert.False(t, handler.verifySignature(body, "invalid"))
	})

	t.Run("empty secret allows all", func(t *testing.T) {
		h := NewWebhookHandler("", nil, nil, nil, "", "default", ctrl.Log.WithName("test"))
		assert.True(t, h.verifySignature([]byte(`{}`), ""))
	})
}

func TestWebhookHandler_ServeHTTP(t *testing.T) {
	secret := "test-secret"
	handler := NewWebhookHandler(secret, nil, nil, nil, "", "default", ctrl.Log.WithName("test"))

	t.Run("rejects GET requests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("rejects invalid signature", func(t *testing.T) {
		body := []byte(`{"action":"created"}`)
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
		req.Header.Set("X-GitHub-Event", "ping")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("accepts valid ping", func(t *testing.T) {
		body := []byte(`{"zen":"test"}`)
		req := signedRequest(t, secret, body, "ping")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestShepherdMentionRegex(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"@shepherd fix this bug", true},
		{"@SHEPHERD fix this bug", true},
		{"@Shepherd fix this bug", true},
		{"Hey @shepherd can you help?", true},
		{"@shepherd", true},
		{"\n@shepherd fix it", true},
		{"@shepherding", false},
		{"no mention here", false},
		{"email@shepherd.io", false},
		{"user@shepherd", false},
		{"test@shepherd.com stuff", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.match, shepherdMentionRegex.MatchString(tc.input))
		})
	}
}

func TestWebhookHandler_BuildContext(t *testing.T) {
	t.Run("includes issue body and comments", func(t *testing.T) {
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v3/repos/testorg/testrepo/issues/42/comments", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"user":{"login":"alice"},"body":"First comment"},
				{"user":{"login":"bob"},"body":"Second comment"}
			]`))
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewWebhookHandler("secret", ghClient, nil, nil, "", "default", ctrl.Log.WithName("test"))

		result := handler.buildContext(context.Background(), "testorg", "testrepo", 42, "Issue body text")

		assert.Contains(t, result, "## Issue Description")
		assert.Contains(t, result, "Issue body text")
		assert.Contains(t, result, "## Comments")
		assert.Contains(t, result, "**alice** wrote:")
		assert.Contains(t, result, "First comment")
		assert.Contains(t, result, "**bob** wrote:")
		assert.Contains(t, result, "Second comment")
	})

	t.Run("truncates at maxContextSize", func(t *testing.T) {
		// Create many large comments that will exceed maxContextSize
		var comments []string
		for i := range 10 {
			largeBody := make([]byte, 200000)
			for j := range largeBody {
				largeBody[j] = 'a'
			}
			comments = append(comments, fmt.Sprintf(`{"user":{"login":"user%d"},"body":"%s"}`, i, string(largeBody)))
		}
		var commentsJSON strings.Builder
		commentsJSON.WriteString("[" + comments[0])
		for i := 1; i < len(comments); i++ {
			commentsJSON.WriteString("," + comments[i])
		}
		commentsJSON.WriteString("]")

		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(commentsJSON.String()))
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewWebhookHandler("secret", ghClient, nil, nil, "", "default", ctrl.Log.WithName("test"))

		result := handler.buildContext(context.Background(), "testorg", "testrepo", 1, "Short issue body")

		assert.Contains(t, result, "truncated due to size limit")
		assert.LessOrEqual(t, len(result), maxContextSize+500)
	})

	t.Run("handles comment fetch error gracefully", func(t *testing.T) {
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewWebhookHandler("secret", ghClient, nil, nil, "", "default", ctrl.Log.WithName("test"))

		result := handler.buildContext(context.Background(), "testorg", "testrepo", 1, "Issue body")

		assert.Contains(t, result, "## Issue Description")
		assert.Contains(t, result, "Issue body")
		assert.NotContains(t, result, "## Comments")
	})
}

func TestWebhookHandler_ProcessTask(t *testing.T) {
	t.Run("deduplication - posts already running comment when active task exists", func(t *testing.T) {
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == testAPITasksPath && r.URL.Query().Get("active") == "true" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"id":"existing-task","status":{"phase":"Running"}}]`))
			}
		}))
		defer apiServer.Close()

		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == testGHCommentsPath {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		apiClient := NewAPIClient(apiServer.URL)
		callbackHandler := NewCallbackHandler("secret", ghClient, apiClient, ctrl.Log.WithName("test"))
		handler := NewWebhookHandler(
			"secret",
			ghClient,
			apiClient,
			callbackHandler,
			"http://callback",
			"default",
			ctrl.Log.WithName("test"),
		)

		event := createTestIssueCommentEvent("org", "repo", 42, "@shepherd fix this")
		handler.processTask(context.Background(), event, "fix this")

		assert.Contains(t, postedComment, "existing-task")
		assert.Contains(t, postedComment, "already running")
	})

	t.Run("happy path - creates task and posts acknowledgment", func(t *testing.T) {
		var createdTask map[string]any
		var postedComment string

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == testAPITasksPath {
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`[]`))
				case http.MethodPost:
					_ = json.NewDecoder(r.Body).Decode(&createdTask)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"new-task-123","status":{"phase":"Pending"}}`))
				}
			}
		}))
		defer apiServer.Close()

		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == testGHCommentsPath {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			} else if r.Method == http.MethodGet && r.URL.Path == testGHCommentsPath {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		apiClient := NewAPIClient(apiServer.URL)
		callbackHandler := NewCallbackHandler("secret", ghClient, apiClient, ctrl.Log.WithName("test"))
		handler := NewWebhookHandler(
			"secret",
			ghClient,
			apiClient,
			callbackHandler,
			"http://callback",
			"custom-template",
			ctrl.Log.WithName("test"),
		)

		event := createTestIssueCommentEvent("org", "repo", 42, "@shepherd fix this bug")
		handler.processTask(context.Background(), event, "fix this bug")

		assert.Contains(t, postedComment, "new-task-123")
		assert.Contains(t, postedComment, "working on your request")
		assert.NotNil(t, createdTask)
		taskMap := createdTask["task"].(map[string]any)
		assert.Equal(t, "fix this bug", taskMap["description"])
		runnerMap := createdTask["runner"].(map[string]any)
		assert.Equal(t, "custom-template", runnerMap["sandboxTemplateName"])
	})

	t.Run("API failure - posts error comment", func(t *testing.T) {
		var postedComment string

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == testAPITasksPath {
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`[]`))
				case http.MethodPost:
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"repo.url is required"}`))
				}
			}
		}))
		defer apiServer.Close()

		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == testGHCommentsPath {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			} else if r.Method == http.MethodGet && r.URL.Path == testGHCommentsPath {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		apiClient := NewAPIClient(apiServer.URL)
		callbackHandler := NewCallbackHandler("secret", ghClient, apiClient, ctrl.Log.WithName("test"))
		handler := NewWebhookHandler(
			"secret",
			ghClient,
			apiClient,
			callbackHandler,
			"http://callback",
			"default",
			ctrl.Log.WithName("test"),
		)

		event := createTestIssueCommentEvent("org", "repo", 42, "@shepherd fix this")
		handler.processTask(context.Background(), event, "fix this")

		// Should show generic error message, not internal API error details (security fix)
		assert.Contains(t, postedComment, "unable to complete")
		assert.Contains(t, postedComment, "Failed to create task")
		assert.NotContains(t, postedComment, "repo.url")
	})
}

// Helper to create a test GitHub client from an httptest server
func newTestClientFromServer(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	ghClient := gh.NewClient(nil)
	ghClient, _ = ghClient.WithEnterpriseURLs(srv.URL+"/", srv.URL+"/upload/")
	return newClientFromGH(ghClient)
}

// Helper to create a test IssueCommentEvent
func createTestIssueCommentEvent(owner, repo string, issueNum int, commentBody string) *gh.IssueCommentEvent {
	return &gh.IssueCommentEvent{
		Action: gh.Ptr("created"),
		Repo: &gh.Repository{
			Owner:    &gh.User{Login: gh.Ptr(owner)},
			Name:     gh.Ptr(repo),
			FullName: gh.Ptr(owner + "/" + repo),
			CloneURL: gh.Ptr("https://github.com/" + owner + "/" + repo + ".git"),
		},
		Issue: &gh.Issue{
			Number:  gh.Ptr(issueNum),
			HTMLURL: gh.Ptr("https://github.com/" + owner + "/" + repo + "/issues/" + fmt.Sprintf("%d", issueNum)),
			Body:    gh.Ptr("Issue body"),
		},
		Comment: &gh.IssueComment{
			Body: gh.Ptr(commentBody),
			User: &gh.User{Login: gh.Ptr("testuser")},
		},
	}
}
