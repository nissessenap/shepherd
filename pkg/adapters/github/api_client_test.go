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

package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NissesSenap/shepherd/pkg/api"
)

func TestAPIClient_GetActiveTasks(t *testing.T) {
	t.Run("returns active tasks", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks", r.URL.Path)
			assert.Equal(t, "org-repo", r.URL.Query().Get("repo"))
			assert.Equal(t, "123", r.URL.Query().Get("issue"))
			assert.Equal(t, "true", r.URL.Query().Get("active"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"task-abc","status":{"phase":"Running"}}]`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		tasks, err := client.GetActiveTasks(context.Background(), "org-repo", "123")
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "task-abc", tasks[0].ID)
		assert.Equal(t, "Running", tasks[0].Status.Phase)
	})

	t.Run("returns empty array", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		tasks, err := client.GetActiveTasks(context.Background(), "org-repo", "123")
		require.NoError(t, err)
		assert.Empty(t, tasks)
	})

	t.Run("handles API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal server error"}`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		_, err := client.GetActiveTasks(context.Background(), "org-repo", "123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "internal server error")
	})
}

func TestAPIClient_GetTask(t *testing.T) {
	t.Run("returns task", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks/task-abc", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"task-abc","status":{"phase":"Running"},"task":{"sourceURL":"https://github.com/org/repo/issues/42"}}`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		task, err := client.GetTask(context.Background(), "task-abc")
		require.NoError(t, err)
		assert.Equal(t, "task-abc", task.ID)
	})

	t.Run("handles 404", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"task not found"}`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		_, err := client.GetTask(context.Background(), "nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "task not found")
	})
}

func TestAPIClient_CreateTask(t *testing.T) {
	t.Run("creates task successfully", func(t *testing.T) {
		var receivedReq api.CreateTaskRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			_ = json.NewDecoder(r.Body).Decode(&receivedReq)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"task-xyz","status":{"phase":"Pending"}}`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		resp, err := client.CreateTask(context.Background(), api.CreateTaskRequest{
			Repo:     api.RepoRequest{URL: "https://github.com/org/repo"},
			Task:     api.TaskRequest{Description: "Fix bug"},
			Callback: "http://adapter/callback",
		})
		require.NoError(t, err)
		assert.Equal(t, "task-xyz", resp.ID)
		assert.Equal(t, "https://github.com/org/repo", receivedReq.Repo.URL)
	})

	t.Run("handles API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"repo.url is required"}`))
		}))
		defer srv.Close()

		client := NewAPIClient(srv.URL)
		_, err := client.CreateTask(context.Background(), api.CreateTaskRequest{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repo.url is required")
	})
}
