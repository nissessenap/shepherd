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

package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// testKey generates a throwaway RSA key for tests.
func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// mockGitHubTokenServer creates a test server that mimics the GitHub installation token endpoint.
func mockGitHubTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's the right endpoint pattern
		if !strings.Contains(r.URL.Path, "/app/installations/") || !strings.HasSuffix(r.URL.Path, "/access_tokens") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}

		// Verify auth header
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}

		// Check if repo scoping was requested
		var reqBody map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_test_token_123","expires_at":"2026-02-02T12:00:00Z"}`))
	}))
}

func newTokenTestHandler(t *testing.T, objs ...metav1.Object) *taskHandler {
	t.Helper()

	s := testScheme()
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{})
	for _, obj := range objs {
		builder = builder.WithObjects(obj.(*toolkitv1alpha1.AgentTask))
	}
	c := builder.Build()

	ghServer := mockGitHubTokenServer(t)
	t.Cleanup(ghServer.Close)

	return &taskHandler{
		client:          c,
		namespace:       "default",
		callback:        newCallbackSender(""),
		githubAppID:     12345,
		githubInstallID: 67890,
		githubAPIURL:    ghServer.URL,
		githubKey:       testKey(t),
		httpClient:      ghServer.Client(),
	}
}

func TestGetTaskToken_ReturnsToken(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-token-1",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	h := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-token-1/token")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ghs_test_token_123", resp.Token)
	assert.Equal(t, "2026-02-02T12:00:00Z", resp.ExpiresAt)
}

func TestGetTaskToken_NotFound(t *testing.T) {
	h := newTokenTestHandler(t)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/nonexistent/token")

	assert.Equal(t, http.StatusNotFound, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task not found", errResp.Error)
}

func TestGetTaskToken_TerminalTaskRejected(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-done",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			Conditions: []metav1.Condition{
				{
					Type:   toolkitv1alpha1.ConditionSucceeded,
					Status: metav1.ConditionTrue,
					Reason: toolkitv1alpha1.ReasonSucceeded,
				},
			},
		},
	}

	h := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-done/token")

	assert.Equal(t, http.StatusGone, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task is terminal", errResp.Error)
}

func TestGetTaskToken_NoGitHubConfig(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-nogh",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	// Handler without GitHub config
	h := newTestHandler(task)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-nogh/token")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "GitHub App not configured", errResp.Error)
}

func TestGetTaskToken_GitHubAPIError(t *testing.T) {
	// Mock server that returns an error
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"internal error"}`))
	}))
	defer failServer.Close()

	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-ghfail",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).WithObjects(task).Build()

	h := &taskHandler{
		client:          c,
		namespace:       "default",
		callback:        newCallbackSender(""),
		githubAppID:     12345,
		githubInstallID: 67890,
		githubAPIURL:    failServer.URL,
		githubKey:       testKey(t),
		httpClient:      failServer.Client(),
	}
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-ghfail/token")

	assert.Equal(t, http.StatusBadGateway, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to generate GitHub token", errResp.Error)
}

func TestGetTaskToken_ScopesToRepo(t *testing.T) {
	var receivedBody map[string]any

	scopeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_scoped","expires_at":"2026-02-02T12:00:00Z"}`))
	}))
	defer scopeServer.Close()

	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-scope",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/myorg/myrepo.git"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).WithObjects(task).Build()

	h := &taskHandler{
		client:          c,
		namespace:       "default",
		callback:        newCallbackSender(""),
		githubAppID:     12345,
		githubInstallID: 67890,
		githubAPIURL:    scopeServer.URL,
		githubKey:       testKey(t),
		httpClient:      scopeServer.Client(),
	}
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-scope/token")

	require.Equal(t, http.StatusOK, w.Code)

	// Verify the request scoped to the repo (stripped .git suffix)
	repos, ok := receivedBody["repositories"].([]any)
	require.True(t, ok, "expected repositories array in request body")
	require.Len(t, repos, 1)
	assert.Equal(t, "myrepo", fmt.Sprintf("%v", repos[0]))
}
