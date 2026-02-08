/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Options configures the GitHub adapter.
type Options struct {
	ListenAddr     string // ":8082"
	WebhookSecret  string // GitHub webhook secret
	AppID          int64  // GitHub App ID
	InstallationID int64  // GitHub Installation ID
	PrivateKeyPath string // Path to private key PEM file
	APIURL         string // Shepherd API URL (e.g., "http://shepherd-api:8080")
	CallbackSecret string // Shared secret for callback HMAC verification
	CallbackURL    string // URL for API to call back (e.g., "http://github-adapter:8082/callback")
}

// Run starts the GitHub adapter server.
func Run(opts Options) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log := ctrl.Log.WithName("github-adapter")

	// TODO: Phase 2 - Create GitHub client
	// TODO: Phase 4 - Create API client

	// Health tracking
	var healthy atomic.Bool
	healthy.Store(true)

	// Build router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Health endpoints
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unhealthy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// TODO: Phase 3 - Webhook endpoint (rate-limited, with requireJSON)
	// r.Route("/webhook", func(r chi.Router) {
	//     r.Use(httprate.LimitByIP(100, time.Minute))
	//     r.Use(requireJSON)
	//     r.Post("/", webhookHandler.ServeHTTP)
	// })
	// TODO: Phase 5 - Callback endpoint (with requireJSON)
	// r.With(requireJSON).Post("/callback", callbackHandler.ServeHTTP)

	srv := &http.Server{
		Addr:         opts.ListenAddr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting GitHub adapter", "addr", opts.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("server error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down GitHub adapter")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
