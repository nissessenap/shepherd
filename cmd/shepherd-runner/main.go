package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// NOTE: This stub intentionally creates short-lived http.Client instances per request
// rather than using a shared client. The overhead is negligible in this simple stub,
// and optimizing it would add unnecessary complexity for minimal benefit.

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
		slog.Info("task assigned", "taskID", ta.TaskID, "apiURL", ta.APIURL)
		if err := executeTask(ctx, ta); err != nil {
			slog.Error("task execution failed, reporting failure", "error", err)
			_ = reportStatus(ctx, ta, "failed", err.Error())
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	_ = srv.Shutdown(context.Background())
}

func executeTask(ctx context.Context, ta TaskAssignment) error {
	client := &http.Client{Timeout: 30 * time.Second}

	// 1. Fetch task data
	dataURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/data"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dataURL, nil)
	if err != nil {
		return fmt.Errorf("creating data request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching task data: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected data response: %d %s", resp.StatusCode, string(body))
	}
	slog.Info("task data fetched", "taskID", ta.TaskID)

	// 2. Report completed status
	return reportStatus(ctx, ta, "completed", "stub runner completed successfully")
}

func reportStatus(ctx context.Context, ta TaskAssignment, event, message string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	statusURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/status"
	payload, _ := json.Marshal(map[string]string{
		"event":   event,
		"message": message,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, statusURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating status request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reporting status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status response: %d %s", resp.StatusCode, string(body))
	}
	slog.Info("status reported", "taskID", ta.TaskID, "event", event)
	return nil
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
