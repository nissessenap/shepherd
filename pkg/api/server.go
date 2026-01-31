package api

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
	ListenAddr     string
	CallbackSecret string
	Namespace      string
}

// Run starts the API server.
func Run(opts Options) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log := ctrl.Log.WithName("api")

	// Build K8s client
	cfg := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating k8s client: %w", err)
	}

	cb := newCallbackSender(opts.CallbackSecret)

	handler := &taskHandler{
		client:    k8sClient,
		namespace: opts.Namespace,
		callback:  cb,
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
	// TODO: Add middleware to validate Content-Type is application/json on mutating requests.
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tasks", handler.createTask)
		r.Get("/tasks", handler.listTasks)
		r.Get("/tasks/{taskID}", handler.getTask)
		r.Post("/tasks/{taskID}/status", handler.updateTaskStatus)
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
