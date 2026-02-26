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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestStreamEvents_WebSocketUpgradeAndReplay(t *testing.T) {
	task := newTask("task-ws", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)

	// Publish some events before connecting
	h.eventHub.Publish("task-ws", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Analyzing"},
		{Sequence: 2, Timestamp: "2026-01-01T00:00:01Z", Type: EventTypeToolCall, Summary: "Reading file", Tool: "Read"},
	})

	router := testRouter(h)
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, srv.URL+"/api/v1/tasks/task-ws/events", nil)
	require.NoError(t, err)
	defer conn.CloseNow() //nolint:errcheck

	// Should receive historical events
	var msg1 WSMessage
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg1))
	assert.Equal(t, "task_event", msg1.Type)

	var msg2 WSMessage
	_, data, err = conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg2))
	assert.Equal(t, "task_event", msg2.Type)

	// Publish a live event
	h.eventHub.Publish("task-ws", []TaskEvent{
		{Sequence: 3, Timestamp: "2026-01-01T00:00:02Z", Type: EventTypeToolResult, Summary: "File contents"},
	})

	var msg3 WSMessage
	_, data, err = conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg3))
	assert.Equal(t, "task_event", msg3.Type)
}

func TestStreamEvents_AfterReconnection(t *testing.T) {
	task := newTask("task-reconnect", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)

	h.eventHub.Publish("task-reconnect", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "First"},
		{Sequence: 2, Timestamp: "2026-01-01T00:00:01Z", Type: EventTypeToolCall, Summary: "Second"},
		{Sequence: 3, Timestamp: "2026-01-01T00:00:02Z", Type: EventTypeToolResult, Summary: "Third"},
	})

	router := testRouter(h)
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect with ?after=1, should only get events 2 and 3
	conn, _, err := websocket.Dial(ctx, srv.URL+"/api/v1/tasks/task-reconnect/events?after=1", nil)
	require.NoError(t, err)
	defer conn.CloseNow() //nolint:errcheck

	var msg1 WSMessage
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg1))

	// Extract sequence from the data
	eventData, _ := json.Marshal(msg1.Data)
	var event TaskEvent
	require.NoError(t, json.Unmarshal(eventData, &event))
	assert.Equal(t, int64(2), event.Sequence)
}

func TestStreamEvents_CompletedTaskSendsCompleteMessage(t *testing.T) {
	task := newTask("task-completed", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	})
	task.Status.Result.PRURL = "https://github.com/org/repo/pull/1"

	h := newTestHandler(task)

	// Publish events then complete
	h.eventHub.Publish("task-completed", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Done"},
	})
	h.eventHub.Complete("task-completed")

	router := testRouter(h)
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, srv.URL+"/api/v1/tasks/task-completed/events", nil)
	require.NoError(t, err)
	defer conn.CloseNow() //nolint:errcheck

	// Should receive historical event
	var msg1 WSMessage
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg1))
	assert.Equal(t, "task_event", msg1.Type)

	// Should receive task_complete message
	var msg2 WSMessage
	_, data, err = conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg2))
	assert.Equal(t, "task_complete", msg2.Type)

	// Extract complete data
	completeData, _ := json.Marshal(msg2.Data)
	var complete TaskCompleteData
	require.NoError(t, json.Unmarshal(completeData, &complete))
	assert.Equal(t, "task-completed", complete.TaskID)
	assert.Equal(t, "Succeeded", complete.Status)
	assert.Equal(t, "https://github.com/org/repo/pull/1", complete.PRURL)
}

func TestStreamEvents_LiveCompleteSendsCompleteMessage(t *testing.T) {
	task := newTask("task-live-complete", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, srv.URL+"/api/v1/tasks/task-live-complete/events", nil)
	require.NoError(t, err)
	defer conn.CloseNow() //nolint:errcheck

	// Publish an event
	h.eventHub.Publish("task-live-complete", []TaskEvent{
		{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Working"},
	})

	// Read the event
	var msg WSMessage
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &msg))
	assert.Equal(t, "task_event", msg.Type)

	// Complete the task
	h.eventHub.Complete("task-live-complete")

	// Should receive task_complete message
	var completeMsg WSMessage
	_, data, err = conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &completeMsg))
	assert.Equal(t, "task_complete", completeMsg.Type)
}

func TestStreamEvents_TaskNotFound(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	// Non-WebSocket request should return 404
	w := doGet(t, router, "/api/v1/tasks/nonexistent/events")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStreamEvents_InvalidAfterParameter(t *testing.T) {
	task := newTask("task-badafter", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	// A plain HTTP GET (no WebSocket upgrade) with a non-numeric ?after value
	// must be rejected with 400 before the WebSocket upgrade is attempted.
	w := doGet(t, router, "/api/v1/tasks/task-badafter/events?after=not-a-number")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
