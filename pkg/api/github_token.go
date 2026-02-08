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
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v75/github"
)

// TokenProvider generates GitHub installation tokens.
// Implemented by GitHubClient; test code can substitute a mock.
type TokenProvider interface {
	GetToken(ctx context.Context, repoURL string) (token string, expiresAt time.Time, err error)
}

// GitHubClient wraps GitHub API operations using ghinstallation.
type GitHubClient struct {
	appsTransport  *ghinstallation.AppsTransport
	installationID int64
}

// NewGitHubClient creates a new GitHub client from app credentials.
func NewGitHubClient(appID, installationID int64, privateKeyPath string) (*GitHubClient, error) {
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading private key: %w", err)
	}

	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, keyData)
	if err != nil {
		return nil, fmt.Errorf("creating apps transport: %w", err)
	}

	return &GitHubClient{
		appsTransport:  atr,
		installationID: installationID,
	}, nil
}

// GetToken returns a token for the installation, optionally scoped to a repository.
func (c *GitHubClient) GetToken(ctx context.Context, repoURL string) (string, time.Time, error) {
	// Create a fresh transport per call to support per-repo scoping.
	// NewFromAppsTransport is cheap (no network call).
	tr := ghinstallation.NewFromAppsTransport(c.appsTransport, c.installationID)

	if repoURL != "" {
		repoName, err := parseRepoName(repoURL)
		if err != nil {
			return "", time.Time{}, err
		}
		tr.InstallationTokenOptions = &gh.InstallationTokenOptions{
			Repositories: []string{repoName},
		}
	}

	token, err := tr.Token(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("getting installation token: %w", err)
	}

	// ghinstallation tokens are valid for 1 hour
	return token, time.Now().Add(time.Hour), nil
}

// parseRepoName extracts "repo" from "https://github.com/org/repo.git" or "https://github.com/org/repo".
func parseRepoName(repoURL string) (string, error) {
	if repoURL == "" {
		return "", fmt.Errorf("repo URL is required")
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("invalid repo URL: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("repo URL must be owner/repo format: %s", repoURL)
	}
	name := parts[1]
	return strings.TrimSuffix(name, ".git"), nil
}
