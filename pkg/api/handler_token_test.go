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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// mockTokenProvider implements TokenProvider for tests.
type mockTokenProvider struct {
	token     string
	expiresAt time.Time
	err       error
	lastRepo  string // captures the repoURL passed to GetToken
}

func (m *mockTokenProvider) GetToken(_ context.Context, repoURL string) (string, time.Time, error) {
	m.lastRepo = repoURL
	return m.token, m.expiresAt, m.err
}

func newTokenTestHandler(t *testing.T, objs ...metav1.Object) (*taskHandler, *mockTokenProvider) {
	t.Helper()

	s := testScheme()
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{})
	for _, obj := range objs {
		builder = builder.WithObjects(obj.(*toolkitv1alpha1.AgentTask))
	}
	c := builder.Build()

	mock := &mockTokenProvider{
		token:     "ghs_test_token_123",
		expiresAt: time.Date(2026, 2, 2, 12, 0, 0, 0, time.UTC),
	}

	return &taskHandler{
		client:       c,
		namespace:    "default",
		callback:     newCallbackSender(""),
		githubClient: mock,
	}, mock
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

	h, _ := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-token-1/token")

	assert.Equal(t, http.StatusOK, w.Code)

	// Contract validation
	doc := loadSpec(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-token-1/token", nil)
	validateResponse(t, doc, req, w)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ghs_test_token_123", resp.Token)
	assert.Equal(t, "2026-02-02T12:00:00Z", resp.ExpiresAt)
}

func TestGetTaskToken_NotFound(t *testing.T) {
	h, _ := newTokenTestHandler(t)
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

	h, _ := newTokenTestHandler(t, task)
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
		client:    c,
		namespace: "default",
		callback:  newCallbackSender(""),
		githubClient: &mockTokenProvider{
			err: fmt.Errorf("GitHub API returned 500"),
		},
	}
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-ghfail/token")

	assert.Equal(t, http.StatusBadGateway, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to generate GitHub token", errResp.Error)
}

func TestGetTaskToken_SetsTokenIssued(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-issued-1",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	h, _ := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-issued-1/token")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify TokenIssued was set
	var updatedTask toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-issued-1"}, &updatedTask)
	require.NoError(t, err)
	assert.True(t, updatedTask.Status.TokenIssued, "TokenIssued should be true after token fetch")
}

func TestGetTaskToken_RejectsSecondFetch(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-issued-2",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			TokenIssued: true, // Already issued
		},
	}

	h, _ := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-issued-2/token")

	assert.Equal(t, http.StatusConflict, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "token already issued for this execution", errResp.Error)
}

func TestGetTaskToken_ScopesToRepo(t *testing.T) {
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

	h, mock := newTokenTestHandler(t, task)
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-scope/token")

	require.Equal(t, http.StatusOK, w.Code)

	// Verify the repo URL was passed to the mock
	assert.Equal(t, "https://github.com/myorg/myrepo.git", mock.lastRepo)
}

func TestGetTaskToken_RetriesOnConflict(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-retry",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	s := testScheme()

	// Track number of Status().Update() attempts
	updateAttempts := 0

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, cli client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				updateAttempts++
				// Fail first attempt with conflict error, succeed on second
				if updateAttempts == 1 {
					return errors.NewConflict(
						toolkitv1alpha1.GroupVersion.WithResource("agenttasks").GroupResource(),
						obj.GetName(),
						fmt.Errorf("resource version mismatch"),
					)
				}
				// Succeed on second attempt by calling the real update
				return cli.Status().Update(ctx, obj, opts...)
			},
		}).
		Build()

	mock := &mockTokenProvider{
		token:     "ghs_test_token_123",
		expiresAt: time.Date(2026, 2, 2, 12, 0, 0, 0, time.UTC),
	}

	h := &taskHandler{
		client:       c,
		namespace:    "default",
		callback:     newCallbackSender(""),
		githubClient: mock,
	}
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-retry/token")

	// Should succeed after retry
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 2, updateAttempts, "should have attempted update twice (1 conflict + 1 success)")

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ghs_test_token_123", resp.Token)
}

func TestGetTaskToken_ExhaustsRetriesOnPersistentConflict(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-exhausted",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	s := testScheme()

	// Track number of Status().Update() attempts
	updateAttempts := 0

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, obj client.Object, _ ...client.SubResourceUpdateOption) error {
				updateAttempts++
				// Always return conflict error to exhaust retries
				return errors.NewConflict(
					toolkitv1alpha1.GroupVersion.WithResource("agenttasks").GroupResource(),
					obj.GetName(),
					fmt.Errorf("resource version mismatch"),
				)
			},
		}).
		Build()

	mock := &mockTokenProvider{
		token:     "ghs_test_token_123",
		expiresAt: time.Date(2026, 2, 2, 12, 0, 0, 0, time.UTC),
	}

	h := &taskHandler{
		client:       c,
		namespace:    "default",
		callback:     newCallbackSender(""),
		githubClient: mock,
	}
	r := chi.NewRouter()
	r.Get("/api/v1/tasks/{taskID}/token", h.getTaskToken)

	w := doGet(t, r, "/api/v1/tasks/task-exhausted/token")

	// Should return 409 after exhausting all retries
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 3, updateAttempts, "should have attempted update 3 times (maxRetries)")

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "concurrent update conflict", errResp.Error)
}
