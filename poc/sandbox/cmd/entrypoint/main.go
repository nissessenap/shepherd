package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// TaskAssignment is the payload POSTed to /task.
type TaskAssignment struct {
	TaskID  string `json:"taskID"`
	Message string `json:"message"`
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("entrypoint starting", "port", 8888)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	taskCh := make(chan TaskAssignment, 1)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
		var req TaskAssignment
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		log.Info("received task assignment", "taskID", req.TaskID, "message", req.Message)

		select {
		case taskCh <- req:
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		default:
			http.Error(w, "task already assigned", http.StatusConflict)
		}
	})

	srv := &http.Server{Addr: ":8888", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	log.Info("waiting for task assignment...")

	// Block until task arrives or context is cancelled
	var assignment TaskAssignment
	select {
	case assignment = <-taskCh:
		log.Info("task received, shutting down HTTP server")
	case <-ctx.Done():
		log.Info("context cancelled, shutting down")
		srv.Shutdown(context.Background())
		return
	}

	// Shutdown HTTP server before doing work
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)

	// Do the work
	log.Info("executing task", "taskID", assignment.TaskID)

	// Simulate work: write a file, sleep
	workDir := "/tmp/work"
	os.MkdirAll(workDir, 0o755)
	resultFile := fmt.Sprintf("%s/result-%s.txt", workDir, assignment.TaskID)
	content := fmt.Sprintf("Task %s completed.\nMessage: %s\nTime: %s\n",
		assignment.TaskID, assignment.Message, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(resultFile, []byte(content), 0o644); err != nil {
		log.Error("failed to write result", "error", err)
		os.Exit(1)
	}

	log.Info("work complete", "taskID", assignment.TaskID, "resultFile", resultFile)

	// Simulate some processing time
	time.Sleep(2 * time.Second)

	log.Info("task finished successfully", "taskID", assignment.TaskID)
}
