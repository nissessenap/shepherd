// pkg/adapters/github/adapter.go
package github

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Config holds GitHub adapter configuration
type Config struct {
	ListenAddr    string
	WebhookSecret string
	AppID         int64
	PrivateKey    string
}

// Adapter is the GitHub adapter module
type Adapter struct {
	cfg    Config
	server *http.Server
	client *Client
}

// NewAdapter creates a new GitHub adapter
func NewAdapter(cfg Config) (*Adapter, error) {
	var client *Client
	var err error

	if cfg.AppID != 0 && cfg.PrivateKey != "" {
		client, err = NewClient(cfg.AppID, cfg.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("create github client: %w", err)
		}
	}

	webhookHandler := NewWebhookHandler(cfg.WebhookSecret, nil)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Post("/webhook", webhookHandler.HandleWebhook)
	r.Post("/callback/{owner}/{repo}/{number}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	return &Adapter{
		cfg:    cfg,
		client: client,
		server: &http.Server{Addr: cfg.ListenAddr, Handler: r},
	}, nil
}

// Name returns the module name
func (a *Adapter) Name() string {
	return "github-adapter"
}

// Run starts the GitHub adapter
func (a *Adapter) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		fmt.Printf("GitHub adapter listening on %s\n", a.cfg.ListenAddr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return a.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
