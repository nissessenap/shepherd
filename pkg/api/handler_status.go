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
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// updateTaskStatus handles POST /api/v1/tasks/{taskID}/status.
func (h *taskHandler) updateTaskStatus(w http.ResponseWriter, r *http.Request) {
	log := ctrl.Log.WithName("api")
	taskID := chi.URLParam(r, "taskID")

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MiB
	var req StatusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if req.Event == "" {
		writeError(w, http.StatusBadRequest, "event is required", "")
		return
	}

	// Fetch the task
	var task toolkitv1alpha1.AgentTask
	key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
	if err := h.client.Get(r.Context(), key, &task); err != nil {
		if errors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "task not found", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get task", err.Error())
		return
	}

	// For terminal events, check dedup before doing any work
	isTerminal := req.Event == "completed" || req.Event == "failed"
	if isTerminal {
		notifiedCond := apimeta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionNotified)
		if notifiedCond != nil && notifiedCond.Status == metav1.ConditionTrue {
			writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "note": "already notified"})
			return
		}
	}

	// Update CRD status fields based on event
	switch req.Event {
	case "completed":
		if prURL, ok := req.Details["pr_url"].(string); ok {
			task.Status.Result.PRUrl = prURL
		}
	case "failed":
		if errMsg, ok := req.Details["error"].(string); ok {
			task.Status.Result.Error = errMsg
		}
	}

	// For terminal events, set Notified condition in the SAME update to avoid
	// a double-write race (resource version changes after first update).
	if isTerminal {
		apimeta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               toolkitv1alpha1.ConditionNotified,
			Status:             metav1.ConditionTrue,
			Reason:             toolkitv1alpha1.ReasonCallbackSent,
			Message:            fmt.Sprintf("Adapter notified: %s", req.Event),
			ObservedGeneration: task.Generation,
		})
	}

	// Single status update with all changes (result + Notified condition)
	if err := h.client.Status().Update(r.Context(), &task); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update task status", err.Error())
		return
	}

	// Forward callback to adapter (after successful status update)
	callbackURL := task.Spec.Callback.URL
	payload := CallbackPayload{
		TaskID:  taskID,
		Event:   req.Event,
		Message: req.Message,
		Details: req.Details,
	}

	if err := h.callback.send(r.Context(), callbackURL, payload); err != nil {
		log.Error(err, "failed to send adapter callback", "taskID", taskID, "callbackURL", callbackURL)
		// Don't fail the request — the runner callback was accepted and status is updated
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}
