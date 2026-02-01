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

	"fmt"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = toolkitv1alpha1.AddToScheme(s)
	return s
}

func newTestHandler(objs ...client.Object) *taskHandler {
	s := testScheme()
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	c := builder.Build()
	return &taskHandler{
		client:    c,
		namespace: "default",
		callback:  newCallbackSender(""),
	}
}

func testRouter(h *taskHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", h.createTask)
		r.Get("/tasks", h.listTasks)
		r.Get("/tasks/{taskID}", h.getTask)
		r.Post("/tasks/{taskID}/status", h.updateTaskStatus)
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

func postJSON(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func doGet(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func postCreateTask(t *testing.T, router http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(t, router, "/api/v1/tasks", body)
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

func TestCreateTask_HTTPRepoURL(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Repo.URL = "http://github.com/test-org/test-repo"
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "repo.url must start with https://", errResp.Error)
	assert.Equal(t, "CRD schema requires HTTPS URLs", errResp.Details)
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

func TestCreateTask_EmptyContextIsAllowed(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := validCreateRequest()
	req.Task.Context = ""
	w := postCreateTask(t, router, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Verify the CRD has no context
	var task toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{
		Namespace: "default",
		Name:      resp.ID,
	}, &task)
	require.NoError(t, err)
	assert.Empty(t, task.Spec.Task.Context)
	assert.Empty(t, task.Spec.Task.ContextEncoding)
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
	assert.Empty(t, resp.Status.SandboxClaimName)
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

func TestCreateTask_K8sClientError(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
				return fmt.Errorf("API server connection refused")
			},
		}).
		Build()

	h := &taskHandler{
		client:    c,
		namespace: "default",
		callback:  newCallbackSender(""),
	}
	router := testRouter(h)

	w := postCreateTask(t, router, validCreateRequest())

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to create task", errResp.Error)
	// Internal K8s error details should not be leaked in 5xx responses
	assert.Empty(t, errResp.Details)
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
			SandboxClaimName: "task-abc-1-job",
		},
	}
	status := extractStatus(task)
	assert.Equal(t, "Running", status.Phase)
	assert.Equal(t, "Job is running", status.Message)
	assert.Equal(t, "task-abc-1-job", status.SandboxClaimName)
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

// --- Phase 3: List and Get tests ---

func newTask(name string, labels map[string]string, conditions []metav1.Condition) *toolkitv1alpha1.AgentTask {
	return &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    labels,
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/test/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "test", Context: "ctx"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			Conditions: conditions,
		},
	}
}

func TestListTasks_EmptyReturnsEmptyArray(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Empty(t, tasks)
	// Verify it's [] not null
	assert.Equal(t, "[]", strings.TrimSpace(w.Body.String()))
}

func TestListTasks_ReturnsAllTasks(t *testing.T) {
	task1 := newTask("task-aaa", nil, nil)
	task2 := newTask("task-bbb", nil, nil)

	h := newTestHandler(task1, task2)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks")

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 2)

	ids := []string{tasks[0].ID, tasks[1].ID}
	assert.Contains(t, ids, "task-aaa")
	assert.Contains(t, ids, "task-bbb")
}

func TestListTasks_FilterByRepoLabel(t *testing.T) {
	task1 := newTask("task-aaa", map[string]string{"shepherd.io/repo": "org-repo1"}, nil)
	task2 := newTask("task-bbb", map[string]string{"shepherd.io/repo": "org-repo2"}, nil)

	h := newTestHandler(task1, task2)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?repo=org-repo1")

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 1)
	assert.Equal(t, "task-aaa", tasks[0].ID)
}

func TestListTasks_FilterByIssueLabel(t *testing.T) {
	task1 := newTask("task-aaa", map[string]string{"shepherd.io/issue": "42"}, nil)
	task2 := newTask("task-bbb", map[string]string{"shepherd.io/issue": "99"}, nil)

	h := newTestHandler(task1, task2)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?issue=42")

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 1)
	assert.Equal(t, "task-aaa", tasks[0].ID)
}

func TestListTasks_ActiveFilterExcludesTerminal(t *testing.T) {
	activeTask := newTask("task-active", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})
	succeededTask := newTask("task-done", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	})
	failedTask := newTask("task-fail", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionFalse,
			Reason: toolkitv1alpha1.ReasonFailed,
		},
	})
	pendingTask := newTask("task-pending", nil, nil) // no conditions = pending

	h := newTestHandler(activeTask, succeededTask, failedTask, pendingTask)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?active=true")

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 2, "should include active (Running) and pending tasks")

	ids := []string{tasks[0].ID, tasks[1].ID}
	assert.Contains(t, ids, "task-active")
	assert.Contains(t, ids, "task-pending")
}

func TestListTasks_CombinedFilters(t *testing.T) {
	// active + repo + issue
	matchActive := newTask("task-match", map[string]string{
		"shepherd.io/repo":  "org-repo",
		"shepherd.io/issue": "42",
	}, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})
	matchTerminal := newTask("task-terminal", map[string]string{
		"shepherd.io/repo":  "org-repo",
		"shepherd.io/issue": "42",
	}, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	})
	differentRepo := newTask("task-other", map[string]string{
		"shepherd.io/repo":  "other-repo",
		"shepherd.io/issue": "42",
	}, nil)

	h := newTestHandler(matchActive, matchTerminal, differentRepo)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?repo=org-repo&issue=42&active=true")

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 1)
	assert.Equal(t, "task-match", tasks[0].ID)
}

func TestGetTask_ReturnsTaskDetails(t *testing.T) {
	task := newTask("task-detail", nil, []metav1.Condition{
		{
			Type:    toolkitv1alpha1.ConditionSucceeded,
			Status:  metav1.ConditionUnknown,
			Reason:  toolkitv1alpha1.ReasonRunning,
			Message: "Job is running",
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-detail")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "task-detail", resp.ID)
	assert.Equal(t, "default", resp.Namespace)
	assert.Equal(t, "https://github.com/test/repo", resp.Repo.URL)
	assert.Equal(t, "Running", resp.Status.Phase)
	assert.Equal(t, "Job is running", resp.Status.Message)
}

func TestGetTask_NotFound(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/nonexistent")

	assert.Equal(t, http.StatusNotFound, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task not found", errResp.Error)
}

func TestGetTask_K8sClientError(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
				return fmt.Errorf("API server unavailable")
			},
		}).
		Build()

	h := &taskHandler{client: c, namespace: "default", callback: newCallbackSender("")}
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-abc")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to get task", errResp.Error)
}

func TestListTasks_K8sClientError(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
				return fmt.Errorf("API server unavailable")
			},
		}).
		Build()

	h := &taskHandler{client: c, namespace: "default", callback: newCallbackSender("")}
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to list tasks", errResp.Error)
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{
			name:       "no conditions (pending)",
			conditions: nil,
			want:       false,
		},
		{
			name: "Succeeded=Unknown (running)",
			conditions: []metav1.Condition{
				{Type: toolkitv1alpha1.ConditionSucceeded, Status: metav1.ConditionUnknown},
			},
			want: false,
		},
		{
			name: "Succeeded=True (terminal)",
			conditions: []metav1.Condition{
				{Type: toolkitv1alpha1.ConditionSucceeded, Status: metav1.ConditionTrue},
			},
			want: true,
		},
		{
			name: "Succeeded=False (terminal)",
			conditions: []metav1.Condition{
				{Type: toolkitv1alpha1.ConditionSucceeded, Status: metav1.ConditionFalse},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &toolkitv1alpha1.AgentTask{
				Status: toolkitv1alpha1.AgentTaskStatus{Conditions: tt.conditions},
			}
			assert.Equal(t, tt.want, task.IsTerminal())
		})
	}
}
