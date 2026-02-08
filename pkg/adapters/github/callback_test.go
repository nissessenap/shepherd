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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/NissesSenap/shepherd/pkg/api"
)

func signedCallbackRequest(t *testing.T, secret string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader(body))
	req.Header.Set("X-Shepherd-Signature", sig)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCallbackHandler_SignatureVerification(t *testing.T) {
	secret := "callback-secret"
	handler := NewCallbackHandler(secret, nil, nil, ctrl.Log.WithName("test"))

	t.Run("valid signature", func(t *testing.T) {
		body := []byte(`{"taskID":"abc","event":"completed"}`)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		assert.True(t, handler.verifySignature(body, sig))
	})

	t.Run("invalid signature", func(t *testing.T) {
		body := []byte(`{"taskID":"abc","event":"completed"}`)
		assert.False(t, handler.verifySignature(body, "sha256=invalid"))
	})

	t.Run("missing prefix", func(t *testing.T) {
		body := []byte(`{"taskID":"abc","event":"completed"}`)
		assert.False(t, handler.verifySignature(body, "invalid"))
	})

	t.Run("empty secret allows all", func(t *testing.T) {
		h := NewCallbackHandler("", nil, nil, ctrl.Log.WithName("test"))
		assert.True(t, h.verifySignature([]byte(`{}`), ""))
	})
}

func TestCallbackHandler_ServeHTTP(t *testing.T) {
	secret := "callback-secret"

	t.Run("rejects GET requests", func(t *testing.T) {
		handler := NewCallbackHandler(secret, nil, nil, ctrl.Log.WithName("test"))

		req := httptest.NewRequest(http.MethodGet, "/callback", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("rejects invalid signature", func(t *testing.T) {
		handler := NewCallbackHandler(secret, nil, nil, ctrl.Log.WithName("test"))

		body := []byte(`{"taskID":"abc","event":"completed"}`)
		req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader(body))
		req.Header.Set("X-Shepherd-Signature", "sha256=invalid")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		handler := NewCallbackHandler("", nil, nil, ctrl.Log.WithName("test"))

		req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader([]byte(`not json`)))
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("accepts valid callback and posts completed comment", func(t *testing.T) {
		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler(secret, ghClient, nil, ctrl.Log.WithName("test"))

		// Register task metadata
		handler.RegisterTask("task-123", TaskMetadata{
			Owner:       "org",
			Repo:        "repo",
			IssueNumber: 42,
		})

		payload := api.CallbackPayload{
			TaskID:  "task-123",
			Event:   api.EventCompleted,
			Message: "Task completed",
			Details: map[string]any{"prURL": "https://github.com/org/repo/pull/99"},
		}
		req := signedCallbackRequest(t, secret, payload)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, postedComment, "https://github.com/org/repo/pull/99")
		assert.Contains(t, postedComment, "completed")

		// Task metadata should be cleaned up
		handler.mu.RLock()
		_, exists := handler.tasks["task-123"]
		handler.mu.RUnlock()
		assert.False(t, exists)
	})
}

func TestCallbackHandler_TaskMetadata(t *testing.T) {
	handler := NewCallbackHandler("", nil, nil, ctrl.Log.WithName("test"))

	handler.RegisterTask("task-123", TaskMetadata{
		Owner:       "test-org",
		Repo:        "test-repo",
		IssueNumber: 42,
	})

	handler.mu.RLock()
	meta, ok := handler.tasks["task-123"]
	handler.mu.RUnlock()

	assert.True(t, ok)
	assert.Equal(t, "test-org", meta.Owner)
	assert.Equal(t, "test-repo", meta.Repo)
	assert.Equal(t, 42, meta.IssueNumber)
}

func TestParseSourceURL(t *testing.T) {
	t.Run("valid issue URL", func(t *testing.T) {
		meta, err := parseSourceURL("https://github.com/myorg/myrepo/issues/42")
		require.NoError(t, err)
		assert.Equal(t, "myorg", meta.Owner)
		assert.Equal(t, "myrepo", meta.Repo)
		assert.Equal(t, 42, meta.IssueNumber)
	})

	t.Run("empty URL", func(t *testing.T) {
		_, err := parseSourceURL("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty sourceURL")
	})

	t.Run("non-issue URL", func(t *testing.T) {
		_, err := parseSourceURL("https://github.com/myorg/myrepo/pull/42")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected sourceURL format")
	})

	t.Run("invalid issue number", func(t *testing.T) {
		_, err := parseSourceURL("https://github.com/myorg/myrepo/issues/abc")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid issue number")
	})

	t.Run("too few path segments", func(t *testing.T) {
		_, err := parseSourceURL("https://github.com/myorg")
		require.Error(t, err)
	})
}

func TestCallbackHandler_HandleCallback(t *testing.T) {
	t.Run("completed event with PR URL posts completed comment", func(t *testing.T) {
		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler("", ghClient, nil, ctrl.Log.WithName("test"))

		handler.RegisterTask("task-1", TaskMetadata{
			Owner: "org", Repo: "repo", IssueNumber: 10,
		})

		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID:  "task-1",
			Event:   api.EventCompleted,
			Details: map[string]any{"prURL": "https://github.com/org/repo/pull/5"},
		})

		assert.Contains(t, postedComment, "https://github.com/org/repo/pull/5")
		assert.Contains(t, postedComment, "completed")
	})

	t.Run("completed event without PR URL posts generic success", func(t *testing.T) {
		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler("", ghClient, nil, ctrl.Log.WithName("test"))

		handler.RegisterTask("task-2", TaskMetadata{
			Owner: "org", Repo: "repo", IssueNumber: 10,
		})

		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID: "task-2",
			Event:  api.EventCompleted,
		})

		assert.Contains(t, postedComment, "completed the task successfully")
	})

	t.Run("failed event posts error comment", func(t *testing.T) {
		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler("", ghClient, nil, ctrl.Log.WithName("test"))

		handler.RegisterTask("task-3", TaskMetadata{
			Owner: "org", Repo: "repo", IssueNumber: 10,
		})

		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID:  "task-3",
			Event:   api.EventFailed,
			Message: "Build failed",
		})

		assert.Contains(t, postedComment, "Build failed")

		// Task metadata should be cleaned up
		handler.mu.RLock()
		_, exists := handler.tasks["task-3"]
		handler.mu.RUnlock()
		assert.False(t, exists)
	})

	t.Run("started event does not post comment", func(t *testing.T) {
		commentPosted := false
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				commentPosted = true
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler("", ghClient, nil, ctrl.Log.WithName("test"))

		handler.RegisterTask("task-4", TaskMetadata{
			Owner: "org", Repo: "repo", IssueNumber: 10,
		})

		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID: "task-4",
			Event:  api.EventStarted,
		})

		assert.False(t, commentPosted)

		// Task metadata should NOT be cleaned up for intermediate events
		handler.mu.RLock()
		_, exists := handler.tasks["task-4"]
		handler.mu.RUnlock()
		assert.True(t, exists)
	})

	t.Run("progress event does not post comment", func(t *testing.T) {
		commentPosted := false
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				commentPosted = true
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler("", ghClient, nil, ctrl.Log.WithName("test"))

		handler.RegisterTask("task-5", TaskMetadata{
			Owner: "org", Repo: "repo", IssueNumber: 10,
		})

		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID:  "task-5",
			Event:   api.EventProgress,
			Message: "50% done",
		})

		assert.False(t, commentPosted)
	})

	t.Run("API fallback resolves task metadata after restart", func(t *testing.T) {
		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		// API server returns task with sourceURL
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks/task-recovered", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"task-recovered",
				"status":{"phase":"Completed"},
				"task":{"sourceURL":"https://github.com/recovered-org/recovered-repo/issues/99"}
			}`))
		}))
		defer apiServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		apiClient := NewAPIClient(apiServer.URL)
		handler := NewCallbackHandler("", ghClient, apiClient, ctrl.Log.WithName("test"))

		// Don't register task - simulate restart
		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID:  "task-recovered",
			Event:   api.EventCompleted,
			Details: map[string]any{"prURL": "https://github.com/recovered-org/recovered-repo/pull/100"},
		})

		assert.Contains(t, postedComment, "https://github.com/recovered-org/recovered-repo/pull/100")
	})

	t.Run("failed error from details takes precedence", func(t *testing.T) {
		var postedComment string
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				postedComment = body["body"]
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}
		}))
		defer ghServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		handler := NewCallbackHandler("", ghClient, nil, ctrl.Log.WithName("test"))

		handler.RegisterTask("task-6", TaskMetadata{
			Owner: "org", Repo: "repo", IssueNumber: 10,
		})

		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID:  "task-6",
			Event:   api.EventFailed,
			Message: "generic error",
			Details: map[string]any{"error": "specific error from details"},
		})

		assert.Contains(t, postedComment, "specific error from details")
		assert.NotContains(t, postedComment, "generic error")
	})

	t.Run("API fallback with malformed sourceURL does not post comment", func(t *testing.T) {
		commentPosted := false
		ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				commentPosted = true
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ghServer.Close()

		// API server returns task with invalid sourceURL (PR instead of issue)
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks/task-bad-url", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"task-bad-url",
				"status":{"phase":"Completed"},
				"task":{"sourceURL":"https://github.com/org/repo/pull/42"}
			}`))
		}))
		defer apiServer.Close()

		ghClient := newTestClientFromServer(t, ghServer)
		apiClient := NewAPIClient(apiServer.URL)
		handler := NewCallbackHandler("", ghClient, apiClient, ctrl.Log.WithName("test"))

		// Don't register task - simulate restart scenario
		handler.handleCallback(context.Background(), &api.CallbackPayload{
			TaskID:  "task-bad-url",
			Event:   api.EventCompleted,
			Details: map[string]any{"prURL": "https://github.com/org/repo/pull/100"},
		})

		assert.False(t, commentPosted)
	})
}
