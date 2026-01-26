// cmd/shepherd/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NissesSenap/shepherd/pkg/shepherd"
)

func main() {
	var cfg shepherd.Config
	cfg.RegisterFlags(flag.CommandLine)
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	s, err := shepherd.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create shepherd: %v\n", err)
		os.Exit(1)
	}

	if err := s.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "shepherd error: %v\n", err)
		os.Exit(1)
	}
}
