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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestPostEvents_Valid(t *testing.T) {
	task := newTask("task-events", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	req := PostEventRequest{
		Events: []TaskEvent{
			{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Analyzing code"},
			{Sequence: 2, Timestamp: "2026-01-01T00:00:01Z", Type: EventTypeToolCall, Summary: "Reading file", Tool: "Read"},
		},
	}

	w := postJSON(t, router, "/api/v1/tasks/task-events/events", req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Contract validation
	doc := loadSpec(t)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-events/events", nil)
	httpReq.Header.Set("Content-Type", "application/json")
	validateResponse(t, doc, httpReq, w)

	// Verify events were published to the hub
	history, _, unsub := h.eventHub.Subscribe("task-events", 0)
	defer unsub()

	assert.Len(t, history, 2)
	assert.Equal(t, "Analyzing code", history[0].Summary)
}

func TestPostEvents_TaskNotFound(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	req := PostEventRequest{
		Events: []TaskEvent{
			{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Test"},
		},
	}

	w := postJSON(t, router, "/api/v1/tasks/nonexistent/events", req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	// Contract validation
	doc := loadSpec(t)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/nonexistent/events", nil)
	httpReq.Header.Set("Content-Type", "application/json")
	validateResponse(t, doc, httpReq, w)
}

func TestPostEvents_TerminalTask(t *testing.T) {
	task := newTask("task-done", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionTrue,
			Reason: toolkitv1alpha1.ReasonSucceeded,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	req := PostEventRequest{
		Events: []TaskEvent{
			{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Too late"},
		},
	}

	w := postJSON(t, router, "/api/v1/tasks/task-done/events", req)

	assert.Equal(t, http.StatusGone, w.Code)

	// Contract validation
	doc := loadSpec(t)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-done/events", nil)
	httpReq.Header.Set("Content-Type", "application/json")
	validateResponse(t, doc, httpReq, w)
}

func TestPostEvents_EmptyEvents(t *testing.T) {
	task := newTask("task-empty", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	req := PostEventRequest{Events: []TaskEvent{}}

	w := postJSON(t, router, "/api/v1/tasks/task-empty/events", req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "events array is required and must not be empty", errResp.Error)
}

func TestPostEvents_MissingEventType(t *testing.T) {
	task := newTask("task-nomethod", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	req := PostEventRequest{
		Events: []TaskEvent{
			{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: "", Summary: "No type"},
		},
	}

	w := postJSON(t, router, "/api/v1/tasks/task-nomethod/events", req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "event type is required", errResp.Error)
}

func TestPostEvents_MissingSummary(t *testing.T) {
	task := newTask("task-nosummary", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	req := PostEventRequest{
		Events: []TaskEvent{
			{Sequence: 1, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: ""},
		},
	}

	w := postJSON(t, router, "/api/v1/tasks/task-nosummary/events", req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "event summary is required", errResp.Error)
}

func TestPostEvents_InvalidSequence(t *testing.T) {
	task := newTask("task-badseq", nil, []metav1.Condition{
		{
			Type:   toolkitv1alpha1.ConditionSucceeded,
			Status: metav1.ConditionUnknown,
			Reason: toolkitv1alpha1.ReasonRunning,
		},
	})

	h := newTestHandler(task)
	router := testRouter(h)

	req := PostEventRequest{
		Events: []TaskEvent{
			{Sequence: 0, Timestamp: "2026-01-01T00:00:00Z", Type: EventTypeThinking, Summary: "Bad seq"},
		},
	}

	w := postJSON(t, router, "/api/v1/tasks/task-badseq/events", req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "event sequence must be positive", errResp.Error)
}
