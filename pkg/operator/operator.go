// pkg/operator/operator.go
package operator

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	shepherdv1alpha1 "github.com/NissesSenap/shepherd/api/v1alpha1"
	controller "github.com/NissesSenap/shepherd/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(shepherdv1alpha1.AddToScheme(scheme))
}

// Config holds operator configuration
type Config struct {
	MetricsAddr      string
	HealthProbeAddr  string
	LeaderElection   bool
	LeaderElectionID string
}

// Operator is the K8s operator module
type Operator struct {
	cfg Config
	mgr ctrl.Manager
}

// NewOperator creates a new Operator
func NewOperator(cfg Config) (*Operator, error) {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: cfg.HealthProbeAddr,
		LeaderElection:         cfg.LeaderElection,
		LeaderElectionID:       cfg.LeaderElectionID,
	})
	if err != nil {
		return nil, fmt.Errorf("create manager: %w", err)
	}

	// Setup AgentTask controller
	if err := (&controller.AgentTaskReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("setup controller: %w", err)
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("add healthz check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("add readyz check: %w", err)
	}

	return &Operator{
		cfg: cfg,
		mgr: mgr,
	}, nil
}

// Name returns the module name
func (o *Operator) Name() string {
	return "operator"
}

// Run starts the operator
func (o *Operator) Run(ctx context.Context) error {
	logger := ctrl.Log.WithName("operator")
	logger.Info("Starting operator", "leaderElection", o.cfg.LeaderElection)
	return o.mgr.Start(ctx)
}
