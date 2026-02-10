package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchTaskData(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks/task-1/data", r.URL.Path)
			assert.Equal(t, http.MethodGet, r.Method)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskDataResponse{
				Description: "fix the bug",
				Context:     "some context",
				SourceURL:   "https://github.com/org/repo/issues/1",
				Repo: struct {
					URL string `json:"url"`
					Ref string `json:"ref,omitempty"`
				}{
					URL: "https://github.com/org/repo",
					Ref: "main",
				},
			})
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		data, err := c.FetchTaskData(context.Background(), "task-1")
		require.NoError(t, err)

		assert.Equal(t, "task-1", data.TaskID)
		assert.Equal(t, srv.URL, data.APIURL)
		assert.Equal(t, "fix the bug", data.Description)
		assert.Equal(t, "some context", data.Context)
		assert.Equal(t, "https://github.com/org/repo/issues/1", data.SourceURL)
		assert.Equal(t, "https://github.com/org/repo", data.RepoURL)
		assert.Equal(t, "main", data.RepoRef)
	})

	t.Run("not found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"task not found"}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		_, err := c.FetchTaskData(context.Background(), "task-missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("gone (terminal)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"error":"task is terminal"}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		_, err := c.FetchTaskData(context.Background(), "task-done")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "terminal")
	})
}

func TestFetchToken(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks/task-1/token", r.URL.Path)
			assert.Equal(t, http.MethodGet, r.Method)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tokenResponse{
				Token:     "ghs_test_token",
				ExpiresAt: "2026-02-10T12:00:00Z",
			})
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		token, expiresAt, err := c.FetchToken(context.Background(), "task-1")
		require.NoError(t, err)

		assert.Equal(t, "ghs_test_token", token)
		assert.Equal(t, 2026, expiresAt.Year())
		assert.Equal(t, 12, expiresAt.Hour())
	})

	t.Run("409 conflict (already issued)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"token already issued"}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		_, _, err := c.FetchToken(context.Background(), "task-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-retriable")
	})
}

func TestReportStatus(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/tasks/task-1/status", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			var req statusUpdateRequest
			require.NoError(t, json.Unmarshal(body, &req))
			assert.Equal(t, "completed", req.Event)
			assert.Equal(t, "task done", req.Message)
			assert.Equal(t, "https://github.com/org/repo/pull/1", req.Details["pr_url"])

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		err := c.ReportStatus(context.Background(), "task-1", "completed", "task done", map[string]any{
			"pr_url": "https://github.com/org/repo/pull/1",
		})
		require.NoError(t, err)
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)
		err := c.ReportStatus(context.Background(), "task-1", "started", "starting", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500")
	})
}
