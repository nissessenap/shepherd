// pkg/shepherd/config.go
package shepherd

import (
	"errors"
	"flag"
)

// Target constants for single-binary multi-target pattern
const (
	TargetAll           = "all"
	TargetAPI           = "api"
	TargetOperator      = "operator"
	TargetGitHubAdapter = "github-adapter"
)

// Config holds all configuration for Shepherd
type Config struct {
	Target string

	// API configuration
	APIListenAddr string

	// Operator configuration
	MetricsAddr      string
	HealthProbeAddr  string
	LeaderElection   bool
	LeaderElectionID string

	// GitHub Adapter configuration
	GitHubAdapterAddr   string
	GitHubWebhookSecret string
	GitHubAppID         int64
	GitHubPrivateKey    string
}

// RegisterFlags registers configuration flags
func (c *Config) RegisterFlags(f *flag.FlagSet) {
	f.StringVar(&c.Target, "target", TargetAll, "Component to run: all, api, operator, github-adapter")
	f.StringVar(&c.APIListenAddr, "api.listen-addr", ":8080", "API server listen address")
	f.StringVar(&c.MetricsAddr, "metrics.addr", ":9090", "Metrics server address")
	f.StringVar(&c.HealthProbeAddr, "health.addr", ":8081", "Health probe address")
	f.BoolVar(&c.LeaderElection, "leader-election", false, "Enable leader election")
	f.StringVar(&c.LeaderElectionID, "leader-election-id", "shepherd-operator", "Leader election ID")
	f.StringVar(&c.GitHubAdapterAddr, "github.listen-addr", ":8082", "GitHub adapter listen address")
	f.StringVar(&c.GitHubWebhookSecret, "github.webhook-secret", "", "GitHub webhook secret")
	f.Int64Var(&c.GitHubAppID, "github.app-id", 0, "GitHub App ID")
	f.StringVar(&c.GitHubPrivateKey, "github.private-key", "", "Path to GitHub App private key")
}

// Validate validates the configuration
func (c *Config) Validate() error {
	switch c.Target {
	case TargetAll, TargetAPI, TargetOperator, TargetGitHubAdapter:
		// valid
	default:
		return errors.New("invalid target: must be one of all, api, operator, github-adapter")
	}
	return nil
}
