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
	"net/http"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// getTaskToken handles GET /api/v1/tasks/{taskID}/token.
// Generates a short-lived GitHub installation token scoped to the task's repo.
// Uses TokenIssued flag to prevent replay attacks - each task can only fetch a token once.
func (h *taskHandler) getTaskToken(w http.ResponseWriter, r *http.Request) {
	log := ctrl.Log.WithName("api")
	taskID := chi.URLParam(r, "taskID")

	const maxRetries = 3
	for attempt := range maxRetries {
		if r.Context().Err() != nil {
			return
		}
		var task toolkitv1alpha1.AgentTask
		key := client.ObjectKey{Namespace: h.namespace, Name: taskID}
		if err := h.client.Get(r.Context(), key, &task); err != nil {
			if errors.IsNotFound(err) {
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

		// One-time fetch: block replay within same execution
		if task.Status.TokenIssued {
			writeError(w, http.StatusConflict, "token already issued for this execution", "")
			return
		}

		if h.githubKey == nil {
			writeError(w, http.StatusServiceUnavailable, "GitHub App not configured", "")
			return
		}

		// Mark TokenIssued BEFORE generating token (fail-safe: if token gen fails,
		// the flag is set but no token was leaked)
		task.Status.TokenIssued = true
		if err := h.client.Status().Update(r.Context(), &task); err != nil {
			if errors.IsConflict(err) {
				log.V(1).Info("conflict updating TokenIssued, retrying", "taskID", taskID, "attempt", attempt+1)
				continue // Retry with fresh task
			}
			log.Error(err, "failed to update TokenIssued", "taskID", taskID)
			writeError(w, http.StatusInternalServerError, "failed to update task status", "")
			return
		}

		// Generate and return token
		jwtToken, err := createJWT(h.githubAppID, h.githubKey)
		if err != nil {
			log.Error(err, "failed to create JWT", "taskID", taskID)
			writeError(w, http.StatusInternalServerError, "failed to generate token", "")
			return
		}

		repoName, err := parseRepoName(task.Spec.Repo.URL)
		if err != nil {
			log.Error(err, "failed to parse repo URL", "taskID", taskID, "url", task.Spec.Repo.URL)
			writeError(w, http.StatusInternalServerError, "failed to parse repo URL", "")
			return
		}

		httpClient := h.httpClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}

		token, expiresAt, err := exchangeToken(r.Context(), httpClient, h.githubAPIURL, h.githubInstallID, jwtToken, repoName)
		if err != nil {
			log.Error(err, "failed to exchange token", "taskID", taskID)
			writeError(w, http.StatusBadGateway, "failed to generate GitHub token", "")
			return
		}

		writeJSON(w, http.StatusOK, TokenResponse{
			Token:     token,
			ExpiresAt: expiresAt,
		})
		return
	}

	// Exhausted retries
	log.Error(nil, "exhausted retries updating TokenIssued", "taskID", taskID)
	writeError(w, http.StatusConflict, "concurrent update conflict", "")
}
