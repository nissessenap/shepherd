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
	"maps"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

// taskHandler holds dependencies for task endpoints.
type taskHandler struct {
	client    client.Client
	namespace string
}

// createTask handles POST /api/v1/tasks.
func (h *taskHandler) createTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MiB
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// Validate required fields
	if req.Repo.URL == "" {
		writeError(w, http.StatusBadRequest, "repo.url is required", "")
		return
	}
	if req.Task.Description == "" {
		writeError(w, http.StatusBadRequest, "task.description is required", "")
		return
	}
	if req.Task.Context == "" {
		writeError(w, http.StatusBadRequest, "task.context is required", "")
		return
	}
	if req.Callback == "" {
		writeError(w, http.StatusBadRequest, "callbackUrl is required", "")
		return
	}

	// Compress context
	compressedCtx, encoding, err := compressContext(req.Task.Context)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compress context", err.Error())
		return
	}

	// Generate task name
	taskName := fmt.Sprintf("task-%s", rand.String(8))

	// Build runner spec
	runnerSpec := toolkitv1alpha1.RunnerSpec{}
	if req.Runner != nil {
		if req.Runner.Timeout != "" {
			d, err := time.ParseDuration(req.Runner.Timeout)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid runner.timeout", err.Error())
				return
			}
			runnerSpec.Timeout = metav1.Duration{Duration: d}
		}
		runnerSpec.ServiceAccountName = req.Runner.ServiceAccountName
	}

	// Build labels — pass through adapter-provided labels
	labels := make(map[string]string)
	maps.Copy(labels, req.Labels)

	// Create AgentTask CRD
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskName,
			Namespace: h.namespace,
			Labels:    labels,
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo: toolkitv1alpha1.RepoSpec{
				URL: req.Repo.URL,
				Ref: req.Repo.Ref,
			},
			Task: toolkitv1alpha1.TaskSpec{
				Description:     req.Task.Description,
				Context:         compressedCtx,
				ContextEncoding: encoding,
				ContextURL:      req.Task.ContextURL,
			},
			Callback: toolkitv1alpha1.CallbackSpec{
				URL: req.Callback,
			},
			Runner: runnerSpec,
		},
	}

	if err := h.client.Create(r.Context(), task); err != nil {
		if errors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "task already exists", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create task", err.Error())
		return
	}

	resp := taskToResponse(task)
	writeJSON(w, http.StatusCreated, resp)
}

// listTasks handles GET /api/v1/tasks.
// Query parameters:
//   - repo: filter by shepherd.io/repo label
//   - issue: filter by shepherd.io/issue label
//   - active: if "true", only return tasks with Succeeded=Unknown (non-terminal)
func (h *taskHandler) listTasks(w http.ResponseWriter, r *http.Request) {
	var taskList toolkitv1alpha1.AgentTaskList

	listOpts := []client.ListOption{
		client.InNamespace(h.namespace),
	}

	// Build label selector from query params
	labelSelector := map[string]string{}
	if repo := r.URL.Query().Get("repo"); repo != "" {
		labelSelector["shepherd.io/repo"] = repo
	}
	if issue := r.URL.Query().Get("issue"); issue != "" {
		labelSelector["shepherd.io/issue"] = issue
	}
	if len(labelSelector) > 0 {
		listOpts = append(listOpts, client.MatchingLabels(labelSelector))
	}

	if err := h.client.List(r.Context(), &taskList, listOpts...); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tasks", err.Error())
		return
	}

	// Filter active tasks in-memory if requested
	active := r.URL.Query().Get("active") == "true"

	tasks := make([]TaskResponse, 0, len(taskList.Items))
	for i := range taskList.Items {
		task := &taskList.Items[i]
		if active && isTerminalFromStatus(task) {
			continue
		}
		tasks = append(tasks, taskToResponse(task))
	}

	writeJSON(w, http.StatusOK, tasks)
}

// getTask handles GET /api/v1/tasks/{taskID}.
func (h *taskHandler) getTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")

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

	writeJSON(w, http.StatusOK, taskToResponse(&task))
}

// isTerminalFromStatus checks if a task has reached a terminal condition.
func isTerminalFromStatus(task *toolkitv1alpha1.AgentTask) bool {
	cond := apimeta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
	if cond == nil {
		return false
	}
	return cond.Status != metav1.ConditionUnknown
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal encoding error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, msg, details string) {
	writeJSON(w, status, ErrorResponse{Error: msg, Details: details})
}

func taskToResponse(task *toolkitv1alpha1.AgentTask) TaskResponse {
	resp := TaskResponse{
		ID:        task.Name,
		Namespace: task.Namespace,
		Repo: RepoRequest{
			URL: task.Spec.Repo.URL,
			Ref: task.Spec.Repo.Ref,
		},
		Task: TaskRequest{
			Description: task.Spec.Task.Description,
			ContextURL:  task.Spec.Task.ContextURL,
		},
		CallbackURL: task.Spec.Callback.URL,
		Status:      extractStatus(task),
		CreatedAt:   task.CreationTimestamp.UTC().Format(time.RFC3339),
	}
	if task.Status.CompletionTime != nil {
		ct := task.Status.CompletionTime.UTC().Format(time.RFC3339)
		resp.CompletionTime = &ct
	}
	return resp
}

func extractStatus(task *toolkitv1alpha1.AgentTask) TaskStatusSummary {
	cond := apimeta.FindStatusCondition(task.Status.Conditions, toolkitv1alpha1.ConditionSucceeded)
	phase := "Pending"
	message := ""
	if cond != nil {
		phase = cond.Reason
		message = cond.Message
	}
	return TaskStatusSummary{
		Phase:   phase,
		Message: message,
		JobName: task.Status.JobName,
		PRUrl:   task.Status.Result.PRUrl,
		Error:   task.Status.Result.Error,
	}
}
