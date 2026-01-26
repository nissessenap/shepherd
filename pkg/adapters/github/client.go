// pkg/adapters/github/client.go
package github

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v68/github"
)

// Client wraps the GitHub API client
type Client struct {
	appID      int64
	privateKey []byte
}

// NewClient creates a new GitHub client
func NewClient(appID int64, privateKeyPath string) (*Client, error) {
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	return &Client{appID: appID, privateKey: key}, nil
}

// GetInstallationClient returns a client authenticated for a specific installation
func (c *Client) GetInstallationClient(installationID int64) (*github.Client, error) {
	itr, err := ghinstallation.New(http.DefaultTransport, c.appID, installationID, c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("create installation transport: %w", err)
	}
	return github.NewClient(&http.Client{Transport: itr}), nil
}

// PostComment posts a comment on an issue or PR
func (c *Client) PostComment(ctx context.Context, installationID int64, owner, repo string, number int, body string) error {
	client, err := c.GetInstallationClient(installationID)
	if err != nil {
		return err
	}
	_, _, err = client.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: &body})
	return err
}
