package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// Server handles task assignment and delegates to a TaskRunner.
type Server struct {
	runner   TaskRunner
	client   APIClient
	addr     string
	logger   logr.Logger
	assigned chan TaskAssignment
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithAddr sets the listen address.
func WithAddr(addr string) ServerOption {
	return func(s *Server) { s.addr = addr }
}

// WithLogger sets the logger.
func WithLogger(l logr.Logger) ServerOption {
	return func(s *Server) { s.logger = l }
}

// WithClient sets the API client (useful for testing).
func WithClient(c APIClient) ServerOption {
	return func(s *Server) { s.client = c }
}

// NewServer creates a runner server.
func NewServer(runner TaskRunner, opts ...ServerOption) *Server {
	s := &Server{
		runner:   runner,
		addr:     ":8888",
		logger:   logr.Discard(),
		assigned: make(chan TaskAssignment, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// newMux creates the HTTP handler for the server.
func (s *Server) newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		var ta TaskAssignment
		if err := json.NewDecoder(r.Body).Decode(&ta); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if ta.TaskID == "" || ta.APIURL == "" {
			http.Error(w, "taskID and apiURL are required", http.StatusBadRequest)
			return
		}
		s.logger.Info("received task assignment", "taskID", ta.TaskID, "apiURL", ta.APIURL)
		select {
		case s.assigned <- ta:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			http.Error(w, "task already assigned", http.StatusConflict)
		}
	})
	return mux
}

// Serve starts the HTTP server and blocks until the task is complete or context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{Addr: s.addr, Handler: s.newMux()}

	go func() {
		s.logger.Info("runner listening", "addr", s.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error(err, "server error")
		}
	}()

	// Wait for task assignment or context cancellation
	var ta TaskAssignment
	select {
	case ta = <-s.assigned:
		s.logger.Info("task assigned", "taskID", ta.TaskID, "apiURL", ta.APIURL)
	case <-ctx.Done():
		s.logger.Info("shutting down, no task received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}

	// Shut down HTTP server to prevent second assignment
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Error(err, "server shutdown error")
	}

	return s.executeTask(ctx, ta)
}

// executeTask runs the full task lifecycle: report started, fetch data, fetch token, run, report result.
func (s *Server) executeTask(ctx context.Context, ta TaskAssignment) error {
	log := s.logger.WithValues("taskID", ta.TaskID)

	// Use injected client (testing) or create a new one
	client := s.client
	if client == nil {
		client = NewClient(ta.APIURL, WithClientLogger(log))
	}

	// Report started status
	if err := client.ReportStatus(ctx, ta.TaskID, "started", "runner starting task", nil); err != nil {
		log.Error(err, "failed to report started status")
		// Continue — non-fatal
	}

	// Fetch task data
	taskData, err := client.FetchTaskData(ctx, ta.TaskID)
	if err != nil {
		msg := fmt.Sprintf("failed to fetch task data: %v", err)
		log.Error(err, "failed to fetch task data")
		_ = client.ReportStatus(ctx, ta.TaskID, "failed", msg, nil)
		return fmt.Errorf("fetching task data: %w", err)
	}
	taskData.APIURL = ta.APIURL

	// Fetch GitHub token (409 = fatal, non-retriable)
	token, expiresAt, err := client.FetchToken(ctx, ta.TaskID)
	if err != nil {
		msg := fmt.Sprintf("failed to fetch token: %v", err)
		log.Error(err, "failed to fetch token")
		_ = client.ReportStatus(ctx, ta.TaskID, "failed", msg, nil)
		return fmt.Errorf("fetching token: %w", err)
	}
	log.Info("token fetched", "expiresAt", expiresAt.Format(time.RFC3339))

	// Run the task
	result, err := s.runner.Run(ctx, *taskData, token)
	if err != nil {
		// Fallback: report failed if hook didn't fire
		msg := fmt.Sprintf("runner failed: %v", err)
		log.Error(err, "runner execution failed")
		_ = client.ReportStatus(ctx, ta.TaskID, "failed", msg, nil)
		return fmt.Errorf("running task: %w", err)
	}

	log.Info("task completed", "success", result.Success, "prURL", result.PRURL)
	return nil
}
