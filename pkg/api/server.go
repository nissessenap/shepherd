// pkg/api/server.go
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server is the API server module
type Server struct {
	addr   string
	server *http.Server
}

// Config holds API server configuration
type Config struct {
	ListenAddr string
}

// bodyLimitMiddleware limits the size of request bodies to prevent DoS attacks
func bodyLimitMiddleware(maxBytes int64) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// NewServer creates a new API server
func NewServer(cfg Config) (*Server, error) {
	handlers := NewHandlers()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(middleware.SetHeader("Content-Type", "application/json"))
	r.Use(bodyLimitMiddleware(1 << 20)) // 1MB limit

	r.Get("/healthz", handlers.HealthCheck)
	r.Get("/readyz", handlers.ReadyCheck)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", handlers.CreateTask)
		r.Get("/tasks/{id}", handlers.GetTaskStatus)
		r.Post("/tasks/{id}/status", handlers.UpdateTaskStatus)
	})

	return &Server{
		addr:   cfg.ListenAddr,
		server: &http.Server{Addr: cfg.ListenAddr, Handler: r},
	}, nil
}

// Name returns the module name
func (s *Server) Name() string {
	return "api"
}

// Run starts the API server
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		fmt.Printf("API server listening on %s\n", s.addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
