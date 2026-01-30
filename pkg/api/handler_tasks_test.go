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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = toolkitv1alpha1.AddToScheme(s)
	return s
}

// TODO(Phase 3): Add variadic ...client.Object param to pre-seed fake client with tasks for list/get tests.
func newTestHandler() *taskHandler {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).Build()
	return &taskHandler{
		client:    c,
		namespace: "default",
	}
}

func testRouter(h *taskHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", h.createTask)
	})
	return r
}

func validCreateRequest() CreateTaskRequest {
	return CreateTaskRequest{
		Repo: RepoRequest{URL: "https://github.com/test-org/test-repo"},
		Task: TaskRequest{
			Description: "Fix the login bug",
			Context:     "Issue #42: login page throws NPE on empty password",
		},
		Callback: "https://example.com/callback",
	}
}

// TODO(Phase 3/4): Generalize to postJSON(t, router, path, body) when multiple endpoints need testing.
func postCreateTask(t *testing.T, router http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestCreateTask_Valid(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := postCreateTask(t, router, validCreateRequest())

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, strings.HasPrefix(resp.ID, "task-"), "task ID should start with 'task-'")
	assert.Equal(t, "default", resp.Namespace)
	assert.Equal(t, "https://github.com/test-org/test-repo", resp.Repo.URL)
	assert.Equal(t, "Fix the login bug", resp.Task.Description)
	assert.Equal(t, "https://example.com/callback", resp.CallbackURL)
	assert.Equal(t, "Pending", resp.Status.Phase)
}

func TestCreateTask_ContextIsCompressed(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := postCreateTask(t, router, validCreateRequest())
	require.Equal(t, http.StatusCreated, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Fetch the created CRD to verify compression
	var task toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{
		Namespace: "default",
		Name:      resp.ID,
	}, &task)
	require.NoError(t, err)
	assert.Equal(t, "gzip", task.Spec.Task.ContextEncoding)
	assert.NotEqual(t, "Issue #42: login page throws NPE on empty password", task.Spec.Task.Context,
		"context should be compressed, not stored as plaintext")
}

func TestCreateTask_MissingRepoURL(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Repo.URL = ""
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "repo.url is required", errResp.Error)
}

func TestCreateTask_MissingDescription(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Task.Description = ""
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task.description is required", errResp.Error)
}

func TestCreateTask_MissingContext(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Task.Context = ""
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task.context is required", errResp.Error)
}

func TestCreateTask_MissingCallbackURL(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Callback = ""
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "callbackUrl is required", errResp.Error)
}

func TestCreateTask_InvalidBody(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "invalid request body", errResp.Error)
}

func TestCreateTask_RunnerTimeout(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Runner = &RunnerConfig{Timeout: "30m"}
	w := postCreateTask(t, router, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Verify the CRD has the timeout set
	var task toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{
		Namespace: "default",
		Name:      resp.ID,
	}, &task)
	require.NoError(t, err)
	assert.Equal(t, "30m0s", task.Spec.Runner.Timeout.Duration.String())
}

func TestCreateTask_InvalidRunnerTimeout(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Runner = &RunnerConfig{Timeout: "invalid"}
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "invalid runner.timeout", errResp.Error)
}

func TestCreateTask_WithLabels(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Labels = map[string]string{
		"shepherd.io/repo":  "test-org-test-repo",
		"shepherd.io/issue": "42",
	}
	w := postCreateTask(t, router, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Verify labels on the CRD
	var task toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{
		Namespace: "default",
		Name:      resp.ID,
	}, &task)
	require.NoError(t, err)
	assert.Equal(t, "test-org-test-repo", task.Labels["shepherd.io/repo"])
	assert.Equal(t, "42", task.Labels["shepherd.io/issue"])
}

func TestCreateTask_TaskNameHasRandomSuffix(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	// Create two tasks and verify they get different names
	w1 := postCreateTask(t, router, validCreateRequest())
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := postCreateTask(t, router, validCreateRequest())
	require.Equal(t, http.StatusCreated, w2.Code)

	var resp1, resp2 TaskResponse
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))

	assert.True(t, strings.HasPrefix(resp1.ID, "task-"))
	assert.True(t, strings.HasPrefix(resp2.ID, "task-"))
	assert.NotEqual(t, resp1.ID, resp2.ID, "task names should be unique")
}

func TestCreateTask_ResponseStatus(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := postCreateTask(t, router, validCreateRequest())
	require.Equal(t, http.StatusCreated, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Pending", resp.Status.Phase)
	assert.Empty(t, resp.Status.Message)
	assert.Empty(t, resp.Status.JobName)
}

func TestCreateTask_BodyTooLarge(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	// Create a body over 10 MiB
	largeBody := strings.Repeat("x", 11<<20) // 11 MiB
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestExtractStatus_NoConditions(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{}
	status := extractStatus(task)
	assert.Equal(t, "Pending", status.Phase)
	assert.Empty(t, status.Message)
}

func TestExtractStatus_WithCondition(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		Status: toolkitv1alpha1.AgentTaskStatus{
			Conditions: []metav1.Condition{
				{
					Type:    toolkitv1alpha1.ConditionSucceeded,
					Status:  metav1.ConditionUnknown,
					Reason:  toolkitv1alpha1.ReasonRunning,
					Message: "Job is running",
				},
			},
			JobName: "task-abc-1-job",
		},
	}
	status := extractStatus(task)
	assert.Equal(t, "Running", status.Phase)
	assert.Equal(t, "Job is running", status.Message)
	assert.Equal(t, "task-abc-1-job", status.JobName)
}

func TestExtractStatus_Terminal(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		Status: toolkitv1alpha1.AgentTaskStatus{
			Conditions: []metav1.Condition{
				{
					Type:    toolkitv1alpha1.ConditionSucceeded,
					Status:  metav1.ConditionTrue,
					Reason:  toolkitv1alpha1.ReasonSucceeded,
					Message: "Task completed successfully",
				},
			},
			Result: toolkitv1alpha1.TaskResult{
				PRUrl: "https://github.com/org/repo/pull/1",
			},
		},
	}
	status := extractStatus(task)
	assert.Equal(t, "Succeeded", status.Phase)
	assert.Equal(t, "Task completed successfully", status.Message)
	assert.Equal(t, "https://github.com/org/repo/pull/1", status.PRUrl)
}

func TestTaskToResponse_CompletionTime(t *testing.T) {
	now := metav1.Now()
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-abc",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/test/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "test", Context: "ctx"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			CompletionTime: &now,
		},
	}

	resp := taskToResponse(task)
	assert.NotNil(t, resp.CompletionTime)
}

func TestTaskToResponse_NoCompletionTime(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-abc",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/test/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "test", Context: "ctx"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	resp := taskToResponse(task)
	assert.Nil(t, resp.CompletionTime)
}
