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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v75/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a Client backed by a test HTTP server.
func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	ghClient := gh.NewClient(nil)
	ghClient, _ = ghClient.WithEnterpriseURLs(srv.URL+"/", srv.URL+"/upload/")
	return newClientFromGH(ghClient), srv
}

func TestClient_PostComment(t *testing.T) {
	var receivedBody map[string]string
	var receivedPath string

	client, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 1, "body": "test"}`))
	}))
	defer srv.Close()

	err := client.PostComment(context.Background(), "myorg", "myrepo", 42, "Hello from Shepherd")
	require.NoError(t, err)
	assert.Equal(t, "/api/v3/repos/myorg/myrepo/issues/42/comments", receivedPath)
	assert.Equal(t, "Hello from Shepherd", receivedBody["body"])
}

func TestClient_GetIssue(t *testing.T) {
	client, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/repos/myorg/myrepo/issues/7", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number": 7, "title": "Test issue", "body": "Issue body"}`))
	}))
	defer srv.Close()

	issue, err := client.GetIssue(context.Background(), "myorg", "myrepo", 7)
	require.NoError(t, err)
	assert.Equal(t, 7, issue.GetNumber())
	assert.Equal(t, "Test issue", issue.GetTitle())
}

func TestClient_ListIssueComments(t *testing.T) {
	t.Run("single page", func(t *testing.T) {
		client, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v3/repos/myorg/myrepo/issues/5/comments", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id": 1, "body": "comment 1"}, {"id": 2, "body": "comment 2"}]`))
		}))
		defer srv.Close()

		comments, err := client.ListIssueComments(context.Background(), "myorg", "myrepo", 5)
		require.NoError(t, err)
		assert.Len(t, comments, 2)
		assert.Equal(t, "comment 1", comments[0].GetBody())
		assert.Equal(t, "comment 2", comments[1].GetBody())
	})
}

func TestCommentTemplates(t *testing.T) {
	t.Run("acknowledge", func(t *testing.T) {
		result := formatAcknowledge("task-abc123")
		assert.Contains(t, result, "task-abc123")
		assert.Contains(t, result, "working on your request")
	})

	t.Run("already running", func(t *testing.T) {
		result := formatAlreadyRunning("task-xyz", "Running")
		assert.Contains(t, result, "task-xyz")
		assert.Contains(t, result, "Running")
		assert.Contains(t, result, "already running")
	})

	t.Run("completed", func(t *testing.T) {
		result := formatCompleted("https://github.com/org/repo/pull/42")
		assert.Contains(t, result, "https://github.com/org/repo/pull/42")
		assert.Contains(t, result, "completed")
	})

	t.Run("failed with message", func(t *testing.T) {
		result := formatFailed("Build failed")
		assert.Contains(t, result, "Build failed")
	})

	t.Run("failed empty message", func(t *testing.T) {
		result := formatFailed("")
		assert.Contains(t, result, "Unknown error")
	})
}
