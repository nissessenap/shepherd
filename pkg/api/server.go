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

package api

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	toolkitv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(toolkitv1alpha1.AddToScheme(scheme))
}

// Options configures the API server.
type Options struct {
	ListenAddr           string
	CallbackSecret       string
	Namespace            string
	GithubAppID          int64
	GithubInstallationID int64
	GithubAPIURL         string
	GithubPrivateKeyPath string
}

// contentTypeMiddleware validates Content-Type header on mutating requests.
func contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only validate Content-Type on POST, PUT, PATCH
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			ct := r.Header.Get("Content-Type")
			if ct == "" || !strings.HasPrefix(ct, "application/json") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnsupportedMediaType)
				_, _ = w.Write([]byte(`{"error":"Content-Type must be application/json"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Run starts the API server.
func Run(opts Options) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log := ctrl.Log.WithName("api")

	// Build K8s client
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("getting k8s config: %w", err)
	}
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating k8s client: %w", err)
	}

	cb := newCallbackSender(opts.CallbackSecret)

	// Load GitHub App private key if configured
	var githubKey *rsa.PrivateKey
	if opts.GithubPrivateKeyPath != "" {
		var err error
		githubKey, err = readPrivateKey(opts.GithubPrivateKeyPath)
		if err != nil {
			return fmt.Errorf("reading GitHub private key: %w", err)
		}
		log.Info("GitHub App configured", "appID", opts.GithubAppID)
	}

	handler := &taskHandler{
		client:          k8sClient,
		namespace:       opts.Namespace,
		callback:        cb,
		githubAppID:     opts.GithubAppID,
		githubInstallID: opts.GithubInstallationID,
		githubAPIURL:    opts.GithubAPIURL,
		githubKey:       githubKey,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
	}

	// Health tracking for watcher and cache goroutines
	var watcherHealthy, cacheHealthy atomic.Bool
	watcherHealthy.Store(true)
	cacheHealthy.Store(true)

	// Create standalone cache for CRD status watching.
	// This gives us typed informers without the full manager overhead.
	taskCache, err := ctrlcache.New(cfg, ctrlcache.Options{
		Scheme: scheme,
		DefaultNamespaces: map[string]ctrlcache.Config{
			opts.Namespace: {},
		},
	})
	if err != nil {
		return fmt.Errorf("creating cache: %w", err)
	}

	// Start cache in background — stops when ctx is cancelled
	go func() {
		if err := taskCache.Start(ctx); err != nil {
			log.Error(err, "cache failed")
			cacheHealthy.Store(false)
		}
	}()

	// Wait for cache to sync before starting HTTP server
	if !taskCache.WaitForCacheSync(ctx) {
		return fmt.Errorf("cache sync failed")
	}

	// Start CRD status watcher
	watcher := &statusWatcher{
		client:   k8sClient,
		callback: cb,
		cache:    taskCache,
		log:      ctrl.Log.WithName("status-watcher"),
	}

	go func() {
		if err := watcher.run(ctx); err != nil {
			log.Error(err, "status watcher failed")
			watcherHealthy.Store(false)
		}
	}()

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
		if !watcherHealthy.Load() || !cacheHealthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("watcher or cache unhealthy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/tasks", handler.createTask)
		r.Get("/tasks", handler.listTasks)
		r.Get("/tasks/{taskID}", handler.getTask)
		r.Post("/tasks/{taskID}/status", handler.updateTaskStatus)
		r.Get("/tasks/{taskID}/data", handler.getTaskData)
		r.Get("/tasks/{taskID}/token", handler.getTaskToken)
	})

	srv := &http.Server{
		Addr:         opts.ListenAddr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		log.Info("starting API server", "addr", opts.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or error
	select {
	case <-ctx.Done():
		log.Info("shutting down API server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}
}
