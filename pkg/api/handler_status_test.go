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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// newTestHandlerWithCallback creates a test handler with a callback sender using the given secret.
func newTestHandlerWithCallback(secret string, objs ...client.Object) *taskHandler {
	s := testScheme()
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	c := builder.Build()

	return &taskHandler{
		client:    c,
		namespace: "default",
		callback:  newCallbackSender(secret),
	}
}

// statusTask creates a task with a callback URL for status handler tests.
func statusTask(name, callbackURL string, conditions []metav1.Condition) *toolkitv1alpha1.AgentTask {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/test/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "test", Context: "ctx"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: callbackURL},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			Conditions: conditions,
		},
	}
	return task
}

func TestUpdateTaskStatus_StartedEvent(t *testing.T) {
	var callbackReceived atomic.Bool
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackReceived.Store(true)
		var payload CallbackPayload
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "started", payload.Event)
		assert.Equal(t, "task-abc", payload.TaskID)
		assert.Equal(t, "cloning repository", payload.Message)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "started",
		Message: "cloning repository",
	})

	assert.Equal(t, http.StatusOK, w.Code)

	// Contract validation
	doc := loadSpec(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-abc/status", nil)
	req.Header.Set("Content-Type", "application/json")
	validateResponse(t, doc, req, w)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "accepted", resp["status"])
	assert.True(t, callbackReceived.Load(), "adapter should have received callback")
}

func TestUpdateTaskStatus_CompletedWithPRUrl(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "completed",
		Message: "Task completed successfully",
		Details: map[string]any{"pr_url": "https://github.com/org/repo/pull/1"},
	})

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify CRD was updated with PR URL and Notified condition
	var updated toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/org/repo/pull/1", updated.Status.Result.PRURL)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status)
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackSent, notified.Reason)
}

func TestUpdateTaskStatus_FailedWithError(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-fail", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-fail/status", StatusUpdateRequest{
		Event:   "failed",
		Message: "Task failed",
		Details: map[string]any{"error": "compilation error in main.go"},
	})

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify CRD was updated with error
	var updated toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-fail"}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "compilation error in main.go", updated.Status.Result.Error)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status)
}

func TestUpdateTaskStatus_MissingEvent(t *testing.T) {
	task := statusTask("task-abc", "http://localhost/cb", nil)
	h := newTestHandlerWithCallback("", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Message: "missing event field",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "event is required", errResp.Error)
}

func TestUpdateTaskStatus_TaskNotFound(t *testing.T) {
	h := newTestHandler() // no tasks
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/nonexistent/status", StatusUpdateRequest{
		Event:   "started",
		Message: "test",
	})

	assert.Equal(t, http.StatusNotFound, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task not found", errResp.Error)
}

func TestUpdateTaskStatus_TerminalSetsNotifiedCondition(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "completed",
		Message: "done",
	})

	assert.Equal(t, http.StatusOK, w.Code)

	var updated toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &updated)
	require.NoError(t, err)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified, "Notified condition should be set")
	assert.Equal(t, metav1.ConditionTrue, notified.Status)
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackSent, notified.Reason)
	assert.Contains(t, notified.Message, "completed")
}

func TestUpdateTaskStatus_DuplicateTerminalSkipsCallback(t *testing.T) {
	tests := []struct {
		name      string
		condition metav1.Condition
	}{
		{
			name: "already notified with CallbackSent",
			condition: metav1.Condition{
				Type:   toolkitv1alpha1.ConditionNotified,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonCallbackSent,
			},
		},
		{
			name: "already notified with CallbackPending",
			condition: metav1.Condition{
				Type:               toolkitv1alpha1.ConditionNotified,
				Status:             metav1.ConditionUnknown,
				Reason:             toolkitv1alpha1.ReasonCallbackPending,
				LastTransitionTime: metav1.Now(), // Fresh timestamp, within TTL
			},
		},
		{
			name: "already notified with CallbackFailed",
			condition: metav1.Condition{
				Type:   toolkitv1alpha1.ConditionNotified,
				Status: metav1.ConditionTrue,
				Reason: toolkitv1alpha1.ReasonCallbackFailed,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var callbackCount atomic.Int32
			adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				callbackCount.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer adapter.Close()

			task := statusTask("task-abc", adapter.URL, []metav1.Condition{tt.condition})
			h := newTestHandlerWithCallback("test-secret", task)
			router := testRouter(h)

			w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
				Event:   "completed",
				Message: "done again",
			})

			assert.Equal(t, http.StatusOK, w.Code)
			var resp map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			// For CallbackPending with fresh timestamp, expect "callback pending"
			expectedNote := "already notified"
			if tt.condition.Reason == toolkitv1alpha1.ReasonCallbackPending {
				expectedNote = "callback pending"
			}
			assert.Equal(t, expectedNote, resp["note"])
			assert.Equal(t, int32(0), callbackCount.Load(), "no callback should be sent for duplicate")
		})
	}
}

func TestUpdateTaskStatus_AdapterFailureDoesNotFailRequest(t *testing.T) {
	// Adapter that always returns 500
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "started",
		Message: "test",
	})

	// Should still return 200 even though adapter callback failed
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "accepted", resp["status"])
}

func TestUpdateTaskStatus_ProgressEventForwardsWithoutNotified(t *testing.T) {
	var callbackReceived atomic.Bool
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackReceived.Store(true)
		var payload CallbackPayload
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "progress", payload.Event)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "progress",
		Message: "50% complete",
	})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, callbackReceived.Load(), "adapter should receive progress callback")

	// Verify Notified condition is NOT set for progress events
	var updated toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &updated)
	require.NoError(t, err)
	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	assert.Nil(t, notified, "Notified condition should not be set for progress events")
}

func TestUpdateTaskStatus_BodyTooLarge(t *testing.T) {
	task := statusTask("task-abc", "http://localhost/cb", nil)
	h := newTestHandlerWithCallback("", task)
	router := testRouter(h)

	largeBody := strings.Repeat("x", 11<<20) // 11 MiB
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-abc/status", strings.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateTaskStatus_InvalidBody(t *testing.T) {
	task := statusTask("task-abc", "http://localhost/cb", nil)
	h := newTestHandlerWithCallback("", task)
	router := testRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-abc/status", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "invalid request body", errResp.Error)
}

func TestUpdateTaskStatus_K8sGetError(t *testing.T) {
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

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "started",
		Message: "test",
	})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to get task", errResp.Error)
}

func TestUpdateTaskStatus_K8sStatusUpdateError(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)

	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ ...client.SubResourceUpdateOption) error {
				return fmt.Errorf("conflict: resource version changed")
			},
		}).
		Build()

	h := &taskHandler{client: c, namespace: "default", callback: newCallbackSender("test-secret")}
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "completed",
		Message: "done",
	})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "failed to update task status", errResp.Error)
}

func TestUpdateTaskStatus_NonTerminalEventDoesNotUpdateStatus(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		message string
	}{
		{name: "started event", event: "started", message: "cloning repository"},
		{name: "progress event", event: "progress", message: "50% complete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer adapter.Close()

			task := statusTask("task-abc", adapter.URL, nil)
			h := newTestHandlerWithCallback("test-secret", task)

			var initialTask toolkitv1alpha1.AgentTask
			err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &initialTask)
			require.NoError(t, err)
			initialResourceVersion := initialTask.ResourceVersion

			router := testRouter(h)

			w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
				Event:   tt.event,
				Message: tt.message,
			})

			assert.Equal(t, http.StatusOK, w.Code)

			var updated toolkitv1alpha1.AgentTask
			err = h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &updated)
			require.NoError(t, err)
			assert.Equal(t, initialResourceVersion, updated.ResourceVersion, "resourceVersion should be unchanged for non-terminal events")
		})
	}
}

func TestUpdateTaskStatus_TwoPhaseCondition_CallbackSuccess(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "completed",
		Message: "done",
	})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), callbackCount.Load(), "callback should be sent once")

	// Verify final condition is CallbackSent (Status=True)
	var updated toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &updated)
	require.NoError(t, err)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status, "final status should be True after successful callback")
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackSent, notified.Reason)
	assert.Contains(t, notified.Message, "completed")
}

func TestUpdateTaskStatus_TwoPhaseCondition_CallbackFailure(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer adapter.Close()

	task := statusTask("task-abc", adapter.URL, nil)
	h := newTestHandlerWithCallback("test-secret", task)
	router := testRouter(h)

	w := postJSON(t, router, "/api/v1/tasks/task-abc/status", StatusUpdateRequest{
		Event:   "failed",
		Message: "task failed",
	})

	assert.Equal(t, http.StatusOK, w.Code, "request should succeed even if callback fails")
	assert.Equal(t, int32(1), callbackCount.Load(), "callback should be attempted once")

	// Verify final condition is CallbackFailed (Status=True)
	var updated toolkitv1alpha1.AgentTask
	err := h.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-abc"}, &updated)
	require.NoError(t, err)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status, "final status should be True even for failed callback")
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackFailed, notified.Reason)
	assert.Contains(t, notified.Message, "failed")
}
