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
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
)

// buildTestRouters creates public and internal routers matching the production
// configuration for testing route separation.
func buildTestRouters(h *taskHandler) (publicRouter, internalRouter *chi.Mux) {
	healthzHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	readyzHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}

	// Public router (port 8080) - external API for adapters/UI
	publicRouter = chi.NewRouter()
	publicRouter.Use(middleware.RequestID)
	publicRouter.Use(middleware.RealIP)
	publicRouter.Use(middleware.Recoverer)
	publicRouter.Get("/healthz", healthzHandler)
	publicRouter.Get("/readyz", readyzHandler)
	publicRouter.Route("/api/v1", func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/tasks", h.createTask)
		r.Get("/tasks", h.listTasks)
		r.Get("/tasks/{taskID}", h.getTask)
	})

	// Internal router (port 8081) - runner-only API (NetworkPolicy protected)
	internalRouter = chi.NewRouter()
	internalRouter.Use(middleware.RequestID)
	internalRouter.Use(middleware.RealIP)
	internalRouter.Use(middleware.Recoverer)
	internalRouter.Get("/healthz", healthzHandler)
	internalRouter.Get("/readyz", readyzHandler)
	internalRouter.Route("/api/v1", func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/tasks/{taskID}/status", h.updateTaskStatus)
		r.Get("/tasks/{taskID}/data", h.getTaskData)
		r.Get("/tasks/{taskID}/token", h.getTaskToken)
	})

	return publicRouter, internalRouter
}

func TestDualPortRouting(t *testing.T) {
	h := newTestHandler()
	publicRouter, internalRouter := buildTestRouters(h)

	type routeTest struct {
		name       string
		router     http.Handler
		method     string
		path       string
		wantStatus int
		wantNotIn  bool // if true, expect route to NOT be in router (404 or 405)
	}

	tests := []routeTest{
		// Public router - routes that SHOULD be available
		{"public: POST /tasks available", publicRouter, http.MethodPost, "/api/v1/tasks", http.StatusBadRequest, false},
		{"public: GET /tasks available", publicRouter, http.MethodGet, "/api/v1/tasks", http.StatusOK, false},
		{"public: GET /tasks/{id} available", publicRouter, http.MethodGet, "/api/v1/tasks/test-task", http.StatusNotFound, false},
		{"public: GET /healthz available", publicRouter, http.MethodGet, "/healthz", http.StatusOK, false},
		{"public: GET /readyz available", publicRouter, http.MethodGet, "/readyz", http.StatusOK, false},
		// Public router - routes that should NOT be available (internal-only)
		{"public: POST /tasks/{id}/status not available", publicRouter, http.MethodPost, "/api/v1/tasks/test-task/status", 0, true},
		{"public: GET /tasks/{id}/data not available", publicRouter, http.MethodGet, "/api/v1/tasks/test-task/data", 0, true},
		{"public: GET /tasks/{id}/token not available", publicRouter, http.MethodGet, "/api/v1/tasks/test-task/token", 0, true},
		// Internal router - routes that SHOULD be available
		{"internal: POST /tasks/{id}/status available", internalRouter, http.MethodPost, "/api/v1/tasks/test-task/status", http.StatusBadRequest, false},
		{"internal: GET /tasks/{id}/data available", internalRouter, http.MethodGet, "/api/v1/tasks/test-task/data", http.StatusNotFound, false},
		{"internal: GET /tasks/{id}/token available", internalRouter, http.MethodGet, "/api/v1/tasks/test-task/token", http.StatusNotFound, false},
		{"internal: GET /healthz available", internalRouter, http.MethodGet, "/healthz", http.StatusOK, false},
		{"internal: GET /readyz available", internalRouter, http.MethodGet, "/readyz", http.StatusOK, false},
		// Internal router - routes that should NOT be available (public-only)
		{"internal: POST /tasks not available", internalRouter, http.MethodPost, "/api/v1/tasks", 0, true},
		{"internal: GET /tasks not available", internalRouter, http.MethodGet, "/api/v1/tasks", 0, true},
		{"internal: GET /tasks/{id} not available", internalRouter, http.MethodGet, "/api/v1/tasks/test-task", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			tt.router.ServeHTTP(w, req)

			if tt.wantNotIn {
				assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed,
					"expected 404 or 405, got %d for %s %s", w.Code, tt.method, tt.path)
			} else {
				assert.Equal(t, tt.wantStatus, w.Code)
			}
		})
	}
}
