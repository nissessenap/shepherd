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
	"strconv"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// streamEvents handles GET /api/v1/tasks/{taskID}/events (WebSocket upgrade, public port 8080).
func (h *taskHandler) streamEvents(w http.ResponseWriter, r *http.Request) {
	log := ctrl.Log.WithName("api")
	taskID := chi.URLParam(r, "taskID")

	// Validate task exists
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

	// Parse ?after parameter
	var after int64
	if afterParam := r.URL.Query().Get("after"); afterParam != "" {
		var err error
		after, err = strconv.ParseInt(afterParam, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid after parameter", err.Error())
			return
		}
	}

	// Accept WebSocket upgrade
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Error(err, "failed to accept websocket", "taskID", taskID)
		return
	}
	defer conn.CloseNow() //nolint:errcheck

	ctx := r.Context()

	// Use CloseRead for write-only mode — reads client close frames
	conn.CloseRead(ctx)

	// Subscribe to EventHub
	history, ch, unsubscribe := h.eventHub.Subscribe(taskID, after)
	if unsubscribe != nil {
		defer unsubscribe()
	}

	// Send historical events (replay)
	for _, e := range history {
		msg := WSMessage{Type: "task_event", Data: e}
		data, err := json.Marshal(msg)
		if err != nil {
			log.Error(err, "failed to marshal event", "taskID", taskID)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			return
		}
	}

	// If task is already complete and no live channel, send complete and close
	if ch == nil {
		completeData := TaskCompleteData{
			TaskID: taskID,
			Status: extractStatus(&task).Phase,
			PRURL:  task.Status.Result.PRURL,
			Error:  task.Status.Result.Error,
		}
		msg := WSMessage{Type: "task_complete", Data: completeData}
		data, _ := json.Marshal(msg)
		_ = conn.Write(ctx, websocket.MessageText, data)
		_ = conn.Close(websocket.StatusNormalClosure, "task complete")
		return
	}

	// Stream live events until task completes or client disconnects
	for e := range ch {
		msg := WSMessage{Type: "task_event", Data: e}
		data, err := json.Marshal(msg)
		if err != nil {
			log.Error(err, "failed to marshal event", "taskID", taskID)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			return
		}
	}

	// Channel closed — determine whether it closed because the task completed
	// (Complete() was called) or because this subscriber was evicted for being
	// too slow (Publish() drops slow consumers). Only send task_complete in the
	// former case; otherwise close with a policy violation status.
	if !h.eventHub.IsStreamDone(taskID) {
		// Slow consumer eviction: do not send a false task_complete message.
		_ = conn.Close(websocket.StatusPolicyViolation, "slow consumer evicted")
		return
	}

	// Re-fetch task to get terminal status.
	var freshTask toolkitv1alpha1.AgentTask
	if err := h.client.Get(ctx, key, &freshTask); err != nil {
		log.Error(err, "failed to get task for completion", "taskID", taskID)
		_ = conn.Close(websocket.StatusInternalError, "failed to get task status")
		return
	}

	completeData := TaskCompleteData{
		TaskID: taskID,
		Status: extractStatus(&freshTask).Phase,
		PRURL:  freshTask.Status.Result.PRURL,
		Error:  freshTask.Status.Result.Error,
	}
	msg := WSMessage{Type: "task_complete", Data: completeData}
	data, _ := json.Marshal(msg)
	_ = conn.Write(ctx, websocket.MessageText, data)
	_ = conn.Close(websocket.StatusNormalClosure, "task complete")
}
