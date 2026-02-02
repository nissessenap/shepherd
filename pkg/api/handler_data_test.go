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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

func TestGetTaskData_ReturnsDecompressedContext(t *testing.T) {
	// Compress context as createTask would
	compressed, encoding, err := compressContext("Additional context for the task")
	require.NoError(t, err)

	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-data-1",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo: toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo", Ref: "main"},
			Task: toolkitv1alpha1.TaskSpec{
				Description:     "Fix the login bug",
				Context:         compressed,
				ContextEncoding: encoding,
				SourceURL:       "https://github.com/org/repo/issues/42",
			},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	h := newTestHandler(task)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-data-1/data")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TaskDataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Fix the login bug", resp.Description)
	assert.Equal(t, "Additional context for the task", resp.Context)
	assert.Equal(t, "https://github.com/org/repo/issues/42", resp.SourceURL)
	assert.Equal(t, "https://github.com/org/repo", resp.Repo.URL)
	assert.Equal(t, "main", resp.Repo.Ref)
}

func TestGetTaskData_NotFound(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/nonexistent/data")

	assert.Equal(t, http.StatusNotFound, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task not found", errResp.Error)
}

func TestGetTaskData_PlaintextContext(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-plain",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo: toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task: toolkitv1alpha1.TaskSpec{
				Description: "A task",
				Context:     "raw plaintext context",
				// No ContextEncoding — empty string means no encoding
			},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	h := newTestHandler(task)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-plain/data")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TaskDataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "raw plaintext context", resp.Context)
}

func TestGetTaskData_EmptyContext(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-noctx",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
	}

	h := newTestHandler(task)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-noctx/data")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TaskDataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "", resp.Context)
	assert.Equal(t, "A task", resp.Description)
}

func TestGetTaskData_TerminalTaskRejected(t *testing.T) {
	task := &toolkitv1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-done",
			Namespace: "default",
		},
		Spec: toolkitv1alpha1.AgentTaskSpec{
			Repo:     toolkitv1alpha1.RepoSpec{URL: "https://github.com/org/repo"},
			Task:     toolkitv1alpha1.TaskSpec{Description: "A task"},
			Callback: toolkitv1alpha1.CallbackSpec{URL: "https://example.com/cb"},
		},
		Status: toolkitv1alpha1.AgentTaskStatus{
			Conditions: []metav1.Condition{
				{
					Type:   toolkitv1alpha1.ConditionSucceeded,
					Status: metav1.ConditionTrue,
					Reason: toolkitv1alpha1.ReasonSucceeded,
				},
			},
		},
	}

	h := newTestHandler(task)
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks/task-done/data")

	assert.Equal(t, http.StatusGone, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "task is terminal", errResp.Error)
}
