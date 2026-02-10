package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/NissesSenap/shepherd/pkg/runner"
)

type ServeCmd struct {
	Addr      string `help:"Listen address" default:":8888" env:"SHEPHERD_RUNNER_ADDR"`
	WorkDir   string `help:"Working directory for cloning repos" default:"/workspace" env:"SHEPHERD_WORK_DIR"`
	ConfigDir string `help:"Directory with baked-in CC config" default:"/etc/shepherd" env:"SHEPHERD_CONFIG_DIR"`
}

func (c *ServeCmd) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := log.FromContext(ctx, "component", "runner")
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}

	taskRunner := &GoRunner{
		workDir:   c.WorkDir,
		configDir: c.ConfigDir,
		logger:    logger,
		execCmd:   &osExecutor{},
	}

	srv := runner.NewServer(taskRunner, runner.WithAddr(c.Addr), runner.WithLogger(logger))

	return srv.Serve(ctx)
}
