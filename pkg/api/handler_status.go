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
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	// Validate event type
	validEvents := map[string]bool{
		EventStarted:   true,
		EventProgress:  true,
		EventCompleted: true,
		EventFailed:    true,
	}
	if !validEvents[req.Event] {
		writeError(w, http.StatusBadRequest, "invalid event type", fmt.Sprintf("must be one of: %s, %s, %s, %s", EventStarted, EventProgress, EventCompleted, EventFailed))
		return
	}

	// Fetch the task
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

	// For terminal events, check dedup before doing any work
	isTerminal := req.Event == EventCompleted || req.Event == EventFailed
	if isTerminal {
		notifiedCond := apimeta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionNotified)
		if notifiedCond != nil {
			// Only dedup on definitively complete callbacks (CallbackSent or CallbackFailed)
			if notifiedCond.Reason == toolkitv1alpha1.ReasonCallbackSent ||
				notifiedCond.Reason == toolkitv1alpha1.ReasonCallbackFailed {
				writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "note": "already notified"})
				return
			}
			// If CallbackPending and stale (>5 min), allow re-claim
			if notifiedCond.Reason == toolkitv1alpha1.ReasonCallbackPending {
				if time.Since(notifiedCond.LastTransitionTime.Time) < callbackPendingTTL {
					writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "note": "callback pending"})
					return
				}
				// Stale CallbackPending — fall through to re-claim
			}
		}
	}

	// Update CRD status fields based on event
	// Only terminal events modify status fields
	if isTerminal {
		switch req.Event {
		case EventCompleted:
			if prURL, ok := req.Details["pr_url"].(string); ok {
				task.Status.Result.PRUrl = prURL
			}
		case EventFailed:
			if errMsg, ok := req.Details["error"].(string); ok {
				task.Status.Result.Error = errMsg
			}
		}

		// Phase 1: Set Notified condition to CallbackPending (Unknown status) in the SAME update
		// as result fields to avoid a double-write race (resource version changes after first update).
		apimeta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
			Type:               toolkitv1alpha1.ConditionNotified,
			Status:             metav1.ConditionUnknown,
			Reason:             toolkitv1alpha1.ReasonCallbackPending,
			Message:            fmt.Sprintf("Sending callback to adapter: %s", req.Event),
			ObservedGeneration: task.Generation,
		})

		// Single status update with all changes (result + Notified condition)
		if err := h.client.Status().Update(r.Context(), &task); err != nil {
			if apierrors.IsConflict(err) {
				// Someone else claimed the task first — treat as accepted
				writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "note": "task already claimed"})
				return
			}
			log.Error(err, "failed to update task status", "taskID", taskID)
			writeError(w, http.StatusInternalServerError, "failed to update task status", "")
			return
		}
	}

	// Forward callback to adapter (after successful status update)
	callbackURL := task.Spec.Callback.URL
	payload := CallbackPayload{
		TaskID:  taskID,
		Event:   req.Event,
		Message: req.Message,
		Details: req.Details,
	}

	callbackErr := h.callback.send(r.Context(), callbackURL, payload)

	// Phase 2: Update Notified condition based on callback result (terminal events only)
	if isTerminal {
		// Re-fetch the task to get fresh resourceVersion
		var freshTask toolkitv1alpha1.AgentTask
		key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
		if err := h.client.Get(r.Context(), key, &freshTask); err != nil {
			log.Error(err, "failed to re-fetch task for callback status update", "taskID", taskID)
			// Continue without updating callback status — watcher can retry if CallbackPending TTL expires
		} else {
			// Update Notified condition based on callback result
			if callbackErr != nil {
				apimeta.SetStatusCondition(&freshTask.Status.Conditions, metav1.Condition{
					Type:               toolkitv1alpha1.ConditionNotified,
					Status:             metav1.ConditionTrue,
					Reason:             toolkitv1alpha1.ReasonCallbackFailed,
					Message:            fmt.Sprintf("Adapter callback failed: %v", callbackErr),
					ObservedGeneration: freshTask.Generation,
				})
			} else {
				apimeta.SetStatusCondition(&freshTask.Status.Conditions, metav1.Condition{
					Type:               toolkitv1alpha1.ConditionNotified,
					Status:             metav1.ConditionTrue,
					Reason:             toolkitv1alpha1.ReasonCallbackSent,
					Message:            fmt.Sprintf("Adapter notified: %s", req.Event),
					ObservedGeneration: freshTask.Generation,
				})
			}

			if err := h.client.Status().Update(r.Context(), &freshTask); err != nil {
				log.Error(err, "failed to update callback status", "taskID", taskID)
				// Don't fail the request — the condition remains CallbackPending which watcher can retry if TTL expires
			}
		}

		if callbackErr != nil {
			log.Error(callbackErr, "failed to send adapter callback", "taskID", taskID, "callbackURL", callbackURL)
		}
	} else {
		// Non-terminal events: just log callback errors, don't update condition
		if callbackErr != nil {
			log.Error(callbackErr, "failed to send adapter callback", "taskID", taskID, "callbackURL", callbackURL)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}
