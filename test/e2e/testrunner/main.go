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

// event represents a single agent activity event to POST to the API.
type event struct {
	Seq     int64
	Type    string
	Summary string
	Tool    string
	Input   map[string]any
	Success bool
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
			_ = reportStatus(ctx, ta, "failed", err.Error(), nil)
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

	// 2. POST a realistic sequence of fake events
	readInput := map[string]any{"file_path": "src/main.go"}
	editInput := map[string]any{"file_path": "src/main.go"}
	bashInput := map[string]any{"command": "go test ./..."}

	events := []event{
		{Seq: 1, Type: "thinking", Summary: "Analyzing the codebase structure..."},
		{Seq: 2, Type: "tool_call", Summary: "Reading src/main.go",
			Tool: "Read", Input: readInput},
		{Seq: 3, Type: "tool_result", Summary: "package main\\nfunc main() {...}",
			Tool: "Read", Success: true},
		{Seq: 4, Type: "tool_call", Summary: "Editing src/main.go",
			Tool: "Edit", Input: editInput},
		{Seq: 5, Type: "tool_result", Summary: "Modified lines 10-15 (+3/-1)",
			Tool: "Edit", Success: true},
		{Seq: 6, Type: "thinking", Summary: "Running tests to verify the changes..."},
		{Seq: 7, Type: "tool_call", Summary: "go test ./...",
			Tool: "Bash", Input: bashInput},
		{Seq: 8, Type: "tool_result", Summary: "PASS\\nok  project/pkg 0.42s",
			Tool: "Bash", Success: true},
	}

	for _, e := range events {
		if postErr := postEvent(ctx, client, ta, e); postErr != nil {
			slog.Warn("failed to post event", "seq", e.Seq, "error", postErr)
			// Best-effort — don't fail the task if event posting fails
		}
		// Small delay between events so observers can see them arriving
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 3. Report completed status with a fake PR URL
	return reportStatus(ctx, ta, "completed", "stub runner completed successfully",
		map[string]any{"pr_url": "https://github.com/test-org/test-repo/pull/42"})
}

func postEvent(ctx context.Context, client *http.Client, ta TaskAssignment, e event) error {
	eventsURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/events"
	payload, _ := json.Marshal(map[string]any{
		"events": []map[string]any{{
			"sequence":  e.Seq,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"type":      e.Type,
			"summary":   e.Summary,
			"tool":      e.Tool,
			"input":     e.Input,
			"output":    map[string]any{"success": e.Success, "summary": e.Summary},
		}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("event POST returned %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func reportStatus(ctx context.Context, ta TaskAssignment, statusEvent, message string, details map[string]any) error {
	client := &http.Client{Timeout: 30 * time.Second}

	statusURL := ta.APIURL + "/api/v1/tasks/" + ta.TaskID + "/status"
	payload := map[string]any{
		"event":   statusEvent,
		"message": message,
	}
	if details != nil {
		payload["details"] = details
	}
	payloadBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, statusURL, bytes.NewReader(payloadBytes))
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
	slog.Info("status reported", "taskID", ta.TaskID, "event", statusEvent)
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
