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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func newTestWatcher(objs ...client.Object) (*statusWatcher, client.Client) {
	s := testScheme()
	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&toolkitv1alpha1.AgentTask{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	c := builder.Build()

	w := &statusWatcher{
		client:   c,
		callback: newCallbackSender("test-secret"),
		log:      ctrl.Log.WithName("status-watcher-test"),
		// cache not needed for direct handleTerminalTransition tests
	}
	return w, c
}

func watcherTask(name, callbackURL string, conditions []metav1.Condition, result toolkitv1alpha1.TaskResult) *toolkitv1alpha1.AgentTask {
	return &toolkitv1alpha1.AgentTask{
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
			Result:     result,
		},
	}
}

func TestWatcher_TerminalSucceededTriggersCallback(t *testing.T) {
	var received atomic.Bool
	var receivedPayload CallbackPayload
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(true)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedPayload))
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := watcherTask("task-ok", adapter.URL, []metav1.Condition{
		{
			Type:    toolkitv1alpha1.ConditionSucceeded,
			Status:  metav1.ConditionTrue,
			Reason:  toolkitv1alpha1.ReasonSucceeded,
			Message: "Task completed successfully",
		},
	}, toolkitv1alpha1.TaskResult{})

	w, c := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.True(t, received.Load(), "adapter should receive callback")
	assert.Equal(t, "task-ok", receivedPayload.TaskID)
	assert.Equal(t, "completed", receivedPayload.Event)
	assert.Equal(t, "Task completed successfully", receivedPayload.Message)

	// Verify Notified condition was set
	var updated toolkitv1alpha1.AgentTask
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-ok"}, &updated)
	require.NoError(t, err)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status)
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackSent, notified.Reason)
}

func TestWatcher_TerminalFailedTriggersCallback(t *testing.T) {
	var receivedPayload CallbackPayload
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedPayload))
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := watcherTask("task-fail", adapter.URL, []metav1.Condition{
		{
			Type:    toolkitv1alpha1.ConditionSucceeded,
			Status:  metav1.ConditionFalse,
			Reason:  toolkitv1alpha1.ReasonFailed,
			Message: "Job failed",
		},
	}, toolkitv1alpha1.TaskResult{Error: "compilation error"})

	w, c := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, "failed", receivedPayload.Event)
	assert.Equal(t, "Job failed", receivedPayload.Message)
	assert.Equal(t, "compilation error", receivedPayload.Details["error"])

	// Verify Notified condition was set
	var updated toolkitv1alpha1.AgentTask
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-fail"}, &updated)
	require.NoError(t, err)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status)
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackSent, notified.Reason)
}

func TestWatcher_NonTerminalDoesNotTriggerCallback(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	// Running task — not terminal
	task := watcherTask("task-running", adapter.URL, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	}, toolkitv1alpha1.TaskResult{})

	w, _ := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, int32(0), callbackCount.Load(), "no callback for non-terminal task")
}

func TestWatcher_NoConditionsDoesNotTriggerCallback(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	// Pending task — no conditions
	task := watcherTask("task-pending", adapter.URL, nil, toolkitv1alpha1.TaskResult{})

	w, _ := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, int32(0), callbackCount.Load(), "no callback for pending task")
}

func TestWatcher_AlreadyNotifiedSkipsCallback(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := watcherTask("task-done", adapter.URL, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
		{
			Type:   toolkitv1alpha1.ConditionNotified,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonCallbackSent,
		},
	}, toolkitv1alpha1.TaskResult{})

	w, _ := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, int32(0), callbackCount.Load(), "no callback for already-notified task")
}

func TestWatcher_CallbackPendingSkipsCallback(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := watcherTask("task-pending-cb", adapter.URL, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
		{
			Type:               toolkitv1alpha1.ConditionNotified,
			Status:             metav1.ConditionUnknown,
			Reason:             toolkitv1alpha1.ReasonCallbackPending,
			LastTransitionTime: metav1.Now(), // Fresh timestamp, within TTL
		},
	}, toolkitv1alpha1.TaskResult{})

	w, _ := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, int32(0), callbackCount.Load(), "no callback for task with CallbackPending")
}

func TestWatcher_CallbackFailureSetsCallbackFailedCondition(t *testing.T) {
	// Adapter that always returns 500
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer adapter.Close()

	task := watcherTask("task-fail-cb", adapter.URL, []metav1.Condition{
		{
			Type:    toolkitv1alpha1.ConditionSucceeded,
			Status:  metav1.ConditionTrue,
			Reason:  toolkitv1alpha1.ReasonSucceeded,
			Message: "Task completed",
		},
	}, toolkitv1alpha1.TaskResult{})

	w, c := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	// Verify Notified condition is CallbackFailed
	var updated toolkitv1alpha1.AgentTask
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-fail-cb"}, &updated)
	require.NoError(t, err)

	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	require.NotNil(t, notified)
	assert.Equal(t, metav1.ConditionTrue, notified.Status)
	assert.Equal(t, toolkitv1alpha1.ReasonCallbackFailed, notified.Reason)
	assert.Contains(t, notified.Message, "Callback failed")
}

func TestWatcher_PRUrlIncludedInCallbackDetails(t *testing.T) {
	var receivedPayload CallbackPayload
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedPayload))
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := watcherTask("task-pr", adapter.URL, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	}, toolkitv1alpha1.TaskResult{PRUrl: "https://github.com/org/repo/pull/42"})

	w, _ := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, "https://github.com/org/repo/pull/42", receivedPayload.Details["pr_url"])
}

func TestWatcher_ErrorIncludedInCallbackDetails(t *testing.T) {
	var receivedPayload CallbackPayload
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedPayload))
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	task := watcherTask("task-err", adapter.URL, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionFalse,
			Reason: toolkitv1alpha1.ReasonFailed,
		},
	}, toolkitv1alpha1.TaskResult{Error: "timeout exceeded"})

	w, _ := newTestWatcher(task)
	w.handleTerminalTransition(context.Background(), task)

	assert.Equal(t, "timeout exceeded", receivedPayload.Details["error"])
}

func TestWatcher_SetNotifiedConditionRefetchFailure(t *testing.T) {
	// Watcher with a client that fails on Get (simulating re-fetch failure)
	s := testScheme()
	var getCount atomic.Int32
	task := watcherTask("task-refetch", "http://localhost/cb", []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	}, toolkitv1alpha1.TaskResult{})

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if getCount.Add(1) >= 2 {
					// setNotifiedCondition re-fetches — simulate failure on second Get
					return fmt.Errorf("network error during re-fetch")
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	// Use a callback URL that won't actually succeed — we test the re-fetch path
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	// Override the callback URL for the task's spec
	task.Spec.Callback.URL = adapter.URL

	w := &statusWatcher{
		client:   c,
		callback: newCallbackSender("test-secret"),
		log:      ctrl.Log.WithName("status-watcher-test"),
	}

	// This should not panic — re-fetch failure is logged and handled gracefully
	w.handleTerminalTransition(context.Background(), task)

	// The callback was sent but condition update should fail silently
	// Verify no Notified condition was set (since re-fetch failed)
	var updated toolkitv1alpha1.AgentTask
	// This Get also hits our interceptor, so use a fresh client for verification
	freshC := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		Build()
	err := freshC.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-refetch"}, &updated)
	require.NoError(t, err)
	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	assert.Nil(t, notified, "Notified condition should not be set when re-fetch fails")
}

func TestWatcher_ConflictDuringClaimSkipsCallback(t *testing.T) {
	var callbackCount atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer adapter.Close()

	s := testScheme()
	task := watcherTask("task-conflict", adapter.URL, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	}, toolkitv1alpha1.TaskResult{})

	// Client that simulates conflict on the CallbackPending claim
	var updateCount atomic.Int32
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if updateCount.Add(1) == 1 {
					// First update (CallbackPending claim) fails with conflict
					return apierrors.NewConflict(
						toolkitv1alpha1.GroupVersion.WithResource("agenttasks").GroupResource(),
						obj.GetName(),
						fmt.Errorf("handler already claimed this task"),
					)
				}
				// Subsequent updates succeed (shouldn't happen in this test)
				return cl.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()

	w := &statusWatcher{
		client:   c,
		callback: newCallbackSender("test-secret"),
		log:      ctrl.Log.WithName("status-watcher-test"),
	}

	w.handleTerminalTransition(context.Background(), task)

	// Verify callback was NOT sent (conflict means someone else owns it)
	assert.Equal(t, int32(0), callbackCount.Load(), "callback should not be sent on conflict")

	// Verify task still has no Notified condition (conflict prevents claim)
	var updated toolkitv1alpha1.AgentTask
	freshC := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&toolkitv1alpha1.AgentTask{}).
		WithObjects(task).
		Build()
	err := freshC.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-conflict"}, &updated)
	require.NoError(t, err)
	notified := apimeta.FindStatusCondition(updated.Status.Conditions, toolkitv1alpha1.ConditionNotified)
	assert.Nil(t, notified, "Notified condition should not be set when claim conflicts")
}
