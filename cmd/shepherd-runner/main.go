package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// TaskAssignment is the payload sent by the operator when assigning a task.
type TaskAssignment struct {
	TaskID string `json:"taskID"`
	APIURL string `json:"apiURL"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	assigned := make(chan TaskAssignment, 1)

	mux := newMux(assigned)

	srv := &http.Server{Addr: ":8888", Handler: mux}
	go func() {
		slog.Info("runner stub listening", "addr", ":8888")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for task assignment or shutdown
	select {
	case ta := <-assigned:
		slog.Info("task assigned, stub exiting", "taskID", ta.TaskID)
		// In full implementation: pull data, clone repo, run claude, report results
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	_ = srv.Shutdown(context.Background())
}

func newMux(assigned chan<- TaskAssignment) *http.ServeMux {
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
		slog.Info("received task assignment", "taskID", ta.TaskID, "apiURL", ta.APIURL)
		select {
		case assigned <- ta:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			http.Error(w, "task already assigned", http.StatusConflict)
		}
	})
	return mux
}
