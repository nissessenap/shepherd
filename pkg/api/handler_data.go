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

// getTaskData handles GET /api/v1/tasks/{taskID}/data.
// Returns decompressed task description, context, repo info.
// TODO: Authenticate via per-task bearer token (see #22)
func (h *taskHandler) getTaskData(w http.ResponseWriter, r *http.Request) {
	log := ctrl.Log.WithName("api")
	taskID := chi.URLParam(r, "taskID")

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

	context, err := decompressContext(task.Spec.Task.Context, task.Spec.Task.ContextEncoding)
	if err != nil {
		log.Error(err, "failed to decompress context", "taskID", taskID)
		writeError(w, http.StatusInternalServerError, "failed to decompress context", "")
		return
	}

	resp := TaskDataResponse{
		Description: task.Spec.Task.Description,
		Context:     context,
		SourceURL:   task.Spec.Task.SourceURL,
		Repo: RepoRequest{
			URL: task.Spec.Repo.URL,
			Ref: task.Spec.Repo.Ref,
		},
	}

	writeJSON(w, http.StatusOK, resp)
}
