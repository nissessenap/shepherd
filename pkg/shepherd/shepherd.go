// pkg/shepherd/shepherd.go
package shepherd

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// Module represents a runnable component
type Module interface {
	Name() string
	Run(ctx context.Context) error
}

// Shepherd orchestrates all modules
type Shepherd struct {
	cfg     Config
	modules []Module
}

// New creates a new Shepherd instance
func New(cfg Config) (*Shepherd, error) {
	s := &Shepherd{cfg: cfg}

	if err := s.initModules(); err != nil {
		return nil, fmt.Errorf("init modules: %w", err)
	}

	return s, nil
}

func (s *Shepherd) initModules() error {
	// Module initialization will be added during integration phase
	// Each module track develops independently, wiring happens in Phase 4
	return nil
}

// Run starts all modules and blocks until context is cancelled
func (s *Shepherd) Run(ctx context.Context) error {
	if len(s.modules) == 0 {
		fmt.Println("No modules configured for target:", s.cfg.Target)
		fmt.Println("Run with -target=api, -target=operator, or -target=github-adapter")
		<-ctx.Done()
		return nil
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, m := range s.modules {
		g.Go(func() error {
			fmt.Printf("Starting module: %s\n", m.Name())
			return m.Run(ctx)
		})
	}

	return g.Wait()
}
