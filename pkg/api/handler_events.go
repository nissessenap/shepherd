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
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// postEvents handles POST /api/v1/tasks/{taskID}/events (internal port 8081).
func (h *taskHandler) postEvents(w http.ResponseWriter, r *http.Request) {
	log := ctrl.Log.WithName("api")
	taskID := chi.URLParam(r, "taskID")

	// Validate task exists and is not terminal
	var task toolkitv1alpha1.AgentTask
	key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
	if err := h.client.Get(r.Context(), key, &task); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "task not found", "")
			return
		}
		log.Error(err, "failed to get task", "taskID", taskID)
		writeError(w, http.StatusInternalServerError, "failed to get task", "")
		return
	}

	if task.IsTerminal() {
		writeError(w, http.StatusGone, "task is terminal", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MiB
	var req PostEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "events array is required and must not be empty", "")
		return
	}

	// Known event types matching the OpenAPI spec enum.
	validEventTypes := map[TaskEventType]bool{
		EventTypeThinking:   true,
		EventTypeToolCall:   true,
		EventTypeToolResult: true,
		EventTypeError:      true,
	}

	// Validate each event
	for i, e := range req.Events {
		if e.Type == "" {
			writeError(w, http.StatusBadRequest, "event type is required", "")
			return
		}
		if !validEventTypes[e.Type] {
			writeError(w, http.StatusBadRequest, "invalid event type", "must be one of: thinking, tool_call, tool_result, error")
			return
		}
		if e.Summary == "" {
			writeError(w, http.StatusBadRequest, "event summary is required", "")
			return
		}
		if e.Sequence <= 0 {
			writeError(w, http.StatusBadRequest, "event sequence must be positive", "")
			return
		}
		if _, err := time.Parse(time.RFC3339, e.Timestamp); err != nil {
			writeError(w, http.StatusBadRequest, "invalid event timestamp", "must be RFC3339 date-time format")
			return
		}
		_ = i // validated
	}

	h.eventHub.Publish(taskID, req.Events)

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}
