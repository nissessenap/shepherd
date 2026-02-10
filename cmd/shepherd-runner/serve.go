package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NissesSenap/shepherd/pkg/runner"
)

type ServeCmd struct {
	Addr string `help:"Listen address" default:":8888" env:"SHEPHERD_RUNNER_ADDR"`
}

func (c *ServeCmd) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// TODO(phase3): Replace stubRunner with GoRunner once implemented.
	taskRunner := &stubRunner{}

	client := runner.NewClient("") // base URL comes from task assignment
	srv := runner.NewServer(taskRunner, client, runner.WithAddr(c.Addr))

	return srv.Serve(ctx)
}

// stubRunner is a placeholder TaskRunner until GoRunner is implemented in Phase 3.
type stubRunner struct{}

func (s *stubRunner) Run(_ context.Context, _ runner.TaskData, _ string) (*runner.Result, error) {
	return nil, fmt.Errorf("GoRunner not implemented yet")
}
